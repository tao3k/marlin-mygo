// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { defineMarlinPanel } from "@tao3k/marlin-panel-sdk";

const summaryEl = document.getElementById("summary");
const receiptEl = document.getElementById("receipt");

const panel = defineMarlinPanel({
  pluginId: "tao3k.session-radar",
  events: {
    "session.summary": (ev) => {
      if (summaryEl) {
        summaryEl.textContent = JSON.stringify(ev.payload, null, 2);
      }
    },
  },
});

document.getElementById("ping")?.addEventListener("click", async () => {
  const receipt = await panel.submit("session.radar.ping", { source: "ts-radar" });
  if (receiptEl) {
    receiptEl.textContent = receipt.admitted
      ? `Admitted: ${receipt.receiptId || "ok"}`
      : `Denied: ${receipt.denial || "rejected"}`;
  }
});
