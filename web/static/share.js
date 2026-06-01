"use strict";

(function () {
  const button = document.querySelector("[data-share]");
  if (!button) return;

  const canShare = typeof navigator.share === "function";
  const canCopy = navigator.clipboard && typeof navigator.clipboard.writeText === "function";
  if (!canShare && !canCopy) return;

  const original = button.textContent;
  button.hidden = false;

  function canonicalURL() {
    const link = document.querySelector('link[rel="canonical"]');
    return link && link.href ? link.href : location.href.split("#")[0];
  }

  function description() {
    const meta = document.querySelector('meta[property="og:description"], meta[name="description"]');
    return meta && meta.content ? meta.content : "";
  }

  function flash(label) {
    button.textContent = label;
    window.setTimeout(() => {
      button.textContent = original;
    }, 1400);
  }

  button.addEventListener("click", async () => {
    const url = canonicalURL();
    const payload = {
      title: document.title || "igrec",
      text: description(),
      url: url
    };

    try {
      if (canShare) {
        await navigator.share(payload);
        return;
      }
      await navigator.clipboard.writeText(url);
      flash("скопировано");
    } catch (error) {
      if (error && error.name === "AbortError") return;
      if (!canCopy) return;
      await navigator.clipboard.writeText(url);
      flash("скопировано");
    }
  });
})();
