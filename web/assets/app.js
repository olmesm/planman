/* planman frontend glue: mermaid rendering, SSE live reload, author memory. */
(function () {
  "use strict";

  mermaid.initialize({ startOnLoad: false });

  function renderMermaid() {
    var nodes = document.querySelectorAll(".mermaid:not([data-processed])");
    if (nodes.length) {
      mermaid.run({ nodes: nodes }).catch(function (err) {
        console.error("mermaid:", err);
      });
    }
  }

  // Author name persistence.
  var author = document.getElementById("author");
  if (author) {
    author.value = localStorage.getItem("planman-author") || "";
    author.addEventListener("input", function () {
      localStorage.setItem("planman-author", author.value);
    });
  }

  // Re-render mermaid after any htmx swap (doc refresh, comment posted).
  document.body.addEventListener("htmx:afterSwap", renderMermaid);

  // Live reload: server broadcasts when the file changes on disk.
  var es = new EventSource("/events");
  es.addEventListener("change", function () {
    htmx.ajax("GET", "/doc", { target: "#doc", swap: "innerHTML" });
  });

  renderMermaid();
})();
