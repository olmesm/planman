# planman

Markdown reviews for coding agents — a self-contained, single-binary port of
the [roughdraft](https://roughdraft.md) idea in Go.

An agent (or you) runs `planman open plan.md`; a browser opens with the
rendered document in an always-on comment mode. You leave comments on
individual blocks or on the whole page, then hit **Hand back to agent**. The
process exits, and every comment is sitting *inside the markdown file* as
[CriticMarkup](https://github.com/CriticMarkup/CriticMarkup-toolkit) for the
agent to read — no database, no cloud, no auth, no CDN.

```
┌──────────┐  planman open plan.md --json   ┌─────────┐
│  agent   │ ──────────────────────────────▶│ planman │──▶ browser review UI
│ (blocks) │ ◀────────────────────────────── │  (Go)   │◀── comments → plan.md
└──────────┘   {"event":"handback", ...}     └─────────┘
```

![The review UI: a rendered plan with an inline comment thread and a mermaid diagram](docs/review.png)

## Install

Grab a binary from [Releases](../../releases) — Linux, macOS, and Windows,
amd64 and arm64. It is fully self-contained (htmx, mermaid, styles are
embedded); nothing is fetched from the network at runtime.

Or build from source: `go build .`

## Usage

```
planman open <file.md> [flags]

  --json           Emit machine-readable JSON events on stdout
  --timeout DUR    Give up after DUR (default 30m)
  --port N         Listen on a fixed port (default: ephemeral)
  --no-browser     Don't open the browser automatically
```

The command **blocks** until one of:

| outcome     | exit code | JSON event                                     |
|-------------|-----------|------------------------------------------------|
| handed back | 0         | `{"event":"handback","comments_added":2,...}`  |
| timeout     | 2         | `{"event":"timeout",...}`                      |
| interrupted | 130       | `{"event":"interrupted",...}`                  |

On start it prints `{"event":"ready","url":"http://127.0.0.1:PORT"}` (with
`--json`) so callers can find the UI.

### Agent workflow

Tell your coding agent something like:

> After writing plan.md, run `planman open plan.md --json` and wait for it to
> exit. Then re-read plan.md — review comments appear inline as
> `{>>comment<<}{id="..."}` markers next to the blocks they refer to, with
> authors, timestamps, and reply threads in the `planman:comments` endmatter
> at the bottom of the file.

### Editing

There is no WYSIWYG — edit the file in your normal editor. The **Open in
editor** button launches `$PLANMAN_EDITOR`, `$VISUAL`, or `$EDITOR` (falling
back to the OS default handler). Terminal editors can't be spawned from the
browser; set a GUI editor like `code` for the button, or just have the file
open in your editor already. The page live-reloads on every save.

## What renders

- GitHub Flavored Markdown: tables, task lists, strikethrough, autolinks
- Syntax-highlighted code blocks (server-side via chroma — ~200 languages)
- Mermaid diagrams (embedded mermaid.js, rendered in the browser)
- Raw inline HTML

## Comment format

Hover any block, hit **+**, and write:

![Adding a comment to a syntax-highlighted code block](docs/comment.png)

Block comments live in the file, next to their block:

```markdown
Some paragraph that got reviewed.

{>>this needs a source<<}{id="c1a2b"}
```

Thread metadata (and page-level comments) live in a YAML endmatter section
wrapped in an HTML comment, so ordinary renderers ignore it:

```markdown
<!-- planman:comments
comments:
    - id: c1a2b
      author: reviewer
      ts: 2026-08-03T12:00:00Z
      replies:
        - author: agent
          ts: 2026-08-03T12:05:00Z
          text: fixed in the next revision
-->
```

Suggested edits are deliberately out of scope for now; CriticMarkup already
defines `{++add++}`, `{--del--}`, and `{~~old~>new~~}` for when we want them.

## Security posture

No auth by design — planman binds to `127.0.0.1` only and is meant for local
use. It rejects requests with non-loopback `Host` or cross-origin `Origin`
headers (DNS-rebinding/CSRF hardening) and serves everything with a
same-origin Content-Security-Policy.

## Development

```sh
go test ./...        # unit tests (comment format, rendering)
cd e2e && npm ci && npm test   # Playwright end-to-end tests
```

Releases are cut by pushing a `v*` tag; GitHub Actions runs GoReleaser to
build all six platform binaries and attach them to the release.
