// @ts-check
// Phase-1 codiff-parity features: markdown comments, copy review,
// whitespace toggle, viewed auto-collapse, resizable sidebar, banner latch.
const { test, expect } = require("@playwright/test");
const { launchDiff } = require("./helpers");

test.describe("quick wins", () => {
  /** @type {Awaited<ReturnType<typeof launchDiff>>} */
  let app;
  test.afterEach(() => app?.kill());

  test("comment bodies render markdown, raw HTML stays inert", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    const row = page.locator('.file[data-path="notes.txt"] tr.line.add').first();
    await row.evaluate((el) => el.scrollIntoView({ block: "center" }));
    await row.hover();
    await row.locator(".add-comment-btn").click();
    await page.fill(
      'tr.form-row textarea[name="text"]',
      "**bold claim** with `code` <script>window.pwned=1</script>"
    );
    await page.click('tr.form-row button[type="submit"]');

    const body = page.locator(".comment .comment-text").first();
    await expect(body.locator("strong")).toHaveText("bold claim");
    await expect(body.locator("code").first()).toHaveText("code");
    // Raw HTML is dropped by the restricted renderer and never executes.
    await expect(body.locator("script")).toHaveCount(0);
    expect(await page.evaluate(() => window.pwned)).toBeUndefined();
  });

  test("copy review puts the markdown export on the clipboard", async ({ page, context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    app = await launchDiff();
    await page.goto(app.url);

    const row = page.locator('.file[data-path="notes.txt"] tr.line.add').first();
    await row.evaluate((el) => el.scrollIntoView({ block: "center" }));
    await row.hover();
    await row.locator(".add-comment-btn").click();
    await page.fill('tr.form-row textarea[name="text"]', "exported note");
    await page.click('tr.form-row button[type="submit"]');
    await expect(page.locator(".comment", { hasText: "exported note" })).toBeVisible();

    await page.click("#copy-review-btn");
    await expect(page.locator("#copy-review-btn")).toHaveText("Copied ✓");
    const clip = await page.evaluate(() => navigator.clipboard.readText());
    expect(clip).toContain("planman review");
    expect(clip).toContain("exported note");
    expect(clip).toContain("notes.txt");
  });

  test("hide whitespace drops whitespace-only changes", async ({ page }) => {
    app = await launchDiff();
    app.repo.write("keep.txt", "unchanged   \n"); // trailing spaces only
    app.repo.git("add", "keep.txt");
    await page.goto(app.url);

    await expect(page.locator('.file[data-path="keep.txt"]')).toBeVisible();
    await page.click("#ws-toggle");
    await expect(page.locator("#ws-toggle")).toHaveClass(/active/);
    await expect(page.locator('.file[data-path="keep.txt"]')).toHaveCount(0);
    // Real changes stay visible.
    await expect(page.locator('.file[data-path="main.go"]')).toBeVisible();
  });

  test("viewed collapses the card and dims the tree entry", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    const card = page.locator('.file[data-path="main.go"]');
    await card.locator(".viewed-box").check();
    await expect(card).toHaveClass(/collapsed/);
    await expect(
      page.locator('.tree-file[data-path="main.go"]:not(.unchanged)')
    ).toHaveClass(/viewed/);

    await card.locator(".viewed-box").uncheck();
    await expect(card).not.toHaveClass(/collapsed/);
  });

  test("sidebar drags to a new width and persists across refresh", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    const handle = page.locator(".sidebar-handle");
    const sidebar = page.locator(".sidebar");
    const before = (await sidebar.boundingBox()).width;

    const box = await handle.boundingBox();
    await page.mouse.move(box.x + box.width / 2, box.y + 40);
    await page.mouse.down();
    await page.mouse.move(box.x + 120, box.y + 40, { steps: 4 });
    await page.mouse.up();

    const after = (await sidebar.boundingBox()).width;
    expect(after).toBeGreaterThan(before + 60);

    // Width survives a content refresh (it lives in localStorage).
    await page.click('button[data-preset="working"]');
    await expect(sidebar).toBeVisible();
    expect((await sidebar.boundingBox()).width).toBeCloseTo(after, 0);
  });

  test("refresh banner: dismiss latches until the next change", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    app.repo.write("notes.txt", "remember the milk\nand the eggs\n");
    const banner = page.locator("#refresh-banner");
    await expect(banner).toBeVisible({ timeout: 10_000 });

    await page.click("#refresh-banner-dismiss");
    await expect(banner).toBeHidden();

    // The next source change re-arms the banner.
    app.repo.write("notes.txt", "remember the milk\nand the eggs\nand butter\n");
    await expect(banner).toBeVisible({ timeout: 10_000 });
  });
});
