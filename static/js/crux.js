// Full-size viewer for the /crux page. Every image is a link to its file, so
// without JavaScript (or without <dialog>) a click simply opens the image.
(function () {
  "use strict";

  function init() {
    var viewer = document.getElementById("crux-viewer");
    if (!viewer || typeof viewer.showModal !== "function") return;

    var img = viewer.querySelector("img");
    var caption = viewer.querySelector(".crux-viewer-caption");
    var closeBtn = viewer.querySelector(".crux-viewer-close");

    document.querySelectorAll("a.crux-zoom").forEach(function (link) {
      link.addEventListener("click", function (e) {
        // Leave modified clicks alone so "open in new tab" still works.
        if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        e.preventDefault();

        var thumb = link.querySelector("img");
        var fig = link.closest("figure");
        var cap = fig ? fig.querySelector("figcaption") : null;

        img.src = link.href;
        img.alt = thumb ? thumb.alt : "";
        caption.textContent = cap ? cap.textContent : "";
        viewer.showModal();
      });
    });

    closeBtn.addEventListener("click", function () { viewer.close(); });

    // Click the image or the dark surround to close.
    viewer.addEventListener("click", function (e) {
      if (e.target === viewer || e.target === img) viewer.close();
    });

    // Drop the image so a large file is not held, and the next one does not
    // flash the previous picture while it loads.
    viewer.addEventListener("close", function () { img.removeAttribute("src"); });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
