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
	flowEntry          string
	flowSrcScope       string
	flowTarget         string
	flowDstScope       string
	derivID            string
	derivPolicy        string
	derivPremises      string
	derivParents       string
	taintSrc           string
	taintDst           string
	taintRole          string
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
		flowEntry:      "vault-secret",
		flowSrcScope:   "private",
		flowTarget:     "ext-webhook",
		flowDstScope:   "external",
		derivID:        "D-DEPLOY",
		derivPolicy:    "pol.deploy",
		derivPremises:  "approval-sig",
		derivParents:   "",
		taintSrc:       "chat-ext",
		taintDst:       "shared-ctx",
		taintRole:      "source",
	}
}

type governanceInspectorActions struct {
	toggleInspector bool
	toggleStepUp    bool
	runCustomProbe  bool
	runAddDelegate  bool
	cycleFlowSrc    bool
	cycleFlowDst    bool
	runRecordFlow   bool
	runRegisterDer  bool
	cycleTaintRole  bool
	runAddTaintEdge bool
}

// SetShowCedarInspector toggles the expandable Cedar Policy Source Inspector &
// Interactive Fact-Graph Builder drawer inside the Tier-0 Governance Console.
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

// SetContextFlowDraft programmatically populates the interactive 5-Scope Context Flow Builder fields.
func (g *GovernanceController) SetContextFlowDraft(entry, srcScope, target, dstScope string) {
	g.mu.Lock()
	g.inspector.flowEntry = entry
	g.inspector.flowSrcScope = srcScope
	g.inspector.flowTarget = target
	g.inspector.flowDstScope = dstScope
	g.inspector.inputRev++
	g.mu.Unlock()
}

// SetDerivationDraft programmatically populates the interactive Bi-Temporal Derivation Builder fields.
func (g *GovernanceController) SetDerivationDraft(derivationID, policyID, premisesCSV, parentsCSV string) {
	g.mu.Lock()
	g.inspector.derivID = derivationID
	g.inspector.derivPolicy = policyID
	g.inspector.derivPremises = premisesCSV
	g.inspector.derivParents = parentsCSV
	g.inspector.inputRev++
	g.mu.Unlock()
}

// SetTaintEdgeDraft programmatically populates the interactive CaMeL Taint Edge Builder fields.
func (g *GovernanceController) SetTaintEdgeDraft(src, dst, role string) {
	g.mu.Lock()
	g.inspector.taintSrc = src
	g.inspector.taintDst = dst
	g.inspector.taintRole = role
	g.inspector.inputRev++
	g.mu.Unlock()
}

func nextGovernanceScope(cur string) string {
	order := []string{"private", "dm", "channel", "workspace", "external"}
	for i, scope := range order {
		if scope == cur {
			return order[(i+1)%len(order)]
		}
	}
	return "private"
}

func nextTaintRole(cur string) string {
	order := []string{"both", "source", "sink", "hop"}
	for i, role := range order {
		if role == cur {
			return order[(i+1)%len(order)]
		}
	}
	return "source"
}

func splitCSVTokens(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
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

			ui.Text(c, "LIVE 5-SCOPE CONTEXT FLOW BUILDER (EXFIL CEDAR SYNTHESIS)").
				FontSize(10).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.InkMuted))
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				renderGovernanceCompactInput(c, dt, fmt.Sprintf("gov-flow-entry-%d", st.inputRev), &st.flowEntry, "entry")
				if ApplePillButton(c, "gov-flow-src-scope", "Src: "+st.flowSrcScope, BtnGlass, dt, nil, true).Clicked() {
					act.cycleFlowSrc = true
				}
				ui.Text(c, "->").FontSize(11).FontWeight(700).TextColor(toColor(dt.InkMuted))
				renderGovernanceCompactInput(c, dt, fmt.Sprintf("gov-flow-target-%d", st.inputRev), &st.flowTarget, "target")
				if ApplePillButton(c, "gov-flow-dst-scope", "Dst: "+st.flowDstScope, BtnGlass, dt, nil, true).Clicked() {
					act.cycleFlowDst = true
				}
				if ApplePillButton(c, "gov-record-flow", "Record Flow", BtnGlass, dt, nil, true).Clicked() {
					act.runRecordFlow = true
				}
			})

			ui.Text(c, "LIVE BI-TEMPORAL DERIVATION DAG BUILDER (TRUTH MAINTENANCE)").
				FontSize(10).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.InkMuted))
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				renderGovernanceCompactInput(c, dt, fmt.Sprintf("gov-deriv-id-%d", st.inputRev), &st.derivID, "derivation_id")
				renderGovernanceCompactInput(c, dt, fmt.Sprintf("gov-deriv-pol-%d", st.inputRev), &st.derivPolicy, "policy_id")
				renderGovernanceCompactInput(c, dt, fmt.Sprintf("gov-deriv-prem-%d", st.inputRev), &st.derivPremises, "premises (csv)")
				renderGovernanceCompactInput(c, dt, fmt.Sprintf("gov-deriv-par-%d", st.inputRev), &st.derivParents, "parents (csv)")
				if ApplePillButton(c, "gov-register-deriv", "Register Derivation", BtnGlass, dt, nil, true).Clicked() {
					act.runRegisterDer = true
				}
			})

			ui.Text(c, "LIVE CAMEL TAINT FLOW BUILDER (STRATIFIED DATALOG)").
				FontSize(10).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.InkMuted))
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				renderGovernanceCompactInput(c, dt, fmt.Sprintf("gov-taint-src-%d", st.inputRev), &st.taintSrc, "src")
				ui.Text(c, "->").FontSize(11).FontWeight(700).TextColor(toColor(dt.InkMuted))
				renderGovernanceCompactInput(c, dt, fmt.Sprintf("gov-taint-dst-%d", st.inputRev), &st.taintDst, "dst")
				if ApplePillButton(c, "gov-taint-role", "Role: "+st.taintRole, BtnGlass, dt, nil, true).Clicked() {
					act.cycleTaintRole = true
				}
				if ApplePillButton(c, "gov-add-taint-edge", "Add Taint Edge", BtnGlass, dt, nil, true).Clicked() {
					act.runAddTaintEdge = true
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

func activeRevocableActors(snap BackendGovernanceSnapshot, maxItems int) []string {
	out := make([]string, 0, maxItems)
	seen := make(map[string]struct{}, len(snap.FencedActors)+maxItems)
	for _, fenced := range snap.FencedActors {
		seen[fenced] = struct{}{}
	}
	for _, edge := range snap.FactDelegations {
		for _, candidate := range []string{edge.Parent, edge.Child} {
			c := strings.TrimSpace(candidate)
			if c == "" {
				continue
			}
			if _, exists := seen[c]; exists {
				continue
			}
			seen[c] = struct{}{}
			out = append(out, c)
			if len(out) >= maxItems {
				return out
			}
		}
	}
	return out
}

func activeDeclassifiableEntries(snap BackendGovernanceSnapshot, maxItems int) []string {
	out := make([]string, 0, maxItems)
	seen := make(map[string]struct{}, len(snap.FactDeclassifiedEntries)+maxItems)
	for _, dec := range snap.FactDeclassifiedEntries {
		seen[dec] = struct{}{}
	}
	for _, flow := range snap.FactContextFlows {
		e := strings.TrimSpace(flow.Entry)
		if e == "" {
			continue
		}
		if _, exists := seen[e]; exists {
			continue
		}
		seen[e] = struct{}{}
		out = append(out, e)
		if len(out) >= maxItems {
			return out
		}
	}
	return out
}

func activeRetractablePremises(snap BackendGovernanceSnapshot, maxItems int) []string {
	out := make([]string, 0, maxItems)
	seen := make(map[string]struct{}, len(snap.FactRetractedSubjects)+maxItems)
	for _, r := range snap.FactRetractedSubjects {
		seen[r] = struct{}{}
	}
	for _, d := range snap.FactDerivations {
		for _, prem := range d.SubjectPremises {
			p := strings.TrimSpace(prem)
			if p == "" {
				continue
			}
			if _, exists := seen[p]; exists {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
			if len(out) >= maxItems {
				return out
			}
		}
	}
	return out
}

func activeSanitizableTaintNodes(snap BackendGovernanceSnapshot, maxItems int) []string {
	out := make([]string, 0, maxItems)
	blocked := make(map[string]struct{}, len(snap.FactTaintSanitizers)+len(snap.FactTaintSources)+len(snap.FactTaintSinks))
	for _, s := range snap.FactTaintSanitizers {
		blocked[s] = struct{}{}
	}
	for _, s := range snap.FactTaintSources {
		blocked[s] = struct{}{}
	}
	for _, s := range snap.FactTaintSinks {
		blocked[s] = struct{}{}
	}
	for _, edge := range snap.FactTaintEdges {
		for _, candidate := range []string{edge.Dst, edge.Src} {
			n := strings.TrimSpace(candidate)
			if n == "" {
				continue
			}
			if _, skip := blocked[n]; skip {
				continue
			}
			blocked[n] = struct{}{}
			out = append(out, n)
			if len(out) >= maxItems {
				return out
			}
		}
	}
	return out
}
