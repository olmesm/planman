/* planman frontend glue: live reload, theme, diff chrome (viewed /
   collapse / copy path), mermaid, author memory. Server-side HTML does
   the heavy lifting; this file only wires client-held state. */
(function () {
  "use strict";

  var content = function () { return document.getElementById("content"); };

  // --- Theme: follow the OS by default; ?mode= or the toggle forces it.
  var THEME_KEY = "planman-theme";
  function applyTheme(theme) {
    if (theme === "light" || theme === "dark") {
      document.documentElement.dataset.theme = theme;
    } else {
      delete document.documentElement.dataset.theme;
    }
  }
  var urlMode = new URLSearchParams(location.search).get("mode");
  if (urlMode === "light" || urlMode === "dark") {
    localStorage.setItem(THEME_KEY, urlMode);
  }
  applyTheme(localStorage.getItem(THEME_KEY));
  var themeToggle = document.getElementById("theme-toggle");
  if (themeToggle) {
    themeToggle.addEventListener("click", function () {
      var order = ["", "light", "dark"];
      var cur = localStorage.getItem(THEME_KEY) || "";
      var next = order[(order.indexOf(cur) + 1) % order.length];
      if (next) localStorage.setItem(THEME_KEY, next);
      else localStorage.removeItem(THEME_KEY);
      applyTheme(next);
      themeToggle.textContent = next === "light" ? "☀" : next === "dark" ? "☾" : "◐";
    });
  }

  // --- Author name persistence.
  var author = document.getElementById("author");
  if (author) {
    author.value = localStorage.getItem("planman-author") || "";
    author.addEventListener("input", function () {
      localStorage.setItem("planman-author", author.value);
    });
  }

  // --- Mermaid (doc mode only; the script is not loaded in diff mode).
  function renderMermaid() {
    if (typeof mermaid === "undefined") return;
    var nodes = document.querySelectorAll(".mermaid:not([data-processed])");
    if (nodes.length) {
      mermaid.run({ nodes: nodes }).catch(function (err) {
        console.error("mermaid:", err);
      });
    }
  }
  if (typeof mermaid !== "undefined") mermaid.initialize({ startOnLoad: false });

  // --- Viewed files: checking "Viewed" collapses the file and persists
  // per path + patch fingerprint, so it resets when the patch changes.
  var VIEWED_KEY = "planman-viewed:" + document.title;
  function viewedState() {
    try {
      return JSON.parse(localStorage.getItem(VIEWED_KEY)) || {};
    } catch (e) {
      return {};
    }
  }
  function saveViewed(state) {
    localStorage.setItem(VIEWED_KEY, JSON.stringify(state));
  }
  var collapsed = {}; // per-page collapse state, keyed by path

  function applyFileState() {
    var state = viewedState();
    document.querySelectorAll(".file").forEach(function (file) {
      // Skip cards without a fingerprint (full files, walkthrough hunks).
      if (file.dataset.fullFile || !file.dataset.fp) return;
      var path = file.dataset.path;
      var isViewed = state[path] === file.dataset.fp;
      file.classList.toggle("viewed", isViewed);
      var box = file.querySelector(".viewed-box");
      if (box) box.checked = isViewed;
      file.classList.toggle("collapsed", !!collapsed[path]);
      var treeEntry = document.querySelector(
        '.tree-file[data-path="' + CSS.escape(path) + '"]:not(.unchanged)'
      );
      if (treeEntry) treeEntry.classList.toggle("viewed", isViewed);
    });
    var panel = document.getElementById("history-panel");
    if (panel) panel.hidden = localStorage.getItem("planman-history-open") !== "1";
    applySidebar();
  }

  // --- History navigator: pick compare/base endpoints from the graph.
  function setEndpoint(params) {
    htmx.ajax("GET", "/content", {
      target: "#content",
      swap: "innerHTML",
      values: params,
    });
  }

  document.body.addEventListener("click", function (e) {
    if (e.target.closest("#history-toggle")) {
      var panel = document.getElementById("history-panel");
      var open = panel.hidden;
      panel.hidden = !open;
      localStorage.setItem("planman-history-open", open ? "1" : "0");
      return;
    }
    var row = e.target.closest(".hist-row");
    if (row) {
      if (e.shiftKey && !row.dataset.pseudo) setEndpoint({ base: row.dataset.ref });
      else setEndpoint({ head: row.dataset.ref });
      return;
    }
    var entry = e.target.closest(".stack-entry");
    if (entry) {
      pendingScroll = "#comment-" + entry.dataset.comment;
      if (entry.dataset.navBase && entry.dataset.navHead) {
        setEndpoint({ base: entry.dataset.navBase, head: entry.dataset.navHead, mb: "0" });
      } else {
        scrollPending();
      }
      return;
    }
    var full = e.target.closest(".tree-file.unchanged");
    if (full) {
      e.preventDefault();
      var path = full.dataset.path;
      var existing = document.querySelector('section.file[data-full-file][data-path="' + CSS.escape(path) + '"]');
      if (existing) {
        existing.scrollIntoView({ block: "start" });
        return;
      }
      pendingScroll = 'section.file[data-full-file][data-path="' + CSS.escape(path) + '"]';
      htmx.ajax("GET", "/file", {
        target: "#extra-files",
        swap: "beforeend",
        values: { path: path },
      });
      return;
    }
  });

  // --- Range comments: press on a line number and drag down (or up)
  // the gutter to select a span, release to open the comment form on
  // the span's last line.
  var gutterDrag = null;
  function gutterRef(el) {
    var td = el.closest && el.closest("td.num");
    if (!td) return null;
    var src = td.dataset.line ? td : td.closest("tr.line");
    if (!src || !src.dataset || !src.dataset.line || !src.dataset.side) return null;
    return {
      file: src.dataset.file,
      side: src.dataset.side,
      line: parseInt(src.dataset.line, 10),
      el: td.closest("tr"),
    };
  }
  function clearRangeSel() {
    document.querySelectorAll(".range-sel").forEach(function (el) {
      el.classList.remove("range-sel");
    });
  }
  function markRangeSel(file, side, a, b) {
    clearRangeSel();
    var lo = Math.min(a, b);
    var hi = Math.max(a, b);
    var scope = document.querySelector('section.file[data-path="' + CSS.escape(file) + '"]');
    if (!scope) return;
    scope
      .querySelectorAll('[data-side="' + side + '"][data-line]')
      .forEach(function (el) {
        var line = parseInt(el.dataset.line, 10);
        if (line >= lo && line <= hi) (el.closest("tr") || el).classList.add("range-sel");
      });
  }
  document.body.addEventListener("mousedown", function (e) {
    if (e.button !== 0) return;
    var ref = gutterRef(e.target);
    if (!ref) return;
    e.preventDefault(); // no text selection while dragging the gutter
    gutterDrag = { start: ref, end: ref };
  });
  document.body.addEventListener("mouseover", function (e) {
    if (!gutterDrag) return;
    var ref = gutterRef(e.target);
    if (!ref || ref.file !== gutterDrag.start.file || ref.side !== gutterDrag.start.side) return;
    gutterDrag.end = ref;
    markRangeSel(ref.file, ref.side, gutterDrag.start.line, ref.line);
  });
  document.body.addEventListener("mouseup", function () {
    if (!gutterDrag) return;
    var d = gutterDrag;
    gutterDrag = null;
    if (d.end.line === d.start.line) {
      clearRangeSel();
      return; // plain click: the per-line + button handles it
    }
    var first = d.start.line <= d.end.line ? d.start : d.end;
    var last = d.start.line <= d.end.line ? d.end : d.start;
    openCommentForm(
      { file: last.file, side: last.side, line: last.line, el: last.el },
      first.line
    );
  });

  // --- Keyboard: one dispatcher with explicit precedence — overlays
  // first, then the history panel while it is open, then global diff
  // navigation. Guarded so shortcuts never fire while typing.
  var isMac = /Mac|iP(hone|ad|od)/.test(navigator.platform);
  function modKey(e) { return isMac ? e.metaKey : e.ctrlKey; }
  function typingTarget() {
    var el = document.activeElement;
    return el && (/INPUT|TEXTAREA|SELECT/.test(el.tagName) || el.isContentEditable);
  }
  var diffMode = document.body.dataset.mode === "diff";

  function historyPanelKeys(e) {
    var panel = document.getElementById("history-panel");
    if (!panel || panel.hidden) return false;
    var rows = Array.from(panel.querySelectorAll(".hist-row"));
    var idx = rows.indexOf(panel.querySelector(".hist-row.kb-cursor"));
    if (e.key === "j" || e.key === "k") {
      idx = e.key === "j" ? Math.min(idx + 1, rows.length - 1) : Math.max(idx - 1, 0);
      rows.forEach(function (r) { r.classList.remove("kb-cursor"); });
      rows[idx].classList.add("kb-cursor");
      rows[idx].scrollIntoView({ block: "nearest" });
      return true;
    }
    if ((e.key === " " || e.key === "b") && idx >= 0) {
      var row = rows[idx];
      if (e.key === "b" && !row.dataset.pseudo) setEndpoint({ base: row.dataset.ref });
      if (e.key === " ") setEndpoint({ head: row.dataset.ref });
      return true;
    }
    return false;
  }

  // --- Hunk navigation: j/k walk the hunk headers of expanded files;
  // Enter opens a comment on the current hunk's last changed line.
  function hunkRows() {
    return Array.from(
      document.querySelectorAll("#content section.file:not(.collapsed) tr.hunk-row")
    );
  }
  function setKbTarget(row) {
    document.querySelectorAll("tr.kb-target").forEach(function (r) {
      r.classList.remove("kb-target");
    });
    if (row) {
      row.classList.add("kb-target");
      row.scrollIntoView({ block: "center" });
    }
  }
  function hunkNav(delta) {
    var rows = hunkRows();
    if (!rows.length) return;
    var idx = rows.indexOf(document.querySelector("tr.hunk-row.kb-target"));
    idx = idx < 0 ? (delta > 0 ? 0 : rows.length - 1)
                  : Math.max(0, Math.min(rows.length - 1, idx + delta));
    setKbTarget(rows[idx]);
  }
  // The rows of the hunk under the cursor, up to the next hunk header.
  function currentHunkLines() {
    var cur = document.querySelector("tr.hunk-row.kb-target");
    if (!cur) return [];
    var lines = [];
    for (var row = cur.nextElementSibling; row; row = row.nextElementSibling) {
      if (row.classList.contains("hunk-row")) break;
      if (row.classList.contains("line")) lines.push(row);
    }
    return lines;
  }
  // rowInfo extracts the anchor identity of a diff row for the given
  // change kind, handling both unified rows and split cells.
  function rowInfo(rowEl, kind) {
    if (rowEl.classList.contains(kind) && rowEl.dataset.line) {
      return {
        file: rowEl.dataset.file,
        side: rowEl.dataset.side,
        line: parseInt(rowEl.dataset.line, 10),
        el: rowEl,
      };
    }
    var td = rowEl.querySelector("td.code." + kind);
    if (td && td.dataset.line) {
      return {
        file: td.dataset.file,
        side: td.dataset.side,
        line: parseInt(td.dataset.line, 10),
        el: rowEl,
      };
    }
    return null;
  }
  // contextTextOf reads the anchored line text back out of the row's
  // comment-button URL, so client-built forms re-anchor like clicks do.
  function contextTextOf(el, side) {
    var btn =
      el.querySelector('td.code[data-side="' + side + '"] .add-comment-btn') ||
      el.querySelector(".add-comment-btn");
    var hx = btn && btn.getAttribute("hx-get");
    if (!hx || hx.indexOf("?") < 0) return "";
    return new URLSearchParams(hx.slice(hx.indexOf("?") + 1)).get("context") || "";
  }
  function openCommentForm(info, startLine) {
    var values = {
      file: info.file,
      side: info.side,
      line: info.line,
      context: contextTextOf(info.el, info.side),
    };
    if (startLine && startLine < info.line) values.start_line = startLine;
    htmx.ajax("GET", "/comment-form", {
      source: info.el,
      target: info.el,
      swap: "afterend",
      values: values,
    });
  }
  function commentOnCurrentHunk() {
    var lines = currentHunkLines();
    if (!lines.length) return;
    // Prefer the last contiguous run of added lines, then deleted ones,
    // then just the last line — commenting the span of the change.
    function lastRun(kind) {
      var run = [];
      var best = null;
      for (var i = 0; i < lines.length; i++) {
        var info = rowInfo(lines[i], kind);
        if (info) {
          run.push(info);
          best = run.slice();
        } else if (run.length) {
          run = [];
        }
      }
      return best;
    }
    var run = lastRun("add") || lastRun("del");
    if (run) {
      openCommentForm(run[run.length - 1], run[0].line);
      return;
    }
    var info = rowInfo(lines[lines.length - 1], "context");
    if (info) openCommentForm(info, 0);
  }
  function fileNav(delta) {
    var files = Array.from(document.querySelectorAll("#content section.file"));
    if (!files.length) return;
    var top = window.scrollY + 80;
    var idx = -1;
    for (var i = 0; i < files.length; i++) {
      if (files[i].offsetTop <= top) idx = i;
    }
    idx = Math.max(0, Math.min(files.length - 1, idx + delta));
    files[idx].scrollIntoView({ block: "start" });
  }
  function currentFile() {
    var target = document.querySelector("tr.kb-target");
    if (target) return target.closest("section.file");
    var files = Array.from(document.querySelectorAll("#content section.file"));
    var top = window.scrollY + 80;
    var best = files[0];
    for (var i = 0; i < files.length; i++) {
      if (files[i].offsetTop <= top) best = files[i];
    }
    return best;
  }

  // --- Command palette + fuzzy file filter (one overlay, two modes).
  var backdrop = document.getElementById("palette-backdrop");
  var paletteInput = document.getElementById("palette-input");
  var paletteList = document.getElementById("palette-list");
  var paletteMode = "commands";

  function fuzzyMatches(query, text) {
    // Subsequence match, case-insensitive (codiff's file-filter rule).
    query = query.toLowerCase();
    text = text.toLowerCase();
    var qi = 0;
    for (var ti = 0; ti < text.length && qi < query.length; ti++) {
      if (text[ti] === query[qi]) qi++;
    }
    return qi === query.length;
  }

  function commandItems() {
    function click(sel) {
      return function () {
        var el = document.querySelector(sel);
        if (el) el.click();
      };
    }
    return [
      { label: "Go to file…", hint: "Ctrl/⌘ P", run: function () { openPalette("files"); return true; } },
      { label: "Find in diffs…", hint: "Ctrl/⌘ F", run: function () { openSearch(); } },
      { label: "Preset: working tree", run: function () { setEndpoint({ preset: "working" }); } },
      { label: "Preset: staged", run: function () { setEndpoint({ preset: "staged" }); } },
      { label: "Preset: branch", run: function () { setEndpoint({ preset: "branch" }); } },
      { label: "Preset: all since merge-base", run: function () { setEndpoint({ preset: "all" }); } },
      { label: "View: unified", run: function () { setEndpoint({ view: "unified" }); } },
      { label: "View: split", run: function () { setEndpoint({ view: "split" }); } },
      { label: "Toggle merge-base (three-dot)", run: click("#mb-toggle") },
      { label: "Toggle all files in sidebar", run: click("#allfiles-toggle") },
      { label: "Toggle hide whitespace", run: click("#ws-toggle") },
      { label: "Toggle history panel", run: click("#history-toggle") },
      { label: "Toggle sidebar", hint: "Ctrl/⌘ B", run: toggleSidebar },
      { label: "Collapse all files", run: function () { setAllCollapsed(true); } },
      { label: "Expand all files", run: function () { setAllCollapsed(false); } },
      { label: "Mark all files viewed", run: function () { setAllViewed(true); } },
      { label: "Clear all viewed marks", run: function () { setAllViewed(false); } },
      { label: "Copy review as markdown", run: click("#copy-review-btn") },
      { label: "Comment on review", run: click("#page-comment-btn") },
      { label: "Toggle live reload on repo changes", run: function () {
          var on = localStorage.getItem("planman-live-reload") === "1";
          if (on) localStorage.removeItem("planman-live-reload");
          else localStorage.setItem("planman-live-reload", "1");
        } },
      { label: "Toggle color theme", run: click("#theme-toggle") },
    ];
  }
  function setAllCollapsed(want) {
    document.querySelectorAll("#content section.file").forEach(function (f) {
      collapsed[f.dataset.path] = want;
    });
    applyFileState();
  }
  function setAllViewed(want) {
    var state = viewedState();
    document.querySelectorAll("#content section.file:not([data-full-file])").forEach(function (f) {
      if (want) {
        state[f.dataset.path] = f.dataset.fp;
        collapsed[f.dataset.path] = true;
      } else {
        delete state[f.dataset.path];
        delete collapsed[f.dataset.path];
      }
    });
    saveViewed(state);
    applyFileState();
  }
  function fileItems() {
    return Array.from(document.querySelectorAll(".tree-file[data-path]")).map(function (el) {
      var path = el.dataset.path;
      return {
        label: path,
        hint: el.classList.contains("unchanged") ? "unchanged" : "",
        run: function () { el.click(); },
      };
    });
  }

  function renderPalette() {
    var q = paletteInput.value.trim();
    var items = paletteMode === "files" ? fileItems() : commandItems();
    var shown = q ? items.filter(function (i) { return fuzzyMatches(q, i.label); }) : items;
    shown = shown.slice(0, 50);
    paletteList.innerHTML = "";
    shown.forEach(function (item, i) {
      var div = document.createElement("div");
      div.className = "palette-item" + (i === 0 ? " sel" : "");
      var label = document.createElement("span");
      label.textContent = item.label;
      div.appendChild(label);
      if (item.hint) {
        var hint = document.createElement("span");
        hint.className = "palette-hint";
        hint.textContent = item.hint;
        div.appendChild(hint);
      }
      div.addEventListener("click", function () { runPaletteItem(item); });
      paletteList.appendChild(div);
    });
    paletteItemsShown = shown;
  }
  var paletteItemsShown = [];
  function runPaletteItem(item) {
    var keepOpen = item.run();
    if (!keepOpen) closePalette();
  }
  function openPalette(mode) {
    paletteMode = mode;
    backdrop.hidden = false;
    paletteInput.value = "";
    paletteInput.placeholder = mode === "files" ? "Go to file…" : "Run a command…";
    renderPalette();
    paletteInput.focus();
  }
  function closePalette() {
    backdrop.hidden = true;
    paletteInput.blur();
  }
  if (backdrop) {
    backdrop.addEventListener("mousedown", function (e) {
      if (e.target === backdrop) closePalette();
    });
    paletteInput.addEventListener("input", renderPalette);
    paletteInput.addEventListener("keydown", function (e) {
      var sel = paletteList.querySelector(".palette-item.sel");
      var all = Array.from(paletteList.querySelectorAll(".palette-item"));
      var idx = all.indexOf(sel);
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        idx = e.key === "ArrowDown" ? Math.min(all.length - 1, idx + 1) : Math.max(0, idx - 1);
        all.forEach(function (el) { el.classList.remove("sel"); });
        if (all[idx]) {
          all[idx].classList.add("sel");
          all[idx].scrollIntoView({ block: "nearest" });
        }
        e.preventDefault();
      } else if (e.key === "Enter") {
        if (idx >= 0 && paletteItemsShown[idx]) runPaletteItem(paletteItemsShown[idx]);
        e.preventDefault();
      }
    });
  }

  // --- Find in diffs: server-side match list, client-side dim + cycle.
  var searchBar = document.getElementById("search-bar");
  var searchInput = document.getElementById("search-input");
  var searchCount = document.getElementById("search-count");
  var searchMatches = [];
  var searchIdx = -1;
  var searchTimer = null;

  function openSearch() {
    if (!searchBar) return;
    searchBar.hidden = false;
    searchInput.focus();
    searchInput.select();
  }
  function clearSearchClasses() {
    document.querySelectorAll(".search-dimmed").forEach(function (el) {
      el.classList.remove("search-dimmed");
    });
    document.querySelectorAll(".search-hit").forEach(function (el) {
      el.classList.remove("search-hit");
    });
  }
  function closeSearch() {
    if (!searchBar) return;
    searchBar.hidden = true;
    searchInput.blur();
    clearSearchClasses();
    searchMatches = [];
    searchIdx = -1;
    searchCount.textContent = "";
  }
  function applySearch(res) {
    clearSearchClasses();
    searchMatches = [];
    searchIdx = -1;
    var matched = {};
    res.files.forEach(function (f) {
      matched[f.path] = true;
      (f.matches || []).forEach(function (mt) {
        searchMatches.push({ path: f.path, side: mt.side, line: mt.line });
      });
    });
    if (res.query) {
      document.querySelectorAll("#content section.file:not([data-full-file])").forEach(function (el) {
        if (!matched[el.dataset.path]) el.classList.add("search-dimmed");
      });
      document.querySelectorAll(".tree-file:not(.unchanged)").forEach(function (el) {
        if (!matched[el.dataset.path]) el.classList.add("search-dimmed");
      });
    }
    searchCount.textContent = res.query
      ? res.total + " match" + (res.total === 1 ? "" : "es")
      : "";
  }
  function runSearch() {
    var q = searchInput.value.trim();
    fetch("/search?q=" + encodeURIComponent(q))
      .then(function (r) { return r.json(); })
      .then(applySearch);
  }
  function matchRow(m) {
    var file = document.querySelector(
      '#content section.file[data-path="' + CSS.escape(m.path) + '"]'
    );
    if (!file) return null;
    var el = file.querySelector('[data-side="' + m.side + '"][data-line="' + m.line + '"]');
    return el && (el.closest("tr") || el);
  }
  function searchNav(delta) {
    if (!searchMatches.length) return;
    document.querySelectorAll(".search-hit").forEach(function (el) {
      el.classList.remove("search-hit");
    });
    searchIdx = (searchIdx + delta + searchMatches.length) % searchMatches.length;
    var m = searchMatches[searchIdx];
    var row = matchRow(m);
    if (row) {
      var file = row.closest("section.file");
      if (file && file.classList.contains("collapsed")) {
        delete collapsed[file.dataset.path];
        file.classList.remove("collapsed");
      }
      row.classList.add("search-hit");
      row.scrollIntoView({ block: "center" });
    }
    searchCount.textContent = (searchIdx + 1) + " of " + searchMatches.length;
  }
  if (searchBar) {
    searchInput.addEventListener("input", function () {
      clearTimeout(searchTimer);
      searchTimer = setTimeout(runSearch, 150);
    });
    searchInput.addEventListener("keydown", function (e) {
      if (e.key === "Enter") {
        searchNav(e.shiftKey ? -1 : 1);
        e.preventDefault();
      }
    });
    document.getElementById("search-next").addEventListener("click", function () { searchNav(1); });
    document.getElementById("search-prev").addEventListener("click", function () { searchNav(-1); });
    document.getElementById("search-close").addEventListener("click", closeSearch);
  }

  // --- Definition navigation: hold Ctrl/Cmd to underline identifiers,
  // Mod+click asks the server for likely definitions (git grep + a
  // conservative declaration classifier — no language server).
  var defPopover = null;
  function closeDefPopover() {
    if (defPopover) {
      defPopover.remove();
      defPopover = null;
    }
  }
  var IDENT_RE = /^[$_A-Za-z][$\w]*$/;
  function identFromClick(e) {
    var t = e.target;
    if (t.nodeType === 1 && t.closest(".code-text") && t !== t.closest(".code-text")) {
      var txt = (t.textContent || "").trim();
      if (IDENT_RE.test(txt)) return txt;
    }
    var pos = document.caretPositionFromPoint
      ? document.caretPositionFromPoint(e.clientX, e.clientY)
      : null;
    var node = pos && pos.offsetNode;
    if (node && node.nodeType === 3) {
      var s = node.textContent;
      var off = pos.offset;
      var isw = function (ch) { return /[$\w]/.test(ch); };
      var a = off;
      while (a > 0 && isw(s[a - 1])) a--;
      var b = off;
      while (b < s.length && isw(s[b])) b++;
      var word = s.slice(a, b);
      if (IDENT_RE.test(word)) return word;
    }
    return null;
  }
  function navigateToDef(path, line) {
    closeDefPopover();
    var file = document.querySelector('section.file[data-path="' + CSS.escape(path) + '"]:not([data-full-file])');
    var el = file &&
      (file.querySelector('[data-side="new"][data-line="' + line + '"]') ||
       file.querySelector('[data-side="old"][data-line="' + line + '"]'));
    if (el) {
      if (file.classList.contains("collapsed")) {
        delete collapsed[file.dataset.path];
        file.classList.remove("collapsed");
      }
      var row = el.closest("tr") || el;
      row.scrollIntoView({ block: "center" });
      row.classList.add("flash");
      setTimeout(function () { row.classList.remove("flash"); }, 1500);
      return;
    }
    // Not visible in the diff: open the file in full and jump there.
    var full = 'section.file[data-full-file][data-path="' + CSS.escape(path) + '"]';
    var existing = document.querySelector(full);
    pendingScroll = full + ' tr[data-line="' + line + '"]';
    if (existing) {
      scrollPending();
      return;
    }
    htmx.ajax("GET", "/file", {
      target: "#extra-files",
      swap: "beforeend",
      values: { path: path },
    });
  }
  function showDefPopover(e, ident, res) {
    closeDefPopover();
    var pop = document.createElement("div");
    pop.className = "def-popover";
    var head = document.createElement("div");
    head.className = "def-popover-head";
    head.textContent = ident;
    pop.appendChild(head);
    if (res.status !== "ready") {
      var note = document.createElement("div");
      note.className = "def-popover-note";
      note.textContent = res.reason || "Definition search unavailable.";
      pop.appendChild(note);
    } else if (!res.candidates.length) {
      var none = document.createElement("div");
      none.className = "def-popover-note";
      none.textContent = "No definition found.";
      pop.appendChild(none);
    } else {
      res.candidates.forEach(function (c) {
        var row = document.createElement("button");
        row.className = "def-candidate";
        var kind = document.createElement("span");
        kind.className = "def-kind";
        kind.textContent = c.kind;
        var loc = document.createElement("span");
        loc.className = "def-loc";
        loc.textContent = c.path + ":" + c.line_number;
        var code = document.createElement("code");
        code.textContent = c.line;
        row.appendChild(kind);
        row.appendChild(loc);
        row.appendChild(code);
        row.addEventListener("click", function () {
          navigateToDef(c.path, c.line_number);
        });
        pop.appendChild(row);
      });
    }
    pop.style.left = Math.min(e.pageX, window.scrollX + window.innerWidth - 440) + "px";
    pop.style.top = e.pageY + 12 + "px";
    document.body.appendChild(pop);
    defPopover = pop;
  }
  document.body.addEventListener("click", function (e) {
    if (defPopover && !e.target.closest(".def-popover")) closeDefPopover();
    if (!diffMode || !modKey(e)) return;
    var code = e.target.closest && e.target.closest("td.code");
    if (!code) return;
    var src = code.dataset.file ? code : code.closest("tr.line");
    if (!src || !src.dataset || !src.dataset.file) return;
    var ident = identFromClick(e);
    if (!ident) return;
    e.preventDefault();
    fetch(
      "/defs?ident=" + encodeURIComponent(ident) +
      "&file=" + encodeURIComponent(src.dataset.file) +
      "&side=" + encodeURIComponent(src.dataset.side || "new")
    )
      .then(function (r) { return r.json(); })
      .then(function (res) { showDefPopover(e, ident, res); });
  });
  document.body.addEventListener("keydown", function (e) {
    if (e.key === "Control" || e.key === "Meta") document.body.classList.add("def-mode");
  });
  document.body.addEventListener("keyup", function (e) {
    if (e.key === "Control" || e.key === "Meta") document.body.classList.remove("def-mode");
  });
  window.addEventListener("blur", function () {
    document.body.classList.remove("def-mode");
  });

  // --- Hold-? shortcut overlay.
  var kbdHelp = document.getElementById("kbd-help");

  document.body.addEventListener("keydown", function (e) {
    if (!diffMode) return;
    // Escape closes overlays even while their inputs are focused.
    if (e.key === "Escape") {
      if (defPopover) { closeDefPopover(); e.preventDefault(); return; }
      if (backdrop && !backdrop.hidden) { closePalette(); e.preventDefault(); return; }
      if (searchBar && !searchBar.hidden) { closeSearch(); e.preventDefault(); return; }
      return;
    }
    // Chorded shortcuts work anywhere except while typing a comment.
    if (modKey(e) && !e.altKey) {
      var inComment = document.activeElement && document.activeElement.tagName === "TEXTAREA";
      if (!inComment) {
        var key = e.key.toLowerCase();
        if (key === "p" && e.shiftKey) { openPalette("commands"); e.preventDefault(); return; }
        if (key === "p") { openPalette("files"); e.preventDefault(); return; }
        if (key === "f") { openSearch(); e.preventDefault(); return; }
        if (key === "b") { toggleSidebar(); e.preventDefault(); return; }
      }
    }
    if (typingTarget()) return;
    if (e.key === "?" && kbdHelp) {
      kbdHelp.hidden = false;
      e.preventDefault();
      return;
    }
    if (modKey(e) || e.altKey) return;
    if (historyPanelKeys(e)) {
      e.preventDefault();
      return;
    }
    var panel = document.getElementById("history-panel");
    if (panel && !panel.hidden) return; // panel owns j/k/space/b while open
    // Walkthrough view: arrows step through the tour. Issue the request
    // directly from the button's URL rather than clicking it — a click
    // can land while htmx is still settling freshly swapped content.
    if (document.querySelector(".walkthrough")) {
      if (e.key === "ArrowRight" || e.key === "ArrowLeft") {
        var stepBtn = document.getElementById(e.key === "ArrowRight" ? "walk-next" : "walk-prev");
        if (stepBtn) {
          htmx.ajax("GET", stepBtn.getAttribute("hx-get"), {
            target: "#content",
            swap: "innerHTML",
          });
        }
        e.preventDefault();
        return;
      }
    }
    switch (e.key) {
      case "j": hunkNav(1); e.preventDefault(); break;
      case "k": hunkNav(-1); e.preventDefault(); break;
      case "n": fileNav(1); e.preventDefault(); break;
      case "p": fileNav(-1); e.preventDefault(); break;
      case "Enter": commentOnCurrentHunk(); e.preventDefault(); break;
      case "v": {
        var file = currentFile();
        var box = file && file.querySelector(".viewed-box");
        if (box) box.click();
        e.preventDefault();
        break;
      }
    }
  });
  document.body.addEventListener("keyup", function (e) {
    if (kbdHelp && !kbdHelp.hidden && (e.key === "?" || e.key === "/" || e.key === "Shift")) {
      kbdHelp.hidden = true;
    }
  });

  // Deferred scrolling after a swap (stack navigation, full-file load).
  var pendingScroll = null;
  function scrollPending() {
    if (!pendingScroll) return;
    var el = document.querySelector(pendingScroll);
    pendingScroll = null;
    if (el) {
      el.scrollIntoView({ block: "center" });
      el.classList.add("flash");
      setTimeout(function () { el.classList.remove("flash"); }, 1500);
    }
  }

  document.body.addEventListener("change", function (e) {
    if (!e.target.classList.contains("viewed-box")) return;
    var file = e.target.closest(".file");
    var state = viewedState();
    if (e.target.checked) {
      state[file.dataset.path] = file.dataset.fp;
      collapsed[file.dataset.path] = true; // viewed files fold away
    } else {
      delete state[file.dataset.path];
      delete collapsed[file.dataset.path];
    }
    saveViewed(state);
    applyFileState();
  });

  document.body.addEventListener("click", function (e) {
    var chevron = e.target.closest(".chevron");
    if (chevron) {
      var file = chevron.closest(".file");
      collapsed[file.dataset.path] = !file.classList.contains("collapsed");
      file.classList.toggle("collapsed");
      return;
    }
    var copy = e.target.closest(".copy-path");
    if (copy) {
      navigator.clipboard.writeText(copy.dataset.path).then(function () {
        copy.textContent = "✓";
        setTimeout(function () { copy.textContent = "⧉"; }, 1200);
      });
      return;
    }
    var mdBtn = e.target.closest(".md-preview-btn");
    if (mdBtn) {
      var body = mdBtn.closest(".file").querySelector(".file-body");
      var table = body.querySelector(".diff-table");
      var preview = body.querySelector(".md-preview");
      if (preview) {
        // The table never leaves the DOM: toggling back is instant.
        var showDiff = preview.style.display !== "none";
        preview.style.display = showDiff ? "none" : "";
        if (table) table.style.display = showDiff ? "" : "none";
        mdBtn.classList.toggle("active", !showDiff);
      } else {
        fetch("/mdpreview?path=" + encodeURIComponent(mdBtn.dataset.path))
          .then(function (r) {
            if (!r.ok) throw new Error("preview failed");
            return r.text();
          })
          .then(function (html) {
            if (table) table.style.display = "none";
            body.insertAdjacentHTML("afterbegin", html);
            mdBtn.classList.add("active");
          })
          .catch(function () {});
      }
      return;
    }
    var commitCopy = e.target.closest("#walk-commit-copy");
    if (commitCopy) {
      var pre = document.getElementById("walk-commit-text");
      navigator.clipboard.writeText(pre ? pre.textContent : "").then(function () {
        commitCopy.textContent = "Copied ✓";
        setTimeout(function () { commitCopy.textContent = "Copy message"; }, 1500);
      });
      return;
    }
    var copyReview = e.target.closest("#copy-review-btn");
    if (copyReview) {
      fetch("/export.md").then(function (r) { return r.text(); }).then(function (text) {
        return navigator.clipboard.writeText(text);
      }).then(function () {
        copyReview.textContent = "Copied ✓";
        setTimeout(function () { copyReview.textContent = "Copy review"; }, 1500);
      });
    }
  });

  // --- Resizable / collapsible sidebar. Width is a CSS variable on the
  // layout; drag past the collapse threshold folds the sidebar away.
  var SIDEBAR_KEY = "planman-sidebar";
  function sidebarPrefs() {
    try {
      return JSON.parse(localStorage.getItem(SIDEBAR_KEY)) || {};
    } catch (e) {
      return {};
    }
  }
  function applySidebar() {
    var layout = document.querySelector(".diff-layout");
    if (!layout) return;
    var prefs = sidebarPrefs();
    if (prefs.width) layout.style.setProperty("--sidebar-w", prefs.width + "px");
    layout.classList.toggle("sidebar-collapsed", !!prefs.collapsed);
  }
  function setSidebar(prefs) {
    localStorage.setItem(SIDEBAR_KEY, JSON.stringify(prefs));
    applySidebar();
  }
  function toggleSidebar() {
    var prefs = sidebarPrefs();
    prefs.collapsed = !prefs.collapsed;
    setSidebar(prefs);
  }
  document.body.addEventListener("pointerdown", function (e) {
    var handle = e.target.closest(".sidebar-handle");
    if (!handle) return;
    e.preventDefault();
    handle.setPointerCapture(e.pointerId);
    var layout = handle.closest(".diff-layout");
    var startX = e.clientX;
    var startW = layout.querySelector(".sidebar").getBoundingClientRect().width;
    var prefs = sidebarPrefs();
    function onMove(ev) {
      var w = startW + (ev.clientX - startX);
      if (w < 120) {
        layout.classList.add("sidebar-collapsed");
        prefs.collapsed = true;
      } else {
        layout.classList.remove("sidebar-collapsed");
        prefs.collapsed = false;
        prefs.width = Math.max(180, Math.min(480, Math.round(w)));
        layout.style.setProperty("--sidebar-w", prefs.width + "px");
      }
    }
    function onUp(ev) {
      handle.releasePointerCapture(ev.pointerId);
      handle.removeEventListener("pointermove", onMove);
      handle.removeEventListener("pointerup", onUp);
      setSidebar(prefs);
    }
    handle.addEventListener("pointermove", onMove);
    handle.addEventListener("pointerup", onUp);
  });

  // --- The current content view: /content (files) or /walkthrough?…
  // Live refreshes re-fetch this URL, so an SSE update never yanks the
  // reviewer out of the walkthrough; a comment POST (whose response is
  // always the files view) re-fetches it to restore the surface.
  var contentURL = "/content";

  // --- After swaps: reapply client state; keep only the newest inline
  // comment form when one is opened next to a diff row. htmx 4 fires
  // htmx:after:swap with a request context: the resolved target element
  // and the final request URL live on detail.ctx.
  document.body.addEventListener("htmx:after:swap", function (e) {
    renderMermaid();
    applyFileState();
    scrollPending();
    var ctx = e.detail && e.detail.ctx;
    var target = ctx && ctx.target;
    if (target && target.id === "content" && ctx.request) {
      var rp = ctx.request.action || "";
      if (rp.indexOf("/walkthrough") === 0 || rp.indexOf("/content") === 0) {
        contentURL = rp;
      } else if (contentURL.indexOf("/walkthrough") === 0) {
        htmx.ajax("GET", contentURL, { target: "#content", swap: "innerHTML" });
      }
    }
    var swapped = document.querySelectorAll("tr.form-row");
    for (var i = 0; i < swapped.length - 1; i++) swapped[i].remove();
    var ta = document.querySelector("tr.form-row textarea, .form-slot textarea, #page-form-slot textarea");
    if (ta) ta.focus();
  });

  // --- Live follow: the server broadcasts source and comment changes
  // separately. Comment churn (another tab, the agent API) refreshes
  // silently, held while the reviewer is mid-comment. Source churn in
  // diff mode shows a refresh banner instead of yanking the view —
  // unless live reload is forced on; doc mode always follows live,
  // since the reviewed file itself is what changed.
  var refreshPending = false;
  function typing() {
    var el = document.activeElement;
    return el && content().contains(el) &&
      (el.tagName === "TEXTAREA" || el.tagName === "INPUT");
  }
  function refresh() {
    if (typing()) {
      refreshPending = true;
      return;
    }
    refreshPending = false;
    hideBanner();
    htmx.ajax("GET", contentURL, { target: "#content", swap: "innerHTML" });
  }
  setInterval(function () {
    if (refreshPending) refresh();
  }, 2000);

  var banner = document.getElementById("refresh-banner");
  function hideBanner() {
    if (banner) banner.hidden = true;
  }
  function liveReload() {
    return document.body.dataset.mode !== "diff" ||
      localStorage.getItem("planman-live-reload") === "1";
  }
  function onSourceChange() {
    if (liveReload() || !banner) {
      refresh();
      return;
    }
    // Dismissing latches only until the next change comes in.
    banner.hidden = false;
  }
  if (banner) {
    document.getElementById("refresh-banner-go").addEventListener("click", refresh);
    document.getElementById("refresh-banner-dismiss").addEventListener("click", hideBanner);
  }

  // A live refresh can race a walkthrough deletion: the stored tour is
  // gone, /walkthrough answers 404 (left unswapped), and the page would
  // sit on a dead view. Fall back to the files view.
  document.body.addEventListener("htmx:response:error", function (e) {
    var ctx = e.detail && e.detail.ctx;
    if (!ctx || !ctx.request || ctx.request.action.indexOf("/walkthrough") !== 0) return;
    if (contentURL.indexOf("/walkthrough") === 0) {
      contentURL = "/content";
      htmx.ajax("GET", contentURL, { target: "#content", swap: "innerHTML" });
    }
  });

  var es = new EventSource("/events");
  es.addEventListener("source-change", onSourceChange);
  es.addEventListener("comments-change", refresh);

  renderMermaid();
  applyFileState();
})();
