// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mapper_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	uiv1 "github.com/tao3k/marlin-mygo/gen/marlin/ui/v1"
	"github.com/tao3k/marlin-mygo/runtime/mapper"
)

func TestSchemeProtoToMyGoDeclarativeMapperEndToEnd(t *testing.T) {
	cmdTheme := "switch-themes"
	cmdHelm := "helm"
	rowID := "sess-1"
	pb := &uiv1.UiWorkbenchSnapshot{
		Generation:   11,
		ProfileName:  "doom-one",
		ModelLabel:   "claude-sonnet",
		SessionLabel: "marlin-main",
		Theme: &uiv1.UiTheme{
			Canvas:  &uiv1.UiRgb{Red: 40, Green: 44, Blue: 52},
			Ink:     &uiv1.UiRgb{Red: 187, Green: 194, Blue: 207},
			Bar:     &uiv1.UiRgb{Red: 33, Green: 36, Blue: 43},
			Accent:  &uiv1.UiRgb{Red: 198, Green: 120, Blue: 221},
			Info:    &uiv1.UiRgb{Red: 81, Green: 175, Blue: 239},
			Success: &uiv1.UiRgb{Red: 152, Green: 190, Blue: 101},
			Warning: &uiv1.UiRgb{Red: 236, Green: 190, Blue: 123},
			Muted:   &uiv1.UiRgb{Red: 140, Green: 146, Blue: 156},
		},
		Layout: &uiv1.UiLayoutPolicy{
			FrameTree: &uiv1.UiElement{
				Node: &uiv1.UiElement_Rows{
					Rows: &uiv1.UiElementSequence{
						Children: []*uiv1.UiElement{
							{
								Node: &uiv1.UiElement_Bar{
									Bar: &uiv1.UiBarNode{
										Segments: []*uiv1.UiBarSegment{
											{
												Side:        uiv1.UiBarSide_UI_BAR_SIDE_LEFT,
												CommandName: &cmdTheme,
												Child: &uiv1.UiElement{
													Node: &uiv1.UiElement_Template{
														Template: &uiv1.UiTemplateNode{
															Spans: []*uiv1.UiTemplateSpan{
																{Value: &uiv1.UiTemplateSpan_Literal{Literal: "MARLIN "}, Tone: uiv1.UiTone_UI_TONE_ACCENT, Bold: true},
																{Value: &uiv1.UiTemplateSpan_BindingId{BindingId: "model"}, Tone: uiv1.UiTone_UI_TONE_INFO},
															},
														},
													},
												},
											},
										},
									},
								},
							},
							{
								Node: &uiv1.UiElement_List{
									List: &uiv1.UiListNode{
										Id:         "sessions",
										SourceKind: uiv1.UiListSourceKind_UI_LIST_SOURCE_KIND_INLINE_ROWS,
										InlineRows: []*uiv1.UiListRowValue{
											{Id: &rowID, Fields: map[string]string{"name": "Primary Workspace"}},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		Regions: []*uiv1.UiRegion{
			{
				Kind: &uiv1.UiRegion_Status{
					Status: &uiv1.UiStatusRegion{
						Items: []*uiv1.UiStatusItem{
							{Id: "mode", Label: "NORMAL", CompactLabel: "N", CompactBelow: 60, KeepPriority: 100, Tone: uiv1.UiTone_UI_TONE_WARNING},
							{Id: "model", Label: "claude-sonnet", MinWidth: 40, KeepPriority: 80, Tone: uiv1.UiTone_UI_TONE_INFO},
						},
					},
				},
			},
		},
		Transients: []*uiv1.UiTransient{
			{
				Id:      "doom-leader",
				Foreign: uiv1.UiTransientForeign_UI_TRANSIENT_FOREIGN_EXIT,
				Heads: []*uiv1.UiTransientHead{
					{Key: "s", CommandName: &cmdHelm, Label: "Helm Search", Exit: true},
					{Key: "t", CommandName: &cmdTheme, Label: "Cycle Theme", Exit: false},
				},
			},
		},
	}

	snap, err := grpcwire.WorkbenchFromProto(pb)
	if err != nil {
		t.Fatalf("WorkbenchFromProto: %v", err)
	}

	// Verify width-aware compaction matches Ratatui priority semantics.
	compact := mapper.ProjectStatusItems(snap.StatusItems, 50, nil)
	if len(compact) != 2 || compact[0].Text != "N" {
		t.Fatalf("expected compact label 'N' below 60 cols, got %+v", compact)
	}

	skin := mapper.SkinFromUITheme(snap.Theme)
	var ranCmd, selectedList, selectedRow string
	dismissed := false

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Background(skin.Canvas).AlignItems(ui.Stretch).Children(func() {
			mapper.MapElement(c, snap.FrameTree, skin, mapper.Context{
				WidthCols: 120,
				Bindings:  map[string]string{"model": snap.ModelLabel},
				OnCommand: func(cmd string) { ranCmd = cmd },
				OnSelectRow: func(listID, rID string) {
					selectedList = listID
					selectedRow = rID
				},
			})
			mapper.MapStatusBar(c, snap.StatusItems, 120, nil, skin)
			mapper.MapTransientPopup(c, snap.Transients[0], skin, func(cmd string) { ranCmd = cmd }, func() { dismissed = true })
		})
	}, 1024, 640)

	if !tt.HasText("MARLIN ") || !tt.HasText("claude-sonnet") || !tt.HasText("Primary Workspace") || !tt.HasText("NORMAL") || !tt.HasText("Helm Search") {
		t.Fatalf("expected mapped Scheme UI elements, status bar, and Hydra popup, got %q", strings.Join(tt.Texts(), " | "))
	}

	if err := tt.Click("Primary Workspace"); err != nil {
		t.Fatalf("click list row: %v", err)
	}
	tt.Frame()
	if selectedList != "sessions" || selectedRow != "sess-1" {
		t.Fatalf("expected row selection (sessions, sess-1), got (%q, %q)", selectedList, selectedRow)
	}

	if err := tt.Click("Helm Search"); err != nil {
		t.Fatalf("click Hydra head: %v", err)
	}
	tt.Frame()
	if ranCmd != "helm" || !dismissed {
		t.Fatalf("expected Hydra exit head to dispatch 'helm' and dismiss, got cmd=%q dismissed=%v", ranCmd, dismissed)
	}
}
