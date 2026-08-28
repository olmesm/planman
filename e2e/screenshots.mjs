// Regenerates the README screenshots in docs/ against a demo repo.
// Run from e2e/ after `npm run pretest`:  node screenshots.mjs
import { execFileSync, spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "@playwright/test";

const here = path.dirname(fileURLToPath(import.meta.url));
const BIN = path.join(here, "bin", process.platform === "win32" ? "planman.exe" : "planman");
const DOCS = path.join(here, "..", "docs");

// --- Demo repo: a small Go service with a tagged release, a feature
// branch, and in-flight working tree edits.
function makeDemoRepo() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "planman-shots-"));
  const git = (...args) =>
    execFileSync("git", ["-C", dir, ...args], {
      env: {
        ...process.env,
        GIT_AUTHOR_NAME: "olie",
        GIT_AUTHOR_EMAIL: "olie@example.com",
        GIT_COMMITTER_NAME: "olie",
        GIT_COMMITTER_EMAIL: "olie@example.com",
      },
    });
  const write = (name, content) => {
    const p = path.join(dir, name);
    fs.mkdirSync(path.dirname(p), { recursive: true });
    fs.writeFileSync(p, content);
  };

  git("init", "-b", "main");
  write(
    "limiter/limiter.go",
    `package limiter

import (
	"sync"
	"time"
)

// Limiter is a simple token bucket.
type Limiter struct {
	mu     sync.Mutex
	tokens float64
	rate   float64
	last   time.Time
}

// New creates a limiter that refills at rate tokens per second.
func New(rate float64) *Limiter {
	return &Limiter{tokens: rate, rate: rate, last: time.Now()}
}

// Allow reports whether one request may proceed.
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refill()
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

func (l *Limiter) refill() {
	now := time.Now()
	l.tokens += now.Sub(l.last).Seconds() * l.rate
	if l.tokens > l.rate {
		l.tokens = l.rate
	}
	l.last = now
}
`
  );
  write(
    "server.go",
    `package main

import (
	"log"
	"net/http"
)

func main() {
	http.HandleFunc("/", handle)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handle(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}
`
  );
  write("README.md", "# ratelimitd\n\nA tiny rate-limited HTTP service.\n");
  git("add", ".");
  git("commit", "-m", "Initial service with a token-bucket limiter");
  git("tag", "v1.0.0");

  git("checkout", "-b", "burst-support");
  write(
    "limiter/limiter.go",
    `package limiter

import (
	"sync"
	"time"
)

// Limiter is a token bucket with a configurable burst.
type Limiter struct {
	mu     sync.Mutex
	tokens float64
	rate   float64
	burst  float64
	last   time.Time
}

// New creates a limiter that refills at rate tokens per second and
// allows bursts up to burst tokens.
func New(rate, burst float64) *Limiter {
	return &Limiter{tokens: burst, rate: rate, burst: burst, last: time.Now()}
}

// Allow reports whether one request may proceed.
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refill()
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

func (l *Limiter) refill() {
	now := time.Now()
	l.tokens += now.Sub(l.last).Seconds() * l.rate
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	l.last = now
}
`
  );
  git("commit", "-am", "Let the bucket hold a configurable burst");

  // Working tree: wire the limiter into the server, plus notes.
  write(
    "server.go",
    `package main

import (
	"log"
	"net/http"

	"ratelimitd/limiter"
)

var limit = limiter.New(50, 100)

func main() {
	http.HandleFunc("/", handle)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handle(w http.ResponseWriter, r *http.Request) {
	if !limit.Allow() {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	w.Write([]byte("ok"))
}
`
  );
  write("docs/limits.md", "# Limits\n\nDefaults: 50 rps, burst 100.\n");
  return { dir, git, write };
}

const DEMO_PLAN = `# Rate limiting rollout plan

Add a token-bucket limiter in front of every public endpoint before the
launch. Defaults stay generous; ops can tighten them per route later.

## Rollout

\`\`\`mermaid
graph LR;
  Dev --> Staging;
  Staging --> Canary;
  Canary --> Production;
\`\`\`

## Defaults

| route | rate | burst |
|-------|------|-------|
| /api  | 50/s | 100   |
| /auth | 5/s  | 10    |

## Tasks

- [x] Implement the token bucket
- [ ] Wire it into the router
- [ ] Emit a metric on every rejected request

\`\`\`go
func handle(w http.ResponseWriter, r *http.Request) {
	if !limit.Allow() {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
}
\`\`\`
`;

function launch(args) {
  const proc = spawn(BIN, [...args, "--json", "--no-browser", "--timeout", "5m"], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  proc.stdout.setEncoding("utf8");
  return {
    proc,
    ready: new Promise((resolve, reject) => {
      let buf = "";
      const timer = setTimeout(() => reject(new Error("no ready event")), 15000);
      proc.stdout.on("data", (d) => {
        buf += d;
        for (const line of buf.split("\n")) {
          try {
            const ev = JSON.parse(line);
            if (ev.event === "ready") {
              clearTimeout(timer);
              resolve(ev.url);
            }
          } catch {}
        }
      });
    }),
    kill: () => proc.kill("SIGKILL"),
  };
}

async function newPage(browser, width, height, mode) {
  const context = await browser.newContext({
    viewport: { width, height },
    deviceScaleFactor: 2,
    colorScheme: mode === "dark" ? "dark" : "light",
  });
  return context.newPage();
}

async function addLineComment(page, row, text) {
  await row.evaluate((el) => el.scrollIntoView({ block: "center" }));
  await row.hover();
  await row.locator(".add-comment-btn").first().click();
  await page.fill("tr.form-row textarea", text);
  await page.click('tr.form-row button[type="submit"]');
  await page.waitForSelector("tr.form-row textarea", { state: "detached" });
}

// Same browser resolution as playwright.config.js: prefer the sandbox's
// pre-installed Chromium, fall back to playwright's own registry.
const preinstalled = "/opt/pw-browsers/chromium";
const executablePath =
  process.env.PLANMAN_CHROMIUM || (fs.existsSync(preinstalled) ? preinstalled : undefined);
const browser = await chromium.launch(executablePath ? { executablePath } : {});
try {
  // --- Diff-mode shots ---
  const repo = makeDemoRepo();
  const diffApp = launch(["diff", repo.dir]);
  const diffURL = await diffApp.ready;

  {
    const page = await newPage(browser, 1500, 900, "light");
    await page.goto(diffURL + "?mode=light");
    await page.fill("#author", "olie");

    // A range comment on the server wiring and a reply, so the comment
    // stack and thread chrome show up.
    await addLineComment(
      page,
      page
        .locator('.file[data-path="server.go"] tr.line.add', { hasText: "rate limited" })
        .first(),
      "Guard the health endpoint too? `/healthz` should never be limited."
    );
    await page
      .locator(".comment .reply-form input")
      .first()
      .fill("Good catch — splitting the mux next.");
    await page.locator(".comment .reply-form button").first().click();
    await page.waitForSelector(".comment .reply");

    // Compare the working tree against the tagged release, panel open.
    await page.click("#history-toggle");
    await page.waitForSelector("#history-panel:not([hidden])");
    await page
      .locator(".hist-row", { has: page.locator(".ref-pill", { hasText: "v1.0.0" }) })
      .click({ modifiers: ["Shift"] });
    await page.waitForSelector(".hist-row.sel-base .ref-pill:has-text('v1.0.0')");
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({ path: path.join(DOCS, "diff.png") });
    await page.context().close();
    console.log("wrote docs/diff.png");
  }

  {
    const page = await newPage(browser, 1500, 900, "dark");
    await page.goto(diffURL + "?mode=dark&view=split&base=v1.0.0&mb=0");
    await page.waitForSelector(".diff-table.split");
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({ path: path.join(DOCS, "split.png") });
    await page.context().close();
    console.log("wrote docs/split.png");
  }

  {
    // Walkthrough: author a short tour over the live diff via the API.
    const page = await newPage(browser, 1500, 900, "light");
    await page.goto(diffURL + "?mode=light&view=unified");
    const hunks = await (await page.request.get(diffURL + "/api/hunks")).json();
    const idsFor = (p) => hunks.files.find((f) => f.path === p).hunks.map((h) => h.id);
    await page.request.post(diffURL + "/api/walkthrough", {
      data: {
        version: 1,
        title: "Rate limiting rollout",
        focus: "Check the limiter defaults before shipping",
        chapters: [
          {
            title: "Core change",
            icon: "⚙️",
            stops: [
              {
                title: "Every request passes the limiter",
                importance: "critical",
                prose:
                  "The handler now consults the **token bucket** before doing any work. Rejections return `429 Too Many Requests` so clients can back off.",
                hunk_ids: idsFor("server.go"),
              },
            ],
          },
          {
            title: "Documentation",
            icon: "📝",
            stops: [
              {
                title: "Defaults are written down",
                importance: "context",
                prose: "Ops gets a one-pager with the default rate and burst.",
                hunk_ids: idsFor("docs/limits.md"),
              },
            ],
          },
        ],
        commit: { subject: "Wire the token-bucket limiter into the server" },
      },
    });
    await page.reload();
    await page.click("#walkthrough-btn");
    await page.waitForSelector(".walk-stop-head h2");
    await page.screenshot({ path: path.join(DOCS, "walkthrough.png") });
    await page.context().close();
    console.log("wrote docs/walkthrough.png");
  }

  diffApp.kill();

  // --- Doc-mode shots ---
  const planDir = fs.mkdtempSync(path.join(os.tmpdir(), "planman-shots-doc-"));
  const planFile = path.join(planDir, "plan.md");
  fs.writeFileSync(planFile, DEMO_PLAN);
  const docApp = launch(["open", planFile]);
  const docURL = await docApp.ready;

  {
    const page = await newPage(browser, 1280, 1080, "light");
    await page.goto(docURL + "?mode=light");
    await page.fill("#author", "olie");

    // Comment on the tasks list, with a reply.
    const block = page.locator(".block", { hasText: "Emit a metric" }).last();
    await block.hover();
    await block.locator(".add-comment-btn").click();
    await page.fill(".form-slot textarea", "Which metric name? Use `ratelimit_rejected_total`.");
    await page.click('.form-slot button[type="submit"]');
    await page.waitForSelector(".comment");
    await page.locator(".comment .reply-form input").fill("Done — added to the task.");
    await page.locator(".comment .reply-form button").click();
    await page.waitForSelector(".comment .reply");
    await page.waitForSelector(".mermaid svg");
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({ path: path.join(DOCS, "review.png") });
    await page.context().close();
    console.log("wrote docs/review.png");
  }

  docApp.kill();

  {
    // A fresh document for the comment-form shot, so the review thread
    // from the previous capture stays out of frame.
    const dir2 = fs.mkdtempSync(path.join(os.tmpdir(), "planman-shots-doc2-"));
    const file2 = path.join(dir2, "plan.md");
    fs.writeFileSync(file2, DEMO_PLAN);
    const docApp2 = launch(["open", file2]);
    const url2 = await docApp2.ready;

    const page = await newPage(browser, 1280, 560, "light");
    await page.goto(url2 + "?mode=light");
    await page.fill("#author", "olie");
    const code = page.locator(".block", { hasText: "func handle" }).last();
    await code.evaluate((el) => el.scrollIntoView({ block: "start" }));
    await code.hover();
    await code.locator(".add-comment-btn").click();
    await page.fill(".form-slot textarea", "Log the client IP when a request is rejected?");
    await code.evaluate((el) => el.scrollIntoView({ block: "start" }));
    await page.evaluate(() => window.scrollBy(0, -70));
    await page.screenshot({ path: path.join(DOCS, "comment.png") });
    await page.context().close();
    docApp2.kill();
    console.log("wrote docs/comment.png");
  }
} finally {
  await browser.close();
}
