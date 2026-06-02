"use strict";

(function () {
  const root = document.querySelector("[data-compose]");
  if (!root) return;

  const fileInput = root.querySelector("[data-image-file]");
  const previewWrap = root.querySelector("[data-preview-wrap]");
  const previewImage = root.querySelector("[data-preview-image]");
  const previewEmpty = root.querySelector("[data-preview-empty]");
  const wordInput = root.querySelector("[data-word-input]");

  fileInput.addEventListener("change", () => {
    const file = fileInput.files && fileInput.files[0];
    if (!file) return;

    previewImage.src = URL.createObjectURL(file);
    previewImage.hidden = false;
    previewEmpty.hidden = true;
    previewWrap.classList.add("ready");
    if (wordInput) wordInput.focus();
  });
})();
