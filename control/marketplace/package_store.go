// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/async"
)

// PackageKind classifies a governed Marketplace package across Marlin's 3-stream registry.
type PackageKind string

const (
	PackageKindPlugin PackageKind = "plugin"
	PackageKindTheme  PackageKind = "theme"
	PackageKindLayout PackageKind = "layout"
)

// PackageStatus tracks the local installation lifecycle of one Marketplace package.
type PackageStatus string

const (
	PackageStatusAvailable       PackageStatus = "available"
	PackageStatusInstalled       PackageStatus = "installed"
	PackageStatusUpdateAvailable PackageStatus = "update-available"
)

var (
	ErrPackageNotInstalled = errors.New("marketplace: package is not installed")
)

// ManagedPackage is the unified view of an Official Marketplace plugin, theme,
// or Emacs layout managed by PackageStore (analogous to Emacs package.el).
type ManagedPackage struct {
	ID               string               `json:"id"`
	Kind             PackageKind          `json:"kind"`
	Name             string               `json:"name"`
	Category         string               `json:"category"`
	Version          string               `json:"version"`
	InstalledVersion string               `json:"installedVersion,omitempty"`
	TrustTier        TrustTier            `json:"trustTier"`
	PublisherID      schema.PublisherID   `json:"publisherId"`
	Summary          string               `json:"summary"`
	Status           PackageStatus        `json:"status"`
	Active           bool                 `json:"active"`
	ThemePack        *ThemePack           `json:"-"`
	LayoutSpec       *shell.LayoutSpec    `json:"-"`
	PluginEntry      *RegistryPluginEntry `json:"-"`
}

// PackageStoreSnapshot is an immutable point-in-time snapshot published through
// async.SnapshotStore so 120Hz GPU views read package lists wait-free (< 5ns).
type PackageStoreSnapshot struct {
	Source     string
	SyncedAt   time.Time
	Refreshing bool
	LastError  string
	Packages   []ManagedPackage
}

// PackageStoreConfig configures the unified Official Marketplace package manager.
type PackageStoreConfig struct {
	// SyncClient fetches and validates the upstream tao3k/marlin-ui-marketplace
	// registry. When nil, a default fail-closed client pinned strictly to
	// DefaultOfficialRegistryURL is created.
	SyncClient *OfficialSyncClient

	// StateFile optionally persists installed package IDs and versions as atomic JSON.
	StateFile string

	// UIDir optionally points to ~/.config/marlin/ui so installed Marketplace theme
	// packs are automatically materialized under themes/<slug>/{theme.toml, style.css}
	// and cleaned up on Remove.
	UIDir string
}

type persistedPackageState struct {
	SchemaVersion int               `json:"schemaVersion"`
	Installed     map[string]string `json:"installed"` // packageID -> installed version
	ActiveTheme   string            `json:"activeTheme,omitempty"`
	ActiveLayout  string            `json:"activeLayout,omitempty"`
}

// PackageStore is Marlin's unified, registry-governed package manager covering
// Plugins, Themes, and Emacs Layouts from tao3k/marlin-ui-marketplace.
// End-user clients NEVER install arbitrary external URLs; every install/update
// resolves strictly through the validated Official Marketplace registry.
type PackageStore struct {
	mu           sync.Mutex
	syncClient   *OfficialSyncClient
	stateFile    string
	uiDir        string
	source       string
	syncedAt     time.Time
	refreshing   bool
	lastError    string
	catalog      map[string]ManagedPackage
	installed    map[string]string
	activeTheme  string
	activeLayout string
	snap         *async.SnapshotStore[PackageStoreSnapshot]
}

// NewPackageStore initializes a PackageStore pre-seeded with built-in curated
// Official Marketplace packages (so offline startup works immediately) and
// restores any previously installed state from cfg.StateFile.
func NewPackageStore(cfg PackageStoreConfig) (*PackageStore, error) {
	client := cfg.SyncClient
	if client == nil {
		var err error
		client, err = NewOfficialSyncClient(OfficialSyncConfig{})
		if err != nil {
			return nil, err
		}
	}
	ps := &PackageStore{
		syncClient: client,
		stateFile:  strings.TrimSpace(cfg.StateFile),
		uiDir:      strings.TrimSpace(cfg.UIDir),
		source:     "builtin-official",
		catalog:    make(map[string]ManagedPackage),
		installed:  make(map[string]string),
	}
	for _, pkg := range defaultOfficialPackages() {
		ps.catalog[pkg.ID] = pkg
	}
	ps.loadStateLocked()
	ps.recomputeStatusesLocked()
	ps.snap = async.NewSnapshotStore(ps.buildSnapshotLocked())
	return ps, nil
}

// Snapshot returns the current immutable package list wait-free (zero locks).
func (ps *PackageStore) Snapshot() PackageStoreSnapshot {
	if ps == nil || ps.snap == nil {
		return PackageStoreSnapshot{}
	}
	if s := ps.snap.Load(); s != nil {
		return s.Value
	}
	return PackageStoreSnapshot{}
}

// List returns packages filtered by kind ("", "all", "plugin", "theme", "layout").
func (ps *PackageStore) List(kind string) []ManagedPackage {
	snap := ps.Snapshot()
	norm := strings.ToLower(strings.TrimSpace(kind))
	if norm == "" || norm == "all" {
		return snap.Packages
	}
	out := make([]ManagedPackage, 0, len(snap.Packages))
	for _, p := range snap.Packages {
		if string(p.Kind) == norm {
			out = append(out, p)
		}
	}
	return out
}

// Refresh fetches the latest validated Official Marketplace registry snapshot
// off-lock (never blocking Snapshot() readers or the GPU thread), updates
// available/update-available versions, and publishes a new immutable snapshot.
func (ps *PackageStore) Refresh(ctx context.Context) (PackageStoreSnapshot, error) {
	ps.mu.Lock()
	ps.refreshing = true
	ps.lastError = ""
	ps.publishSnapshotLocked()
	client := ps.syncClient
	ps.mu.Unlock()

	synced, err := client.Sync(ctx)

	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.refreshing = false
	if err != nil {
		ps.lastError = err.Error()
		ps.publishSnapshotLocked()
		return ps.buildSnapshotLocked(), err
	}
	ps.mergeSyncedLocked(synced)
	ps.recomputeStatusesLocked()
	ps.publishSnapshotLocked()
	return ps.buildSnapshotLocked(), nil
}

// Install installs a verified Official Marketplace package by ID.
// For Theme packages, when UIDir is configured, it also materializes the
// governed theme pack under <UIDir>/themes/<slug>/{theme.toml, style.css}.
func (ps *PackageStore) Install(id string) (ManagedPackage, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	pkg, ok := ps.catalog[strings.TrimSpace(id)]
	if !ok {
		return ManagedPackage{}, fmt.Errorf("%w: %q", ErrPackageNotFound, id)
	}
	if pkg.Kind == PackageKindTheme && pkg.ThemePack != nil && ps.uiDir != "" {
		if err := materializeThemePack(ps.uiDir, *pkg.ThemePack); err != nil {
			return ManagedPackage{}, err
		}
	}
	ps.installed[pkg.ID] = pkg.Version
	ps.recomputeStatusesLocked()
	if err := ps.saveStateLocked(); err != nil {
		return ManagedPackage{}, err
	}
	ps.publishSnapshotLocked()
	return ps.catalog[pkg.ID], nil
}

// Update upgrades an installed package to the latest synced registry revision.
func (ps *PackageStore) Update(id string) (ManagedPackage, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	id = strings.TrimSpace(id)
	pkg, ok := ps.catalog[id]
	if !ok {
		return ManagedPackage{}, fmt.Errorf("%w: %q", ErrPackageNotFound, id)
	}
	if _, installed := ps.installed[id]; !installed {
		return ManagedPackage{}, fmt.Errorf("%w: %q", ErrPackageNotInstalled, id)
	}
	if pkg.Kind == PackageKindTheme && pkg.ThemePack != nil && ps.uiDir != "" {
		if err := materializeThemePack(ps.uiDir, *pkg.ThemePack); err != nil {
			return ManagedPackage{}, err
		}
	}
	ps.installed[id] = pkg.Version
	ps.recomputeStatusesLocked()
	if err := ps.saveStateLocked(); err != nil {
		return ManagedPackage{}, err
	}
	ps.publishSnapshotLocked()
	return ps.catalog[id], nil
}

// Remove uninstalls a package and removes any materialized theme files on disk.
func (ps *PackageStore) Remove(id string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	id = strings.TrimSpace(id)
	pkg, ok := ps.catalog[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrPackageNotFound, id)
	}
	if _, installed := ps.installed[id]; !installed {
		return fmt.Errorf("%w: %q", ErrPackageNotInstalled, id)
	}
	if pkg.Kind == PackageKindTheme && ps.uiDir != "" {
		slug := ThemePackSlug(pkg.ID)
		if slug != "" {
			_ = os.RemoveAll(filepath.Join(ps.uiDir, "themes", slug))
		}
	}
	delete(ps.installed, id)
	if ps.activeTheme == id {
		ps.activeTheme = ""
	}
	if ps.activeLayout == id {
		ps.activeLayout = ""
	}
	ps.recomputeStatusesLocked()
	if err := ps.saveStateLocked(); err != nil {
		return err
	}
	ps.publishSnapshotLocked()
	return nil
}

// SetActive marks an installed Theme or Layout package as active.
func (ps *PackageStore) SetActive(id string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	id = strings.TrimSpace(id)
	pkg, ok := ps.catalog[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrPackageNotFound, id)
	}
	switch pkg.Kind {
	case PackageKindTheme:
		ps.activeTheme = id
	case PackageKindLayout:
		ps.activeLayout = id
	}
	ps.recomputeStatusesLocked()
	_ = ps.saveStateLocked()
	ps.publishSnapshotLocked()
	return nil
}

// ThemePackSlug converts an Official Marketplace theme ID (e.g. "tao3k.theme-catppuccin")
// into a safe local theme directory slug (e.g. "catppuccin").
func ThemePackSlug(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	if idx := strings.Index(s, "theme-"); idx >= 0 {
		s = s[idx+len("theme-"):]
	} else if idx := strings.LastIndex(s, "."); idx >= 0 {
		s = s[idx+1:]
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-_")
}

// RenderThemePackCSS projects a validated 8-color Official Marketplace ThemePack
// into deterministic Marlin CSS variables consumed by marlin-ui themes.
func RenderThemePackCSS(pack ThemePack) string {
	p := pack.Palette
	return fmt.Sprintf(`/* Managed by Marlin Official Marketplace (%s) */
:root {
  --bg: %s;
  --bar: %s;
  --surface: %s;
  --hover: %s;
  --border: %s;
  --border-subtle: %s;
  --fg: %s;
  --muted: %s;
  --accent: %s;
  --info: %s;
  --success: %s;
  --warning: %s;
  --danger: %s;
}
`,
		pack.ID,
		rgbHex(p.Canvas),
		rgbHex(p.Bar),
		rgbHex(blendRGB(p.Canvas, p.Bar, 0.55)),
		rgbHex(blendRGB(p.Bar, p.Ink, 0.14)),
		rgbHex(blendRGB(p.Bar, p.Muted, 0.40)),
		rgbHex(blendRGB(p.Canvas, p.Muted, 0.25)),
		rgbHex(p.Ink),
		rgbHex(p.Muted),
		rgbHex(p.Accent),
		rgbHex(p.Info),
		rgbHex(p.Success),
		rgbHex(p.Warning),
		rgbHex(dangerRGB(pack.Appearance)),
	)
}

func dangerRGB(appearance string) schema.RGB {
	if appearance == "light" {
		return schema.RGB{R: 220, G: 38, B: 38}
	}
	return schema.RGB{R: 248, G: 113, B: 113}
}

func (ps *PackageStore) mergeSyncedLocked(snap *OfficialRegistrySnapshot) {
	if snap == nil || snap.Manifest == nil {
		return
	}
	ps.source = snap.Source
	ps.syncedAt = snap.SyncedAt
	next := make(map[string]ManagedPackage, len(snap.Manifest.Plugins)+len(snap.Themes)+len(snap.Layouts))

	for _, pe := range snap.Manifest.Plugins {
		entry := pe
		ver := shortRev(entry.CommitSHA, entry.LocalPath, entry.Summary)
		next[string(entry.ID)] = ManagedPackage{
			ID:          string(entry.ID),
			Kind:        PackageKindPlugin,
			Name:        entry.Name,
			Category:    entry.Category,
			Version:     ver,
			TrustTier:   entry.TrustTier,
			PublisherID: entry.PublisherID,
			Summary:     entry.Summary,
			PluginEntry: &entry,
		}
	}
	for _, st := range snap.Themes {
		pack := st.Pack
		ver := themePackVersion(pack)
		next[string(st.Entry.ID)] = ManagedPackage{
			ID:          string(st.Entry.ID),
			Kind:        PackageKindTheme,
			Name:        st.Entry.Name,
			Category:    st.Entry.Appearance + "-theme",
			Version:     ver,
			TrustTier:   st.Entry.TrustTier,
			PublisherID: st.Entry.PublisherID,
			Summary:     st.Entry.Summary,
			ThemePack:   &pack,
		}
	}
	for _, sl := range snap.Layouts {
		spec := sl.Spec
		ver := layoutPackVersion(spec)
		next[string(sl.Entry.ID)] = ManagedPackage{
			ID:          string(sl.Entry.ID),
			Kind:        PackageKindLayout,
			Name:        sl.Entry.Name,
			Category:    sl.Entry.Perspective,
			Version:     ver,
			TrustTier:   sl.Entry.TrustTier,
			PublisherID: sl.Entry.PublisherID,
			Summary:     sl.Entry.Summary,
			LayoutSpec:  &spec,
		}
	}
	ps.catalog = next
}

func (ps *PackageStore) recomputeStatusesLocked() {
	for id, pkg := range ps.catalog {
		if instVer, ok := ps.installed[id]; ok {
			pkg.InstalledVersion = instVer
			if instVer != pkg.Version {
				pkg.Status = PackageStatusUpdateAvailable
			} else {
				pkg.Status = PackageStatusInstalled
			}
		} else {
			pkg.InstalledVersion = ""
			pkg.Status = PackageStatusAvailable
		}
		pkg.Active = (pkg.Kind == PackageKindTheme && id == ps.activeTheme) ||
			(pkg.Kind == PackageKindLayout && id == ps.activeLayout)
		ps.catalog[id] = pkg
	}
}

func (ps *PackageStore) buildSnapshotLocked() PackageStoreSnapshot {
	list := make([]ManagedPackage, 0, len(ps.catalog))
	for _, p := range ps.catalog {
		list = append(list, p)
	}
	kindRank := func(k PackageKind) int {
		switch k {
		case PackageKindTheme:
			return 0
		case PackageKindPlugin:
			return 1
		case PackageKindLayout:
			return 2
		default:
			return 3
		}
	}
	sort.Slice(list, func(i, j int) bool {
		ri, rj := kindRank(list[i].Kind), kindRank(list[j].Kind)
		if ri != rj {
			return ri < rj
		}
		return list[i].Name < list[j].Name
	})
	return PackageStoreSnapshot{
		Source:     ps.source,
		SyncedAt:   ps.syncedAt,
		Refreshing: ps.refreshing,
		LastError:  ps.lastError,
		Packages:   list,
	}
}

func (ps *PackageStore) publishSnapshotLocked() {
	if ps.snap == nil {
		return
	}
	next := ps.buildSnapshotLocked()
	ps.snap.Commit(func(_ PackageStoreSnapshot) PackageStoreSnapshot { return next })
}

func (ps *PackageStore) loadStateLocked() {
	if ps.stateFile == "" {
		return
	}
	raw, err := os.ReadFile(ps.stateFile)
	if err != nil || len(raw) == 0 || len(raw) > 512*1024 {
		return
	}
	var st persistedPackageState
	if err := json.Unmarshal(raw, &st); err != nil {
		return
	}
	for k, v := range st.Installed {
		ps.installed[k] = v
	}
	ps.activeTheme = st.ActiveTheme
	ps.activeLayout = st.ActiveLayout
}

func (ps *PackageStore) saveStateLocked() error {
	if ps.stateFile == "" {
		return nil
	}
	st := persistedPackageState{
		SchemaVersion: 1,
		Installed:     ps.installed,
		ActiveTheme:   ps.activeTheme,
		ActiveLayout:  ps.activeLayout,
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicBytes(ps.stateFile, append(raw, '\n'))
}

func materializeThemePack(uiDir string, pack ThemePack) error {
	slug := ThemePackSlug(string(pack.ID))
	if slug == "" {
		return fmt.Errorf("%w: cannot derive theme slug for %q", ErrThemePackInvalid, pack.ID)
	}
	packDir := filepath.Join(uiDir, "themes", slug)
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		return err
	}
	variant := "dark"
	if pack.Appearance == "light" {
		variant = "light"
	}
	meta := fmt.Sprintf("id = %q\nname = %q\nvariant = %q\nbase = %q\nmarketplace_id = %q\n",
		slug, pack.Name, variant, "nord-bento", string(pack.ID))
	if err := writeAtomicBytes(filepath.Join(packDir, "theme.toml"), []byte(meta)); err != nil {
		return err
	}
	css := RenderThemePackCSS(pack)
	return writeAtomicBytes(filepath.Join(packDir, "style.css"), []byte(css))
}

func writeAtomicBytes(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func shortRev(commitSHA, fallbackA, fallbackB string) string {
	s := strings.TrimSpace(commitSHA)
	if len(s) >= 7 {
		return "rev:" + s[:7]
	}
	h := sha256.Sum256([]byte(fallbackA + "|" + fallbackB))
	return "sha:" + hex.EncodeToString(h[:4])
}

func themePackVersion(pack ThemePack) string {
	h := sha256.Sum256([]byte(RenderThemePackCSS(pack)))
	return "sha:" + hex.EncodeToString(h[:4])
}

func layoutPackVersion(spec shell.LayoutSpec) string {
	raw, _ := json.Marshal(spec)
	h := sha256.Sum256(raw)
	return "sha:" + hex.EncodeToString(h[:4])
}

func rgbHex(c schema.RGB) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

func blendRGB(a, b schema.RGB, t float64) schema.RGB {
	mix := func(x, y uint8) uint8 {
		v := float64(x)*(1-t) + float64(y)*t
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v + 0.5)
	}
	return schema.RGB{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B)}
}

func defaultOfficialPackages() []ManagedPackage {
	mkTheme := func(id, name, app, summary string, pal schema.UITheme) ManagedPackage {
		tp := ThemePack{
			SchemaVersion: 1,
			ID:            schema.PluginID(id),
			Name:          name,
			Appearance:    app,
			Palette:       pal,
		}
		return ManagedPackage{
			ID:          id,
			Kind:        PackageKindTheme,
			Name:        name,
			Category:    app + "-theme",
			Version:     themePackVersion(tp),
			TrustTier:   TrustTierOfficial,
			PublisherID: "tao3k.official",
			Summary:     summary,
			ThemePack:   &tp,
		}
	}
	mkPlugin := func(id, name, cat string, rt schema.PanelRuntimeKind, slot schema.SlotID, summary string) ManagedPackage {
		pe := RegistryPluginEntry{
			ID:          schema.PluginID(id),
			Name:        name,
			Category:    cat,
			Runtime:     rt,
			SlotID:      slot,
			TrustTier:   TrustTierOfficial,
			PublisherID: "tao3k.official",
			Summary:     summary,
		}
		return ManagedPackage{
			ID:          id,
			Kind:        PackageKindPlugin,
			Name:        name,
			Category:    cat,
			Version:     shortRev("", id, summary),
			TrustTier:   TrustTierOfficial,
			PublisherID: "tao3k.official",
			Summary:     summary,
			PluginEntry: &pe,
		}
	}
	mkLayout := func(id, name, persp, summary string) ManagedPackage {
		spec := shell.LayoutSpec{Perspective: persp}
		return ManagedPackage{
			ID:          id,
			Kind:        PackageKindLayout,
			Name:        name,
			Category:    persp,
			Version:     shortRev("", id, persp),
			TrustTier:   TrustTierOfficial,
			PublisherID: "tao3k.official",
			Summary:     summary,
			LayoutSpec:  &spec,
		}
	}

	return []ManagedPackage{
		mkTheme("tao3k.theme-catppuccin", "Catppuccin Mocha", "dark",
			"Warm Catppuccin Mocha pastel semantic theme across GPU chrome and WebView islands.",
			schema.UITheme{
				Canvas: schema.RGB{R: 30, G: 30, B: 46}, Bar: schema.RGB{R: 24, G: 24, B: 37},
				Ink: schema.RGB{R: 205, G: 214, B: 244}, Accent: schema.RGB{R: 203, G: 166, B: 247},
				Info: schema.RGB{R: 137, G: 180, B: 250}, Success: schema.RGB{R: 166, G: 227, B: 161},
				Warning: schema.RGB{R: 249, G: 226, B: 175}, Muted: schema.RGB{R: 127, G: 132, B: 156},
			}),
		mkTheme("tao3k.theme-nord-polar", "Nord Polar", "dark",
			"Arctic Nord Polar Night 8-color palette with crisp frost accents.",
			schema.UITheme{
				Canvas: schema.RGB{R: 46, G: 52, B: 64}, Bar: schema.RGB{R: 59, G: 66, B: 82},
				Ink: schema.RGB{R: 236, G: 239, B: 244}, Accent: schema.RGB{R: 136, G: 192, B: 208},
				Info: schema.RGB{R: 129, G: 161, B: 193}, Success: schema.RGB{R: 163, G: 190, B: 140},
				Warning: schema.RGB{R: 235, G: 203, B: 139}, Muted: schema.RGB{R: 123, G: 136, B: 161},
			}),
		mkTheme("tao3k.theme-paper-light", "Paper Light", "light",
			"Crisp Paper-Snow & Cobalt light theme inspired by Apple HIG surfaces.",
			schema.UITheme{
				Canvas: schema.RGB{R: 248, G: 249, B: 252}, Bar: schema.RGB{R: 238, G: 241, B: 247},
				Ink: schema.RGB{R: 24, G: 30, B: 46}, Accent: schema.RGB{R: 37, G: 99, B: 235},
				Info: schema.RGB{R: 2, G: 132, B: 199}, Success: schema.RGB{R: 22, G: 163, B: 74},
				Warning: schema.RGB{R: 217, G: 119, B: 6}, Muted: schema.RGB{R: 100, G: 116, B: 139},
			}),
		mkTheme("tao3k.theme-tokyo-night", "Tokyo Night", "dark",
			"Deep indigo Tokyo Night Storm palette tuned for long coding sessions.",
			schema.UITheme{
				Canvas: schema.RGB{R: 26, G: 27, B: 38}, Bar: schema.RGB{R: 36, G: 40, B: 59},
				Ink: schema.RGB{R: 192, G: 202, B: 245}, Accent: schema.RGB{R: 187, G: 154, B: 247},
				Info: schema.RGB{R: 122, G: 162, B: 247}, Success: schema.RGB{R: 158, G: 206, B: 106},
				Warning: schema.RGB{R: 224, G: 175, B: 104}, Muted: schema.RGB{R: 86, G: 95, B: 137},
			}),
		mkPlugin("tao3k.git-workbench", "Git Workbench", "developer-tools", schema.PanelRuntimeKind("webview-untrusted"), "workbench.main",
			"React 19 Git status, branch switching, hunk triage, and policy-gated commit workbench."),
		mkPlugin("tao3k.github-workbench", "GitHub Workbench", "developer-tools", schema.PanelRuntimeKind("webview-untrusted"), "workbench.main",
			"React 19 GitHub Pull Requests, Issues, and Actions CI status workbench."),
		mkPlugin("tao3k.ai-workflow-cockpit", "Multi-Agent Workflow Cockpit", "ai-workflows", schema.PanelRuntimeKind("webview-untrusted"), "workbench.main",
			"Multi-agent graph execution cockpit with live token metrics and Tier-0 gate badges."),
		mkPlugin("tao3k.org-agenda-planner", "Org Agenda & Daily Planner", "productivity", schema.PanelRuntimeKind("webview-untrusted"), "workbench.main",
			"Org-mode daily planner, time-block overlap detection, and orgize-wasm capture."),
		mkPlugin("tao3k.focus-clock-reactor", "Focus Clock GPU Dock", "system-dock", schema.PanelRuntimeKind("wasm-ui-ir"), "workbench.inspector",
			"Zero-WebView Go WASI reactor rendering a 120Hz native GPU Pomodoro & Org clock dock."),
		mkLayout("tao3k.layout-cockpit", "AI Engineering Cockpit", "cockpit",
			"3-pane Emacs layout combining Workspace Panel, AI Transcript, and WASI Focus Clock."),
		mkLayout("tao3k.layout-focus", "Deep Focus", "focus",
			"Clean 2-pane workspace pairing primary plugin with resident GPU dock."),
		mkLayout("tao3k.layout-dev-git-github", "Git + GitHub Studio", "dev-git-github",
			"4-pane composable studio pairing Git Workbench, GitHub Workbench, AI Transcript, and Focus Clock."),
	}
}
