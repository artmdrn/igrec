"use strict";

(function () {
  const root = document.querySelector("[data-compose]");
  if (!root) return;

  const fileInput = root.querySelector("[data-image-file]");
  const previewWrap = root.querySelector("[data-preview-wrap]");
  const previewImage = root.querySelector("[data-preview-image]");
  const previewEmpty = root.querySelector("[data-preview-empty]");
  const focusX = root.querySelector("[data-focus-x]");
  const focusY = root.querySelector("[data-focus-y]");
  const focusTarget = root.querySelector("[data-focus-target]");

  const setFocus = (x, y) => {
    const safeX = Math.min(1, Math.max(0, x));
    const safeY = Math.min(1, Math.max(0, y));
    focusX.value = safeX.toFixed(4);
    focusY.value = safeY.toFixed(4);
    previewImage.style.objectPosition = `${(safeX * 100).toFixed(1)}% ${(safeY * 100).toFixed(1)}%`;
    focusTarget.style.left = `${(safeX * 100).toFixed(1)}%`;
    focusTarget.style.top = `${(safeY * 100).toFixed(1)}%`;
  };

  fileInput.addEventListener("change", () => {
    const file = fileInput.files && fileInput.files[0];
    if (!file) return;

    previewImage.src = URL.createObjectURL(file);
    previewImage.hidden = false;
    previewEmpty.hidden = true;
    focusTarget.hidden = false;
    previewWrap.classList.add("ready");
    setFocus(0.5, 0.5);
  });

  previewWrap.addEventListener("pointerdown", (event) => {
    if (!previewWrap.classList.contains("ready")) return;
    const rect = previewWrap.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) return;
    setFocus((event.clientX - rect.left) / rect.width, (event.clientY - rect.top) / rect.height);
  });
})();
