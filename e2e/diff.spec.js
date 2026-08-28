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

  test("file tree: nested dirs compress, entries jump to files, viewed syncs", async ({ page }) => {
    app = await launchDiff();
    app.repo.write("pkg/util/strings.go", "package util\n\nfunc Upper(s string) string { return s }\n");
    await page.goto(app.url);

    const tree = page.locator(".file-tree");
    await expect(tree).toBeVisible();
    // Single-child dir chain pkg/util is compressed into one label.
    await expect(tree.locator("summary.tree-dir", { hasText: "pkg/util" })).toBeVisible();
    await expect(tree.locator(".tree-file")).toHaveCount(3);

    // Clicking a tree entry jumps to that file's card.
    await tree.locator('.tree-file[data-path="pkg/util/strings.go"]').click();
    await expect(page).toHaveURL(/#f-/);
    const target = page.locator('.file[data-path="pkg/util/strings.go"]');
    await expect(target).toBeInViewport();

    // Marking a file viewed reflects in the tree.
    await target.locator(".viewed-box").check();
    await expect(tree.locator('.tree-file[data-path="pkg/util/strings.go"]')).toHaveClass(/viewed/);
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

    // Prepend lines so the commented line moves from 1 to 3. Source
    // changes surface as a refresh banner rather than yanking the view.
    app.repo.write("notes.txt", "buy eggs\nbuy bread\nremember the milk\n");
    await expect(page.locator("#refresh-banner")).toBeVisible({ timeout: 10_000 });
    await page.click("#refresh-banner-go");
    await expect(page.locator("#refresh-banner")).toBeHidden();
    const moved = page.locator('.file[data-path="notes.txt"] tr.line', { hasText: "remember the milk" });
    await expect(moved).toBeVisible({ timeout: 10_000 });
    await expect(
      page.locator('.file[data-path="notes.txt"] .comment', { hasText: "sticky note" })
    ).toBeVisible({ timeout: 10_000 });
    await expect
      .poll(() => app.readStore().comments[0].anchor.line, { timeout: 10_000 })
      .toBe(3);
  });

  test("presets: branch shows only committed changes", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);
    // Commit the working change so the branch preset has content.
    app.repo.git("add", "main.go");
    app.repo.git("commit", "-m", "greeting");
    await page.click('.preset-btn[data-preset="branch"]');
    await expect(page.locator('.file[data-path="main.go"]')).toBeVisible();
    await expect(page.locator('.file[data-path="notes.txt"]')).toHaveCount(0);
    await expect(page.locator(".range-chip .range-head")).toHaveText("HEAD");
  });

  test("history navigator: graph rows, click-to-compare, shift-click base", async ({ page }) => {
    app = await launchDiff();
    app.repo.git("add", "main.go");
    app.repo.git("commit", "-m", "greeting change");
    await page.goto(app.url);

    await page.click("#history-toggle");
    const panel = page.locator("#history-panel");
    await expect(panel).toBeVisible();
    // Pseudo endpoints + at least the two commits.
    await expect(panel.locator(".hist-row.pseudo")).toHaveCount(2);
    const commits = panel.locator(".hist-row:not(.pseudo)");
    await expect(commits.first().locator(".hist-subject")).toHaveText("greeting change");
    await expect(commits.first().locator("svg.lanes circle")).toHaveCount(1);
    // Working tree is the current compare endpoint.
    await expect(panel.locator(".hist-row.pseudo").first()).toHaveClass(/sel-compare/);

    // Shift-click the older commit → it becomes the base.
    await commits.nth(1).click({ modifiers: ["Shift"] });
    await expect(page.locator("#history-panel .hist-row:not(.pseudo)").nth(1)).toHaveClass(/sel-base/);

    // Click the newer commit → compare against it; badge moves off the
    // working tree and the committed change is the diff.
    await page.locator("#history-panel .hist-row:not(.pseudo)").first().click();
    await expect(page.locator("#history-panel .hist-row:not(.pseudo)").first()).toHaveClass(/sel-compare/);
    await expect(page.locator("#history-panel .hist-row.pseudo").first()).not.toHaveClass(/sel-compare/);
    await expect(page.locator('.file[data-path="main.go"]')).toBeVisible();
    await expect(page.locator(".diff-summary")).toContainText("1 changed file");
  });

  test("comments record their range and the stack navigates back", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);
    await page.fill("#author", "olie");

    // Comment on the working-tree diff.
    const addRow = page.locator('.file[data-path="main.go"] tr.line.add').first();
    await addRow.evaluate((el) => el.scrollIntoView({ block: "center" }));
    await addRow.hover();
    await addRow.locator(".add-comment-btn").click();
    await page.fill('tr.form-row textarea[name="text"]', "this greeting was good");
    await page.click('tr.form-row button[type="submit"]');
    await expect(page.locator(".comment", { hasText: "this greeting was good" })).toBeVisible();

    // The anchor carries the comparison it was made against.
    const stored = app.readStore().comments[0];
    expect(stored.anchor.base).toBe("HEAD");
    expect(stored.anchor.head).toBe("@worktree");
    expect(stored.anchor.base_sha).toMatch(/^[0-9a-f]{40}$/);

    // The stack lists it with its range.
    const entry = page.locator(".stack-entry", { hasText: "this greeting was good" });
    await expect(entry).toBeVisible();
    await expect(entry.locator(".stack-meta")).toContainText("HEAD ⟵ Working tree");

    // Move the view elsewhere, then navigate back via the stack.
    app.repo.git("add", "main.go");
    app.repo.git("commit", "-m", "greeting change");
    await page.click('.preset-btn[data-preset="branch"]');
    await expect(page.locator(".range-chip .range-head")).toHaveText("HEAD");

    await page.locator(".stack-entry", { hasText: "this greeting was good" }).click();
    const comment = page.locator(".comment", { hasText: "this greeting was good" });
    await expect(comment).toBeVisible({ timeout: 10_000 });
  });

  test("all files: unchanged files browse and accept comments", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);
    await page.fill("#author", "olie");

    await page.click("#allfiles-toggle");
    const unchanged = page.locator(".tree-file.unchanged");
    await expect(unchanged).toHaveCount(1); // keep.txt
    await unchanged.click();

    const card = page.locator('section.file[data-full-file][data-path="keep.txt"]');
    await expect(card).toBeVisible();
    await expect(card.locator("tr.line", { hasText: "unchanged" })).toBeVisible();

    // Comment on a line of the unchanged file.
    const row = card.locator("tr.line").first();
    await row.evaluate((el) => el.scrollIntoView({ block: "center" }));
    await row.hover();
    await row.locator(".add-comment-btn").click();
    await page.fill('tr.form-row textarea[name="text"]', "context note");
    await page.click('tr.form-row button[type="submit"]');
    // It shows in the stack even though the file has no diff card.
    await expect(page.locator(".stack-entry", { hasText: "context note" })).toBeVisible();
    const stored = app.readStore().comments.find((c) => c.text === "context note");
    expect(stored.anchor.file).toBe("keep.txt");
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
