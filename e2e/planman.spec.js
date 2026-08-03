// @ts-check
const { test, expect } = require("@playwright/test");
const { launch } = require("./helpers");

test.describe("planman review flow", () => {
  /** @type {Awaited<ReturnType<typeof launch>>} */
  let app;

  test.afterEach(() => {
    if (app) app.kill();
  });

  test("renders GFM: headings, strikethrough, task list, tables", async ({ page }) => {
    app = await launch();
    await page.goto(app.url);
    await expect(page.locator("h1", { hasText: "Test Plan" })).toBeVisible();
    await expect(page.locator("del", { hasText: "strike" })).toBeVisible();
    await expect(page.locator('input[type="checkbox"]')).toHaveCount(2);
    await expect(page.locator('input[type="checkbox"]').nth(1)).toBeChecked();
    await expect(page.locator("table td", { hasText: "a" })).toBeVisible();
  });

  test("code blocks are syntax highlighted server-side", async ({ page }) => {
    app = await launch();
    await page.goto(app.url);
    const highlight = page.locator('.highlight[data-lang="go"]');
    await expect(highlight).toBeVisible();
    // chroma emits inline-styled spans; the keyword "package" gets a color
    const html = await highlight.innerHTML();
    expect(html).toMatch(/<span style="color:[^"]+">package<\/span>/);
  });

  test("mermaid diagrams render to SVG in the browser", async ({ page }) => {
    app = await launch();
    await page.goto(app.url);
    await expect(page.locator("pre.mermaid svg")).toBeVisible({ timeout: 15_000 });
  });

  test("block comment: add, persists as CriticMarkup, survives reload", async ({ page }) => {
    app = await launch();
    await page.goto(app.url);
    await page.fill("#author", "reviewer");

    const block = page.locator('[data-block="1"]'); // intro paragraph
    await block.hover();
    await block.locator(".add-comment-btn").click();
    await page.fill('#form-slot-1 textarea[name="text"]', "tighten this intro");
    await page.click('#form-slot-1 button[type="submit"]');

    const comment = page.locator(".comment", { hasText: "tighten this intro" });
    await expect(comment).toBeVisible();
    await expect(comment.locator(".comment-author")).toHaveText("reviewer");

    const md = app.read();
    expect(md).toContain('{>>tighten this intro<<}{id="');
    expect(md).toContain("planman:comments");
    expect(md).toContain("author: reviewer");

    await page.reload();
    await expect(page.locator(".comment", { hasText: "tighten this intro" })).toBeVisible();
  });

  test("threading: reply to a comment persists in endmatter", async ({ page }) => {
    app = await launch();
    await page.goto(app.url);
    await page.fill("#author", "reviewer");

    const block = page.locator('[data-block="1"]');
    await block.hover();
    await block.locator(".add-comment-btn").click();
    await page.fill('#form-slot-1 textarea[name="text"]', "root comment");
    await page.click('#form-slot-1 button[type="submit"]');
    await expect(page.locator(".comment", { hasText: "root comment" })).toBeVisible();

    await page.fill("#author", "agent");
    const reply = page.locator(".comment .reply-form input[name='text']");
    await reply.fill("on it");
    await reply.press("Enter");

    const replyCard = page.locator(".reply", { hasText: "on it" });
    await expect(replyCard).toBeVisible();
    await expect(replyCard.locator(".comment-author")).toHaveText("agent");

    const md = app.read();
    expect(md).toContain("replies:");
    expect(md).toContain("text: on it");
  });

  test("delete removes the thread from UI and file", async ({ page }) => {
    app = await launch();
    await page.goto(app.url);

    const block = page.locator('[data-block="1"]');
    await block.hover();
    await block.locator(".add-comment-btn").click();
    await page.fill('#form-slot-1 textarea[name="text"]', "temp note");
    await page.click('#form-slot-1 button[type="submit"]');
    await expect(page.locator(".comment", { hasText: "temp note" })).toBeVisible();

    await page.click(".comment .delete-btn");
    await expect(page.locator(".comment")).toHaveCount(0);

    const md = app.read();
    expect(md).not.toContain("{>>");
    expect(md).not.toContain("planman:comments");
  });

  test("page comment lives in endmatter only, no inline marker", async ({ page }) => {
    app = await launch();
    await page.goto(app.url);
    await page.fill("#author", "reviewer");

    await page.click("#page-comment-btn");
    await page.fill('#page-form-slot textarea[name="text"]', "overall: solid plan");
    await page.click('#page-form-slot button[type="submit"]');

    const comment = page.locator("#page-comments .comment", { hasText: "overall: solid plan" });
    await expect(comment).toBeVisible();

    const md = app.read();
    expect(md).toContain("page: true");
    expect(md).toContain("text: 'overall: solid plan'");
    expect(md).not.toContain("{>>overall");
  });

  test("live reload: external edits appear without user refresh", async ({ page }) => {
    app = await launch();
    await page.goto(app.url);
    await expect(page.locator("h1", { hasText: "Test Plan" })).toBeVisible();

    app.append("\n## Appended Externally\n");
    await expect(page.locator("h2", { hasText: "Appended Externally" })).toBeVisible({
      timeout: 10_000,
    });
  });

  test("handback: button ends the process with exit 0 and a JSON event", async ({ page }) => {
    app = await launch();
    await page.goto(app.url);

    const block = page.locator('[data-block="0"]');
    await block.hover();
    await block.locator(".add-comment-btn").click();
    await page.fill('#form-slot-0 textarea[name="text"]', "retitle this");
    await page.click('#form-slot-0 button[type="submit"]');
    await expect(page.locator(".comment", { hasText: "retitle this" })).toBeVisible();

    await page.click("#handback-btn");
    await expect(page.locator(".handback-done")).toBeVisible();

    const code = await app.exited;
    expect(code).toBe(0);
    const done = app.events.find((e) => e.event === "handback");
    expect(done).toBeTruthy();
    expect(done.comments_added).toBe(1);
    expect(done.comments_total).toBe(1);
  });

  test("timeout: process exits 2 with a timeout event", async () => {
    app = await launch({ timeout: "2s" });
    const code = await app.exited;
    expect(code).toBe(2);
    expect(app.events.find((e) => e.event === "timeout")).toBeTruthy();
  });
});
