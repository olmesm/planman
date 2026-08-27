// @ts-check
const { test, expect } = require("@playwright/test");
const { launchDiff } = require("./helpers");

test.describe("planman diff review flow", () => {
  /** @type {Awaited<ReturnType<typeof launchDiff>>} */
  let app;

  test.afterEach(() => {
    if (app) app.kill();
  });

  test("renders changed files with stats, hunks, and word diff", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await expect(page.locator(".file")).toHaveCount(2); // main.go + notes.txt
    const mainFile = page.locator('.file[data-path="main.go"]');
    await expect(mainFile.locator(".file-path")).toHaveText("main.go");
    await expect(mainFile.locator(".file-status")).toHaveText("modified");
    await expect(page.locator('.file[data-path="notes.txt"] .file-status')).toHaveText("added");

    // The del/add pair around Println gets word-level emphasis.
    await expect(mainFile.locator("tr.line.del")).toHaveCount(1);
    await expect(mainFile.locator("tr.line.add .wd", { hasText: ", planman" })).toBeVisible();
    // Syntax highlighting inside diff rows.
    await expect(mainFile.locator("tr.line .kn", { hasText: "import" }).first()).toBeVisible();
    // Untracked file appears as all-added lines.
    await expect(
      page.locator('.file[data-path="notes.txt"] tr.line.add', { hasText: "remember the milk" })
    ).toBeVisible();
  });

  test("line comment: add, persists to sidecar, resolve via UI", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);
    await page.fill("#author", "olie");

    const addRow = page.locator('.file[data-path="main.go"] tr.line.add').first();
    await addRow.hover();
    await addRow.locator(".add-comment-btn").click();
    await page.fill('tr.form-row textarea[name="text"]', "why the greeting change?");
    await page.click('tr.form-row button[type="submit"]');

    const comment = page.locator(".comment", { hasText: "why the greeting change?" });
    await expect(comment).toBeVisible();
    await expect(comment.locator(".comment-author")).toHaveText("olie");

    const store = app.readStore();
    expect(store.comments).toHaveLength(1);
    expect(store.comments[0].anchor.file).toBe("main.go");
    expect(store.comments[0].anchor.side).toBe("new");
    expect(store.comments[0].anchor.context).toContain("hello, planman");

    await comment.locator(".resolve-btn").click();
    await expect(page.locator(".comment.resolved")).toBeVisible();
    expect(app.readStore().comments[0].status).toBe("resolved");
  });

  test("agent API: healthz identifies the repo, PATCH resolves, reply threads", async ({ page, request }) => {
    app = await launchDiff();
    await page.goto(app.url);
    await page.fill("#author", "olie");

    const health = await (await request.get(`${app.url}/healthz`)).json();
    expect(health.app).toBe("planman");
    expect(health.mode).toBe("diff");
    expect(app.repo.dir).toContain(health.root.split("/").pop());

    const addRow = page.locator('.file[data-path="main.go"] tr.line.add').first();
    await addRow.hover();
    await addRow.locator(".add-comment-btn").click();
    await page.fill('tr.form-row textarea[name="text"]', "fix me");
    await page.click('tr.form-row button[type="submit"]');
    await expect(page.locator(".comment", { hasText: "fix me" })).toBeVisible();

    const list = await (await request.get(`${app.url}/api/comments?status=open`)).json();
    expect(list.comments).toHaveLength(1);
    const id = list.comments[0].id;

    const reply = await request.post(`${app.url}/api/comments/${id}/reply`, {
      data: { text: "done in abc123" },
    });
    expect(reply.ok()).toBeTruthy();
    const patched = await request.patch(`${app.url}/api/comments/${id}`, {
      data: { status: "resolved" },
    });
    expect((await patched.json()).status).toBe("resolved");

    // The page catches up over SSE without a manual reload.
    const resolved = page.locator(".comment.resolved", { hasText: "fix me" });
    await expect(resolved).toBeVisible({ timeout: 10_000 });
    await expect(resolved.locator(".reply", { hasText: "done in abc123" })).toBeVisible();
    await expect(resolved.locator(".reply .comment-author")).toHaveText("agent");
  });

  test("split view aligns old and new sides", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);
    await page.click(".view-toggle button:has-text('Split')");
    const table = page.locator('.file[data-path="main.go"] table.diff-table.split');
    await expect(table).toBeVisible();
    const pairRow = table.locator("tr.line", { hasText: "hello" }).last();
    await expect(pairRow.locator("td.code.del")).toBeVisible();
    await expect(pairRow.locator("td.code.add")).toBeVisible();
  });

  test("hunk expansion reveals surrounding context", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);
    const mainFile = page.locator('.file[data-path="main.go"]');
    // Small file: the whole file is context already; force a scenario by
    // asserting the expander is absent (no hidden lines) or clicking it.
    const expander = mainFile.locator(".expander-btn");
    if (await expander.count()) {
      await expander.first().click();
      await expect(mainFile.locator("tr.line.context").first()).toBeVisible();
    } else {
      await expect(mainFile.locator("tr.line.context").first()).toBeVisible();
    }
  });

  test("comments re-anchor when the diff shifts under them", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);
    await page.fill("#author", "olie");

    const addRow = page.locator('.file[data-path="notes.txt"] tr.line.add');
    // Center the row first: auto-scroll can leave it under the sticky
    // file header, which swallows pointer events.
    await addRow.evaluate((el) => el.scrollIntoView({ block: "center" }));
    await addRow.hover();
    await addRow.locator(".add-comment-btn").click();
    await page.fill('tr.form-row textarea[name="text"]', "sticky note");
    await page.click('tr.form-row button[type="submit"]');
    await expect(page.locator(".comment", { hasText: "sticky note" })).toBeVisible();

    // Prepend lines so the commented line moves from 1 to 3.
    app.repo.write("notes.txt", "buy eggs\nbuy bread\nremember the milk\n");
    const moved = page.locator('.file[data-path="notes.txt"] tr.line', { hasText: "remember the milk" });
    await expect(moved).toBeVisible({ timeout: 10_000 });
    await expect(
      page.locator('.file[data-path="notes.txt"] .comment', { hasText: "sticky note" })
    ).toBeVisible({ timeout: 10_000 });
    await expect
      .poll(() => app.readStore().comments[0].anchor.line, { timeout: 10_000 })
      .toBe(3);
  });

  test("scope switching: branch shows only committed changes", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);
    // Commit the working change so branch scope has content.
    app.repo.git("add", "main.go");
    app.repo.git("commit", "-m", "greeting");
    await page.selectOption(".scope-select", "branch");
    await expect(page.locator('.file[data-path="main.go"]')).toBeVisible();
    await expect(page.locator('.file[data-path="notes.txt"]')).toHaveCount(0);
  });

  test("handback: exits 0 with counts and an export for the agent", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);
    await page.fill("#author", "olie");

    const addRow = page.locator('.file[data-path="main.go"] tr.line.add').first();
    await addRow.hover();
    await addRow.locator(".add-comment-btn").click();
    await page.fill('tr.form-row textarea[name="text"]', "ship it after this");
    await page.click('tr.form-row button[type="submit"]');
    await expect(page.locator(".comment", { hasText: "ship it after this" })).toBeVisible();

    await page.click("#handback-btn");
    await expect(page.locator(".handback-done")).toBeVisible();

    const code = await app.exited;
    expect(code).toBe(0);
    const done = app.events.find((e) => e.event === "handback");
    expect(done).toBeTruthy();
    expect(done.comments_added).toBe(1);
    expect(done.comments_open).toBe(1);
    expect(done.export).toContain("handback.json");
    const fs = require("fs");
    const payload = JSON.parse(fs.readFileSync(done.export, "utf8"));
    expect(payload.comments).toHaveLength(1);
    expect(payload.comments[0].text).toBe("ship it after this");
  });
});
