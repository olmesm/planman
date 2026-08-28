// @ts-check
// Find in diffs: server-side match list, dimming, and match cycling.
const { test, expect } = require("@playwright/test");
const { launchDiff } = require("./helpers");

test.describe("find in diffs", () => {
  /** @type {Awaited<ReturnType<typeof launchDiff>>} */
  let app;
  test.afterEach(() => app?.kill());

  test("Mod+F searches, dims non-matching files, cycles matches", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await page.keyboard.press("Control+f");
    await expect(page.locator("#search-bar")).toBeVisible();
    await page.fill("#search-input", "hello");

    // main.go matches (del "hello" + add "hello, planman"); notes.txt dims.
    await expect(page.locator("#search-count")).toContainText("match");
    await expect(page.locator('section.file[data-path="notes.txt"]')).toHaveClass(/search-dimmed/);
    await expect(page.locator('section.file[data-path="main.go"]')).not.toHaveClass(/search-dimmed/);

    await page.keyboard.press("Enter");
    await expect(page.locator(".search-hit")).toHaveCount(1);
    await expect(page.locator("#search-count")).toContainText("1 of");

    await page.keyboard.press("Enter");
    await expect(page.locator("#search-count")).toContainText("2 of");

    await page.keyboard.press("Shift+Enter");
    await expect(page.locator("#search-count")).toContainText("1 of");

    await page.keyboard.press("Escape");
    await expect(page.locator("#search-bar")).toBeHidden();
    await expect(page.locator(".search-dimmed")).toHaveCount(0);
  });

  test("filename-only matches keep the file undimmed", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await page.keyboard.press("Control+f");
    await page.fill("#search-input", "notes.txt");
    await expect(page.locator("#search-count")).toContainText("1 match");
    await expect(page.locator('section.file[data-path="notes.txt"]')).not.toHaveClass(/search-dimmed/);
    await expect(page.locator('section.file[data-path="main.go"]')).toHaveClass(/search-dimmed/);
  });

  test("navigating to a match inside a collapsed file expands it", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    // Collapse main.go, then search for its content.
    await page.locator('section.file[data-path="main.go"] .chevron').click();
    await expect(page.locator('section.file[data-path="main.go"]')).toHaveClass(/collapsed/);

    await page.keyboard.press("Control+f");
    await page.fill("#search-input", "planman");
    await expect(page.locator("#search-count")).toContainText("match");
    await page.keyboard.press("Enter");
    await expect(page.locator('section.file[data-path="main.go"]')).not.toHaveClass(/collapsed/);
    await expect(page.locator(".search-hit")).toBeVisible();
  });
});
