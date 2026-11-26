// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package features

import (
	"errors"
	"testing"

	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/shell"
)

type stubFeature struct {
	meta FeatureMeta
	buf  shell.PaneBuffer
	cmd  input.CommandEntry
}

func (s stubFeature) Meta() FeatureMeta { return s.meta }
func (s stubFeature) Register(host *Host) error {
	if s.buf.BufferID != "" {
		host.AddBuffer(s.buf, func(any, any) {})
	}
	if s.cmd.Name != "" {
		host.AddCommand(s.cmd)
	}
	return nil
}

func TestFeatureSelectionParsingAndGovernedActivation(t *testing.T) {
	reg := NewRegistry()
	if err := reg.RegisterCompiled(stubFeature{
		meta: FeatureMeta{
			ID:                   FeatureChatRoom,
			Name:                 "Agent Chat Room",
			Category:             "collaboration",
			TrustTier:            Tier0HostNative,
			RequiredCapabilities: []string{"projection.rooms", "gate.prompt"},
			DefaultEnabled:       true,
		},
		buf: shell.PaneBuffer{BufferID: "feature:chatroom", Title: "Chat Room", Kind: "native-gpu"},
		cmd: input.CommandEntry{Name: "feature.open-chatroom", Description: "Open Agent Chat Room"},
	}); err != nil {
		t.Fatalf("register chatroom: %v", err)
	}
	if err := reg.RegisterCompiled(stubFeature{
		meta: FeatureMeta{
			ID:                   FeatureCompare,
			Name:                 "Multi-Model Compare",
			Category:             "reasoning",
			TrustTier:            Tier0HostNative,
			RequiredCapabilities: []string{"turn.branch"},
			DefaultEnabled:       false,
		},
		buf: shell.PaneBuffer{BufferID: "feature:compare", Title: "Model Compare", Kind: "native-gpu"},
	}); err != nil {
		t.Fatalf("register compare: %v", err)
	}
	if err := reg.RegisterCompiled(stubFeature{
		meta: FeatureMeta{
			ID:                   FeatureCanvas,
			Name:                 "Untrusted Canvas Preview",
			Category:             "workspace",
			TrustTier:            Tier2UntrustedWebView,
			RequiredCapabilities: []string{"artifact.read"},
			DefaultEnabled:       true,
		},
	}); err != nil {
		t.Fatalf("register canvas: %v", err)
	}

	// Governance policy disables Tier-2 untrusted WebView features fail-closed.
	pol := DefaultGovernancePolicy()
	pol.AllowUntrustedWebUI = false
	ledger := NewGovernanceLedger(pol)

	host := NewHost()
	statuses := reg.Activate(ParseFeatureSpec("+compare"), ledger, host)
	if len(statuses) != 3 {
		t.Fatalf("expected 3 compiled statuses, got %d", len(statuses))
	}
	if !reg.IsEnabled(FeatureChatRoom) || !reg.IsEnabled(FeatureCompare) {
		t.Fatalf("expected chatroom and +compare enabled, got %+v", statuses)
	}
	if reg.IsEnabled(FeatureCanvas) {
		t.Fatal("expected Tier-2 untrusted canvas denied fail-closed by GovernancePolicy")
	}
	if len(host.Buffers()) != 2 || len(host.Commands()) != 1 {
		t.Fatalf("expected 2 feature buffers and 1 command registered, got bufs=%d cmds=%d", len(host.Buffers()), len(host.Commands()))
	}

	// Dynamic runtime disable + blocked feature check.
	if err := reg.SetEnabled(FeatureCompare, false, ledger, host); err != nil {
		t.Fatalf("disable compare: %v", err)
	}
	if reg.IsEnabled(FeatureCompare) {
		t.Fatal("expected compare disabled at runtime")
	}
	ledger.BlockFeature(FeatureCompare, "enterprise lockdown")
	if err := reg.SetEnabled(FeatureCompare, true, ledger, host); !errors.Is(err, ErrFeatureDenied) {
		t.Fatalf("expected ErrFeatureDenied when re-enabling blocked feature, got %v", err)
	}

	// Verify newest-first audit ledger captured admissions & denials.
	trail := ledger.AuditTrail()
	if len(trail) < 4 || trail[0].Outcome != AuditDenied || trail[0].Subject != string(FeatureCompare) {
		t.Fatalf("unexpected governance audit trail: %+v", trail)
	}

	// Verify explicit "none" selection activates zero optional features.
	emptyHost := NewHost()
	reg.Activate(ParseFeatureSpec("none"), ledger, emptyHost)
	if reg.IsEnabled(FeatureChatRoom) || len(emptyHost.Buffers()) != 0 {
		t.Fatal("expected 'none' spec to activate zero optional features")
	}
}
