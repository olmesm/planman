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
      var path = file.dataset.path;
      var isViewed = state[path] === file.dataset.fp;
      file.classList.toggle("viewed", isViewed);
      var box = file.querySelector(".viewed-box");
      if (box) box.checked = isViewed;
      file.classList.toggle("collapsed", !!collapsed[path]);
      var treeEntry = document.querySelector(
        '.tree-file[data-path="' + CSS.escape(path) + '"]'
      );
      if (treeEntry) treeEntry.classList.toggle("viewed", isViewed);
    });
  }

  document.body.addEventListener("change", function (e) {
    if (!e.target.classList.contains("viewed-box")) return;
    var file = e.target.closest(".file");
    var state = viewedState();
    if (e.target.checked) state[file.dataset.path] = file.dataset.fp;
    else delete state[file.dataset.path];
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
    }
  });

  // --- After swaps: reapply client state; keep only the newest inline
  // comment form when one is opened next to a diff row.
  document.body.addEventListener("htmx:afterSwap", function (e) {
    renderMermaid();
    applyFileState();
    if (e.detail && e.detail.target && e.detail.target.closest) {
      var swapped = document.querySelectorAll("tr.form-row");
      for (var i = 0; i < swapped.length - 1; i++) swapped[i].remove();
    }
    var ta = document.querySelector("tr.form-row textarea, .form-slot textarea, #page-form-slot textarea");
    if (ta) ta.focus();
  });

  // --- Live reload: the server broadcasts when the source changes.
  // Hold the refresh while the reviewer is mid-comment.
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
    htmx.ajax("GET", "/content", { target: "#content", swap: "innerHTML" });
  }
  setInterval(function () {
    if (refreshPending) refresh();
  }, 2000);

  var es = new EventSource("/events");
  es.addEventListener("change", refresh);

  renderMermaid();
  applyFileState();
})();
