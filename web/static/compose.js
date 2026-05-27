"use strict";

(function () {
  const root = document.querySelector("[data-compose]");
  if (!root) return;

  const fileInput = root.querySelector("[data-image-file]");
  const previewWrap = root.querySelector("[data-preview-wrap]");
  const previewImage = root.querySelector("[data-preview-image]");
  const previewEmpty = root.querySelector("[data-preview-empty]");
  const focusToggle = root.querySelector("[data-focus-toggle]");
  const focusPin = root.querySelector("[data-focus-pin]");
  const focusX = root.querySelector("[data-focus-x]");
  const focusY = root.querySelector("[data-focus-y]");

  function setPin(x, y) {
    focusX.value = String(x);
    focusY.value = String(y);
    focusPin.style.left = (x * 100).toFixed(2) + "%";
    focusPin.style.top = (y * 100).toFixed(2) + "%";
  }

  function readFile(file) {
    const reader = new FileReader();
    reader.onload = () => {
      previewImage.src = String(reader.result || "");
      previewImage.hidden = false;
      focusPin.hidden = !focusToggle.checked;
      previewEmpty.hidden = true;
      previewWrap.classList.add("ready");
    };
    reader.readAsDataURL(file);
  }

  fileInput.addEventListener("change", () => {
    const file = fileInput.files && fileInput.files[0];
    if (!file) return;
    setPin(0.5, 0.5);
    readFile(file);
  });

  focusToggle.addEventListener("change", () => {
    if (!previewImage.hidden) {
      focusPin.hidden = !focusToggle.checked;
    }
  });

  previewWrap.addEventListener("click", (event) => {
    if (previewImage.hidden || !focusToggle.checked) return;
    const rect = previewWrap.getBoundingClientRect();
    const x = Math.min(1, Math.max(0, (event.clientX - rect.left) / rect.width));
    const y = Math.min(1, Math.max(0, (event.clientY - rect.top) / rect.height));
    setPin(x, y);
  });
})();
