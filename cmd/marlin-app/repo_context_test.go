// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

func TestNormalizeRepoURL(t *testing.T) {
	cases := []struct {
		raw      string
		wantWeb  string
		wantSlug string
		wantGH   bool
	}{
		{
			raw:      "https://github.com/tao3k/marlin-mygo",
			wantWeb:  "https://github.com/tao3k/marlin-mygo",
			wantSlug: "tao3k/marlin-mygo",
			wantGH:   true,
		},
		{
			raw:      "https://github.com/tao3k/marlin-mygo/pull/1",
			wantWeb:  "https://github.com/tao3k/marlin-mygo",
			wantSlug: "tao3k/marlin-mygo",
			wantGH:   true,
		},
		{
			raw:      "git@github.com:tao3k/marlin-ui-marketplace.git",
			wantWeb:  "https://github.com/tao3k/marlin-ui-marketplace",
			wantSlug: "tao3k/marlin-ui-marketplace",
			wantGH:   true,
		},
		{
			raw:      "https://gitlab.com/acme/agent-ui.git",
			wantWeb:  "https://gitlab.com/acme/agent-ui",
			wantSlug: "acme/agent-ui",
			wantGH:   false,
		},
	}
	for _, tc := range cases {
		got, err := NormalizeRepoURL(tc.raw)
		if err != nil {
			t.Fatalf("NormalizeRepoURL(%q) unexpected err: %v", tc.raw, err)
		}
		if got.WebURL != tc.wantWeb || got.RepoSlug != tc.wantSlug || got.IsGitHub != tc.wantGH {
			t.Fatalf("NormalizeRepoURL(%q) = %+v, want web=%q slug=%q gh=%v",
				tc.raw, got, tc.wantWeb, tc.wantSlug, tc.wantGH)
		}
	}

	rejected := []string{
		"",
		"http://github.com/tao3k/marlin-mygo",
		"javascript:alert(1)",
		"file:///etc/passwd",
		"https://user:token@github.com/tao3k/marlin-mygo",
		"https://github.com/tao3k/../evil",
	}
	for _, raw := range rejected {
		if _, err := NormalizeRepoURL(raw); !errors.Is(err, ErrInvalidRepoURL) {
			t.Fatalf("NormalizeRepoURL(%q) expected ErrInvalidRepoURL, got %v", raw, err)
		}
	}
}

func TestShowcaseDispatcherGitAndGitHubComposableFlow(t *testing.T) {
	rc, err := NormalizeRepoURL("https://github.com/tao3k/marlin-mygo")
	if err != nil {
		t.Fatalf("NormalizeRepoURL: %v", err)
	}
	disp := newShowcaseDispatcher(rc)
	ctx := context.Background()

	// 1. Mount a new GitHub URL live via git.repo.connect.
	rcpt, err := disp.SubmitScopedPanelAction(ctx, pluginhost.ScopedActionRequest{
		ActionName: "git.repo.connect",
		Args:       json.RawMessage(`{"repoUrl":"https://github.com/tao3k/marlin-ui-marketplace"}`),
	})
	if err != nil || !rcpt.Admitted {
		t.Fatalf("git.repo.connect failed: rcpt=%+v err=%v", rcpt, err)
	}
	if disp.RepoContext().RepoSlug != "tao3k/marlin-ui-marketplace" {
		t.Fatalf("expected connected repo tao3k/marlin-ui-marketplace, got %q", disp.RepoContext().RepoSlug)
	}

	// 2. Reject unsafe URL scheme fail-closed.
	if _, err := disp.SubmitScopedPanelAction(ctx, pluginhost.ScopedActionRequest{
		ActionName: "github.repo.connect",
		Args:       json.RawMessage(`{"repoUrl":"javascript:alert(1)"}`),
	}); !errors.Is(err, ErrInvalidRepoURL) {
		t.Fatalf("expected ErrInvalidRepoURL for javascript: scheme, got %v", err)
	}

	// 3. Stage, commit, and checkout a PR branch across composable Git + GitHub components.
	_, _ = disp.SubmitScopedPanelAction(ctx, pluginhost.ScopedActionRequest{
		ActionName: "git.file.stage",
		Args:       json.RawMessage(`{"path":"runtime/mygoshell/marketplace_view.go"}`),
	})
	_, _ = disp.SubmitScopedPanelAction(ctx, pluginhost.ScopedActionRequest{
		ActionName: "git.commit.create",
		Args:       json.RawMessage(`{"message":"feat: wire composable git/github panels"}`),
	})
	_, _ = disp.SubmitScopedPanelAction(ctx, pluginhost.ScopedActionRequest{
		ActionName: "github.pr.checkout",
		Args:       json.RawMessage(`{"pr":12,"branch":"feat/official-marketplace-sync"}`),
	})
	if disp.RepoContext().Branch != "feat/official-marketplace-sync" {
		t.Fatalf("expected checked-out branch feat/official-marketplace-sync, got %q", disp.RepoContext().Branch)
	}
}
