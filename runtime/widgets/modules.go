// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package widgets

import "github.com/tao3k/marlin-mygo/runtime/kit"

// Modules tracks hot-pluggable optional visual & interaction modules
// (typically backed by marlin-mygo/engine/features.Registry).
type Modules struct {
	Glass        bool    // Frosted Glass GPU material + macOS NSVisualEffectView transparency
	GlassOpacity float32 // Card surface alpha in [0.08, 0.92] (0 uses kit.DefaultGlassOpacity = 0.34)
	Motion       bool    // 120Hz Spring gauges, hover lift, drifting AI orb & live clock
	Zen          bool    // Immersive borderless desktop hiding outer chrome
}

// DefaultModules enables Frosted Glass (inheriting --glass-opacity or 0.34) and 120Hz Spring Motion by default.
func DefaultModules() Modules {
	return Modules{
		Glass:        true,
		GlassOpacity: 0,
		Motion:       true,
		Zen:          false,
	}
}

// TaskItem represents one live interactive task/matter displayed and toggleable
// directly on the Bento dashboard.
type TaskItem struct {
	ID     string
	Title  string
	Status string // "OPEN" | "IN_PROGRESS" | "BLOCKED" | "DONE"
	Owner  string
}

// Runtime carries live interactive state & callbacks so every Bento widget is
// a real functional control surface rather than static decoration.
type Runtime struct {
	Modules        Modules
	PromptDraft    *string
	NewTaskDraft   *string
	Tasks          []TaskItem
	ActivitySeries []float64
	AgentWorking   bool
	LastReply      string
	OnSubmitPrompt func(prompt string)
	OnAddTask      func(title string)
	OnCycleTask    func(id string)
	OnAction       ActionHandler
	WebHost        kit.WebIslandHost
	collapsed      map[string]bool
}

func (r *Runtime) isSectionCollapsed(key string) bool {
	if r == nil || r.collapsed == nil {
		return false
	}
	return r.collapsed[key]
}

func (r *Runtime) toggleSection(key string) {
	if r == nil {
		return
	}
	if r.collapsed == nil {
		r.collapsed = make(map[string]bool)
	}
	r.collapsed[key] = !r.collapsed[key]
}
