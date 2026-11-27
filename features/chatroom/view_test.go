// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package chatroom_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/features/chatroom"
)

func TestChatRoomDesktopMultiRoleThreadsMattersAndRBACGovernance(t *testing.T) {
	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	mod := chatroom.NewModule(ledger)
	view := mod.View

	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		view.Render(c, theme, false)
	}, 1220, 820)
	tt.Frame()

	// 1. Verify initial Marlin-native Agent Room renders channels, agent roles, and seeded message.
	for _, want := range []string{
		"AGENT ROOM", "#workspace", "#architecture", "@architect", "@reviewer", "@security",
		"Shared context synchronized", "Thread (1)",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected Agent Room desktop view to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// 2. Post a new message mentioning @security and open the Thread Working Memory drawer.
	view.SetDraft("Please verify RBAC trail @security")
	tt.Frame()
	if err := tt.Click("Post"); err != nil {
		t.Fatalf("click Post: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Please verify RBAC trail @security") {
		t.Fatalf("expected posted message in timeline, got %q", strings.Join(tt.Texts(), " | "))
	}

	if err := tt.Click("Thread (1)"); err != nil {
		t.Fatalf("click Thread (1): %v", err)
	}
	tt.Frame()
	if !tt.HasText("THREAD MEMORY") || !tt.HasText("Verified zero cyclic imports") {
		t.Fatalf("expected Thread Memory drawer open, got %q", strings.Join(tt.Texts(), " | "))
	}
	view.SetThreadDraft("Looks solid, ready to ship.")
	tt.Frame()
	if err := tt.Click("Reply"); err != nil {
		t.Fatalf("click Reply in thread: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Looks solid, ready to ship.") {
		t.Fatalf("expected thread reply rendered, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 3. Switch to Matters tab, add a new shared matter, and mark it Done.
	if err := tt.Click("Matters (2)"); err != nil {
		t.Fatalf("click Matters (2): %v", err)
	}
	tt.Frame()
	view.SetItemDraft("Verify build-tag pruning in CI")
	tt.Frame()
	if err := tt.Click("Add Matter"); err != nil {
		t.Fatalf("click Add Matter: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Verify build-tag pruning in CI") {
		t.Fatalf("expected created matter rendered, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 4. Switch to Roles tab and grant CanManageItems to agent-reviewer -> GovernanceLedger records ADMITTED receipt!
	if err := tt.Click("Roles"); err != nil {
		t.Fatalf("click Roles: %v", err)
	}
	tt.Frame()
	if !tt.HasText("GATE APPROVE: HUMAN ONLY") {
		t.Fatalf("expected human-only gate approval invariant badge, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("agent-reviewer Matters: OFF"); err != nil {
		t.Fatalf("click toggle agent-reviewer Matters permission: %v", err)
	}
	tt.Frame()
	if !tt.HasText("agent-reviewer Matters: ON") {
		t.Fatalf("expected agent-reviewer Matters toggled ON, got %q", strings.Join(tt.Texts(), " | "))
	}
	if len(ledger.AuditTrail()) == 0 {
		t.Fatal("expected GovernanceLedger audit receipt recorded for room RBAC update")
	}
}

func TestChatRoomMobileResponsiveViews(t *testing.T) {
	view := chatroom.NewView(chatroom.NewController(nil))
	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		view.Render(c, theme, true)
	}, 393, 852)
	tt.Frame()

	for _, tab := range []struct{ click, want string }{
		{"Rooms", "#architecture"},
		{"Matters (2)", "Audit role permission boundaries"},
		{"Roles", "GATE APPROVE: HUMAN ONLY"},
		{"Timeline", "Shared context synchronized"},
	} {
		if err := tt.Click(tab.click); err != nil {
			t.Fatalf("mobile click %q: %v", tab.click, err)
		}
		tt.Frame()
		if !tt.HasText(tab.want) {
			t.Fatalf("expected mobile %q view to render %q, got %q", tab.click, tab.want, strings.Join(tt.Texts(), " | "))
		}
	}
}
