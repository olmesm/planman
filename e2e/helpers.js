// @ts-check
const { spawn, execFileSync } = require("child_process");
const fs = require("fs");
const os = require("os");
const path = require("path");

const BIN = path.resolve(
  __dirname,
  "bin",
  process.platform === "win32" ? "planman.exe" : "planman"
);

const FIXTURE = `# Test Plan

Intro paragraph with ~~strike~~ text.

- [ ] first task
- [x] second task

| col1 | col2 |
|------|------|
| a    | b    |

\`\`\`go
package main

func main() {}
\`\`\`

\`\`\`mermaid
graph TD;
  Start-->End;
\`\`\`
`;

/**
 * Spawn a planman command and wait for its ready event.
 */
function spawnPlanman(args) {
  const proc = spawn(BIN, args, { stdio: ["ignore", "pipe", "pipe"] });

  const stdout = [];
  let stderr = "";
  proc.stdout.setEncoding("utf8");
  proc.stderr.setEncoding("utf8");
  proc.stderr.on("data", (d) => (stderr += d));

  let buffered = "";
  const events = [];
  proc.stdout.on("data", (d) => {
    buffered += d;
    let idx;
    while ((idx = buffered.indexOf("\n")) >= 0) {
      const line = buffered.slice(0, idx).trim();
      buffered = buffered.slice(idx + 1);
      if (line) {
        stdout.push(line);
        try {
          events.push(JSON.parse(line));
        } catch {
          /* non-JSON noise */
        }
      }
    }
  });

  const exited = new Promise((resolve) => {
    proc.on("exit", (code) => resolve(code));
  });

  const ready = new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error(`no ready event; stderr: ${stderr}`)),
      15_000
    );
    const poll = setInterval(() => {
      const ev = events.find((e) => e.event === "ready");
      if (ev) {
        clearTimeout(timer);
        clearInterval(poll);
        resolve(ev.url);
      }
    }, 50);
    exited.then((code) => {
      clearTimeout(timer);
      clearInterval(poll);
      reject(new Error(`planman exited early (${code}); stderr: ${stderr}`));
    });
  });

  return { proc, events, exited, ready };
}

/**
 * Launch a doc review on a fresh copy of the fixture (or given content).
 */
async function launch(opts = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "planman-e2e-"));
  const file = path.join(dir, "doc.md");
  fs.writeFileSync(file, opts.content ?? FIXTURE);

  const app = spawnPlanman([
    "open",
    file,
    "--json",
    "--no-browser",
    "--timeout",
    opts.timeout ?? "120s",
  ]);
  const url = await app.ready;

  return {
    ...app,
    url,
    file,
    read: () => fs.readFileSync(file, "utf8"),
    append: (text) => fs.appendFileSync(file, text),
    kill: () => {
      try {
        app.proc.kill("SIGKILL");
      } catch {
        /* already gone */
      }
    },
  };
}

/**
 * Build a git repo fixture: a base commit, a committed change on a
 * feature branch, a working-tree edit, and an untracked file.
 */
function makeRepo() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "planman-e2e-repo-"));
  const git = (...args) =>
    execFileSync("git", ["-C", dir, ...args], {
      env: {
        ...process.env,
        GIT_AUTHOR_NAME: "t",
        GIT_AUTHOR_EMAIL: "t@t",
        GIT_COMMITTER_NAME: "t",
        GIT_COMMITTER_EMAIL: "t@t",
      },
    });
  const write = (name, content) =>
    fs.writeFileSync(path.join(dir, name), content);

  git("init", "-b", "main");
  write(
    "main.go",
    'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("hello")\n}\n'
  );
  git("add", ".");
  git("commit", "-m", "base");
  git("checkout", "-b", "feature");
  write(
    "main.go",
    'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("hello, planman")\n}\n'
  );
  write("notes.txt", "remember the milk\n");
  return { dir, git, write };
}

/**
 * Launch a diff review over a fresh repo fixture.
 */
async function launchDiff(opts = {}) {
  const repo = opts.repo ?? makeRepo();
  const args = ["diff", repo.dir, "--json", "--no-browser", "--timeout", opts.timeout ?? "120s"];
  if (opts.scope) args.push("--scope", opts.scope);
  if (opts.base) args.push("--base", opts.base);
  const app = spawnPlanman(args);
  const url = await app.ready;

  return {
    ...app,
    url,
    repo,
    readStore: () => {
      const p = path.join(repo.dir, ".git", "planman", "review.json");
      return fs.existsSync(p) ? JSON.parse(fs.readFileSync(p, "utf8")) : null;
    },
    kill: () => {
      try {
        app.proc.kill("SIGKILL");
      } catch {
        /* already gone */
      }
    },
  };
}

module.exports = { launch, launchDiff, makeRepo, FIXTURE, BIN };
