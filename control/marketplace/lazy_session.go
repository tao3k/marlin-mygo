// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

// LazySessionConfig configures a persistent, Emacs/lazy.nvim-inspired desktop
// Marketplace & LazyLoader session bound to a signed registry, CAS cache,
// marlin-plugins.toml lockfile, and O(1) LRU/idle reclamation loop.
type LazySessionConfig struct {
	Registry      *RegistryClient
	Trust         *schema.ReleaseTrust
	Host          *pluginhost.Host
	Target        schema.TargetKind
	ProfilePath   string // optional marlin-plugins.toml path persisted atomically on change
	MaxActiveLazy int    // O(1) LRU working-set cap (0 = unbounded)
	Now           func() time.Time
}

// LazySession coordinates signed catalog discovery, SHA-256 lockfile persistence
// across restarts, zero-RAM dormant startup, on-demand hook/command/slot waking,
// 1-click rollback, and O(1) idle memory reclamation.
type LazySession struct {
	cfg      LazySessionConfig
	manager  *Manager
	loader   *LazyLoader
	eventSeq atomic.Uint64
	mu       sync.Mutex
	curRaw   map[schema.PluginID][]byte
	prevRaw  map[schema.PluginID][]byte
	prevSpec map[schema.PluginID]PluginSpec
}

// NewLazySession initializes Manager + LazyLoader, reads ProfilePath (if set),
// and reconciles it immediately so lazy plugins start DORMANT at 0MB RAM.
func NewLazySession(ctx context.Context, cfg LazySessionConfig) (*LazySession, error) {
	if cfg.Registry == nil || cfg.Trust == nil || cfg.Host == nil {
		return nil, errors.New("marketplace: LazySession requires Registry, Trust, and Host")
	}
	if cfg.Target == "" {
		cfg.Target = schema.TargetDesktopMacOS
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	s := &LazySession{
		cfg:      cfg,
		manager:  NewManager(cfg.Trust),
		curRaw:   make(map[schema.PluginID][]byte),
		prevRaw:  make(map[schema.PluginID][]byte),
		prevSpec: make(map[schema.PluginID]PluginSpec),
	}

	loader, err := NewLazyLoader(LazyLoaderConfig{
		Manager:       s.manager,
		Host:          cfg.Host,
		Target:        cfg.Target,
		MaxActiveLazy: cfg.MaxActiveLazy,
		Now:           cfg.Now,
		Fetch:         s.fetchBundleForSpec,
	})
	if err != nil {
		return nil, err
	}
	s.loader = loader

	if cfg.ProfilePath != "" {
		prof, err := LoadProfileFile(cfg.ProfilePath)
		if err != nil {
			return nil, fmt.Errorf("load plugins profile %s: %w", cfg.ProfilePath, err)
		}
		if len(prof.Plugins) > 0 {
			if err := s.loader.Reconcile(ctx, *prof); err != nil {
				return nil, fmt.Errorf("reconcile plugins profile %s: %w", cfg.ProfilePath, err)
			}
		}
	}
	return s, nil
}

// Loader returns the underlying LazyLoader for direct inspection or testing.
func (s *LazySession) Loader() *LazyLoader { return s.loader }

// RefreshCatalog fetches and verifies the signed Marketplace catalog.
func (s *LazySession) RefreshCatalog(ctx context.Context) (*CatalogIndex, error) {
	return s.cfg.Registry.RefreshCatalog(ctx)
}

// CachedCatalog returns the last verified CatalogIndex in memory.
func (s *LazySession) CachedCatalog() *CatalogIndex {
	return s.cfg.Registry.CachedCatalog()
}

// InstallOrUpgrade downloads a signed .marlinpkg, verifies its manifest and
// Ed25519 signature, derives its lazy activation policy & SHA-256 lockfile pin,
// wakes it immediately, and atomically persists marlin-plugins.toml.
func (s *LazySession) InstallOrUpgrade(ctx context.Context, id schema.PluginID) (*InstalledPanel, error) {
	entry, ok := s.cfg.Registry.LookupEntry(id)
	if !ok {
		if _, err := s.cfg.Registry.RefreshCatalog(ctx); err != nil {
			return nil, err
		}
		entry, ok = s.cfg.Registry.LookupEntry(id)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrPackageNotFound, id)
		}
	}
	raw, err := s.cfg.Registry.FetchPackage(ctx, entry)
	if err != nil {
		return nil, err
	}
	verified, err := VerifyBundle(raw, s.cfg.Trust)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(raw)
	pinHex := hex.EncodeToString(sum[:])

	spec, err := specFromVerifiedBundle(verified, entry, pinHex)
	if err != nil {
		return nil, err
	}

	// Preserve existing hooks/triggers/rollback history if upgrading in place.
	s.mu.Lock()
	var prevSpecCopy *PluginSpec
	for _, st := range s.loader.States() {
		if st.Spec.PluginID == id {
			cp := st.Spec
			prevSpecCopy = &cp
			if len(spec.Hooks) == 0 && len(cp.Hooks) > 0 {
				spec.Hooks = slices.Clone(cp.Hooks)
			}
			if len(spec.Triggers) == 0 && len(cp.Triggers) > 0 {
				spec.Triggers = slices.Clone(cp.Triggers)
			}
			break
		}
	}
	if oldRaw, ok := s.curRaw[id]; ok && len(oldRaw) > 0 {
		s.prevRaw[id] = oldRaw
		if prevSpecCopy != nil {
			s.prevSpec[id] = *prevSpecCopy
		}
	}
	s.curRaw[id] = raw
	s.mu.Unlock()

	panel, err := s.loader.UpsertSpec(ctx, spec, true)
	if err != nil {
		return nil, err
	}
	if err := s.persistProfile(); err != nil {
		return panel, err
	}
	return panel, nil
}

// Rollback restores the previous cached signed bundle and lockfile pin for id.
func (s *LazySession) Rollback(ctx context.Context, id schema.PluginID) (*InstalledPanel, error) {
	s.mu.Lock()
	prevBytes, okRaw := s.prevRaw[id]
	prevSp, okSpec := s.prevSpec[id]
	if okRaw && okSpec {
		s.curRaw[id] = prevBytes
	}
	s.mu.Unlock()
	if !okRaw || !okSpec || len(prevBytes) == 0 {
		return nil, fmt.Errorf("marketplace: no previous version cached for %s", id)
	}
	prevSp.Enabled = true
	panel, err := s.loader.UpsertSpec(ctx, prevSp, true)
	if err != nil {
		return nil, err
	}
	if err := s.persistProfile(); err != nil {
		return panel, err
	}
	return panel, nil
}

// HasRollback reports whether a previous bundle is cached for 1-click rollback.
func (s *LazySession) HasRollback(id schema.PluginID) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, ok := s.prevSpec[id]
	if !ok || len(s.prevRaw[id]) == 0 {
		return "", false
	}
	return sp.Version, true
}

// Wake activates a dormant profile-managed plugin on demand.
func (s *LazySession) Wake(ctx context.Context, id schema.PluginID) (*InstalledPanel, error) {
	return s.loader.Wake(ctx, id)
}

// Sleep unloads an active plugin back to DORMANT (0MB RAM) while keeping its
// lazy triggers, hooks, and lockfile pin armed.
func (s *LazySession) Sleep(id schema.PluginID) error {
	return s.loader.Sleep(id)
}

// Disable deactivates a plugin and persists enabled = false into marlin-plugins.toml.
func (s *LazySession) Disable(ctx context.Context, id schema.PluginID) error {
	var found *PluginSpec
	for _, st := range s.loader.States() {
		if st.Spec.PluginID == id {
			cp := st.Spec
			found = &cp
			break
		}
	}
	if found == nil {
		return s.cfg.Host.Deactivate(id)
	}
	found.Enabled = false
	if _, err := s.loader.UpsertSpec(ctx, *found, false); err != nil {
		return err
	}
	return s.persistProfile()
}

// TriggerHook wakes any dormant plugins registered for an Emacs-style lifecycle hook
// (e.g. "after-turn-complete", "before-gate-open", "on-session-switch").
func (s *LazySession) TriggerHook(ctx context.Context, hookName string) ([]schema.PluginID, error) {
	return s.loader.EnsureForHook(ctx, hookName)
}

// TriggerCommand wakes any dormant plugins registered for a command trigger.
func (s *LazySession) TriggerCommand(ctx context.Context, commandName string) ([]schema.PluginID, error) {
	return s.loader.EnsureForCommand(ctx, commandName)
}

// TriggerSlot wakes any dormant plugins bound to slotID.
func (s *LazySession) TriggerSlot(ctx context.Context, slotID schema.SlotID) ([]schema.PluginID, error) {
	return s.loader.EnsureForSlot(ctx, slotID)
}

// PublishProjection wakes any dormant on-topic plugins subscribed to topic
// (O(1) zero-alloc fast path when already warm) and delivers payload strictly
// to active plugins authorized by their manifest read_scopes.
func (s *LazySession) PublishProjection(ctx context.Context, topic string, payload any) (woken []schema.PluginID, delivered []schema.PluginID, err error) {
	woken, err = s.loader.EnsureForTopic(ctx, topic)
	if err != nil {
		return woken, nil, err
	}
	var raw json.RawMessage
	switch v := payload.(type) {
	case json.RawMessage:
		raw = v
	case []byte:
		raw = json.RawMessage(v)
	default:
		b, mErr := json.Marshal(v)
		if mErr != nil {
			return woken, nil, mErr
		}
		raw = b
	}
	evID := fmt.Sprintf("proj-%s-%d", topic, s.eventSeq.Add(1))
	delivered, errs := s.cfg.Host.PublishTopic(evID, topic, raw)
	for _, id := range delivered {
		s.loader.Touch(id)
	}
	if len(errs) > 0 {
		for id, dErr := range errs {
			return woken, delivered, fmt.Errorf("deliver topic %q to %s: %w", topic, id, dErr)
		}
	}
	return woken, delivered, nil
}

// ReapIdle executes an O(1) idle reclamation check against now.
func (s *LazySession) ReapIdle(now time.Time) []schema.PluginID {
	return s.loader.ReapIdle(now)
}

// StartIdleReaper starts the periodic background O(1) idle reclamation loop.
func (s *LazySession) StartIdleReaper(ctx context.Context, interval time.Duration, onReaped func([]schema.PluginID)) {
	s.loader.StartIdleReaper(ctx, interval, onReaped)
}

// Stats returns live lazy-loader profiler telemetry.
func (s *LazySession) Stats() LazyLoaderStats {
	return s.loader.Stats()
}

// StatusMap returns live per-plugin lazy lifecycle statuses indexed by PluginID.
func (s *LazySession) StatusMap() map[schema.PluginID]LazyPluginStatus {
	states := s.loader.States()
	out := make(map[schema.PluginID]LazyPluginStatus, len(states))
	for _, st := range states {
		out[st.Spec.PluginID] = st
	}
	return out
}

// PersistHooks attaches Emacs-style hooks to an already-installed plugin spec
// and saves marlin-plugins.toml atomically.
func (s *LazySession) PersistHooks(ctx context.Context, id schema.PluginID, hooks []string) error {
	for _, st := range s.loader.States() {
		if st.Spec.PluginID == id {
			sp := st.Spec
			sp.Hooks = slices.Clone(hooks)
			if _, err := s.loader.UpsertSpec(ctx, sp, false); err != nil {
				return err
			}
			return s.persistProfile()
		}
	}
	return fmt.Errorf("%w: %s", ErrPluginNotInIndex, id)
}

func (s *LazySession) persistProfile() error {
	if s.cfg.ProfilePath == "" {
		return nil
	}
	return SaveProfileFile(s.cfg.ProfilePath, s.loader.Profile())
}

func (s *LazySession) fetchBundleForSpec(ctx context.Context, spec PluginSpec) ([]byte, error) {
	// 1. In-memory active/rollback bundle cache (fast zero-I/O re-wake).
	s.mu.Lock()
	if raw, ok := s.curRaw[spec.PluginID]; ok && len(raw) > 0 {
		if spec.PinnedSHA256 == "" || sha256Hex(raw) == spec.PinnedSHA256 {
			s.mu.Unlock()
			return raw, nil
		}
	}
	s.mu.Unlock()

	// 2. Offline SHA-256 Content-Addressed Store hit (zero-network restart wake).
	if spec.PinnedSHA256 != "" {
		if raw, ok := s.cfg.Registry.readCASBlob(spec.PinnedSHA256); ok {
			s.mu.Lock()
			s.curRaw[spec.PluginID] = raw
			s.mu.Unlock()
			return raw, nil
		}
	}

	// 3. Registry lookup + verified HTTP/local fetch.
	entry, ok := s.cfg.Registry.LookupEntry(spec.PluginID)
	if !ok {
		if _, err := s.cfg.Registry.RefreshCatalog(ctx); err != nil {
			return nil, err
		}
		entry, ok = s.cfg.Registry.LookupEntry(spec.PluginID)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrPackageNotFound, spec.PluginID)
		}
	}
	raw, err := s.cfg.Registry.FetchPackage(ctx, entry)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.curRaw[spec.PluginID] = raw
	s.mu.Unlock()
	return raw, nil
}

func specFromVerifiedBundle(verified *VerifiedBundle, entry CatalogEntry, pinHex string) (PluginSpec, error) {
	m, err := schema.ParsePluginManifestTOML(verified.Verified.ManifestTOML)
	if err != nil {
		return PluginSpec{}, err
	}
	spec := PluginSpec{
		PluginID:      m.ID,
		Version:       m.Version,
		PublisherID:   entry.PublisherID,
		PinnedSHA256:  pinHex,
		Enabled:       true,
		Activation:    schema.ActivationOnSlot,
		SlotID:        entry.SlotID,
		IdleUnloadSec: 300,
	}
	var cids []schema.ContributionID
	for cid := range m.Contributions {
		cids = append(cids, cid)
	}
	slices.Sort(cids)
	for _, cid := range cids {
		if p := m.Contributions[cid].Panel; p != nil {
			if p.SlotID != "" {
				spec.SlotID = p.SlotID
			}
			if p.Activation != "" {
				spec.Activation = p.Activation
			}
			if len(p.Triggers) > 0 {
				spec.Triggers = slices.Clone(p.Triggers)
			}
			if p.IdleUnloadSec > 0 {
				spec.IdleUnloadSec = p.IdleUnloadSec
			}
			break
		}
	}
	return spec, nil
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ParsePublisherPubKeyFile decodes a hex-encoded 32-byte Ed25519 public key file.
func ParsePublisherPubKeyFile(rawHex []byte) (ed25519.PublicKey, error) {
	pubBytes, err := hex.DecodeString(string(trimSpaceBytes(rawHex)))
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return nil, errors.New("marketplace: invalid hex Ed25519 public key")
	}
	return ed25519.PublicKey(pubBytes), nil
}

func trimSpaceBytes(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\t' || b[start] == '\n' || b[start] == '\r') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\n' || b[end-1] == '\r') {
		end--
	}
	return b[start:end]
}
