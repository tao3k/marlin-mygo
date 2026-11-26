// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

type governanceInspectorState struct {
	showCedarInspector bool
	probeActor         string
	probeAction        string
	probeResource      string
	probeStepUp        bool
	delegateParent     string
	delegateChild      string
	inputRev           uint64
}

func defaultGovernanceInspectorState() governanceInspectorState {
	return governanceInspectorState{
		probeActor:     "ops-alice",
		probeAction:    "rearm_breaker",
		probeResource:  "workspace",
		probeStepUp:    true,
		delegateParent: "ops-root",
		delegateChild:  "agent-sub",
	}
}

type governanceInspectorActions struct {
	toggleInspector bool
	toggleStepUp    bool
	runCustomProbe  bool
	runAddDelegate  bool
}

// SetShowCedarInspector toggles the expandable Cedar Policy Source Inspector &
// Interactive Probe/Delegation drawer inside the Tier-0 Governance Console.
func (g *GovernanceController) SetShowCedarInspector(show bool) {
	g.mu.Lock()
	g.inspector.showCedarInspector = show
	g.mu.Unlock()
}

// SetProbeDraft programmatically populates the interactive Cedar Probe form fields.
func (g *GovernanceController) SetProbeDraft(actor, action, resource string, stepUpFresh bool) {
	g.mu.Lock()
	g.inspector.probeActor = actor
	g.inspector.probeAction = action
	g.inspector.probeResource = resource
	g.inspector.probeStepUp = stepUpFresh
	g.inspector.inputRev++
	g.mu.Unlock()
}

// SetDelegationDraft programmatically populates the interactive Delegation Builder fields.
func (g *GovernanceController) SetDelegationDraft(parent, child string) {
	g.mu.Lock()
	g.inspector.delegateParent = parent
	g.inspector.delegateChild = child
	g.inspector.inputRev++
	g.mu.Unlock()
}

func renderGovernanceCedarInspector(
	c *ui.Context,
	dt DerivedTheme,
	bsnap BackendGovernanceSnapshot,
	hasMutator bool,
	st *governanceInspectorState,
) governanceInspectorActions {
	var act governanceInspectorActions

	toggleLabel := fmt.Sprintf("Inspect Cedar Rules (%d)", len(bsnap.SynthesizedCedarRules))
	if st.showCedarInspector {
		toggleLabel = "Hide Cedar Rules"
	}
	ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
		if ApplePillButton(c, "gov-toggle-cedar-inspector", toggleLabel, BtnGlass, dt, nil, true).Clicked() {
			act.toggleInspector = true
		}
	})
	if !st.showCedarInspector {
		return act
	}

	ui.Column(c.Key("gov-cedar-inspector-body")).Gap(8).Padding(8, 10).Radius(10).
		Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).AlignItems(ui.Stretch).Children(func() {
		ui.Text(c, "SYNTHESIZED CEDAR POLICY SOURCE (FORBID OVERRIDES PERMIT)").
			FontSize(10).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.InkMuted))
		if len(bsnap.SynthesizedCedarRules) == 0 {
			ui.Text(c, "No synthesized Cedar rules currently installed.").
				FontSize(11).TextColor(toColor(dt.InkMuted))
		} else {
			for i, rule := range bsnap.SynthesizedCedarRules {
				if i >= 8 {
					ui.Textf(c, "… +%d additional Cedar policies", len(bsnap.SynthesizedCedarRules)-i).
						FontSize(10.5).TextColor(toColor(dt.InkMuted))
					break
				}
				r := rule
				effectLabel := "PERMIT"
				effectTone := dt.Success
				if strings.HasPrefix(strings.TrimSpace(r.Source), "forbid") {
					effectLabel = "FORBID"
					effectTone = dt.Danger
				}
				ui.Column(c.Key(fmt.Sprintf("gov-cedar-rule-%d", i))).Gap(3).Padding(6, 8).Radius(8).
					Background(toColor(dt.SurfaceElevated)).Border(1, glassBorderColor(dt)).AlignItems(ui.Stretch).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
						AppleBadgePill(c, effectLabel, effectTone, dt)
						ui.Text(c, r.PolicyID).FontSize(11).FontWeight(700).TextColor(toColor(dt.Ink))
					})
					compactSrc := strings.Join(strings.Fields(r.Source), " ")
					ui.Text(c, compactSrc).FontSize(10.5).TextColor(toColor(dt.InkSecondary))
				})
			}
		}

		if hasMutator {
			ui.Text(c, "LIVE COMPILED CEDAR PROBE").
				FontSize(10).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.InkMuted))
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				renderGovernanceCompactInput(c, dt, fmt.Sprintf("gov-probe-actor-%d", st.inputRev), &st.probeActor, "actor")
				renderGovernanceCompactInput(c, dt, fmt.Sprintf("gov-probe-action-%d", st.inputRev), &st.probeAction, "action")
				renderGovernanceCompactInput(c, dt, fmt.Sprintf("gov-probe-res-%d", st.inputRev), &st.probeResource, "resource")
				stepLabel := "Step-Up: OFF"
				stepStyle := BtnGlass
				if st.probeStepUp {
					stepLabel = "Step-Up: ON"
					stepStyle = BtnTinted
				}
				if ApplePillButton(c, "gov-probe-stepup-toggle", stepLabel, stepStyle, dt, nil, true).Clicked() {
					act.toggleStepUp = true
				}
				if ApplePillButton(c, "gov-run-custom-probe", "Run Custom Probe", BtnTinted, dt, nil, true).Clicked() {
					act.runCustomProbe = true
				}
			})

			ui.Text(c, "LIVE OBO DELEGATION BUILDER (ASCENT)").
				FontSize(10).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.InkMuted))
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				renderGovernanceCompactInput(c, dt, fmt.Sprintf("gov-del-parent-%d", st.inputRev), &st.delegateParent, "parent")
				ui.Text(c, "->").FontSize(11).FontWeight(700).TextColor(toColor(dt.InkMuted))
				renderGovernanceCompactInput(c, dt, fmt.Sprintf("gov-del-child-%d", st.inputRev), &st.delegateChild, "child")
				if ApplePillButton(c, "gov-add-delegation", "Add Delegation", BtnGlass, dt, nil, true).Clicked() {
					act.runAddDelegate = true
				}
			})
		}
	})

	return act
}

func renderGovernanceCompactInput(c *ui.Context, dt DerivedTheme, key string, val *string, placeholder string) {
	ui.Row(c).Padding(4, 8).Radius(6).AlignItems(ui.Center).Grow(1).
		Background(toColor(dt.SurfaceElevated)).Border(1, toColor(dt.Border)).Children(func() {
		ui.TextInputBase(c.Key(key), val).
			Placeholder(placeholder).
			FontSize(11).TextColor(toColor(dt.Ink)).Grow(1)
	})
}
