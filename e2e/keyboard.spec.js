// @ts-check
// Keyboard UX: hunk navigation, Enter-to-comment, hold-? help, fuzzy
// file filter (Mod+P), and the command palette (Mod+Shift+P).
const { test, expect } = require("@playwright/test");
const { launchDiff } = require("./helpers");

test.describe("keyboard", () => {
  /** @type {Awaited<ReturnType<typeof launchDiff>>} */
  let app;
  test.afterEach(() => app?.kill());

  test("j/k walk hunks, Enter opens a comment on the current hunk", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await page.keyboard.press("j");
    const target = page.locator("tr.hunk-row.kb-target");
    await expect(target).toHaveCount(1);
    const first = await target.getAttribute("data-hunk-id");

    await page.keyboard.press("j");
    const second = await page.locator("tr.hunk-row.kb-target").getAttribute("data-hunk-id");
    expect(second).not.toBe(first);

    await page.keyboard.press("k");
    await expect(page.locator("tr.hunk-row.kb-target")).toHaveAttribute("data-hunk-id", first);

    await page.keyboard.press("Enter");
    await expect(page.locator("tr.form-row textarea")).toBeVisible();
    await expect(page.locator("tr.form-row textarea")).toBeFocused();
  });

  test("holding ? shows the shortcut overlay, releasing hides it", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await page.keyboard.down("Shift");
    await page.keyboard.down("Slash");
    await expect(page.locator("#kbd-help")).toBeVisible();
    await page.keyboard.up("Slash");
    await page.keyboard.up("Shift");
    await expect(page.locator("#kbd-help")).toBeHidden();
  });

  test("Mod+P filters files by subsequence and jumps on Enter", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await page.keyboard.press("Control+p");
    await expect(page.locator("#palette")).toBeVisible();
    await page.fill("#palette-input", "ntstxt"); // subsequence of notes.txt
    await expect(page.locator(".palette-item")).toHaveCount(1);
    await expect(page.locator(".palette-item")).toContainText("notes.txt");
    await page.keyboard.press("Enter");
    await expect(page.locator("#palette-backdrop")).toBeHidden();
  });

  test("command palette runs commands", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await page.keyboard.press("Control+Shift+p");
    await expect(page.locator("#palette")).toBeVisible();
    await page.fill("#palette-input", "view split");
    await page.keyboard.press("Enter");
    await expect(page.locator(".diff-table.split").first()).toBeVisible();

    // And back to unified via the palette again.
    await page.keyboard.press("Control+Shift+p");
    await page.fill("#palette-input", "view unified");
    await page.keyboard.press("Enter");
    await expect(page.locator(".diff-table.unified").first()).toBeVisible();
  });

  test("v toggles viewed on the current file", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await page.keyboard.press("j"); // target a hunk so a file is current
    const file = page.locator("section.file", {
      has: page.locator("tr.hunk-row.kb-target"),
    });
    const path = await file.getAttribute("data-path");
    await page.keyboard.press("v");
    await expect(
      page.locator(`section.file[data-path="${path}"] .viewed-box`)
    ).toBeChecked();
  });

  test("history panel keeps exclusive j/k while open", async ({ page }) => {
    app = await launchDiff();
    await page.goto(app.url);

    await page.click("#history-toggle");
    await expect(page.locator("#history-panel")).toBeVisible();
    await page.keyboard.press("j");
    await expect(page.locator(".hist-row.kb-cursor")).toHaveCount(1);
    await expect(page.locator("tr.hunk-row.kb-target")).toHaveCount(0);
  });
});
