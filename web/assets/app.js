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
      if (file.dataset.fullFile) return;
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

  // Keyboard navigation inside the history panel.
  document.body.addEventListener("keydown", function (e) {
    var panel = document.getElementById("history-panel");
    if (!panel || panel.hidden) return;
    if (/INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName)) return;
    var rows = Array.from(panel.querySelectorAll(".hist-row"));
    var idx = rows.indexOf(panel.querySelector(".hist-row.kb-cursor"));
    if (e.key === "j" || e.key === "k") {
      idx = e.key === "j" ? Math.min(idx + 1, rows.length - 1) : Math.max(idx - 1, 0);
      rows.forEach(function (r) { r.classList.remove("kb-cursor"); });
      rows[idx].classList.add("kb-cursor");
      rows[idx].scrollIntoView({ block: "nearest" });
      e.preventDefault();
    } else if ((e.key === " " || e.key === "b") && idx >= 0) {
      var row = rows[idx];
      if (e.key === "b" && !row.dataset.pseudo) setEndpoint({ base: row.dataset.ref });
      if (e.key === " ") setEndpoint({ head: row.dataset.ref });
      e.preventDefault();
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

  // --- After swaps: reapply client state; keep only the newest inline
  // comment form when one is opened next to a diff row.
  document.body.addEventListener("htmx:afterSwap", function (e) {
    renderMermaid();
    applyFileState();
    scrollPending();
    if (e.detail && e.detail.target && e.detail.target.closest) {
      var swapped = document.querySelectorAll("tr.form-row");
      for (var i = 0; i < swapped.length - 1; i++) swapped[i].remove();
    }
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
    htmx.ajax("GET", "/content", { target: "#content", swap: "innerHTML" });
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

  var es = new EventSource("/events");
  es.addEventListener("source-change", onSourceChange);
  es.addEventListener("comments-change", refresh);

  renderMermaid();
  applyFileState();
})();
