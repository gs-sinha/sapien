// Sapien site: copy buttons, tabs, section highlighting, and the latest
// release number. The page reads fine without any of it.
(function () {
  "use strict";

  var icon = function (id) {
    return '<svg class="i" aria-hidden="true"><use href="#' + id + '"/></svg>';
  };

  // ---- copy ------------------------------------------------------------------

  // A command block's copyable text drops the "$ " prompts and "# ..."
  // comments, which are marked up as .p and .c spans.
  function commandText(pre) {
    var clone = pre.cloneNode(true);
    clone.querySelectorAll(".p, .c").forEach(function (n) {
      n.remove();
    });
    return clone.textContent
      .split("\n")
      .map(function (line) {
        return line.replace(/\s+$/, "");
      })
      .filter(function (line) {
        return line.length > 0;
      })
      .join("\n");
  }

  function writeClipboard(text) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text);
    }
    return new Promise(function (resolve) {
      var ta = document.createElement("textarea");
      ta.value = text;
      ta.setAttribute("readonly", "");
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      try {
        document.execCommand("copy");
      } catch (e) {}
      ta.remove();
      resolve();
    });
  }

  function copyButton(getText) {
    var b = document.createElement("button");
    b.type = "button";
    b.className = "copy";
    b.setAttribute("aria-label", "Copy to clipboard");
    b.innerHTML = icon("i-copy");
    b.addEventListener("click", function () {
      writeClipboard(getText()).then(function () {
        b.classList.add("done");
        b.innerHTML = icon("i-check");
        b.setAttribute("aria-label", "Copied");
        setTimeout(function () {
          b.classList.remove("done");
          b.innerHTML = icon("i-copy");
          b.setAttribute("aria-label", "Copy to clipboard");
        }, 1600);
      });
    });
    return b;
  }

  document.querySelectorAll("pre.code:not([data-nocopy])").forEach(function (pre) {
    var box = document.createElement("div");
    box.className = "codebox";
    pre.parentNode.insertBefore(box, pre);
    box.appendChild(pre);
    box.appendChild(
      copyButton(function () {
        return commandText(pre);
      })
    );
  });

  document.querySelectorAll("figure.prompt").forEach(function (fig) {
    var quote = fig.querySelector("blockquote");
    fig.appendChild(
      copyButton(function () {
        return quote.textContent.trim().replace(/\s+/g, " ");
      })
    );
  });

  // ---- tabs --------------------------------------------------------------------

  document.querySelectorAll("[data-tabs]").forEach(function (group) {
    var list = group.querySelector('[role="tablist"]');
    var tabs = Array.prototype.slice.call(list.querySelectorAll('[role="tab"]'));
    var panels = tabs.map(function (t) {
      return document.getElementById(t.getAttribute("aria-controls"));
    });

    function select(index, focus) {
      tabs.forEach(function (t, i) {
        var on = i === index;
        t.setAttribute("aria-selected", on ? "true" : "false");
        t.tabIndex = on ? 0 : -1;
        panels[i].hidden = !on;
      });
      if (focus) tabs[index].focus();
    }

    tabs.forEach(function (t, i) {
      t.type = "button";
      t.addEventListener("click", function () {
        select(i, false);
      });
      t.addEventListener("keydown", function (e) {
        var step = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
        if (!step) return;
        e.preventDefault();
        select((i + step + tabs.length) % tabs.length, true);
      });
    });

    select(0, false);
  });

  // ---- top bar and section highlighting -------------------------------------

  var bar = document.getElementById("topbar");
  var onScroll = function () {
    bar.classList.toggle("scrolled", window.scrollY > 8);
  };
  window.addEventListener("scroll", onScroll, { passive: true });
  onScroll();

  var links = {};
  document.querySelectorAll(".nav a").forEach(function (a) {
    links[a.getAttribute("href").slice(1)] = a;
  });

  if ("IntersectionObserver" in window) {
    var observer = new IntersectionObserver(
      function (entries) {
        entries.forEach(function (entry) {
          if (!entry.isIntersecting) return;
          Object.keys(links).forEach(function (id) {
            links[id].classList.toggle("active", id === entry.target.id);
          });
        });
      },
      { rootMargin: "-40% 0px -55% 0px" }
    );
    Object.keys(links).forEach(function (id) {
      var section = document.getElementById(id);
      if (section) observer.observe(section);
    });
  }

  // ---- latest release ----------------------------------------------------------

  if (window.fetch) {
    fetch("https://api.github.com/repos/gs-sinha/sapien/releases/latest", {
      headers: { Accept: "application/vnd.github+json" },
    })
      .then(function (r) {
        return r.ok ? r.json() : null;
      })
      .then(function (rel) {
        if (!rel || !/^v\d/.test(rel.tag_name || "")) return;
        document.querySelectorAll("[data-latest]").forEach(function (el) {
          el.textContent = rel.tag_name;
        });
        if (rel.published_at) {
          var when = new Date(rel.published_at).toLocaleDateString(undefined, {
            year: "numeric",
            month: "long",
            day: "numeric",
          });
          document.querySelectorAll("[data-latest-date]").forEach(function (el) {
            el.textContent = when;
          });
        }
        if (rel.html_url) {
          document.querySelectorAll("[data-latest-link]").forEach(function (el) {
            el.href = rel.html_url;
          });
        }
      })
      .catch(function () {});
  }
})();
