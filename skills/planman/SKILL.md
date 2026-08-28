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
  `context`, not just the line number — the file may have shifted. A
  `start_line` field, when present, widens the anchor to the range
  `start_line`–`line` on that side; `line` is always the range's end and
  the anchored `context` line. The
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

## Authoring a walkthrough (diff mode)

When asked to guide the reviewer through a change, post a narrative
walkthrough: a chaptered tour of the diff the reviewer steps through in
the UI, with your prose beside the live hunks.

1. `GET $P/api/hunks` — the manifest of the current diff. Every hunk has
   a stable `id` (`path:hN`) plus its header, line ranges, add/delete
   counts, and first lines. Reference hunks **only by these ids**; never
   embed diff text.
2. Compose the walkthrough JSON and `POST $P/api/walkthrough`:

   ```json
   {
     "version": 1,
     "title": "Add rate limiting to the API",
     "focus": "Check the lock ordering in limiter.go",
     "chapters": [
       {
         "title": "Core change",
         "icon": "⚙️",
         "stops": [
           {
             "title": "The limiter itself",
             "importance": "critical",
             "prose": "Markdown explaining **why**, not what — the diff shows what.",
             "hunk_ids": ["limiter.go:h1", "limiter.go:h2"],
             "notes": [{"hunk_id": "limiter.go:h2", "body": "This lock order matters."}]
           }
         ]
       }
     ],
     "support": [{"hunk_ids": ["gen_api.go:h1"], "reason": "generated"}],
     "commit": {"subject": "Add rate limiting", "body": "Optional body."}
   }
   ```

   Rules: 1-8 chapters; 1-20 stops per chapter; 1-14 `hunk_ids` per stop;
   `importance` is `critical`, `normal`, or `context`; order stops as a
   story (the why, the core change, then the periphery). Put noise
   (generated files, mechanical renames) in `support`, not stops.
3. A `422` response lists `unknown_hunk_ids` — re-fetch the manifest and
   re-map, ids renumber whenever the diff changes. For the same reason,
   re-POST the walkthrough after any further edits to the code.
   `DELETE $P/api/walkthrough` removes a stale tour. `/healthz` reports
   `"walkthrough": true` while one is stored.
