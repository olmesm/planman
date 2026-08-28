---
name: planman
description: Work review comments from a running planman server — find it, read the open threads, fix each one, resolve it, and reply where a decision needs explaining. Use when the user says to check planman, address review comments, or after they reviewed your changes in planman.
---

# Working a planman review

planman is a local review server. The reviewer has left comments on a
diff of this repository (or on a markdown document); your job is to work
them one at a time and mark each resolved as you go. The reviewer is
watching live — the page updates as you resolve and reply.

## 1. Find the server

Scan ports 7350-7359 on 127.0.0.1 and GET `/healthz` on each. A planman
server answers with JSON like:

```json
{"app":"planman","mode":"diff","root":"/abs/path/to/repo","base":"main","head":"@worktree"}
```

Match `root` against this repository's root (`git rev-parse
--show-toplevel`). Doc-mode servers report `"mode":"doc"` and a `file`
path instead — match that file if you were asked about a document
review. If nothing matches, tell the user you couldn't find a running
planman server and ask them to start one with `planman diff --stay`.

Call the matched server `$P` (e.g. `http://127.0.0.1:7350`).

## 2. Read the open threads

```
GET $P/api/comments?status=open
```

Each comment has an `id`, `author`, `text`, `replies`, and an `anchor`:

- Diff threads: `{"file":"path","side":"old|new","line":N,"context":"<exact line text>"}`.
  `side:"new"` lines are at the review's head endpoint (usually the
  worktree); `side:"old"` lines are from the base version (usually
  pointing at something that was removed or changed). Locate the code by
  `context`, not just the line number — the file may have shifted. The
  anchor also records `base`/`head` (and resolved `base_sha`/`head_sha`)
  — the comparison the reviewer was looking at when they wrote it; a
  head of `@worktree` or `@index` means uncommitted state at the time.
- Document threads: `{"line":N}` refers to the reviewed markdown file.
- `{"page":true}` threads are about the whole review.

## 3. Work them one at a time

For each open thread, in order:

1. Make the change it asks for (or decide, with good reason, not to).
2. If you made the change, resolve the thread:

   ```
   PATCH $P/api/comments/<id>
   Content-Type: application/json

   {"status": "resolved"}
   ```

3. If you did **not** make the change, or made a different one than
   asked, reply in the thread explaining why — then resolve it only if
   the reviewer's question is genuinely settled:

   ```
   POST $P/api/comments/<id>/reply
   Content-Type: application/json

   {"author": "agent", "text": "<one or two sentences>"}
   ```

Keep replies short and concrete. Never delete threads. Never resolve a
thread you skipped silently.

## 4. Finish

When no open threads remain, summarize for the user what you changed and
anything you pushed back on. The reviewer's page is already up to date.
