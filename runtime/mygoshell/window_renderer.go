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
	RenderBuffer    func(c *ui.Context, t *ui.Theme, dt DerivedTheme, winID shell.WindowID, buf shell.PaneBuffer)
	NextBufferFor   func(current shell.PaneBuffer) shell.PaneBuffer
	DefaultSplitTo  func() shell.PaneBuffer
	OnLayoutChanged func(snap shell.WindowTreeSnapshot)
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

	notifyLayout := func() {
		if cb.OnLayoutChanged != nil {
			cb.OnLayoutChanged(tree.Snapshot())
		}
	}

	if node.Axis == shell.SplitRow {
		row := ui.Row(c.Key("wrow-"+string(node.ID))).Grow(1).MinWidth(0).MinHeight(0).Gap(4).AlignItems(ui.Stretch)
		spanW := row.Bounds().W
		if spanW <= 1 {
			spanW = 800
		}
		row.Children(func() {
			ui.Column(c.Key("wfirst-"+string(node.ID))).Grow(firstFlex).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				renderWindowNode(c, t, dt, tree, snap, node.First, cb)
			})
			// 120Hz continuous drag-to-resize split divider (double-click snaps 50/50; click cycles).
			div := ui.ButtonBase(c.Key("wdiv-"+string(node.ID))).Label("Resize Split").
				Width(8).AlignItems(ui.Center).Justify(ui.Center).Background(ui.Transparent)
			activeDiv := div.Hovered() || div.Dragging()
			div.Children(func() {
				barCol := toColor(dt.Border)
				barW := float32(2)
				barH := float32(34)
				if activeDiv {
					barCol = toColor(dt.Accent)
					barW = 3
					barH = 48
				}
				ui.Box(c).Width(barW).Height(barH).Radius(2).Background(barCol)
			})
			if dx, _, dragged := div.Dragged(); dragged && dx != 0 {
				if tree.ResizeSplit(node.ID, node.Ratio+dx/spanW) {
					notifyLayout()
				}
			} else if div.DoubleClicked() {
				if tree.ResizeSplit(node.ID, 0.50) {
					notifyLayout()
				}
			} else if div.Clicked() {
				nextRatio := float32(0.65)
				if node.Ratio > 0.60 {
					nextRatio = 0.35
				} else if node.Ratio < 0.40 {
					nextRatio = 0.50
				}
				if tree.ResizeSplit(node.ID, nextRatio) {
					notifyLayout()
				}
			}
			ui.Column(c.Key("wsecond-"+string(node.ID))).Grow(secondFlex).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				renderWindowNode(c, t, dt, tree, snap, node.Second, cb)
			})
		})
		return
	}

	col := ui.Column(c.Key("wcol-"+string(node.ID))).Grow(1).MinWidth(0).MinHeight(0).Gap(4).AlignItems(ui.Stretch)
	spanH := col.Bounds().H
	if spanH <= 1 {
		spanH = 600
	}
	col.Children(func() {
		ui.Column(c.Key("wfirst-"+string(node.ID))).Grow(firstFlex).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			renderWindowNode(c, t, dt, tree, snap, node.First, cb)
		})
		div := ui.ButtonBase(c.Key("wdiv-"+string(node.ID))).Label("Resize Split").
			Height(8).AlignItems(ui.Center).Justify(ui.Center).Background(ui.Transparent)
		activeDiv := div.Hovered() || div.Dragging()
		div.Children(func() {
			barCol := toColor(dt.Border)
			barW := float32(34)
			barH := float32(2)
			if activeDiv {
				barCol = toColor(dt.Accent)
				barW = 48
				barH = 3
			}
			ui.Box(c).Width(barW).Height(barH).Radius(2).Background(barCol)
		})
		if _, dy, dragged := div.Dragged(); dragged && dy != 0 {
			if tree.ResizeSplit(node.ID, node.Ratio+dy/spanH) {
				notifyLayout()
			}
		} else if div.DoubleClicked() {
			if tree.ResizeSplit(node.ID, 0.50) {
				notifyLayout()
			}
		} else if div.Clicked() {
			nextRatio := float32(0.65)
			if node.Ratio > 0.60 {
				nextRatio = 0.35
			} else if node.Ratio < 0.40 {
				nextRatio = 0.50
			}
			if tree.ResizeSplit(node.ID, nextRatio) {
				notifyLayout()
			}
		}
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

			if isFocused && snap.CanUndo {
				if AppleIconPillButton(c, "wundo-"+string(leaf.ID), "Undo Layout", BtnGlass, dt, svgIconUndo).Clicked() {
					if tree.Undo() && cb.OnLayoutChanged != nil {
						cb.OnLayoutChanged(tree.Snapshot())
					}
				}
			}
			if isFocused && snap.CanRedo {
				if AppleIconPillButton(c, "wredo-"+string(leaf.ID), "Redo Layout", BtnGlass, dt, svgIconRedo).Clicked() {
					if tree.Redo() && cb.OnLayoutChanged != nil {
						cb.OnLayoutChanged(tree.Snapshot())
					}
				}
			}
			if cb.NextBufferFor != nil {
				if ApplePillButton(c, "wtabnew-"+string(leaf.ID), "+ Tab", BtnGlass, dt, nil, true).Clicked() {
					tree.FocusWindow(leaf.ID)
					if _, err := tree.OpenTab(leaf.ID, cb.NextBufferFor(leaf.Buffer)); err == nil && cb.OnLayoutChanged != nil {
						cb.OnLayoutChanged(tree.Snapshot())
					}
				}
				if ApplePillButton(c, "wbuf-"+string(leaf.ID), "Buf", BtnGlass, dt, nil, true).Clicked() {
					tree.FocusWindow(leaf.ID)
					tree.SetWindowBuffer(leaf.ID, cb.NextBufferFor(leaf.Buffer))
					if cb.OnLayoutChanged != nil {
						cb.OnLayoutChanged(tree.Snapshot())
					}
				}
			}
			if snap.LeafCount < shell.MaxOpenWindows {
				if AppleIconPillButton(c, "wsplitv-"+string(leaf.ID), "Split Right", BtnGlass, dt, svgIconSplitRight).Clicked() {
					tree.FocusWindow(leaf.ID)
					nextBuf := leaf.Buffer
					if cb.DefaultSplitTo != nil {
						nextBuf = cb.DefaultSplitTo()
					}
					if _, err := tree.SplitRight(nextBuf); err == nil && cb.OnLayoutChanged != nil {
						cb.OnLayoutChanged(tree.Snapshot())
					}
				}
				if AppleIconPillButton(c, "wsplith-"+string(leaf.ID), "Split Below", BtnGlass, dt, svgIconSplitBelow).Clicked() {
					tree.FocusWindow(leaf.ID)
					nextBuf := leaf.Buffer
					if cb.DefaultSplitTo != nil {
						nextBuf = cb.DefaultSplitTo()
					}
					if _, err := tree.SplitBelow(nextBuf); err == nil && cb.OnLayoutChanged != nil {
						cb.OnLayoutChanged(tree.Snapshot())
					}
				}
			}
			if snap.LeafCount > 1 {
				if AppleIconPillButton(c, "wswap-"+string(leaf.ID), "Swap", BtnGlass, dt, svgIconSwap).Clicked() {
					tree.FocusWindow(leaf.ID)
					if tree.SwapWithNext() && cb.OnLayoutChanged != nil {
						cb.OnLayoutChanged(tree.Snapshot())
					}
				}
				zoomLbl := "Zoom"
				zoomVar := BtnGlass
				if snap.Zoomed && isFocused {
					zoomLbl = "Unzoom"
					zoomVar = BtnTinted
				}
				if AppleIconPillButton(c, "wzoom-"+string(leaf.ID), zoomLbl, zoomVar, dt, svgIconExpand).Clicked() {
					tree.FocusWindow(leaf.ID)
					tree.ToggleZoom()
					if cb.OnLayoutChanged != nil {
						cb.OnLayoutChanged(tree.Snapshot())
					}
				}
				if AppleIconPillButton(c, "wclose-"+string(leaf.ID), "Close Pane", BtnDestructive, dt, svgIconXMark).Clicked() {
					if err := tree.DeleteWindow(leaf.ID); err == nil && cb.OnLayoutChanged != nil {
						cb.OnLayoutChanged(tree.Snapshot())
					}
				}
			}
		})

		// Emacs tab-line-mode stacked tab strip (renders only when pane has >1 tab).
		// Inactive tabs paint ZERO body widgets: Paused tabs stay warm; Hibernated
		// tabs occupy 0MB runtime RAM and wake transparently on click.
		if len(leaf.Tabs) > 1 {
			ui.Row(c.Key("wtabs-"+string(leaf.ID))).Padding(4, 10).Gap(6).AlignItems(ui.Center).
				Background(toColor(dt.SurfaceRecessed)).BorderWidth(0, 0, 1, 0).BorderColor(glassBorderColor(dt)).Children(func() {
				for idx, tab := range leaf.Tabs {
					tabTitle := tab.Buffer.Title
					if tabTitle == "" {
						tabTitle = tab.Buffer.BufferID
					}
					var glyph string
					variant := BtnGlass
					switch tab.State {
					case shell.TabActive:
						glyph = "●"
						variant = BtnTinted
					case shell.TabHibernated:
						glyph = "☾"
					default:
						glyph = "○"
					}
					if tab.Pinned {
						glyph = "★"
					}
					lbl := fmt.Sprintf("%s %s", glyph, tabTitle)
					key := fmt.Sprintf("wtab-%s-%d", leaf.ID, idx)
					if ApplePillButton(c, key, lbl, variant, dt, nil, true).Clicked() {
						if tree.SelectTab(leaf.ID, idx) && cb.OnLayoutChanged != nil {
							cb.OnLayoutChanged(tree.Snapshot())
						}
					}
				}
				ui.Spacer(c)
				if ApplePillButton(c, "wtabsleep-"+string(leaf.ID), "Sleep Tab", BtnGlass, dt, nil, true).Clicked() {
					if tree.HibernateTab(leaf.ID, leaf.ActiveTab) && cb.OnLayoutChanged != nil {
						cb.OnLayoutChanged(tree.Snapshot())
					}
				}
				if ApplePillButton(c, "wtabclose-"+string(leaf.ID), "Close Tab", BtnDestructive, dt, nil, true).Clicked() {
					if err := tree.CloseTab(leaf.ID, leaf.ActiveTab); err == nil && cb.OnLayoutChanged != nil {
						cb.OnLayoutChanged(tree.Snapshot())
					}
				}
			})
		}

		// Pane Body: renders ONLY the active tab (paused & hibernated tabs do 0 GPU work).
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
