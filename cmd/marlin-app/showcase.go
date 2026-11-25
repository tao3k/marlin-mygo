// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

var showcaseExampleDirs = []string{
	filepath.Join("examples", "plugins", "ts-org-agenda-planner"),
	filepath.Join("examples", "plugins", "react-ai-memory-inbox"),
	filepath.Join("examples", "plugins", "react-ai-workflow-cockpit"),
	filepath.Join("examples", "plugins", "react-git-workbench"),
	filepath.Join("examples", "plugins", "react-github-workbench"),
	filepath.Join("examples", "plugins", "go-focus-clock-reactor"),
	filepath.Join("examples", "plugins", "declarative-daily-planner-pack"),
}

func resolveShowcasePluginID(alias string) schema.PluginID {
	switch alias {
	case "memory", "inbox", "tao3k.ai-memory-inbox":
		return "tao3k.ai-memory-inbox"
	case "workflow", "cockpit", "tao3k.ai-workflow-cockpit":
		return "tao3k.ai-workflow-cockpit"
	case "git", "git-workbench", "tao3k.git-workbench":
		return "tao3k.git-workbench"
	case "github", "github-workbench", "tao3k.github-workbench":
		return "tao3k.github-workbench"
	default:
		return "tao3k.org-agenda-planner"
	}
}

// prepareShowcaseCatalog compiles, audits, signs, and packages the flagship
// React 19 + Go WASI + Declarative plugins into an ephemeral Marketplace dir
// so `just showcase` opens a live multi-runtime desktop session in one command.
func prepareShowcaseCatalog(ctx context.Context) (catalogDir, pubKeyFile string, cleanup func(), err error) {
	noop := func() {}
	tmpDir, err := os.MkdirTemp("", "marlin-showcase-catalog-*")
	if err != nil {
		return "", "", noop, err
	}
	clean := func() { _ = os.RemoveAll(tmpDir) }

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		clean()
		return "", "", noop, err
	}
	pubPath := filepath.Join(tmpDir, "publisher.pub")
	if err := os.WriteFile(pubPath, []byte(hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
		clean()
		return "", "", noop, err
	}

	var rev [20]byte
	log.Printf("[showcase] certifying & signing %d example plugins (React 19 + Go WASI + Declarative)...", len(showcaseExampleDirs))
	for _, dir := range showcaseExampleDirs {
		rcpt, pubErr := marketplace.PublishPlugin(ctx, marketplace.PublishConfig{
			PluginDir:       dir,
			PublisherID:     "tao3k.official",
			PrivateKey:      priv,
			PublicKey:       pub,
			SourceRev:       rev,
			OutCatalogDir:   tmpDir,
			ReplaceExisting: true,
		})
		if pubErr != nil {
			clean()
			return "", "", noop, fmt.Errorf("publish showcase plugin %s: %w", dir, pubErr)
		}
		log.Printf("[showcase] ready: %-28s (%s, %d bytes)", rcpt.PluginID, rcpt.Runtime, rcpt.PackageBytes)
	}
	return tmpDir, pubPath, clean, nil
}

type showcaseGitFile struct {
	Path        string `json:"path"`
	State       string `json:"state"`
	Additions   int    `json:"additions"`
	Deletions   int    `json:"deletions"`
	HunkSummary string `json:"hunkSummary"`
}

type showcaseGitHubItem struct {
	Number          int      `json:"number"`
	Kind            string   `json:"kind"`
	Title           string   `json:"title"`
	Author          string   `json:"author"`
	Branch          string   `json:"branch,omitempty"`
	ReviewRequested bool     `json:"reviewRequested"`
	CIState         string   `json:"ciState"`
	Labels          []string `json:"labels"`
	UpdatedAgo      string   `json:"updatedAgo"`
}

// showcaseDispatcher wires live bidirectional cross-runtime linkage during
// `-showcase`: actions submitted by a React 19 WebView (e.g. `org.clock.in`,
// `git.file.stage`, `github.pr.checkout`, `workflow.gate.focus`) immediately
// update sibling Git/GitHub panels, the Go WASI GPU Focus Clock reactor, or
// open the Tier-0 Host Approval Gate floating overlay!
type showcaseDispatcher struct {
	mu           sync.Mutex
	host         *pluginhost.Host
	app          *mygoshell.WorkbenchApp
	win          *mygo.Window
	repo         RepoContext
	gitFiles     []showcaseGitFile
	ghItems      []showcaseGitHubItem
	clockRunning bool
	activeTitle  string
	evSeq        atomic.Uint64
}

func newShowcaseDispatcher(repo RepoContext) *showcaseDispatcher {
	return &showcaseDispatcher{
		repo: repo,
		gitFiles: []showcaseGitFile{
			{Path: "control/marketplace/official_sync.go", State: "staged", Additions: 214, Deletions: 0, HunkSummary: "ValidateOfficialRegistryURL + ParseThemePackTOML"},
			{Path: "runtime/mygoshell/marketplace_view.go", State: "modified", Additions: 132, Deletions: 18, HunkSummary: "3-stream Plugins | Themes | Layouts switcher"},
			{Path: "engine/shell/keymap_profile.go", State: "untracked", Additions: 64, Deletions: 0, HunkSummary: "Dual-modal SPC Normal & Emacs Insert Which-Key HUD"},
		},
		ghItems: []showcaseGitHubItem{
			{Number: 1, Kind: "pr", Title: "High-performance modular Marlin MyGo UI engine & plugin ecosystem", Author: "tao3k", Branch: repo.Branch, ReviewRequested: true, CIState: "passing", Labels: []string{"ui-engine", "marketplace"}, UpdatedAgo: "just now"},
			{Number: 12, Kind: "pr", Title: "Official UI Marketplace 3-stream sync (Plugins, Themes, Emacs Layouts)", Author: "tao3k", Branch: "feat/official-marketplace-sync", ReviewRequested: true, CIState: "running", Labels: []string{"marketplace", "security"}, UpdatedAgo: "5m ago"},
			{Number: 24, Kind: "issue", Title: "Community submission: Diff & Blame gutter lens for Git Workbench", Author: "community-dev", ReviewRequested: false, CIState: "none", Labels: []string{"community-plugin"}, UpdatedAgo: "42m ago"},
		},
	}
}

func (d *showcaseDispatcher) SetRepoURL(raw string) error {
	rc, err := NormalizeRepoURL(raw)
	if err != nil {
		return err
	}
	d.mu.Lock()
	if d.repo.Branch != "" {
		rc.Branch = d.repo.Branch
	}
	d.repo = rc
	d.mu.Unlock()
	d.publishRepoProjections()
	return nil
}

func (d *showcaseDispatcher) RepoContext() RepoContext {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.repo
}

func (d *showcaseDispatcher) publishRepoProjections() {
	d.mu.Lock()
	repo := d.repo
	files := append([]showcaseGitFile(nil), d.gitFiles...)
	items := append([]showcaseGitHubItem(nil), d.ghItems...)
	d.mu.Unlock()

	d.publishTopic("git.status", map[string]any{
		"repoName":       repo.RepoSlug,
		"repoUrl":        repo.WebURL,
		"branch":         repo.Branch,
		"upstream":       "origin/" + repo.Branch,
		"ahead":          1,
		"behind":         0,
		"recentBranches": []string{repo.Branch, "main"},
		"changes":        files,
	})
	d.publishTopic("github.workspace", map[string]any{
		"repo":          repo.RepoSlug,
		"repoUrl":       repo.WebURL,
		"defaultBranch": "main",
		"items":         items,
	})
}

func (d *showcaseDispatcher) attach(host *pluginhost.Host, app *mygoshell.WorkbenchApp, win *mygo.Window) {
	d.mu.Lock()
	d.host = host
	d.app = app
	d.win = win
	d.mu.Unlock()
}

func (d *showcaseDispatcher) publishTopic(topic string, payload any) {
	d.mu.Lock()
	h, w := d.host, d.win
	d.mu.Unlock()
	if h == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	evID := fmt.Sprintf("showcase-%d", d.evSeq.Add(1))
	_, _ = h.PublishTopic(evID, topic, raw)
	if w != nil {
		w.Invalidate()
	}
}

func (d *showcaseDispatcher) SubmitScopedPanelAction(_ context.Context, req pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	var body map[string]any
	_ = json.Unmarshal(req.Args, &body)
	title, _ := body["title"].(string)

	switch req.ActionName {
	case "org.clock.in":
		if title == "" {
			title = "Deep Work Block"
		}
		d.mu.Lock()
		d.clockRunning = true
		d.activeTitle = title
		d.mu.Unlock()
		go d.publishTopic("org.clock", map[string]any{
			"schemaVersion": 1,
			"activeTitle":   title,
			"quickNote":     "Clocked in: " + title,
		})

	case "org.clock.out":
		d.mu.Lock()
		d.clockRunning = false
		if title == "" {
			title = d.activeTitle
		}
		d.mu.Unlock()
		go d.publishTopic("org.clock", map[string]any{
			"schemaVersion": 1,
			"quickNote":     "Clocked out: " + title,
		})

	case "org.capture.plan":
		if title == "" {
			title = "Captured org entry"
		}
		go d.publishTopic("org.clock", map[string]any{
			"schemaVersion": 1,
			"quickNote":     "Captured: " + title,
		})

	case "agent.workflow.plan-day":
		go d.publishTopic("org.clock", map[string]any{
			"schemaVersion": 1,
			"quickNote":     "AI Daily Plan scheduled",
		})

	case "workflow.gate.focus":
		gateID, _ := body["gateId"].(string)
		if gateID == "" {
			gateID = "gate-demo-1"
		}
		d.mu.Lock()
		a, w := d.app, d.win
		d.mu.Unlock()
		if a != nil {
			a.ShowPendingGate(mygoshell.PendingGateView{
				GateID:      gateID,
				ToolName:    "device.ios.notify.send",
				Prompt:      "Approve AI Workflow dispatch to iPhone Personal?",
				Description: "Tier-0 host.gate modal floats cleanly above the React 19 WKWebView.",
			})
			if w != nil {
				w.Invalidate()
			}
		}

	case "git.repo.connect", "github.repo.connect":
		rawURL, _ := body["repoUrl"].(string)
		if err := d.SetRepoURL(rawURL); err != nil {
			return pluginhost.ScopedActionReceipt{
				Admitted:  false,
				ReceiptID: fmt.Sprintf("rcpt-showcase-%d", d.evSeq.Add(1)),
				Result:    json.RawMessage(fmt.Sprintf(`{"error":%q}`, err.Error())),
			}, err
		}
		go d.publishTopic("org.clock", map[string]any{
			"schemaVersion": 1,
			"quickNote":     "Connected repo: " + d.RepoContext().RepoSlug,
		})

	case "git.file.stage":
		targetPath, _ := body["path"].(string)
		d.mu.Lock()
		for i := range d.gitFiles {
			if d.gitFiles[i].Path == targetPath {
				d.gitFiles[i].State = "staged"
			}
		}
		d.mu.Unlock()
		go d.publishRepoProjections()

	case "git.file.unstage":
		targetPath, _ := body["path"].(string)
		d.mu.Lock()
		for i := range d.gitFiles {
			if d.gitFiles[i].Path == targetPath {
				d.gitFiles[i].State = "modified"
			}
		}
		d.mu.Unlock()
		go d.publishRepoProjections()

	case "git.commit.create":
		msg, _ := body["message"].(string)
		d.mu.Lock()
		remaining := d.gitFiles[:0]
		for _, f := range d.gitFiles {
			if f.State != "staged" {
				remaining = append(remaining, f)
			}
		}
		d.gitFiles = remaining
		d.mu.Unlock()
		go d.publishRepoProjections()
		go d.publishTopic("org.clock", map[string]any{
			"schemaVersion": 1,
			"quickNote":     "Git commit queued: " + msg,
		})

	case "git.branch.checkout", "github.pr.checkout":
		br, _ := body["branch"].(string)
		if br != "" {
			d.mu.Lock()
			d.repo.Branch = br
			d.mu.Unlock()
			go d.publishRepoProjections()
			go d.publishTopic("org.clock", map[string]any{
				"schemaVersion": 1,
				"quickNote":     "Checked out branch: " + br,
			})
		}

	case "github.checks.rerun":
		prNum, _ := body["pr"].(float64)
		d.mu.Lock()
		for i := range d.ghItems {
			if d.ghItems[i].Number == int(prNum) {
				d.ghItems[i].CIState = "running"
			}
		}
		d.mu.Unlock()
		go d.publishRepoProjections()

	case "github.pr.review":
		prNum, _ := body["pr"].(float64)
		d.mu.Lock()
		for i := range d.ghItems {
			if d.ghItems[i].Number == int(prNum) {
				d.ghItems[i].ReviewRequested = false
			}
		}
		d.mu.Unlock()
		go d.publishRepoProjections()
	}

	return pluginhost.ScopedActionReceipt{
		Admitted:  true,
		ReceiptID: fmt.Sprintf("rcpt-showcase-%d", d.evSeq.Add(1)),
		Result:    json.RawMessage(`{"status":"admitted"}`),
	}, nil
}

// startTickLoop ticks the Go WASI GPU Focus Clock reactor once per second while
// a focus block is active so live countdown updates are visible in real time.
func (d *showcaseDispatcher) startTickLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.mu.Lock()
				running := d.clockRunning
				d.mu.Unlock()
				if running {
					d.publishTopic("org.clock", map[string]any{
						"schemaVersion": 1,
						"tickSeconds":   1,
					})
				}
			}
		}
	}()
}
