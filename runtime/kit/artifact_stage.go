// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package kit

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func stageHex(c ui.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// ArtifactStageLoadScript returns a safe JS snippet that pushes a Vercel AI SDK
// `data-*` Generative UI payload into the sandboxed Artifact Stage WebIsland.
func ArtifactStageLoadScript(dataType, title, rawPayload string) string {
	env := struct {
		DataType string `json:"dataType"`
		Title    string `json:"title"`
		Payload  string `json:"payload"`
	}{
		DataType: dataType,
		Title:    title,
		Payload:  rawPayload,
	}
	b, _ := json.Marshal(env)
	return WebIslandActionScript("artifact:load", string(b))
}

// ArtifactStageHTML returns a zero-CDN, transparent, Liquid-Glass-aware
// Layer 3 Sandboxed WebView HTML stage for rendering Vercel AI SDK `data-*`
// Generative UI parts (data-mermaid, data-workflow, data-chart, data-table,
// data-json) using unified `--marlin-*` and shadcn CSS tokens.
func ArtifactStageHTML(pal Palette) string {
	css := fmt.Sprintf(`:root {
  --marlin-surface: %s;
  --marlin-bar: %s;
  --marlin-ink: %s;
  --marlin-muted: %s;
  --marlin-accent: %s;
  --marlin-info: %s;
  --marlin-success: %s;
  --marlin-warning: %s;
  --marlin-danger: %s;
}`, stageHex(pal.Surface), stageHex(pal.Bar), stageHex(pal.Ink), stageHex(pal.Muted),
		stageHex(pal.Accent), stageHex(pal.Info), stageHex(pal.Success), stageHex(pal.Warning), stageHex(pal.Danger))
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline';" />
<meta name="viewport" content="width=device-width,initial-scale=1" />
<title>Marlin Generative Artifact Stage</title>
<style>
`)
	b.WriteString(css)
	b.WriteString(`
:root {
  --background: transparent;
  --foreground: var(--marlin-ink);
  --card: var(--marlin-surface);
  --primary: var(--marlin-accent);
  --muted-foreground: var(--marlin-muted);
  --border: color-mix(in srgb, var(--marlin-ink) 12%, transparent);
}
* { box-sizing: border-box; }
html, body {
  margin: 0; padding: 0; width: 100%; height: 100%;
  background: transparent; color: var(--foreground);
  font: 13px/1.5 -apple-system, BlinkMacSystemFont, "SF Pro Text", "Inter", system-ui, sans-serif;
  -webkit-font-smoothing: antialiased; overflow: hidden;
}
.shell { display: flex; flex-direction: column; width: 100%; height: 100%; padding: 14px; gap: 12px; }
.hdr {
  display: flex; align-items: center; justify-content: space-between; gap: 8px;
  padding: 8px 12px; border-radius: 10px;
  background: color-mix(in srgb, var(--marlin-bar) 65%, transparent);
  border: 1px solid var(--border);
}
.badge {
  font: 600 11px/1 ui-monospace, "SF Mono", Menlo, monospace;
  padding: 3px 8px; border-radius: 999px;
  color: var(--primary); background: color-mix(in srgb, var(--primary) 16%, transparent);
}
.title { font-weight: 600; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tabs { display: flex; gap: 4px; }
.btn {
  border: 1px solid var(--border); border-radius: 6px; padding: 3px 9px;
  background: transparent; color: var(--muted-foreground); cursor: pointer;
  font: 600 11px/1.3 -apple-system, system-ui, sans-serif;
}
.btn.active, .btn:hover { color: var(--foreground); background: color-mix(in srgb, var(--marlin-surface) 80%, transparent); }
.stage {
  flex: 1; min-height: 0; overflow: auto; border-radius: 12px; padding: 16px;
  background: color-mix(in srgb, var(--marlin-surface) 58%, transparent);
  border: 1px solid var(--border);
}
.stage.paused * { animation-play-state: paused !important; }
svg.diagram { width: 100%; height: auto; min-height: 220px; display: block; }
pre.raw {
  margin: 0; font: 12px/1.55 ui-monospace, "SF Mono", Menlo, monospace;
  color: var(--foreground); white-space: pre-wrap; word-break: break-word;
}
table.grid { width: 100%; border-collapse: collapse; font-size: 12px; }
table.grid th, table.grid td { text-align: left; padding: 7px 10px; border-bottom: 1px solid var(--border); }
table.grid th { color: var(--muted-foreground); font-weight: 600; font-size: 11px; text-transform: uppercase; letter-spacing: 0.04em; }
.empty { height: 100%; display: flex; align-items: center; justify-content: center; color: var(--muted-foreground); text-align: center; padding: 24px; }
</style>
</head>
<body>
<div class="shell">
  <div class="hdr">
    <div style="display:flex;align-items:center;gap:8px;min-width:0;">
      <span id="badge" class="badge">AI SDK</span>
      <span id="title" class="title">Artifact Stage</span>
    </div>
    <div class="tabs">
      <button id="btn-visual" class="btn active" type="button">Visual</button>
      <button id="btn-source" class="btn" type="button">Source</button>
    </div>
  </div>
  <div id="stage" class="stage"><div class="empty">Select any Generative UI card in *conversation* to inspect it on the Layer-3 Stage.</div></div>
</div>
<script>
(function(){
  let current = { dataType: "", title: "Artifact Stage", payload: "" };
  let mode = "visual";
  const stage = document.getElementById("stage");
  const badge = document.getElementById("badge");
  const titleEl = document.getElementById("title");
  const btnVis = document.getElementById("btn-visual");
  const btnSrc = document.getElementById("btn-source");

  function esc(s){ return String(s).replace(/[&<>"]/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c])); }

  function renderMermaidSVG(src) {
    const edges = [];
    const nodes = new Map();
    const ensure = (id, label) => {
      if (!nodes.has(id)) nodes.set(id, { id, label: label || id });
      else if (label && nodes.get(id).label === id) nodes.get(id).label = label;
    };
    const parseNode = (tok) => {
      const m = tok.trim().match(/^([A-Za-z0-9_.-]+)(?:\[(.*?)\]|\((.*?)\)|\{(.*?)\})?$/);
      if (!m) return tok.trim();
      const id = m[1];
      const lbl = m[2] || m[3] || m[4] || id;
      ensure(id, lbl);
      return id;
    };
    for (const rawLine of src.split(/\r?\n/)) {
      const line = rawLine.trim();
      if (!line || /^(graph|flowchart|sequenceDiagram|%%)/i.test(line)) continue;
      const m = line.match(/(.+?)\s*(?:-->|==>|-.->|->)\s*(?:\|([^|]+)\|\s*)?(.+)/);
      if (m) {
        const a = parseNode(m[1]);
        const b = parseNode(m[3]);
        edges.push({ from: a, to: b, label: (m[2] || "").trim() });
      }
    }
    const list = Array.from(nodes.values());
    if (!list.length) return '<pre class="raw">' + esc(src) + '</pre>';
    const cols = Math.min(3, Math.max(1, list.length));
    const cellW = 190, cellH = 92;
    const pos = new Map();
    list.forEach((n, i) => {
      const col = i % cols, row = Math.floor(i / cols);
      pos.set(n.id, { x: 24 + col * cellW, y: 24 + row * cellH });
    });
    const w = 48 + cols * cellW;
    const h = 48 + (Math.floor((list.length - 1) / cols) + 1) * cellH;
    let svg = '<svg class="diagram" viewBox="0 0 ' + w + ' ' + h + '">';
    svg += '<defs><marker id="arr" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path d="M0,1 L9,5 L0,9 z" fill="var(--marlin-info)" /></marker></defs>';
    for (const e of edges) {
      const p1 = pos.get(e.from), p2 = pos.get(e.to);
      if (!p1 || !p2) continue;
      const x1 = p1.x + 148, y1 = p1.y + 24, x2 = p2.x, y2 = p2.y + 24;
      const cx = (x1 + x2) / 2;
      svg += '<path d="M' + x1 + ',' + y1 + ' C' + cx + ',' + y1 + ' ' + cx + ',' + y2 + ' ' + x2 + ',' + y2 + '" fill="none" stroke="var(--marlin-info)" stroke-width="1.8" marker-end="url(#arr)" />';
      if (e.label) svg += '<text x="' + cx + '" y="' + ((y1+y2)/2 - 6) + '" text-anchor="middle" fill="var(--muted-foreground)" font-size="10">' + esc(e.label) + '</text>';
    }
    for (const n of list) {
      const p = pos.get(n.id);
      svg += '<g transform="translate(' + p.x + ',' + p.y + ')">';
      svg += '<rect width="148" height="48" rx="10" fill="color-mix(in srgb, var(--marlin-bar) 84%, transparent)" stroke="var(--primary)" stroke-width="1.4" />';
      svg += '<text x="74" y="28" text-anchor="middle" fill="var(--foreground)" font-size="12" font-weight="600">' + esc(n.label) + '</text>';
      svg += '</g>';
    }
    svg += '</svg>';
    return svg;
  }

  function renderChartSVG(data) {
    const items = Array.isArray(data) ? data : (Array.isArray(data && data.series) ? data.series : []);
    if (!items.length) return '<pre class="raw">' + esc(JSON.stringify(data, null, 2)) + '</pre>';
    const vals = items.map(it => Number(typeof it === "number" ? it : (it.value ?? it.y ?? 0)));
    const maxV = Math.max(1, ...vals);
    const barW = 46, gap = 16, h = 210, w = Math.max(320, 40 + items.length * (barW + gap));
    let svg = '<svg class="diagram" viewBox="0 0 ' + w + ' ' + h + '">';
    items.forEach((it, i) => {
      const v = vals[i];
      const bh = Math.max(6, Math.round((v / maxV) * 140));
      const x = 24 + i * (barW + gap), y = 168 - bh;
      const lbl = typeof it === "object" && it ? (it.label || it.name || String(i+1)) : String(i+1);
      svg += '<rect x="' + x + '" y="' + y + '" width="' + barW + '" height="' + bh + '" rx="6" fill="var(--primary)" fill-opacity="0.82" />';
      svg += '<text x="' + (x + barW/2) + '" y="' + (y - 6) + '" text-anchor="middle" fill="var(--foreground)" font-size="11">' + esc(v) + '</text>';
      svg += '<text x="' + (x + barW/2) + '" y="188" text-anchor="middle" fill="var(--muted-foreground)" font-size="10.5">' + esc(lbl) + '</text>';
    });
    svg += '</svg>';
    return svg;
  }

  function renderTableHTML(rows) {
    if (!Array.isArray(rows) || !rows.length || typeof rows[0] !== "object") {
      return '<pre class="raw">' + esc(JSON.stringify(rows, null, 2)) + '</pre>';
    }
    const cols = Object.keys(rows[0]);
    let html = '<table class="grid"><thead><tr>' + cols.map(c => '<th>' + esc(c) + '</th>').join("") + '</tr></thead><tbody>';
    for (const r of rows) {
      html += '<tr>' + cols.map(c => '<td>' + esc(r[c] ?? "") + '</td>').join("") + '</tr>';
    }
    html += '</tbody></table>';
    return html;
  }

  function render() {
    badge.textContent = current.dataType || "AI SDK";
    titleEl.textContent = current.title || "Artifact Stage";
    btnVis.classList.toggle("active", mode === "visual");
    btnSrc.classList.toggle("active", mode === "source");
    if (!current.payload) return;

    let decoded = current.payload;
    try { decoded = JSON.parse(current.payload); } catch (_) {}

    if (mode === "source") {
      stage.innerHTML = '<pre class="raw">' + esc(typeof decoded === "string" ? decoded : JSON.stringify(decoded, null, 2)) + '</pre>';
      return;
    }
    if (current.dataType === "data-mermaid" || current.dataType === "data-workflow") {
      const src = typeof decoded === "string" ? decoded : JSON.stringify(decoded, null, 2);
      stage.innerHTML = renderMermaidSVG(src);
      return;
    }
    if (current.dataType === "data-chart") {
      stage.innerHTML = renderChartSVG(decoded);
      return;
    }
    if (current.dataType === "data-table") {
      stage.innerHTML = renderTableHTML(decoded);
      return;
    }
    stage.innerHTML = '<pre class="raw">' + esc(typeof decoded === "string" ? decoded : JSON.stringify(decoded, null, 2)) + '</pre>';
  }

  btnVis.onclick = () => { mode = "visual"; render(); };
  btnSrc.onclick = () => { mode = "source"; render(); };

  window.addEventListener("marlin:action", (ev) => {
    const d = ev.detail || {};
    const p = d.payload || d.detail;
    if (d.event === "artifact:load" && p) {
      current = {
        dataType: p.dataType || "data-artifact",
        title: p.title || p.dataType || "Artifact",
        payload: p.payload || ""
      };
      render();
    }
  });
  window.addEventListener("marlin:theme", (ev) => {
    const t = ev.detail || {};
    const root = document.documentElement.style;
    if (t.surface) root.setProperty("--marlin-surface", t.surface);
    if (t.bar) root.setProperty("--marlin-bar", t.bar);
    if (t.ink) root.setProperty("--marlin-ink", t.ink);
    if (t.muted) root.setProperty("--marlin-muted", t.muted);
    if (t.accent) root.setProperty("--marlin-accent", t.accent);
    if (t.info) root.setProperty("--marlin-info", t.info);
  });
  window.addEventListener("marlin:visibility", (ev) => {
    const vis = !ev.detail || ev.detail.visible !== false;
    stage.classList.toggle("paused", !vis);
  });
})();
</script>
</body>
</html>`)
	return fmt.Sprintf("%s", b.String())
}
