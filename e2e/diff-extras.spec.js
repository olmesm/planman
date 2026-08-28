// @ts-check
// Diff extras: rendered markdown previews and image before/after diffs.
const { test, expect } = require("@playwright/test");
const { launchDiff } = require("./helpers");

// Two tiny but distinct valid PNGs (1x1 red, 1x1 blue).
const PNG_RED = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64"
);
const PNG_BLUE = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPj/HwADAgH/p9UsmAAAAABJRU5ErkJggg==",
  "base64"
);

test.describe("diff extras", () => {
  /** @type {Awaited<ReturnType<typeof launchDiff>>} */
  let app;
  test.afterEach(() => app?.kill());

  test("markdown files toggle between diff and rendered preview", async ({ page }) => {
    app = await launchDiff();
    app.repo.write("PLAN.md", "# The Plan\n\nSome *prose* here.\n");
    await page.goto(app.url);

    const card = page.locator('section.file[data-path="PLAN.md"]');
    await expect(card.locator(".md-preview-btn")).toBeVisible();
    await card.locator(".md-preview-btn").click();

    await expect(card.locator(".md-preview h1")).toHaveText("The Plan");
    await expect(card.locator(".md-preview em")).toHaveText("prose");
    await expect(card.locator(".diff-table")).toBeHidden();

    // Toggling back restores the diff without a network round-trip.
    await card.locator(".md-preview-btn").click();
    await expect(card.locator(".diff-table")).toBeVisible();
    await expect(card.locator(".md-preview")).toBeHidden();

    // Non-markdown files get no preview button.
    await expect(
      page.locator('section.file[data-path="main.go"] .md-preview-btn')
    ).toHaveCount(0);
  });

  test("modified images show before and after", async ({ page }) => {
    app = await launchDiff();
    app.repo.write("logo.png", PNG_RED);
    app.repo.git("add", "logo.png");
    app.repo.git("commit", "-m", "add logo");
    app.repo.write("logo.png", PNG_BLUE);
    await page.goto(app.url);

    const card = page.locator('section.file[data-path="logo.png"]');
    const figures = card.locator(".image-diff figure");
    await expect(figures).toHaveCount(2);
    await expect(figures.nth(0).locator("figcaption")).toHaveText("Before");
    await expect(figures.nth(1).locator("figcaption")).toHaveText("After");
    for (const img of await card.locator(".image-diff img").all()) {
      await expect
        .poll(() => img.evaluate((el) => /** @type {HTMLImageElement} */ (el).naturalWidth))
        .toBeGreaterThan(0);
    }
  });

  test("new images show only the after side", async ({ page }) => {
    app = await launchDiff();
    app.repo.write("new.png", PNG_RED);
    await page.goto(app.url);

    const card = page.locator('section.file[data-path="new.png"]');
    await expect(card.locator(".image-diff figure")).toHaveCount(1);
    await expect(card.locator("figcaption")).toHaveText("After");
    const img = card.locator(".image-diff img");
    await expect
      .poll(() => img.evaluate((el) => /** @type {HTMLImageElement} */ (el).naturalWidth))
      .toBeGreaterThan(0);
  });
});
