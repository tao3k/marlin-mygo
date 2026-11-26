// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package kit

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/runtime/style"
)

// WebIslandHost resolves a sandboxed native WebView instance by semantic island
// ID (e.g. "workflow-graph", "bpmn-studio", or a user-authored island under
// ~/.config/marlin/ui/islands/<id>/index.html). Returning nil triggers a clean
// non-blocking GPU-native loading/fallback surface (headless tests, Web WASM,
// marlin_minimal, or async WebView warm-up).
type WebIslandHost interface {
	Resolve(islandID string) ui.NativeWebView
}

// WebIslandConfig configures one Hybrid GPU + Sandboxed WebView surface.
type WebIslandConfig struct {
	Key        string
	IslandID   string
	Title      string
	Subtitle   string
	Height     float32 // 0 means Grow(1) fill
	Borderless bool    // true embeds full-bleed directly into the host Emacs Window with zero redundant chrome
	Palette    Palette
	Sheet      *style.Sheet
	Surface    SurfaceOptions
	Host       WebIslandHost
}

// WebIslandFrame renders a transparent sandboxed native ui.WebView either
// full-bleed inside an Emacs Window (Borderless: true, zero wasted header or
// border chrome) or inside a frosted floating Bento card (Borderless: false).
// Native GPU overlays (Hydra popovers, left EdgeRail, tooltips, modals)
// composite seamlessly over the embedded TS/HTML/BPMN/Flowchart canvas.
func WebIslandFrame(c *ui.Context, cfg WebIslandConfig) {
	key := cfg.Key
	if key == "" {
		key = "kit-webisland-" + cfg.IslandID
	}
	pal := cfg.Palette

	var wv ui.NativeWebView
	if cfg.Host != nil && cfg.IslandID != "" {
		wv = cfg.Host.Resolve(cfg.IslandID)
	}

	if cfg.Borderless {
		vp := ui.Column(c.Key(key+"-full")).AlignItems(ui.Stretch).Clip()
		if cfg.Height > 0 {
			vp.Height(cfg.Height)
		} else {
			vp.Fill()
		}
		vp.Children(func() {
			if wv != nil {
				ui.WebView(c.Key(key+"-wv"), wv).Fill()
				return
			}
			renderWebIslandPlaceholder(c, key, cfg.IslandID, pal)
		})
		return
	}

	cardRad := ResolveRadius(cfg.Sheet, "--radius-card", RadiusCard)
	innerRad := ConcentricRadius(cardRad, 8)
	card := SafeCol(c, key, 8).Padding(12, 14).AlignItems(ui.Stretch)
	if cfg.Height > 0 {
		card.Height(cfg.Height)
	} else {
		card.Grow(1).MinHeight(0)
	}
	ApplyCardSurface(card, []string{"bento-card"}, false, pal, cfg.Sheet, cfg.Surface)

	card.Children(func() {
		if cfg.Title != "" {
			SafeRow(c, key+"-hdr", 8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				Headline(c, cfg.Title, 13.5, pal.Ink)
				if cfg.Subtitle != "" {
					Caption(c, cfg.Subtitle, pal.Muted)
				}
			})
		}

		viewport := ui.Column(c.Key(key+"-vp")).Grow(1).MinHeight(0).Radius(innerRad).Clip().
			Background(WithAlpha(pal.Bar, 0.24)).
			AlignItems(ui.Stretch)
		viewport.Children(func() {
			if wv != nil {
				ui.WebView(c.Key(key+"-wv"), wv).Fill()
				return
			}
			renderWebIslandPlaceholder(c, key, cfg.IslandID, pal)
		})
	})
}

func renderWebIslandPlaceholder(c *ui.Context, key, islandID string, pal Palette) {
	SafeCol(c, key+"-fb", 8).Fill().Justify(ui.Center).AlignItems(ui.Center).Padding(16).Children(func() {
		LoadingIndicator(c, 22, pal.Info, false)
		Caption(c, "Preparing canvas · "+islandID, pal.Muted)
	})
}

func hexRGB(c ui.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// WebIslandThemeScript builds a minimal, injection-safe JS snippet that pushes
// the live Marlin theme palette and glass opacity into any mounted WebView
// island's `:root` CSS custom properties and dispatches a `marlin:theme` event
// without reloading the page.
func WebIslandThemeScript(pal Palette, glassOpacity float32) string {
	if glassOpacity <= 0 {
		glassOpacity = DefaultGlassOpacity
	}
	return fmt.Sprintf(`(function(){
const d={surface:%q,bar:%q,ink:%q,muted:%q,accent:%q,info:%q,success:%q,warning:%q,danger:%q,isLight:%t,glassOpacity:%.3f};
const r=document.documentElement.style;
r.setProperty("--surface",d.surface);
r.setProperty("--bar",d.bar);
r.setProperty("--ink",d.ink);
r.setProperty("--muted",d.muted);
r.setProperty("--accent",d.accent);
r.setProperty("--info",d.info);
r.setProperty("--success",d.success);
r.setProperty("--warning",d.warning);
r.setProperty("--danger",d.danger);
r.setProperty("--glass-opacity",String(d.glassOpacity));
document.documentElement.dataset.theme=d.isLight?"light":"dark";
window.dispatchEvent(new CustomEvent("marlin:theme",{detail:d}));
})();`,
		hexRGB(pal.Surface), hexRGB(pal.Bar), hexRGB(pal.Ink), hexRGB(pal.Muted),
		hexRGB(pal.Accent), hexRGB(pal.Info), hexRGB(pal.Success), hexRGB(pal.Warning), hexRGB(pal.Danger),
		pal.IsLight, glassOpacity,
	)
}

// WebIslandActionScript builds an injection-safe JS snippet dispatching a
// typed `marlin:action` CustomEvent into a live WebView island from native GPU
// widgets or sidebars (e.g. action URI `island:workflow-graph:step`).
func WebIslandActionScript(event, payloadJSON string) string {
	if payloadJSON == "" {
		payloadJSON = "null"
	}
	return fmt.Sprintf(`(function(){window.dispatchEvent(new CustomEvent("marlin:action",{detail:{event:%q,payload:%s}}));})();`, event, payloadJSON)
}

// WorkflowStudioHTML returns a zero-CDN, strict-CSP, full-bleed transparent
// interactive BPMN / Agent DAG Flowchart canvas themed dynamically to pal and
// live-listening for `marlin:theme` updates when the user switches themes or
// edits ~/.config/marlin/ui/manifest.toml.
func WorkflowStudioHTML(pal Palette) string {
	themeMode := "dark"
	if pal.IsLight {
		themeMode = "light"
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="en" data-theme="%s">
<head>
<meta charset="utf-8" />
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline';" />
<title>Marlin Workflow Canvas</title>
<style>
  :root {
    --surface: %s;
    --bar: %s;
    --ink: %s;
    --muted: %s;
    --accent: %s;
    --info: %s;
    --success: %s;
    --warning: %s;
    --danger: %s;
    --glass-opacity: 0.34;
    --dot: rgba(255,255,255,0.10);
    --hairline: rgba(255,255,255,0.16);
    --pill-bg: color-mix(in srgb, var(--surface) 44%%, transparent);
    --node-fill: color-mix(in srgb, var(--surface) 58%%, transparent);
    --inspector-fill: color-mix(in srgb, var(--bar) 64%%, transparent);
  }
  :root[data-theme="light"] {
    --dot: rgba(15,23,42,0.12);
    --hairline: rgba(15,23,42,0.14);
    --pill-bg: color-mix(in srgb, var(--surface) 68%%, transparent);
    --node-fill: color-mix(in srgb, var(--surface) 78%%, transparent);
    --inspector-fill: color-mix(in srgb, var(--bar) 78%%, transparent);
  }
  * { box-sizing: border-box; user-select: none; }
  html, body {
    margin: 0; padding: 0; width: 100%%; height: 100%%; overflow: hidden;
    background: transparent;
    color: var(--ink);
    font-family: -apple-system, BlinkMacSystemFont, "SF Pro Display", system-ui, sans-serif;
    -webkit-font-smoothing: antialiased;
  }
  .stage { position: relative; width: 100%%; height: 100%%; overflow: hidden; }
  svg { width: 100%%; height: 100%%; display: block; }
  .hud {
    position: absolute; top: 12px; right: 14px; display: flex; gap: 6px; z-index: 4;
  }
  .pill {
    background: var(--pill-bg); border: 1px solid var(--hairline);
    color: var(--ink); border-radius: 999px; padding: 5px 12px; font-size: 11px;
    font-weight: 600; cursor: pointer; backdrop-filter: blur(12px);
    transition: transform .12s ease, border-color .15s ease;
  }
  .pill:hover { border-color: var(--info); }
  .pill:active { transform: scale(0.96); }
  .edge { fill: none; stroke: color-mix(in srgb, var(--info) 48%%, transparent); stroke-width: 2; }
  .edge-flow {
    fill: none; stroke: var(--info); stroke-width: 2.6;
    stroke-dasharray: 6 10; animation: flow 1.5s linear infinite;
  }
  @keyframes flow { to { stroke-dashoffset: -32; } }
  .node { cursor: grab; }
  .node:active { cursor: grabbing; }
  .node-box {
    fill: var(--node-fill); stroke: var(--hairline); stroke-width: 1.1;
    rx: 14; ry: 14; transition: stroke .15s ease;
  }
  .node.selected .node-box { stroke: var(--info); stroke-width: 1.8; }
  .node-tag { font-size: 9.5px; font-weight: 700; letter-spacing: 0.08em; fill: var(--muted); }
  .node-title { font-size: 13px; font-weight: 600; fill: var(--ink); }
  .node-sub { font-size: 10.5px; fill: var(--muted); font-family: "SF Mono", ui-monospace, monospace; }
  .inspector {
    position: absolute; right: 14px; bottom: 14px; width: 256px;
    background: var(--inspector-fill); border: 1px solid var(--hairline);
    border-radius: 14px; padding: 11px 13px; backdrop-filter: blur(14px);
    box-shadow: 0 10px 28px rgba(0,0,0,0.28);
  }
  .inspector h4 { margin: 0 0 4px; font-size: 12.5px; color: var(--ink); }
  .inspector p { margin: 0; font-size: 11px; line-height: 1.45; color: var(--muted); }
</style>
</head>
<body>
<div class="stage">
  <div class="hud">
    <button class="pill" id="btn-add">+ Node</button>
    <button class="pill" id="btn-step">Step</button>
    <button class="pill" id="btn-save">Sync</button>
    <button class="pill" id="btn-reset">Reset</button>
  </div>
  <svg id="canvas" viewBox="0 0 920 460" preserveAspectRatio="xMidYMid meet">
    <defs>
      <pattern id="dots" width="22" height="22" patternUnits="userSpaceOnUse">
        <circle cx="2" cy="2" r="1" fill="var(--dot)" />
      </pattern>
    </defs>
    <rect width="100%%" height="100%%" fill="url(#dots)" />
    <g id="edges"></g>
    <g id="nodes"></g>
  </svg>
  <div class="inspector" id="inspector">
    <h4>Planner Agent</h4>
    <p>Decomposes user intent into typed Scheme graph proposals before Rust ABI readiness gating.</p>
  </div>
</div>
<script>
const initialNodes = [
  { id: "start",   x: 36,  y: 185, w: 150, h: 72, tag: "EVENT",     title: "User Intent",    sub: "marlin.ui.v1",   color: "var(--info)",    desc: "Receives prompt or slash action from the native GPU workbench over gRPC." },
  { id: "plan",    x: 242, y: 185, w: 164, h: 72, tag: "AGENT",     title: "Planner Agent",  sub: "scheme/graph",   color: "var(--accent)",  desc: "Decomposes user intent into typed Scheme graph proposals before Rust ABI readiness gating." },
  { id: "gate",    x: 472, y: 84,  w: 170, h: 72, tag: "BPMN GATE", title: "Human Approval", sub: "tier-0 receipt", color: "var(--warning)", desc: "Authoritative Tier-0 PromptGate verifying side-effecting tool calls with idempotent receipts." },
  { id: "exec",    x: 472, y: 286, w: 170, h: 72, tag: "TOOL",      title: "Rust Runtime",   sub: "marlin-core",    color: "var(--success)", desc: "Executes admitted workspace tools inside Rust-owned capability sandboxes." },
  { id: "finish",  x: 710, y: 185, w: 164, h: 72, tag: "STREAM",    title: "GPU Synthesis",  sub: "120Hz markdown", color: "var(--info)",    desc: "Streams structured markdown and live telemetry receipts back to Marlin Desktop." }
];
const initialEdges = [
  ["start", "plan"], ["plan", "gate"], ["plan", "exec"], ["gate", "finish"], ["exec", "finish"]
];
let nodes = structuredClone(initialNodes);
let edges = structuredClone(initialEdges);
let selectedId = "plan";
let activeEdgeIdx = 0;
let customSeq = 1;

const svg = document.getElementById("canvas");
const edgesG = document.getElementById("edges");
const nodesG = document.getElementById("nodes");
const inspector = document.getElementById("inspector");

function byId(id) { return nodes.find(n => n.id === id); }

function render() {
  edgesG.innerHTML = "";
  edges.forEach(([fromId, toId], idx) => {
    const a = byId(fromId), b = byId(toId);
    if (!a || !b) return;
    const x1 = a.x + a.w, y1 = a.y + a.h/2;
    const x2 = b.x, y2 = b.y + b.h/2;
    const dx = Math.max(40, (x2 - x1) * 0.5);
    const d = "M "+x1+" "+y1+" C "+(x1+dx)+" "+y1+", "+(x2-dx)+" "+y2+", "+x2+" "+y2;
    const p = document.createElementNS("http://www.w3.org/2000/svg", "path");
    p.setAttribute("d", d);
    p.setAttribute("class", idx === activeEdgeIdx ? "edge-flow" : "edge");
    edgesG.appendChild(p);
  });

  nodesG.innerHTML = "";
  nodes.forEach(n => {
    const g = document.createElementNS("http://www.w3.org/2000/svg", "g");
    g.setAttribute("class", "node" + (n.id === selectedId ? " selected" : ""));
    g.setAttribute("transform", "translate("+n.x+","+n.y+")");
    g.innerHTML =
      '<rect class="node-box" width="'+n.w+'" height="'+n.h+'" />' +
      '<circle cx="16" cy="18" r="4" fill="'+n.color+'" />' +
      '<text class="node-tag" x="27" y="21">'+n.tag+'</text>' +
      '<text class="node-title" x="14" y="43">'+n.title+'</text>' +
      '<text class="node-sub" x="14" y="60">'+n.sub+'</text>';

    g.addEventListener("pointerdown", (ev) => {
      selectedId = n.id;
      inspector.innerHTML = "<h4>"+n.title+"</h4><p>"+n.desc+"</p>";
      const pt = svg.createSVGPoint();
      const ctm = svg.getScreenCTM().inverse();
      pt.x = ev.clientX; pt.y = ev.clientY;
      const start = pt.matrixTransform(ctm);
      const ox = n.x, oy = n.y;
      const onMove = (me) => {
        pt.x = me.clientX; pt.y = me.clientY;
        const cur = pt.matrixTransform(ctm);
        n.x = Math.max(12, Math.min(904 - n.w, ox + (cur.x - start.x)));
        n.y = Math.max(12, Math.min(444 - n.h, oy + (cur.y - start.y)));
        render();
      };
      const onUp = () => {
        window.removeEventListener("pointermove", onMove);
        window.removeEventListener("pointerup", onUp);
      };
      window.addEventListener("pointermove", onMove);
      window.addEventListener("pointerup", onUp);
      render();
    });
    nodesG.appendChild(g);
  });
}

document.getElementById("btn-add").addEventListener("click", () => {
  const id = "step-" + (customSeq++);
  const parent = byId(selectedId) || nodes[nodes.length - 1];
  const nx = Math.min(730, (parent ? parent.x + 110 : 320));
  const ny = Math.min(365, (parent ? parent.y + 84 : 200));
  nodes.push({ id, x: nx, y: ny, w: 162, h: 72, tag: "TASK", title: "Step " + (customSeq - 1), sub: "custom/node", color: "var(--accent)", desc: "Interactive workflow node spawned in Hybrid WebView canvas." });
  if (parent) edges.push([parent.id, id]);
  selectedId = id;
  inspector.innerHTML = "<h4>Step " + (customSeq - 1) + "</h4><p>Interactive workflow node spawned in Hybrid WebView canvas.</p>";
  render();
});
document.getElementById("btn-reset").addEventListener("click", () => {
  nodes = structuredClone(initialNodes);
  edges = structuredClone(initialEdges);
  selectedId = "plan";
  activeEdgeIdx = 0;
  render();
});
document.getElementById("btn-step").addEventListener("click", () => {
  activeEdgeIdx = (activeEdgeIdx + 1) %% Math.max(1, edges.length);
  render();
});
async function syncGraphToHost() {
  const payload = JSON.stringify({ schemaVersion: "marlin.workflow.graph.v1", nodes, edges });
  const rpc = window.mygo && window.mygo.call;
  if (typeof rpc === "function") {
    try {
      const ack = await rpc("WorkflowBridge.SaveGraph", payload);
      inspector.innerHTML = "<h4>Synced to Marlin Host</h4><p>" + String(ack || "Graph saved") + "</p>";
      return;
    } catch (e) {
      inspector.innerHTML = "<h4>Sync Warning</h4><p>" + String(e) + "</p>";
      return;
    }
  }
  inspector.innerHTML = "<h4>Graph Snapshot Ready</h4><p>" + nodes.length + " nodes · " + edges.length + " edges (local sandbox)</p>";
}
document.getElementById("btn-save").addEventListener("click", () => { syncGraphToHost(); });
window.addEventListener("marlin:action", (ev) => {
  const act = ev.detail && ev.detail.event;
  if (act === "add-node") document.getElementById("btn-add").click();
  else if (act === "step") document.getElementById("btn-step").click();
  else if (act === "save") syncGraphToHost();
  else if (act === "reset") document.getElementById("btn-reset").click();
});
render();
</script>
</body>
</html>`,
		themeMode,
		hexRGB(pal.Surface), hexRGB(pal.Bar), hexRGB(pal.Ink), hexRGB(pal.Muted),
		hexRGB(pal.Accent), hexRGB(pal.Info), hexRGB(pal.Success), hexRGB(pal.Warning), hexRGB(pal.Danger),
	)
}
