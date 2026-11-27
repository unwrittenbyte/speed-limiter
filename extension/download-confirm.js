// SPDX-License-Identifier: GPL-3.0-only
"use strict";
(() => {
  const dialog = document.getElementById("downloadDialog");
  for (const link of document.querySelectorAll("[data-download-trigger]")) {
    link.addEventListener("click", event => {
      event.preventDefault();
      if (!dialog.open) dialog.showModal();
    });
  }
  document.getElementById("cancelDownload").addEventListener("click", () => dialog.close());
  document.getElementById("confirmDownload").addEventListener("click", () => dialog.close());
})();
