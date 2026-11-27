// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/promptgate"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// PromptGateController manages the shared Tier-0 Ask, Option, and Authorization
// modal (Desktop) and bottom-sheet (Mobile iOS/Android) UI above all WebViews.
type PromptGateController struct {
	Queue     *promptgate.Queue
	OnResolve func(req promptgate.PromptRequest, resp promptgate.PromptResponse)

	mu            sync.Mutex
	activeID      string
	selected      map[string]bool
	textInput     string
	textRev       uint64
	validationErr string
}

// NewPromptGateController constructs a fail-closed Tier-0 PromptGateController.
func NewPromptGateController() *PromptGateController {
	return &PromptGateController{
		Queue:    promptgate.NewQueue(),
		selected: make(map[string]bool),
	}
}

// Enqueue pushes an Ask, Option, or Authorization prompt onto the FIFO queue.
func (pg *PromptGateController) Enqueue(req promptgate.PromptRequest) error {
	if pg == nil {
		return promptgate.ErrPromptInvalid
	}
	if pg.Queue == nil {
		pg.Queue = promptgate.NewQueue()
	}
	return pg.Queue.Enqueue(req)
}

// HasPending reports whether at least one Tier-0 prompt or gate is awaiting action.
func (pg *PromptGateController) HasPending() bool {
	return pg != nil && pg.Queue != nil && pg.Queue.HasPending()
}

// PendingCount returns the total queued Ask / Option / Authorization count.
func (pg *PromptGateController) PendingCount() int {
	if pg == nil || pg.Queue == nil {
		return 0
	}
	return pg.Queue.Len()
}

// Active returns the head prompt in the FIFO queue.
func (pg *PromptGateController) Active() (promptgate.PromptRequest, int, int, bool) {
	if pg == nil || pg.Queue == nil {
		return promptgate.PromptRequest{}, 0, 0, false
	}
	return pg.Queue.Active()
}

// SetTextInput programmatically updates the active Ask / Custom Option / Rationale input.
func (pg *PromptGateController) SetTextInput(text string) {
	pg.mu.Lock()
	pg.textInput = text
	pg.textRev++
	pg.validationErr = ""
	pg.mu.Unlock()
}

// ToggleOption toggles a choice ID on the active Option/Auth prompt.
func (pg *PromptGateController) ToggleOption(choiceID string) {
	req, _, _, ok := pg.Active()
	if !ok {
		return
	}
	pg.mu.Lock()
	pg.syncActiveLocked(req.ID)
	pg.toggleChoiceLocked(req, choiceID)
	pg.mu.Unlock()
}

func (pg *PromptGateController) syncActiveLocked(id string) {
	if pg.activeID != id {
		pg.activeID = id
		pg.selected = make(map[string]bool)
		pg.textInput = ""
		pg.validationErr = ""
	}
}

func (pg *PromptGateController) toggleChoiceLocked(req promptgate.PromptRequest, choiceID string) {
	if pg.selected == nil {
		pg.selected = make(map[string]bool)
	}
	if !req.MultiSelect {
		wasSelected := pg.selected[choiceID]
		pg.selected = make(map[string]bool)
		if !wasSelected {
			pg.selected[choiceID] = true
		}
	} else {
		if pg.selected[choiceID] {
			delete(pg.selected, choiceID)
		} else {
			pg.selected[choiceID] = true
		}
	}
	pg.validationErr = ""
}

func (pg *PromptGateController) selectedIDsInOrderLocked(req promptgate.PromptRequest) []string {
	var out []string
	for _, ch := range req.Choices {
		if pg.selected[ch.ID] {
			out = append(out, ch.ID)
		}
	}
	return out
}

// RenderDesktopModal paints a centered Liquid Glass Tier-0 Ask / Option / Auth
// dialog above all embedded WebViews and workbench overlays.
func (pg *PromptGateController) RenderDesktopModal(c *ui.Context, dt DerivedTheme) {
	pg.renderSurface(c, dt, false)
}

// RenderMobileSheet paints a bottom-anchored iOS HIG / Android Material 3
// Tier-0 Ask / Option / Auth sheet with thumb-friendly targets and zero overflow.
func (pg *PromptGateController) RenderMobileSheet(c *ui.Context, dt DerivedTheme) {
	pg.renderSurface(c, dt, true)
}

func (pg *PromptGateController) renderSurface(c *ui.Context, dt DerivedTheme, mobile bool) {
	if pg == nil || pg.Queue == nil {
		return
	}
	req, pos, total, ok := pg.Queue.Active()
	if !ok {
		return
	}

	pg.mu.Lock()
	pg.syncActiveLocked(req.ID)
	draftText := pg.textInput
	textRev := pg.textRev
	valErr := pg.validationErr
	selSnap := make(map[string]bool, len(pg.selected))
	for k, v := range pg.selected {
		selSnap[k] = v
	}
	onResolve := pg.OnResolve
	pg.mu.Unlock()

	var (
		toggledChoiceID string
		submitAction    promptgate.ActionKind
	)

	backdrop := ui.Column(c.Key("promptgate-backdrop-"+req.ID)).
		Absolute().Top(0).Right(0).Bottom(0).Left(0).ZIndex(980).
		Background(ui.RGBA(8, 10, 16, 0.62))
	if mobile {
		backdrop.Justify(ui.End).AlignItems(ui.Stretch)
	} else {
		backdrop.Justify(ui.Center).AlignItems(ui.Center).Padding(20)
	}

	backdrop.Children(func() {
		var card ui.Element
		if mobile {
			card = ui.Column(c.Key("promptgate-sheet-"+req.ID)).
				Padding(10, 14, 18, 14).Gap(10).
				Radius(22, 22, 0, 0).AlignItems(ui.Stretch).
				Material(AppleLiquidGlassSurface(dt, true, false)).
				Border(1, glassBorderColor(dt)).
				Shadow(0, -10, 28, -4, ambientShadowColor(dt))
		} else {
			card = ui.Column(c.Key("promptgate-modal-"+req.ID)).
				Width(540).Padding(18, 20).Gap(12).
				Radius(18).AlignItems(ui.Stretch).
				Material(AppleLiquidGlassSurface(dt, true, false)).
				Border(1, glassBorderColor(dt)).
				Shadow(0, 18, 42, -8, ambientShadowColor(dt))
		}

		card.Children(func() {
			if mobile {
				ui.Row(c).Justify(ui.Center).Padding(0, 0, 2, 0).Children(func() {
					ui.Box(c).Width(38).Height(4).Radius(2).Background(toColor(dt.BorderStrong))
				})
			}

			renderPromptHeader(c, dt, req, pos, total, mobile)

			title := strings.TrimSpace(req.Title)
			if title != "" {
				ui.Text(c, title).FontSize(15).FontWeight(700).TextColor(toColor(dt.Ink))
			}
			ui.Text(c, req.Prompt).FontSize(14).FontWeight(600).LineHeight(1.38).TextColor(toColor(dt.Ink))
			if req.Description != "" {
				ui.Text(c, req.Description).FontSize(12.5).LineHeight(1.35).TextColor(toColor(dt.InkSecondary))
			}

			if req.ToolName != "" || req.DetailsHeading != "" || req.DetailsSummary != "" {
				renderPromptDetailsCard(c, dt, req)
			}

			if len(req.Choices) > 0 {
				renderPromptChoices(c, dt, req, selSnap, func(id string) {
					toggledChoiceID = id
				})
			}

			if req.Kind == promptgate.PromptAsk ||
				(req.Kind == promptgate.PromptOption && req.AllowCustom) ||
				req.Kind == promptgate.PromptAuth {
				renderPromptTextField(c, dt, req, textRev, &draftText)
			}

			if valErr != "" {
				ui.Text(c, valErr).FontSize(11.5).FontWeight(600).TextColor(toColor(dt.Danger))
			}

			renderPromptActions(c, dt, req, mobile, func(act promptgate.ActionKind) {
				submitAction = act
			})
		})
	})

	var resolvedReq *promptgate.PromptRequest
	var resolvedResp *promptgate.PromptResponse

	pg.mu.Lock()
	if pg.activeID == req.ID {
		pg.textInput = draftText
		if toggledChoiceID != "" {
			pg.toggleChoiceLocked(req, toggledChoiceID)
		}
		if submitAction != "" {
			selectedIDs := pg.selectedIDsInOrderLocked(req)
			cand := promptgate.PromptResponse{
				PromptID:    req.ID,
				Kind:        req.Kind,
				Action:      submitAction,
				SelectedIDs: selectedIDs,
				TextInput:   draftText,
			}
			rReq, rResp, err := pg.Queue.Resolve(req.ID, cand)
			if err != nil {
				pg.validationErr = err.Error()
			} else {
				pg.activeID = ""
				pg.selected = make(map[string]bool)
				pg.textInput = ""
				pg.validationErr = ""
				resolvedReq = &rReq
				resolvedResp = &rResp
			}
		}
	}
	pg.mu.Unlock()

	if resolvedReq != nil && resolvedResp != nil && onResolve != nil {
		onResolve(*resolvedReq, *resolvedResp)
	}
}

func renderPromptHeader(c *ui.Context, dt DerivedTheme, req promptgate.PromptRequest, pos, total int, mobile bool) {
	badgeLabel, badgeTone := promptKindBadge(req, dt)
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		AppleBadgePill(c, badgeLabel, badgeTone, dt)
		if req.Kind == promptgate.PromptAuth {
			AppleBadgePill(c, strings.ToUpper(string(req.Risk))+" RISK", promptRiskTone(req.Risk, dt), dt)
			if !mobile {
				AppleBadgePill(c, authFamilyLabel(req.AuthFamily), dt.Info, dt)
			}
		} else if req.Kind == promptgate.PromptOption && req.MultiSelect {
			AppleBadgePill(c, "MULTI-SELECT", dt.Info, dt)
		}
		ui.Spacer(c)
		if total > 1 {
			AppleBadgePill(c, fmt.Sprintf("%d / %d PENDING", pos, total), dt.Warning, dt)
		}
	})
}

func renderPromptDetailsCard(c *ui.Context, dt DerivedTheme, req promptgate.PromptRequest) {
	ui.Column(c).MinWidth(0).Padding(9, 12).Gap(4).Radius(10).
		Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
		if req.ToolName != "" {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, svgIconShield).Size(13, 13).TextColor(toColor(promptRiskTone(req.Risk, dt)))
				ui.Textf(c, "Tool: %s", req.ToolName).FontSize(12).FontWeight(700).TextColor(toColor(dt.Ink))
			})
		}
		if req.DetailsHeading != "" {
			ui.Text(c, req.DetailsHeading).FontSize(11.5).FontWeight(600).TextColor(toColor(dt.InkSecondary))
		}
		if req.DetailsSummary != "" {
			ui.Text(c, req.DetailsSummary).FontSize(11.5).LineHeight(1.35).TextColor(toColor(dt.InkMuted))
		}
	})
}

func renderPromptChoices(
	c *ui.Context,
	dt DerivedTheme,
	req promptgate.PromptRequest,
	selected map[string]bool,
	onToggle func(id string),
) {
	ui.Column(c).MinWidth(0).Gap(6).AlignItems(ui.Stretch).Children(func() {
		for _, ch := range req.Choices {
			choice := ch
			active := selected[choice.ID]
			bg := toColor(dt.SurfaceElevated)
			border := glassBorderColor(dt)
			mark := "( )"
			if req.MultiSelect {
				mark = "[ ]"
			}
			if active {
				bg = rgbaFromRGB(dt.Accent, 0.16)
				border = toColor(dt.Accent)
				if req.MultiSelect {
					mark = "[x]"
				} else {
					mark = "(o)"
				}
			}
			btn := ui.ButtonBase(c.Key("pg-opt-"+req.ID+"-"+choice.ID)).Label(choice.Label).
				Padding(9, 12).Radius(12).Background(bg).Border(1, border).AlignItems(ui.Stretch)
			btn.Children(func() {
				ui.Column(c).MinWidth(0).Gap(2).AlignItems(ui.Stretch).Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, mark).FontSize(11.5).FontWeight(700).TextColor(toColor(dt.Accent))
						ui.Text(c, choice.Label).FontSize(13).FontWeight(600).TextColor(toColor(dt.Ink))
						if choice.Recommended {
							AppleBadgePill(c, "Recommended", dt.Success, dt)
						}
					})
					if choice.Description != "" {
						ui.Text(c, choice.Description).FontSize(11.5).TextColor(toColor(dt.InkSecondary))
					}
				})
			})
			if btn.Clicked() {
				onToggle(choice.ID)
			}
		}
	})
}

func renderPromptTextField(c *ui.Context, dt DerivedTheme, req promptgate.PromptRequest, rev uint64, draft *string) {
	placeholder := strings.TrimSpace(req.Placeholder)
	if placeholder == "" {
		switch req.Kind {
		case promptgate.PromptAsk:
			placeholder = "Type your answer..."
		case promptgate.PromptOption:
			placeholder = "Other / custom response (optional)..."
		default:
			placeholder = "Optional rationale, question, or patch instruction..."
		}
	}
	ui.Row(c).MinWidth(0).Padding(7, 11).Radius(12).
		Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
		ui.TextInputBase(c.Key(fmt.Sprintf("pg-input-%s-%d", req.ID, rev)), draft).
			Placeholder(placeholder).
			FontSize(13).TextColor(toColor(dt.Ink)).Grow(1).AutoFocus()
	})
}

func renderPromptActions(
	c *ui.Context,
	dt DerivedTheme,
	req promptgate.PromptRequest,
	mobile bool,
	onAction func(promptgate.ActionKind),
) {
	switch req.Kind {
	case promptgate.PromptAsk:
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			cancelBtn := ApplePillButton(c, "pg-cancel-"+req.ID, "Cancel", BtnGlass, dt, svgIconXMark, mobile)
			if mobile {
				cancelBtn.Grow(1)
			}
			if cancelBtn.Clicked() {
				onAction(promptgate.ActionCancel)
			}
			submitBtn := ApplePillButton(c, "pg-submit-"+req.ID, "Submit Answer", BtnPrimary, dt, svgIconCheck, mobile)
			if mobile {
				submitBtn.Grow(1)
			}
			if submitBtn.Clicked() {
				onAction(promptgate.ActionSubmit)
			}
		})

	case promptgate.PromptOption:
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			cancelBtn := ApplePillButton(c, "pg-cancel-"+req.ID, "Cancel", BtnGlass, dt, svgIconXMark, mobile)
			if mobile {
				cancelBtn.Grow(1)
			}
			if cancelBtn.Clicked() {
				onAction(promptgate.ActionCancel)
			}
			submitBtn := ApplePillButton(c, "pg-submit-"+req.ID, "Confirm Choice", BtnPrimary, dt, svgIconCheck, mobile)
			if mobile {
				submitBtn.Grow(1)
			}
			if submitBtn.Clicked() {
				onAction(promptgate.ActionSubmit)
			}
		})

	default:
		// Tier-0 Authorization Gate (Tool Review / Device Capability / Graph Human Gate).
		if req.AuthFamily == promptgate.AuthGraphHumanGate {
			ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
					askBtn := ApplePillButton(c, "pg-ask-"+req.ID, "Ask Question", BtnTinted, dt, nil, true)
					patchBtn := ApplePillButton(c, "pg-patch-"+req.ID, "Request Patch", BtnGlass, dt, nil, true)
					if mobile {
						askBtn.Grow(1)
						patchBtn.Grow(1)
					}
					if askBtn.Clicked() {
						onAction(promptgate.ActionAskQuestion)
					}
					if patchBtn.Clicked() {
						onAction(promptgate.ActionRequestPatch)
					}
				})
				ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
					rejBtn := ApplePillButton(c, "pg-reject-"+req.ID, "Reject", BtnDestructive, dt, svgIconXMark, mobile)
					appBtn := ApplePillButton(c, "pg-approve-"+req.ID, "Approve Once", BtnPrimary, dt, svgIconCheck, mobile)
					if mobile {
						rejBtn.Grow(1)
						appBtn.Grow(1)
					}
					if rejBtn.Clicked() {
						onAction(promptgate.ActionReject)
					}
					if appBtn.Clicked() {
						onAction(promptgate.ActionApproveOnce)
					}
				})
			})
			return
		}
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			rejBtn := ApplePillButton(c, "pg-reject-"+req.ID, "Reject", BtnDestructive, dt, svgIconXMark, mobile)
			appBtn := ApplePillButton(c, "pg-approve-"+req.ID, "Approve Once", BtnPrimary, dt, svgIconCheck, mobile)
			if mobile {
				rejBtn.Grow(1)
				appBtn.Grow(1)
			}
			if rejBtn.Clicked() {
				onAction(promptgate.ActionReject)
			}
			if appBtn.Clicked() {
				onAction(promptgate.ActionApproveOnce)
			}
		})
	}
}

func promptKindBadge(req promptgate.PromptRequest, dt DerivedTheme) (string, schema.RGB) {
	switch req.Kind {
	case promptgate.PromptAsk:
		return "ASK QUESTION", dt.Info
	case promptgate.PromptOption:
		return "SELECT OPTION", dt.Accent
	default:
		return "TIER-0 AUTHORIZATION", promptRiskTone(req.Risk, dt)
	}
}

func promptRiskTone(risk promptgate.RiskLevel, dt DerivedTheme) schema.RGB {
	switch risk {
	case promptgate.RiskCritical, promptgate.RiskHigh:
		return dt.Danger
	case promptgate.RiskLow:
		return dt.Info
	default:
		return dt.Warning
	}
}

func authFamilyLabel(f promptgate.AuthGateFamily) string {
	switch f {
	case promptgate.AuthGraphHumanGate:
		return "GRAPH HUMAN GATE"
	case promptgate.AuthDeviceCapability:
		return "DEVICE CAPABILITY"
	default:
		return "TOOL REVIEW"
	}
}
