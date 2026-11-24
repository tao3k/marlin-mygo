// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import (
	"fmt"
	"sync"

	"github.com/tao3k/marlin-mygo/engine/arena"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// WorkbenchController owns the latest admitted Scheme UIWorkbenchSnapshot, its
// compiled BFS Merkle Arena, and enforces transactional monotonic generation
// updates (MAR-UI-R02) with O(1)-pruned incremental diffs (MAR-UI-T06).
type WorkbenchController struct {
	mu            sync.RWMutex
	snapshot      *schema.UIWorkbenchSnapshot
	compiledArena *arena.Arena
	lastPatchOps  []arena.PatchOp
	slots         *SlotRegistry
	themeWatchers []func(schema.UITheme)
}

func NewWorkbenchController(slots *SlotRegistry) *WorkbenchController {
	return &WorkbenchController{slots: slots}
}

// AdmitSnapshot compiles and validates tree bounds into an Arena, computes the
// incremental Merkle diff against the previous generation, and checks monotonic
// generation before publishing. Any rejected candidate preserves prior state.
func (w *WorkbenchController) AdmitSnapshot(next schema.UIWorkbenchSnapshot) error {
	nextArena, err := arena.Compile(next.FrameTree, schema.MaxUITreeNodes, schema.MaxUITreeDepth)
	if err != nil {
		return err
	}
	w.mu.Lock()
	if w.snapshot != nil && next.Generation <= w.snapshot.Generation {
		cur := w.snapshot.Generation
		w.mu.Unlock()
		return fmt.Errorf("%w: got %d <= current %d", ErrStaleGeneration, next.Generation, cur)
	}
	themeChanged := w.snapshot == nil || w.snapshot.Theme != next.Theme
	w.lastPatchOps = arena.Diff(w.compiledArena, nextArena, w.lastPatchOps)
	w.compiledArena = nextArena
	cp := next
	w.snapshot = &cp
	watchers := append([]func(schema.UITheme){}, w.themeWatchers...)
	w.mu.Unlock()

	if themeChanged {
		for _, fn := range watchers {
			fn(next.Theme)
		}
	}
	return nil
}

// LastPatchOps returns a copy of the minimal reconciliation ops from the latest admission.
func (w *WorkbenchController) LastPatchOps() []arena.PatchOp {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return append([]arena.PatchOp(nil), w.lastPatchOps...)
}

func (w *WorkbenchController) Snapshot() (*schema.UIWorkbenchSnapshot, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.snapshot == nil {
		return nil, false
	}
	cp := *w.snapshot
	return &cp, true
}

func (w *WorkbenchController) OnThemeChange(fn func(schema.UITheme)) {
	w.mu.Lock()
	w.themeWatchers = append(w.themeWatchers, fn)
	w.mu.Unlock()
}
