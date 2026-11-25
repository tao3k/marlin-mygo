// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/shell"
)

// WindowRenderCallbacks connects Emacs-style pane headers and body regions to
// the host WorkbenchApp buffers (WebView slots, Go WASI slots, native GPU views).
type WindowRenderCallbacks struct {
	RenderBuffer   func(c *ui.Context, t *ui.Theme, dt DerivedTheme, winID shell.WindowID, buf shell.PaneBuffer)
	NextBufferFor  func(current shell.PaneBuffer) shell.PaneBuffer
	DefaultSplitTo func() shell.PaneBuffer
}

// RenderWindowTree paints an Emacs-style composable binary split tree onto
// MyGo's native GPU UI with active/inactive window borders, per-pane modelines,
// split/zoom/close controls, and arbitrary buffer composition.
func RenderWindowTree(
	c *ui.Context,
	t *ui.Theme,
	dt DerivedTheme,
	tree *shell.WindowTree,
	cb WindowRenderCallbacks,
) {
	if tree == nil {
		return
	}
	snap := tree.Snapshot()
	if snap.Root == nil {
		return
	}
	if snap.Zoomed {
		if focused := findSnapshotLeaf(snap.Root, snap.FocusedID); focused != nil {
			renderWindowLeaf(c, t, dt, tree, snap, focused, cb)
			return
		}
	}
	renderWindowNode(c, t, dt, tree, snap, snap.Root, cb)
}

func renderWindowNode(
	c *ui.Context,
	t *ui.Theme,
	dt DerivedTheme,
	tree *shell.WindowTree,
	snap shell.WindowTreeSnapshot,
	node *shell.WindowNode,
	cb WindowRenderCallbacks,
) {
	if node == nil {
		return
	}
	if !node.IsSplit {
		renderWindowLeaf(c, t, dt, tree, snap, node, cb)
		return
	}

	firstFlex := node.Ratio
	if firstFlex < 0.15 || firstFlex > 0.85 {
		firstFlex = 0.5
	}
	secondFlex := 1.0 - firstFlex

	if node.Axis == shell.SplitRow {
		ui.Row(c.Key("wrow-"+string(node.ID))).Grow(1).MinWidth(0).MinHeight(0).Gap(8).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c.Key("wfirst-"+string(node.ID))).Grow(firstFlex).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				renderWindowNode(c, t, dt, tree, snap, node.First, cb)
			})
			ui.Column(c.Key("wsecond-"+string(node.ID))).Grow(secondFlex).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				renderWindowNode(c, t, dt, tree, snap, node.Second, cb)
			})
		})
		return
	}

	ui.Column(c.Key("wcol-"+string(node.ID))).Grow(1).MinWidth(0).MinHeight(0).Gap(8).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c.Key("wfirst-"+string(node.ID))).Grow(firstFlex).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			renderWindowNode(c, t, dt, tree, snap, node.First, cb)
		})
		ui.Column(c.Key("wsecond-"+string(node.ID))).Grow(secondFlex).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			renderWindowNode(c, t, dt, tree, snap, node.Second, cb)
		})
	})
}

func renderWindowLeaf(
	c *ui.Context,
	t *ui.Theme,
	dt DerivedTheme,
	tree *shell.WindowTree,
	snap shell.WindowTreeSnapshot,
	leaf *shell.WindowNode,
	cb WindowRenderCallbacks,
) {
	isFocused := leaf.ID == snap.FocusedID
	borderColor := toColor(dt.InactiveWindowBorder)
	headerBg := toColor(dt.ModelineInactiveBg)
	titleColor := toColor(dt.ModelineInactiveInk)
	if isFocused {
		borderColor = toColor(dt.ActiveWindowBorder)
		headerBg = toColor(dt.ModelineActiveBg)
		titleColor = toColor(dt.ModelineActiveInk)
	}

	ui.Column(c.Key("win-"+string(leaf.ID))).Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).
		Radius(14).Background(toColor(dt.Surface)).Border(1, borderColor).Clip().Children(func() {
		// Emacs-style compact Apple-refined Window Modeline / Header bar.
		ui.Row(c.Key("wbar-"+string(leaf.ID))).Padding(6, 10).Gap(6).AlignItems(ui.Center).
			Background(headerBg).BorderWidth(0, 0, 1, 0).BorderColor(glassBorderColor(dt)).Children(func() {
			prefix := "○"
			focusVariant := BtnGlass
			if isFocused {
				prefix = "●"
				focusVariant = BtnTinted
			}
			title := leaf.Buffer.Title
			if title == "" {
				title = leaf.Buffer.BufferID
			}
			btnLabel := fmt.Sprintf("%s %s", prefix, title)
			if ApplePillButton(c, "wfocus-"+string(leaf.ID), btnLabel, focusVariant, dt, nil, true).Clicked() {
				tree.FocusWindow(leaf.ID)
			}
			if leaf.Buffer.Kind != "" {
				ui.Text(c, leaf.Buffer.Kind).FontSize(10.5).FontWeight(600).TextColor(titleColor)
			}
			ui.Spacer(c)

			if cb.NextBufferFor != nil {
				if ApplePillButton(c, "wbuf-"+string(leaf.ID), "Buf", BtnGlass, dt, nil, true).Clicked() {
					tree.FocusWindow(leaf.ID)
					tree.SetWindowBuffer(leaf.ID, cb.NextBufferFor(leaf.Buffer))
				}
			}
			if snap.LeafCount < shell.MaxOpenWindows {
				if ApplePillButton(c, "wsplitv-"+string(leaf.ID), "|", BtnGlass, dt, nil, true).Clicked() {
					tree.FocusWindow(leaf.ID)
					nextBuf := leaf.Buffer
					if cb.DefaultSplitTo != nil {
						nextBuf = cb.DefaultSplitTo()
					}
					_, _ = tree.SplitRight(nextBuf)
				}
				if ApplePillButton(c, "wsplith-"+string(leaf.ID), "-", BtnGlass, dt, nil, true).Clicked() {
					tree.FocusWindow(leaf.ID)
					nextBuf := leaf.Buffer
					if cb.DefaultSplitTo != nil {
						nextBuf = cb.DefaultSplitTo()
					}
					_, _ = tree.SplitBelow(nextBuf)
				}
			}
			if snap.LeafCount > 1 {
				zoomLbl := "Zoom"
				zoomVar := BtnGlass
				if snap.Zoomed && isFocused {
					zoomLbl = "Unzoom"
					zoomVar = BtnTinted
				}
				if ApplePillButton(c, "wzoom-"+string(leaf.ID), zoomLbl, zoomVar, dt, nil, true).Clicked() {
					tree.FocusWindow(leaf.ID)
					tree.ToggleZoom()
				}
				if ApplePillButton(c, "wclose-"+string(leaf.ID), "x", BtnDestructive, dt, nil, true).Clicked() {
					_ = tree.DeleteWindow(leaf.ID)
				}
			}
		})

		// Pane Body: renders mounted WebView slot, Go WASI GPU slot, or Native GPU buffer.
		ui.Column(c.Key("wbody-"+string(leaf.ID))).Grow(1).MinWidth(0).MinHeight(0).Padding(8).AlignItems(ui.Stretch).Children(func() {
			if cb.RenderBuffer != nil {
				cb.RenderBuffer(c, t, dt, leaf.ID, leaf.Buffer)
			}
		})
	})
}

func findSnapshotLeaf(n *shell.WindowNode, target shell.WindowID) *shell.WindowNode {
	if n == nil {
		return nil
	}
	if !n.IsSplit {
		if n.ID == target {
			return n
		}
		return nil
	}
	if f := findSnapshotLeaf(n.First, target); f != nil {
		return f
	}
	return findSnapshotLeaf(n.Second, target)
}
