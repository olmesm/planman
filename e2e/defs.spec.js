// @ts-check
// Definition navigation: Mod+click an identifier in the diff to get
// git-grep-backed definition candidates, click one to jump.
const { test, expect } = require("@playwright/test");
const { launchDiff } = require("./helpers");

test.describe("definition navigation", () => {
  /** @type {Awaited<ReturnType<typeof launchDiff>>} */
  let app;
  test.afterEach(() => app?.kill());

  function fixture() {
    // A definition in one file, a call site in the changed line of another.
    app.repo.write("lib.go", 'package main\n\nfunc Greet() string {\n\treturn "hi"\n}\n');
    app.repo.write(
      "main.go",
      'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println(Greet())\n}\n'
    );
  }

  test("Mod+click lists candidates and jumps to the definition", async ({ page }) => {
    app = await launchDiff();
    fixture();
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

    const pop = page.locator(".def-popover");
    await expect(pop).toBeVisible();
    await expect(pop.locator(".def-popover-head")).toHaveText("Greet");
    const candidate = pop.locator(".def-candidate", { hasText: "lib.go:3" });
    await expect(candidate).toBeVisible();
    await expect(candidate.locator(".def-kind")).toHaveText("function");

    await candidate.click();
    await expect(pop).toBeHidden();
    // lib.go is untracked, hence in the diff — its row gets the flash.
    await expect(
      page.locator('section.file[data-path="lib.go"] tr.flash')
    ).toHaveAttribute("data-line", "3");
  });

  test("identifiers without a definition report none found", async ({ page }) => {
    app = await launchDiff();
    fixture();
    await page.goto(app.url);

    const token = page
      .locator('section.file[data-path="main.go"] tr.line.add .code-text span', {
        hasText: /^Println$/,
      })
      .first();
    await token.scrollIntoViewIfNeeded();
    await page.keyboard.down("Control");
    await token.click();
    await page.keyboard.up("Control");

    await expect(page.locator(".def-popover-note")).toHaveText("No definition found.");
    await page.keyboard.press("Escape");
    await expect(page.locator(".def-popover")).toHaveCount(0);
  });

  test("plain clicks never open the popover", async ({ page }) => {
    app = await launchDiff();
    fixture();
    await page.goto(app.url);

    await page
      .locator('section.file[data-path="main.go"] tr.line.add .code-text span', {
        hasText: /^Greet$/,
      })
      .first()
      .click();
    await expect(page.locator(".def-popover")).toHaveCount(0);
  });
});
