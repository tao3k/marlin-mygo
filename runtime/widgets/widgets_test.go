// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package widgets_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/runtime/kit"
	"github.com/tao3k/marlin-mygo/runtime/widgets"
)

func TestSignalsDualParsersAndFlagshipBentoRender(t *testing.T) {
	sig := widgets.NewSignals()
	sig.SetInt("tasks.completed", 5)
	sig.SetInt("tasks.total", 8)
	sig.SetFloat("tasks.progress", 0.625)
	sig.SetFloat("agents.progress", 0.8)
	sig.SetFloat("sessions.progress", 0.5)
	sig.SetFloat("targets.progress", 1.0)
	sig.Set("agent.session", "marlin-main")
	sig.Set("agent.host", "127.0.0.1:50051")
	sig.Set("agent.status", "Online")
	sig.Set("agent.model", "deepseek-chat")
	sig.SetInt("agents.online", 3)
	sig.Set("clock.hour", "10")
	sig.Set("clock.min", "43")
	sig.Set("clock.sec", "12")
	sig.Set("clock.ampm", "PM")
	sig.Set("clock.weekday", "Thursday")
	sig.SetInt("uptime.hours", 1)
	sig.SetInt("uptime.mins", 24)
	sig.SetInt("buffers.count", 9)
	sig.Set("theme.name", "nord-bento")
	sig.Set("mod.glass", "ON")
	sig.Set("mod.motion", "ON")
	sig.Set("mod.zen", "OFF")

	sexpNode, err := widgets.ParseSexp(`
		(card :id "sexp-watch" :on-click "cmd:reload-theme"
		  (kpi :icon "sparkles" :value "{{tasks.completed}} Done" :title "Sexp Watch" :tone "success"))
	`)
	if err != nil {
		t.Fatalf("ParseSexp valid failed: %v", err)
	}
	if _, err := widgets.ParseSexp(`(evil-shell :cmd "id")`); err == nil {
		t.Fatalf("expected unknown S-exp component to fail closed")
	}

	tomlNode, err := widgets.ParseWidgetTOML("toml-watch", `
		kind = "kpi"
		icon = "check"
		title = "TOML Watch"
		value = "5 Ready"
		progress = "{{tasks.progress}}"
		tone = "info"
	`)
	if err != nil {
		t.Fatalf("ParseWidgetTOML valid failed: %v", err)
	}
	if _, err := widgets.ParseWidgetTOML("bad", `kind = "shell-exec"`); err == nil {
		t.Fatalf("expected unknown TOML kind to fail closed")
	}

	pal := kit.Palette{
		Canvas:  ui.Hex("#1e222a"),
		Bar:     ui.Hex("#191d24"),
		Surface: ui.Hex("#2b303c"),
		Hover:   ui.Hex("#353c4b"),
		Border:  ui.Hex("#3d4556"),
		Ink:     ui.Hex("#e5e9f0"),
		Muted:   ui.Hex("#8c96a8"),
		Accent:  ui.Hex("#d48ab8"),
		Info:    ui.Hex("#88c0d0"),
		Success: ui.Hex("#a3be8c"),
		Warning: ui.Hex("#ebcb8b"),
		Danger:  ui.Hex("#e06c75"),
	}

	root := widgets.DefaultDashboard([]*widgets.Node{sexpNode, tomlNode})
	promptDraft := ""
	taskDraft := ""
	cycled := ""
	action := ""
	rt := &widgets.Runtime{
		Modules:      widgets.Modules{Glass: true, Motion: false},
		PromptDraft:  &promptDraft,
		NewTaskDraft: &taskDraft,
		Tasks: []widgets.TaskItem{
			{ID: "t1", Title: "Ship iOS 26 Liquid Glass Bento Kit", Status: "IN_PROGRESS", Owner: "@you"},
		},
		OnCycleTask: func(id string) { cycled = id },
		OnAction:    func(a string) { action = a },
	}

	tt := ui.NewTester(func(c *ui.Context) {
		kit.SafeCol(c, "stage", 0).Fill().Background(pal.Canvas).Padding(16).Children(func() {
			kit.RenderGlassAtmosphere(c, pal, kit.SurfaceOptions{Glass: true, Motion: false})
			widgets.Render(c, root, sig, pal, nil, rt)
		})
	}, 1280, 860)

	joined := strings.Join(tt.Texts(), " | ")
	if !tt.HasText("Marlin AI") || !tt.HasText("5/8 Done") || !tt.HasText("Sexp Watch") || !tt.HasText("TOML Watch") {
		t.Fatalf("expected flagship Bento dashboard + user widgets, got %q", joined)
	}
	if err := tt.Click("Ship iOS 26 Liquid Glass Bento Kit"); err != nil {
		t.Fatalf("click task row: %v", err)
	}
	tt.Frame()
	if cycled != "t1" {
		t.Fatalf("expected task t1 cycled, got %q", cycled)
	}
	if err := tt.Click("Zen"); err != nil {
		t.Fatalf("click Zen tile: %v", err)
	}
	tt.Frame()
	if action != "cmd:zen-mode" {
		t.Fatalf("expected Zen action cmd:zen-mode, got %q", action)
	}
}
