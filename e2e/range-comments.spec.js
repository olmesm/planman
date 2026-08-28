// @ts-check
// Multi-line range comments: gutter drag, Enter-on-hunk ranges,
// persistence, and range labels.
const { test, expect } = require("@playwright/test");
const { launchDiff } = require("./helpers");

async function dragGutter(page, file, fromLine, toLine) {
  const row = (n) =>
    page.locator(`section.file[data-path="${file}"] tr.line[data-line="${n}"] td.num`).last();
  const from = await row(fromLine).boundingBox();
  const to = await row(toLine).boundingBox();
  await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
  await page.mouse.down();
  await page.mouse.move(to.x + to.width / 2, to.y + to.height / 2, { steps: 4 });
  await page.mouse.up();
}

test.describe("range comments", () => {
  /** @type {Awaited<ReturnType<typeof launchDiff>>} */
  let app;
  test.afterEach(() => app?.kill());

  test("gutter drag selects a span; the thread persists with start_line", async ({ page, request }) => {
    app = await launchDiff();
    app.repo.write("multi.txt", "alpha\nbravo\ncharlie\ndelta\necho\n");
    await page.goto(app.url);

    await dragGutter(page, "multi.txt", 2, 4);
    const form = page.locator("tr.form-row textarea");
    await expect(form).toBeVisible();
    await expect(form).toHaveAttribute("placeholder", /lines 2–4/);

    await form.fill("this whole span needs a rethink");
    await page.click('tr.form-row button[type="submit"]');
    const thread = page.locator(".comment", { hasText: "this whole span needs a rethink" });
    await expect(thread).toBeVisible();
    await expect(thread.locator(".range-lines")).toHaveText("lines 2–4");

    // The covered rows are tinted, and the thread sits under the end line.
    const file = page.locator('section.file[data-path="multi.txt"]');
    await expect(file.locator("tr.line.in-range")).toHaveCount(3);
    await expect(
      file.locator('tr.line[data-line="4"] + tr.thread-row .comment')
    ).toBeVisible();

    // Stack label and stored anchor carry the range.
    await expect(page.locator(".stack-entry .stack-meta")).toContainText("multi.txt:2–4");
    const api = await (await request.get(`${app.url}/api/comments`)).json();
    expect(api.comments[0].anchor.start_line).toBe(2);
    expect(api.comments[0].anchor.line).toBe(4);

    // Survives a reload.
    await page.reload();
    await expect(
      page.locator('section.file[data-path="multi.txt"] tr.line.in-range')
    ).toHaveCount(3);
  });

  test("Enter on a hunk selects its changed run as a range", async ({ page }) => {
    app = await launchDiff();
    app.repo.write("multi.txt", "one\ntwo\nthree\n");
    await page.goto(app.url);

    // Walk to multi.txt's hunk (files are path-sorted: keep… main.go,
    // multi.txt, notes.txt — hunks: main.go, multi.txt, notes.txt).
    await page.keyboard.press("j");
    await page.keyboard.press("j");
    await expect(page.locator("tr.hunk-row.kb-target")).toHaveAttribute(
      "data-hunk-id",
      "multi.txt:h1"
    );
    await page.keyboard.press("Enter");
    const form = page.locator("tr.form-row textarea");
    await expect(form).toBeVisible();
    await expect(form).toHaveAttribute("placeholder", /lines 1–3/);
  });

  test("a plain gutter click does not open a range form", async ({ page }) => {
    app = await launchDiff();
    app.repo.write("multi.txt", "one\ntwo\nthree\n");
    await page.goto(app.url);

    const cell = page
      .locator('section.file[data-path="multi.txt"] tr.line[data-line="2"] td.num')
      .last();
    await cell.click();
    await expect(page.locator("tr.form-row")).toHaveCount(0);
  });
});
