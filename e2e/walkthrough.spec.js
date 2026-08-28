// @ts-check
// Narrative walkthroughs: agent API round-trip, the stepper UI, and
// drift degradation.
const { test, expect } = require("@playwright/test");
const { launchDiff } = require("./helpers");

async function postWalkthrough(request, url, overrides = {}) {
  const hunks = await (await request.get(`${url}/api/hunks`)).json();
  const idsFor = (path) =>
    hunks.files.find((f) => f.path === path).hunks.map((h) => h.id);
  const body = {
    version: 1,
    title: "Greeting overhaul",
    focus: "Watch the punctuation",
    chapters: [
      {
        title: "Core",
        icon: "⚙️",
        stops: [
          {
            title: "The new greeting",
            importance: "critical",
            prose: "The greeting now names **planman** itself.",
            hunk_ids: idsFor("main.go"),
          },
        ],
      },
      {
        title: "Notes",
        stops: [
          {
            title: "A reminder file",
            importance: "context",
            prose: "Just a scratch note.",
            hunk_ids: idsFor("notes.txt"),
          },
        ],
      },
    ],
    commit: { subject: "Rework the greeting" },
    ...overrides,
  };
  return request.post(`${url}/api/walkthrough`, { data: body });
}

test.describe("walkthrough", () => {
  /** @type {Awaited<ReturnType<typeof launchDiff>>} */
  let app;
  test.afterEach(() => app?.kill());

  test("agent posts a tour; reviewer steps through it", async ({ page, request }) => {
    app = await launchDiff();
    const res = await postWalkthrough(request, app.url);
    expect(res.status()).toBe(201);

    const health = await (await request.get(`${app.url}/healthz`)).json();
    expect(health.walkthrough).toBe(true);

    await page.goto(app.url);
    await page.click("#walkthrough-btn");

    // Stop 1: title, prose (markdown rendered), and main.go's live hunk.
    await expect(page.locator(".walk-title")).toHaveText("Greeting overhaul");
    await expect(page.locator(".walk-stop-head h2")).toHaveText("The new greeting");
    await expect(page.locator(".walk-prose strong")).toHaveText("planman");
    await expect(page.locator('.walk-card[data-path="main.go"] tr.line.add')).toBeVisible();
    await expect(page.locator(".walk-progress")).toHaveText("stop 1 of 2");

    // Arrow key advances to stop 2.
    await page.keyboard.press("ArrowRight");
    await expect(page.locator(".walk-stop-head h2")).toHaveText("A reminder file");
    await expect(page.locator('.walk-card[data-path="notes.txt"]')).toBeVisible();
    // Last stop shows the commit suggestion.
    await expect(page.locator(".walk-commit pre")).toContainText("Rework the greeting");

    // Commenting works on walkthrough hunks (threads render inline).
    const row = page.locator('.walk-card[data-path="notes.txt"] tr.line.add').first();
    await row.hover();
    await row.locator(".add-comment-btn").click();
    await page.fill('tr.form-row textarea[name="text"]', "tour comment");
    await page.click('tr.form-row button[type="submit"]');
    // The comment POST returns the files view; the client restores the tour.
    await expect(page.locator(".walk-stop-head h2")).toHaveText("A reminder file");
    await expect(page.locator(".comment", { hasText: "tour comment" })).toBeVisible();

    // Back to the diff.
    await page.click(".walk-exit");
    await expect(page.locator(".diff-toolbar")).toBeVisible();
  });

  test("unknown hunk ids are rejected with a 422 listing them", async ({ request }) => {
    app = await launchDiff();
    const res = await postWalkthrough(request, app.url, {
      chapters: [
        {
          title: "Broken",
          stops: [{ title: "Nope", prose: "x", hunk_ids: ["ghost.go:h9"] }],
        },
      ],
    });
    expect(res.status()).toBe(422);
    const body = await res.json();
    expect(body.unknown_hunk_ids).toEqual(["ghost.go:h9"]);
  });

  test("invalid schema is rejected", async ({ request }) => {
    app = await launchDiff();
    const res = await request.post(`${app.url}/api/walkthrough`, {
      data: { version: 1, title: "", chapters: [] },
    });
    expect(res.status()).toBe(422);
  });

  test("editing the repo marks the tour outdated; missing hunks degrade", async ({ page, request }) => {
    app = await launchDiff();
    expect((await postWalkthrough(request, app.url)).status()).toBe(201);

    // Change the referenced files so fingerprints (and hunk shapes) drift.
    app.repo.write("main.go", 'package main\n\nfunc main() {}\n');
    app.repo.write("notes.txt", "remember the milk\nplus more lines\nand more\n");

    await page.goto(app.url);
    await page.click("#walkthrough-btn");
    await expect(page.locator(".walk-outdated")).toBeVisible();
    // Stops still render their prose against the live diff.
    await expect(page.locator(".walk-stop-head h2")).toHaveText("The new greeting");

    // Delete clears the tour and the chip disappears on refresh.
    await request.delete(`${app.url}/api/walkthrough`);
    await page.click(".walk-exit");
    await expect(page.locator("#walkthrough-btn")).toHaveCount(0);
  });
});
