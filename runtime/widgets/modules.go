// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package widgets

// Modules tracks hot-pluggable optional visual & interaction modules
// (typically backed by marlin-mygo/engine/features.Registry).
type Modules struct {
	Glass  bool // Liquid Glass GPU frosted material + macOS VibrancyGlass transparency
	Motion bool // 120Hz Spring gauges, hover lift, drifting AI orb & live clock
	Zen    bool // Immersive borderless desktop hiding outer chrome
}

// DefaultModules enables Liquid Glass and 120Hz Spring Motion by default.
func DefaultModules() Modules {
	return Modules{
		Glass:  true,
		Motion: true,
		Zen:    false,
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
}
