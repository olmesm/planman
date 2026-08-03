// @ts-check
const { spawn } = require("child_process");
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
 * Launch a planman instance on a fresh copy of the fixture (or given
 * content). Resolves with the URL once the server reports ready.
 */
async function launch(opts = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "planman-e2e-"));
  const file = path.join(dir, "doc.md");
  fs.writeFileSync(file, opts.content ?? FIXTURE);

  const args = [
    "open",
    file,
    "--json",
    "--no-browser",
    "--timeout",
    opts.timeout ?? "120s",
  ];
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

  const url = await new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error(`no ready event; stderr: ${stderr}`)),
      15_000
    );
    const poll = setInterval(() => {
      const ready = events.find((e) => e.event === "ready");
      if (ready) {
        clearTimeout(timer);
        clearInterval(poll);
        resolve(ready.url);
      }
    }, 50);
    exited.then((code) => {
      clearTimeout(timer);
      clearInterval(poll);
      reject(new Error(`planman exited early (${code}); stderr: ${stderr}`));
    });
  });

  return {
    proc,
    url,
    file,
    events,
    exited,
    read: () => fs.readFileSync(file, "utf8"),
    append: (text) => fs.appendFileSync(file, text),
    kill: () => {
      try {
        proc.kill("SIGKILL");
      } catch {
        /* already gone */
      }
    },
  };
}

module.exports = { launch, FIXTURE, BIN };
