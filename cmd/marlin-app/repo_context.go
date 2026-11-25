// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

const defaultShowcaseRepoURL = "https://github.com/tao3k/marlin-mygo"

var ErrInvalidRepoURL = errors.New("marlin-app: invalid git/github repository URL")

// RepoContext carries a validated Git / GitHub repository reference projected
// into composable Git Workbench and GitHub Workbench UI components.
type RepoContext struct {
	GitURL    string
	WebURL    string
	Host      string
	RepoSlug  string // e.g. "tao3k/marlin-mygo"
	IsGitHub  bool
	Branch    string
}

// NormalizeRepoURL validates and normalizes HTTPS or SSH Git/GitHub repository
// URLs (e.g. "https://github.com/tao3k/marlin-mygo", "git@github.com:tao3k/marlin-mygo.git")
// while failing closed on unsafe schemes such as javascript:, file:, or data:.
func NormalizeRepoURL(raw string) (RepoContext, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return RepoContext{}, fmt.Errorf("%w: empty URL", ErrInvalidRepoURL)
	}

	// Convert SCP-style SSH Git URL (git@host:owner/repo.git) into canonical HTTPS WebURL.
	if strings.HasPrefix(s, "git@") {
		rest := strings.TrimPrefix(s, "git@")
		colon := strings.IndexByte(rest, ':')
		if colon <= 0 || colon >= len(rest)-1 {
			return RepoContext{}, fmt.Errorf("%w: malformed SSH git URL %q", ErrInvalidRepoURL, raw)
		}
		host := strings.ToLower(strings.TrimSpace(rest[:colon]))
		repoPath := strings.Trim(strings.TrimSpace(rest[colon+1:]), "/")
		repoPath = strings.TrimSuffix(repoPath, ".git")
		if host == "" || !strings.Contains(repoPath, "/") || strings.Contains(repoPath, "..") {
			return RepoContext{}, fmt.Errorf("%w: invalid SSH repo path %q", ErrInvalidRepoURL, raw)
		}
		webURL := "https://" + host + "/" + repoPath
		return RepoContext{
			GitURL:   s,
			WebURL:   webURL,
			Host:     host,
			RepoSlug: repoPath,
			IsGitHub: host == "github.com",
			Branch:   "main",
		}, nil
	}

	u, err := url.Parse(s)
	if err != nil {
		return RepoContext{}, fmt.Errorf("%w: %v", ErrInvalidRepoURL, err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return RepoContext{}, fmt.Errorf("%w: scheme %q forbidden (https:// or git@ required)", ErrInvalidRepoURL, u.Scheme)
	}
	if u.User != nil {
		return RepoContext{}, fmt.Errorf("%w: embedded URL credentials forbidden", ErrInvalidRepoURL)
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return RepoContext{}, fmt.Errorf("%w: missing host", ErrInvalidRepoURL)
	}
	cleanPath := strings.Trim(strings.TrimSpace(u.Path), "/")
	cleanPath = strings.TrimSuffix(cleanPath, ".git")
	if cleanPath == "" || !strings.Contains(cleanPath, "/") || strings.Contains(cleanPath, "..") {
		return RepoContext{}, fmt.Errorf("%w: expected owner/repo path in %q", ErrInvalidRepoURL, raw)
	}
	// Strip trailing GitHub subpaths like /pull/1 or /tree/main when extracting owner/repo slug.
	parts := strings.Split(cleanPath, "/")
	slug := parts[0] + "/" + parts[1]
	webURL := "https://" + host + "/" + slug
	return RepoContext{
		GitURL:   webURL + ".git",
		WebURL:   webURL,
		Host:     host,
		RepoSlug: slug,
		IsGitHub: host == "github.com",
		Branch:   "main",
	}, nil
}

// detectInitialRepoContext resolves the active Git/GitHub repository from an
// explicit -repo-url flag, falling back to `git remote get-url origin`, and
// finally defaulting cleanly to tao3k/marlin-mygo.
func detectInitialRepoContext(explicitURL string) RepoContext {
	if strings.TrimSpace(explicitURL) != "" {
		if rc, err := NormalizeRepoURL(explicitURL); err == nil {
			rc.Branch = detectLocalGitBranch("main")
			return rc
		}
	}
	if out, err := exec.Command("git", "remote", "get-url", "origin").Output(); err == nil {
		if rc, normErr := NormalizeRepoURL(string(out)); normErr == nil {
			rc.Branch = detectLocalGitBranch("main")
			return rc
		}
	}
	rc, _ := NormalizeRepoURL(defaultShowcaseRepoURL)
	rc.Branch = detectLocalGitBranch("feat/marlin-mygo-frontend-engine")
	return rc
}

func detectLocalGitBranch(fallback string) string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return fallback
	}
	b := strings.TrimSpace(string(out))
	if b == "" || b == "HEAD" {
		return fallback
	}
	return b
}
