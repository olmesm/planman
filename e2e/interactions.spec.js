// @ts-check
// Cross-cutting interaction coverage: chrome toggles, palette commands,
// search-bar controls, split-view ranges, full-file definition jumps,
// walkthrough controls, and doc-mode comment rendering.
const { test, expect } = require("@playwright/test");
const { launch, launchDiff } = require("./helpers");

test.describe("chrome", () => {
  /** @type {any} */
  let app;
  test.afterEach(() => app?.kill());

  test("theme toggle cycles auto → light → dark → auto", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    const html = page.locator("html");
    await expect(html).not.toHaveAttribute("data-theme");
    await page.click("#theme-toggle");
    await expect(html).toHaveAttribute("data-theme", "light");
    await page.click("#theme-toggle");
    await expect(html).toHaveAttribute("data-theme", "dark");
    // Forced theme survives a reload, then a third click returns to auto.
    await page.reload();
    await expect(html).toHaveAttribute("data-theme", "dark");
    await page.click("#theme-toggle");
    await expect(html).not.toHaveAttribute("data-theme");
  });

  test("copy path puts the file path on the clipboard", async ({ page, context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    app = await launchDiff();
    await page.goto(app.url);

    const btn = page.locator('section.file[data-path="main.go"] .copy-path');
    await btn.click();
    await expect(btn).toHaveText("✓");
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("main.go");
  });

  test("Mod+B and drag-to-closed both collapse the sidebar", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    const layout = page.locator(".diff-layout");
    await page.keyboard.press("Control+b");
    await expect(layout).toHaveClass(/sidebar-collapsed/);
    await page.keyboard.press("Control+b");
    await expect(layout).not.toHaveClass(/sidebar-collapsed/);

    const handle = page.locator(".sidebar-handle");
    const box = await handle.boundingBox();
    await page.mouse.move(box.x + box.width / 2, box.y + 40);
    await page.mouse.down();
    await page.mouse.move(box.x - 300, box.y + 40, { steps: 5 });
    await page.mouse.up();
    await expect(layout).toHaveClass(/sidebar-collapsed/);
    // Collapse persists across a content refresh.
    await page.click('button[data-preset="working"]');
    await expect(layout).toHaveClass(/sidebar-collapsed/);
  });
});

test.describe("command palette", () => {
  /** @type {any} */
  let app;
  test.afterEach(() => app?.kill());

  async function run(page, command) {
    await page.keyboard.press("Control+Shift+p");
    await expect(page.locator("#palette")).toBeVisible();
    await page.fill("#palette-input", command);
    await page.keyboard.press("Enter");
  }

  test("Escape and backdrop click both close it", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await page.keyboard.press("Control+Shift+p");
    await expect(page.locator("#palette")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.locator("#palette-backdrop")).toBeHidden();

    await page.keyboard.press("Control+Shift+p");
    await page.locator("#palette-backdrop").click({ position: { x: 5, y: 5 } });
    await expect(page.locator("#palette-backdrop")).toBeHidden();
  });

  test("arrow keys select a different command", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await page.keyboard.press("Control+Shift+p");
    await page.keyboard.press("ArrowDown");
    await expect(page.locator(".palette-item.sel")).toHaveText(/Find in diffs/);
    await page.keyboard.press("ArrowUp");
    await expect(page.locator(".palette-item.sel")).toHaveText(/Go to file/);
    // "Go to file…" switches the palette into file mode and stays open.
    await page.keyboard.press("Enter");
    await expect(page.locator("#palette-input")).toHaveAttribute("placeholder", "Go to file…");
    await page.keyboard.press("Escape");
  });

  test("collapse all / expand all files", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await run(page, "collapse all");
    for (const f of await page.locator("#content section.file").all()) {
      await expect(f).toHaveClass(/collapsed/);
    }
    await run(page, "expand all");
    for (const f of await page.locator("#content section.file").all()) {
      await expect(f).not.toHaveClass(/collapsed/);
    }
  });

  test("mark all viewed / clear all viewed marks", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await run(page, "mark all");
    for (const box of await page.locator("#content section.file .viewed-box").all()) {
      await expect(box).toBeChecked();
    }
    await expect(page.locator(".tree-file.viewed")).toHaveCount(
      await page.locator("#content section.file").count()
    );
    await run(page, "clear all viewed");
    for (const box of await page.locator("#content section.file .viewed-box").all()) {
      await expect(box).not.toBeChecked();
    }
  });

  test("toggle hide whitespace via the palette", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await run(page, "hide whitespace");
    await expect(page.locator("#ws-toggle")).toHaveClass(/active/);
  });

  test("toggle live reload: repo edits refresh without the banner", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await run(page, "live reload");
    app.repo.write("notes.txt", "remember the milk\nand the eggs\n");
    // The new line shows up on its own; the banner never appears.
    await expect(
      page.locator('section.file[data-path="notes.txt"] tr.line', { hasText: "and the eggs" })
    ).toBeVisible({ timeout: 10_000 });
    await expect(page.locator("#refresh-banner")).toBeHidden();
  });
});

test.describe("search bar controls", () => {
  /** @type {any} */
  let app;
  test.afterEach(() => app?.kill());

  test("next/prev buttons cycle and × closes", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await page.keyboard.press("Control+f");
    await page.fill("#search-input", "hello");
    await expect(page.locator("#search-count")).toContainText("match");

    await page.click("#search-next");
    await expect(page.locator("#search-count")).toContainText("1 of");
    await page.click("#search-prev");
    // Wraps backwards to the last match.
    const count = await page.locator("#search-count").textContent();
    expect(count).toMatch(/^\d+ of \d+$/);
    expect(count.startsWith("1 of")).toBe(false);

    await page.click("#search-close");
    await expect(page.locator("#search-bar")).toBeHidden();
    await expect(page.locator(".search-dimmed")).toHaveCount(0);
    await expect(page.locator(".search-hit")).toHaveCount(0);
  });
});

test.describe("split view interactions", () => {
  /** @type {any} */
  let app;
  test.afterEach(() => app?.kill());

  test("gutter drag makes a range comment in split view", async ({ page }) => {
    app = await launchDiff();
    app.repo.write("multi.txt", "alpha\nbravo\ncharlie\ndelta\necho\n");
    await page.goto(app.url);
    await page.locator(".view-toggle .btn", { hasText: "Split" }).click();
    await expect(page.locator(".diff-table.split").first()).toBeVisible();

    const cell = (n) =>
      page.locator(
        `section.file[data-path="multi.txt"] td.num[data-side="new"][data-line="${n}"]`
      );
    const from = await cell(2).boundingBox();
    const to = await cell(4).boundingBox();
    await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
    await page.mouse.down();
    await page.mouse.move(to.x + to.width / 2, to.y + to.height / 2, { steps: 4 });
    await page.mouse.up();

    const form = page.locator("tr.form-row textarea");
    await expect(form).toBeVisible();
    await expect(form).toHaveAttribute("placeholder", /lines 2–4/);
    await form.fill("split range note");
    await page.click('tr.form-row button[type="submit"]');

    const file = page.locator('section.file[data-path="multi.txt"]');
    await expect(file.locator(".comment", { hasText: "split range note" })).toBeVisible();
    await expect(file.locator("td.num.in-range")).toHaveCount(3);
  });

  test("split cells accept single-line comments", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);
    await page.locator(".view-toggle .btn", { hasText: "Split" }).click();

    const cell = page
      .locator('section.file[data-path="main.go"] td.code.add')
      .first();
    await cell.hover();
    await cell.locator(".add-comment-btn").click();
    await page.fill('tr.form-row textarea[name="text"]', "split side note");
    await page.click('tr.form-row button[type="submit"]');
    await expect(page.locator(".comment", { hasText: "split side note" })).toBeVisible();
  });
});

test.describe("definition navigation extras", () => {
  /** @type {any} */
  let app;
  test.afterEach(() => app?.kill());

  test("a definition outside the diff opens the file in full", async ({ page }) => {
    app = await launchDiff();
    // lib.go is committed and unchanged, so it has no card in the diff.
    app.repo.write("lib.go", 'package main\n\nfunc Greet() string {\n\treturn "hi"\n}\n');
    app.repo.git("add", "lib.go");
    app.repo.git("commit", "-m", "add lib");
    app.repo.write(
      "main.go",
      'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println(Greet())\n}\n'
    );
    await page.goto(app.url);

    const token = page
      .locator('section.file[data-path="main.go"] tr.line.add .code-text span', {
        hasText: /^Greet$/,
      })
      .first();
    await token.scrollIntoViewIfNeeded();
    await page.keyboard.down("Control");
    await token.click();
    await page.keyboard.up("Control");

    await page.locator(".def-candidate", { hasText: "lib.go:3" }).click();
    const full = page.locator('section.file[data-full-file][data-path="lib.go"]');
    await expect(full).toBeVisible();
    await expect(full.locator('tr[data-line="3"]')).toHaveClass(/flash/);
  });

  test("click-away closes the popover", async ({ page }) => {
    app = await launchDiff();
    app.repo.write("lib.go", 'package main\n\nfunc Greet() string {\n\treturn "hi"\n}\n');
    app.repo.write(
      "main.go",
      'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println(Greet())\n}\n'
    );
    await page.goto(app.url);

    const token = page
      .locator('section.file[data-path="main.go"] tr.line.add .code-text span', {
        hasText: /^Greet$/,
      })
      .first();
    await token.scrollIntoViewIfNeeded();
    await page.keyboard.down("Control");
    await token.click();
    await page.keyboard.up("Control");
    await expect(page.locator(".def-popover")).toBeVisible();

    await page.locator(".diff-summary").click();
    await expect(page.locator(".def-popover")).toHaveCount(0);
  });
});

test.describe("walkthrough controls", () => {
  /** @type {any} */
  let app;
  test.afterEach(() => app?.kill());

  async function postTour(request, url) {
    const hunks = await (await request.get(`${url}/api/hunks`)).json();
    const idsFor = (path) =>
      hunks.files.find((f) => f.path === path).hunks.map((h) => h.id);
    return request.post(`${url}/api/walkthrough`, {
      data: {
        version: 1,
        title: "Tour",
        chapters: [
          {
            title: "One",
            stops: [
              { title: "First stop", prose: "a", hunk_ids: idsFor("main.go") },
              { title: "Second stop", prose: "b", hunk_ids: idsFor("notes.txt") },
            ],
          },
        ],
        commit: { subject: "The subject line" },
      },
    });
  }

  test("rail clicks, Previous, and ArrowLeft all navigate; commit copies", async ({
    page,
    request,
    context,
  }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    app = await launchDiff();
    expect((await postTour(request, app.url)).status()).toBe(201);
    await page.goto(app.url);
    await page.click("#walkthrough-btn");

    // Rail click to stop 2.
    await page.locator(".walk-stop", { hasText: "Second stop" }).click();
    await expect(page.locator(".walk-stop-head h2")).toHaveText("Second stop");
    await expect(page.locator(".walk-stop.current")).toHaveText(/Second stop/);

    // Previous button back to stop 1, ArrowRight forward, ArrowLeft back.
    await page.click("#walk-prev");
    await expect(page.locator(".walk-stop-head h2")).toHaveText("First stop");
    await page.keyboard.press("ArrowRight");
    await expect(page.locator(".walk-stop-head h2")).toHaveText("Second stop");
    await page.keyboard.press("ArrowLeft");
    await expect(page.locator(".walk-stop-head h2")).toHaveText("First stop");

    // Last stop: copy the suggested commit message.
    await page.keyboard.press("ArrowRight");
    await page.click("#walk-commit-copy");
    await expect(page.locator("#walk-commit-copy")).toHaveText("Copied ✓");
    expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(
      "The subject line"
    );
  });
});

test.describe("doc mode comment rendering", () => {
  /** @type {any} */
  let app;
  test.afterEach(() => app?.kill());

  test("comment bodies render markdown in doc mode too", async ({ page }) => {
    app = await launch();
    await page.goto(app.url);

    const block = page.locator('[data-block="0"]');
    await block.hover();
    await block.locator(".add-comment-btn").click();
    await page.fill('#form-slot-0 textarea[name="text"]', "needs *emphasis* and `code`");
    await page.click('#form-slot-0 button[type="submit"]');

    const body = page.locator(".comment .comment-text").first();
    await expect(body.locator("em")).toHaveText("emphasis");
    await expect(body.locator("code")).toHaveText("code");
  });
});
