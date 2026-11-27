// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"testing"

	uiv1 "github.com/tao3k/marlin-mygo/gen/marlin/ui/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestWorkbenchFromProtoLosslessSchemeProjection(t *testing.T) {
	cmdName := "switch-themes"
	rowID := "r-1"
	pb := &uiv1.UiWorkbenchSnapshot{
		Generation:       7,
		ProfileName:      "doom-one",
		ModelLabel:       "claude-sonnet-marlin",
		SessionLabel:     "marlin-main",
		FocusComponentId: "composer",
		Theme: &uiv1.UiTheme{
			Canvas: &uiv1.UiRgb{Red: 40, Green: 44, Blue: 52},
			Ink:    &uiv1.UiRgb{Red: 187, Green: 194, Blue: 207},
			Bar:    &uiv1.UiRgb{Red: 33, Green: 36, Blue: 43},
			Info:   &uiv1.UiRgb{Red: 81, Green: 175, Blue: 239},
		},
		Frame: &uiv1.UiFrame{
			Id:             "main-frame",
			SelectedWindow: "win-1",
			Minibuffer:     "mini-1",
			BufferIds:      []string{"*conversation*", "*Sessions*", "*Help*"},
			Windows:        []*uiv1.UiWindow{{Id: "win-1", BufferId: "*conversation*"}},
		},
		Layout: &uiv1.UiLayoutPolicy{
			RoleRegions: map[string]string{"main": "transcript"},
			FrameTree: &uiv1.UiElement{
				Node: &uiv1.UiElement_Rows{
					Rows: &uiv1.UiElementSequence{
						Children: []*uiv1.UiElement{
							{
								Node: &uiv1.UiElement_Bar{
									Bar: &uiv1.UiBarNode{
										Background: &uiv1.UiBackground{Kind: uiv1.UiBackgroundKind_UI_BACKGROUND_KIND_BAR},
										Segments: []*uiv1.UiBarSegment{
											{
												Side:        uiv1.UiBarSide_UI_BAR_SIDE_LEFT,
												Priority:    100,
												CommandName: &cmdName,
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
										Id:         "sessions-list",
										SourceKind: uiv1.UiListSourceKind_UI_LIST_SOURCE_KIND_INLINE_ROWS,
										InlineRows: []*uiv1.UiListRowValue{
											{Id: &rowID, Fields: map[string]string{"name": "Primary", "state": "active"}},
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
							{Id: "mode", Label: "NORMAL", CompactLabel: "N", Tone: uiv1.UiTone_UI_TONE_WARNING, KeepPriority: 100},
							{Id: "model", Label: "claude-sonnet-marlin", CompactBelow: 80, KeepPriority: 60, Tone: uiv1.UiTone_UI_TONE_INFO},
						},
					},
				},
			},
			{
				Kind: &uiv1.UiRegion_Extension{
					Extension: &uiv1.UiExtensionRegion{
						Id:        "ext-doctor",
						Label:     "*Doctor*",
						Focusable: true,
						Content: &uiv1.UiElement{
							Node: &uiv1.UiElement_Text{
								Text: &uiv1.UiTextNode{
									Spans: []*uiv1.UiSpan{{Text: "All checks nominal", Tone: uiv1.UiTone_UI_TONE_SUCCESS}},
								},
							},
						},
					},
				},
			},
		},
		Commands: []*uiv1.UiCommand{
			{Name: "switch-themes", Description: "Switch Doom theme", Arguments: uiv1.UiCommandArguments_UI_COMMAND_ARGUMENTS_NONE},
		},
		Keymap: []*uiv1.UiKeyBinding{
			{Scope: uiv1.UiKeyScope_UI_KEY_SCOPE_NORMAL, Sequence: []string{"SPC", "t", "t"}, ActionKind: "command", CommandName: &cmdName},
		},
		Transients: []*uiv1.UiTransient{
			{
				Id:      "doom-leader",
				Foreign: uiv1.UiTransientForeign_UI_TRANSIENT_FOREIGN_EXIT,
				Heads: []*uiv1.UiTransientHead{
					{Key: "t", CommandName: &cmdName, Label: "Switch Theme", Exit: true},
				},
			},
		},
	}

	snap, err := WorkbenchFromProto(pb)
	if err != nil {
		t.Fatalf("WorkbenchFromProto: %v", err)
	}
	if len(snap.Frame.BufferIDs) != 3 || snap.Frame.SelectedWindow != "win-1" {
		t.Fatalf("expected lossless Frame projection, got %+v", snap.Frame)
	}
	if len(snap.StatusItems) != 2 || snap.StatusItems[0].Label != "NORMAL" {
		t.Fatalf("expected 2 StatusItems, got %+v", snap.StatusItems)
	}
	if len(snap.Extensions) != 1 || snap.Extensions[0].Label != "*Doctor*" {
		t.Fatalf("expected 1 ExtensionRegion, got %+v", snap.Extensions)
	}
	if len(snap.Commands) != 1 || snap.Commands[0].Name != "switch-themes" {
		t.Fatalf("expected 1 Command, got %+v", snap.Commands)
	}
	if len(snap.Keymap) != 1 || snap.Keymap[0].Scope != schema.KeyScopeNormal || len(snap.Keymap[0].Sequence) != 3 {
		t.Fatalf("expected 1 Normal KeyBinding, got %+v", snap.Keymap)
	}
	if len(snap.Transients) != 1 || snap.Transients[0].ID != "doom-leader" || len(snap.Transients[0].Heads) != 1 {
		t.Fatalf("expected 1 Hydra Transient, got %+v", snap.Transients)
	}
	if len(snap.FrameTree.Children) != 2 || snap.FrameTree.Children[0].Kind != schema.ElemBar || len(snap.FrameTree.Children[0].BarSegments) != 1 {
		t.Fatalf("expected lossless Bar + List tree, got %+v", snap.FrameTree)
	}
	if snap.FrameTree.Children[1].ListSource != schema.ListSourceInlineRows || len(snap.FrameTree.Children[1].InlineRows) != 1 {
		t.Fatalf("expected lossless InlineRows list, got %+v", snap.FrameTree.Children[1])
	}
}
