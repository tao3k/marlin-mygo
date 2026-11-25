// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

var (
	ErrProfileInvalid   = errors.New("marketplace: invalid declarative plugin profile")
	ErrPinMismatch      = errors.New("marketplace: bundle SHA-256 does not match lockfile pinned_sha256")
	ErrPluginNotInIndex = errors.New("marketplace: plugin is not declared in lazy profile")
)

// LazyState describes the lifecycle state of one declaratively managed plugin.
type LazyState string

const (
	LazyStateDormant  LazyState = "DORMANT"
	LazyStateActive   LazyState = "ACTIVE"
	LazyStateDisabled LazyState = "DISABLED"
)

// PluginSpec mirrors an Emacs `use-package` / `elpaca` declaration:
// reproducible version + SHA-256 pin, lazy activation policy, Emacs-style
// lifecycle hooks & triggers, bounded working-set idle reclamation, and config.
type PluginSpec struct {
	PluginID      schema.PluginID         `json:"pluginId"`
	Version       string                  `json:"version,omitempty"`
	PublisherID   schema.PublisherID      `json:"publisherId,omitempty"`
	PinnedSHA256  string                  `json:"pinnedSha256,omitempty"`
	Enabled       bool                    `json:"enabled"`
	PinnedActive  bool                    `json:"pinnedActive,omitempty"` // never LRU-evicted while active
	Activation    schema.ActivationPolicy `json:"activation"`             // defaults to on-slot
	SlotID        schema.SlotID           `json:"slotId,omitempty"`
	Triggers      []string                `json:"triggers,omitempty"`
	Hooks         []string                `json:"hooks,omitempty"` // e.g. "after-turn-complete", "before-gate-open", "on-session-switch"
	IdleUnloadSec uint32                  `json:"idleUnloadSec,omitempty"`
	Config        map[string]string       `json:"config,omitempty"`
}

// PluginProfile is a reproducible, declarative workspace plugin lockfile
// (marlin-plugins.toml) reconciled transactionally by LazyLoader.
type PluginProfile struct {
	SchemaVersion int          `json:"schemaVersion"`
	Plugins       []PluginSpec `json:"plugins"`
}

// LazyPluginStatus captures live telemetry for one plugin managed by LazyLoader.
type LazyPluginStatus struct {
	Spec             PluginSpec
	State            LazyState
	ActiveGeneration uint64
	WakeCount        uint64
	EvictCount       uint64
	LastWakeDuration time.Duration
	LastUsed         time.Time
}

// LazyLoaderStats summarizes real-time lazy-loader profiler metrics (inspired by
// lazy.nvim's `:Lazy profile` and Emacs `use-package-compute-statistics`).
type LazyLoaderStats struct {
	TotalDeclared         int
	ActiveCount           int
	DormantCount          int
	DisabledCount         int
	MaxActiveLazy         int
	TotalWakes            uint64
	TotalReaped           uint64
	TotalEvicted          uint64
	LastReconcileDuration time.Duration
}

// PackageFetcher supplies signed .marlinpkg bytes for one declared PluginSpec.
type PackageFetcher func(ctx context.Context, spec PluginSpec) ([]byte, error)

// LazyLoaderConfig configures an Emacs-inspired declarative, lazy plugin loader
// with lock-free COW trigger/hook dispatch and an O(1) bounded LRU working set.
type LazyLoaderConfig struct {
	Manager *Manager
	Host    *pluginhost.Host
	Target  schema.TargetKind
	Fetch   PackageFetcher
	// MaxActiveLazy bounds simultaneously active non-eager/non-pinned plugins
	// using an O(1) intrusive LRU eviction list (0 = unbounded).
	MaxActiveLazy int
	Now           func() time.Time
}

type lazyEntry struct {
	spec        PluginSpec
	state       LazyState
	activeFlag  atomic.Bool // lock-free fast-path probe for hot-path dispatch
	activeGen   uint64
	wakeCount   uint64
	evictCount  uint64
	lastWakeDur time.Duration
	lastUsed    time.Time

	// Intrusive doubly-linked LRU pointers (only linked while active & evictable).
	inLRU   bool
	lruPrev *lazyEntry // toward MRU head
	lruNext *lazyEntry // toward LRU tail
}

type triggerTable struct {
	byCommand map[string][]*lazyEntry
	byTopic   map[string][]*lazyEntry
	bySlot    map[schema.SlotID][]*lazyEntry
	byHook    map[string][]*lazyEntry
}

type inflightWake struct {
	done chan struct{}
	res  *InstalledPanel
	err  error
}

// LazyLoader keeps 100+ installed plugins dormant at zero RAM/startup cost,
// dispatches command/topic/slot/hook triggers via lock-free Copy-on-Write
// snapshots (0 allocs on steady-state hot path), wakes plugins on demand with
// singleflight deduplication and SHA-256 lockfile verification, and reclaims
// memory in O(1) via an intrusive LRU working set + oldest-first idle reaping.
type LazyLoader struct {
	cfg    LazyLoaderConfig
	genSeq atomic.Uint64

	// Immutable COW trigger/hook table for lock-free, 0-alloc hot-path lookup.
	triggers atomic.Pointer[triggerTable]

	mu       sync.Mutex
	entries  map[schema.PluginID]*lazyEntry
	inflight map[schema.PluginID]*inflightWake

	// Intrusive O(1) LRU list of active evictable (non-eager, non-pinned) plugins.
	lruHead  *lazyEntry // most recently used
	lruTail  *lazyEntry // least recently used (eviction & idle-reap candidate)
	lruCount int
	// Earliest idle expiry hint (unix nano) for O(1) zero-alloc ReapIdle fast-path.
	minIdleUnloadSec atomic.Uint32

	// Live profiler counters (updated only on cold Wake/Reap/Evict/Reconcile).
	totalWakes       uint64
	totalReaped      uint64
	totalEvicted     uint64
	lastReconcileDur time.Duration
}

func NewLazyLoader(cfg LazyLoaderConfig) (*LazyLoader, error) {
	if cfg.Manager == nil || cfg.Host == nil || cfg.Fetch == nil {
		return nil, errors.New("marketplace: LazyLoader requires Manager, Host, and Fetch")
	}
	if cfg.Target == "" {
		cfg.Target = schema.TargetDesktopMacOS
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	l := &LazyLoader{
		cfg:      cfg,
		entries:  make(map[schema.PluginID]*lazyEntry),
		inflight: make(map[schema.PluginID]*inflightWake),
	}
	l.triggers.Store(&triggerTable{})
	return l, nil
}

// Reconcile converges the live host toward profile:
//   - Disabled or removed plugins are cleanly deactivated;
//   - Lazy plugins (on-command / on-slot / on-topic / manual) remain DORMANT
//     with zero runtime instantiation cost;
//   - Eager plugins activate immediately;
//   - Trigger & hook lookup tables are atomically swapped as an immutable COW snapshot.
func (l *LazyLoader) Reconcile(ctx context.Context, profile PluginProfile) error {
	reconcileStart := time.Now()
	desired := make(map[schema.PluginID]PluginSpec, len(profile.Plugins))
	for _, sp := range profile.Plugins {
		if _, err := schema.ParsePluginID(string(sp.PluginID)); err != nil {
			return fmt.Errorf("%w: %v", ErrProfileInvalid, err)
		}
		if sp.Activation == "" {
			sp.Activation = schema.ActivationOnSlot
		}
		if !schema.ValidActivationPolicy(sp.Activation) {
			return fmt.Errorf("%w: invalid activation %q for %s", ErrProfileInvalid, sp.Activation, sp.PluginID)
		}
		desired[sp.PluginID] = sp
	}

	var toDeactivate []schema.PluginID
	var eagerWake []schema.PluginID

	l.mu.Lock()
	for id, ent := range l.entries {
		sp, stillPresent := desired[id]
		if !stillPresent || !sp.Enabled {
			if ent.state == LazyStateActive {
				l.lruRemoveLocked(ent)
				ent.activeFlag.Store(false)
				toDeactivate = append(toDeactivate, id)
			}
			if !stillPresent {
				delete(l.entries, id)
			}
		}
	}

	tbl := &triggerTable{
		byCommand: make(map[string][]*lazyEntry),
		byTopic:   make(map[string][]*lazyEntry),
		bySlot:    make(map[schema.SlotID][]*lazyEntry),
		byHook:    make(map[string][]*lazyEntry),
	}
	var minIdle uint32

	for _, sp := range profile.Plugins {
		ent, exists := l.entries[sp.PluginID]
		if !exists {
			ent = &lazyEntry{spec: sp, state: LazyStateDormant}
			l.entries[sp.PluginID] = ent
		} else {
			ent.spec = sp
		}
		if !sp.Enabled {
			ent.state = LazyStateDisabled
			ent.activeGen = 0
			ent.activeFlag.Store(false)
			l.lruRemoveLocked(ent)
			continue
		}
		if ent.state == LazyStateDisabled {
			ent.state = LazyStateDormant
		}
		if sp.IdleUnloadSec > 0 && (minIdle == 0 || sp.IdleUnloadSec < minIdle) {
			minIdle = sp.IdleUnloadSec
		}
		indexTriggersInto(tbl, ent)
		if sp.Activation == schema.ActivationEager && ent.state != LazyStateActive {
			eagerWake = append(eagerWake, sp.PluginID)
		}
	}
	l.triggers.Store(tbl)
	l.minIdleUnloadSec.Store(minIdle)
	l.mu.Unlock()

	for _, id := range toDeactivate {
		_ = l.cfg.Host.Deactivate(id)
	}
	for _, id := range eagerWake {
		if _, err := l.Wake(ctx, id); err != nil {
			return err
		}
	}
	l.mu.Lock()
	l.lastReconcileDur = time.Since(reconcileStart)
	l.mu.Unlock()
	return nil
}

// UpsertSpec inserts or updates one plugin specification in the live lazy index,
// atomically publishes a fresh COW trigger table, and optionally wakes/reloads it.
func (l *LazyLoader) UpsertSpec(ctx context.Context, sp PluginSpec, wakeNow bool) (*InstalledPanel, error) {
	if _, err := schema.ParsePluginID(string(sp.PluginID)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProfileInvalid, err)
	}
	if sp.Activation == "" {
		sp.Activation = schema.ActivationOnSlot
	}
	if !schema.ValidActivationPolicy(sp.Activation) {
		return nil, fmt.Errorf("%w: invalid activation %q for %s", ErrProfileInvalid, sp.Activation, sp.PluginID)
	}

	var needDeactivate bool
	l.mu.Lock()
	ent, exists := l.entries[sp.PluginID]
	if !exists {
		ent = &lazyEntry{spec: sp, state: LazyStateDormant}
		l.entries[sp.PluginID] = ent
	} else {
		ent.spec = sp
	}
	if !sp.Enabled {
		if ent.state == LazyStateActive {
			l.lruRemoveLocked(ent)
			ent.activeFlag.Store(false)
			needDeactivate = true
		}
		ent.state = LazyStateDisabled
		ent.activeGen = 0
	} else {
		if ent.state == LazyStateDisabled {
			ent.state = LazyStateDormant
		}
		if wakeNow && ent.state == LazyStateActive {
			// Force reload with the updated version/pin on Wake below.
			l.lruRemoveLocked(ent)
			ent.state = LazyStateDormant
			ent.activeFlag.Store(false)
		}
	}
	l.rebuildTriggersLocked()
	l.mu.Unlock()

	if needDeactivate {
		if err := l.cfg.Host.Deactivate(sp.PluginID); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if sp.Enabled && (wakeNow || sp.Activation == schema.ActivationEager) {
		return l.Wake(ctx, sp.PluginID)
	}
	return nil, nil
}

func (l *LazyLoader) rebuildTriggersLocked() {
	tbl := &triggerTable{
		byCommand: make(map[string][]*lazyEntry),
		byTopic:   make(map[string][]*lazyEntry),
		bySlot:    make(map[schema.SlotID][]*lazyEntry),
		byHook:    make(map[string][]*lazyEntry),
	}
	var minIdle uint32
	for _, ent := range l.entries {
		if !ent.spec.Enabled {
			continue
		}
		if ent.spec.IdleUnloadSec > 0 && (minIdle == 0 || ent.spec.IdleUnloadSec < minIdle) {
			minIdle = ent.spec.IdleUnloadSec
		}
		indexTriggersInto(tbl, ent)
	}
	l.triggers.Store(tbl)
	l.minIdleUnloadSec.Store(minIdle)
}

// EnsureForCommand wakes any dormant plugins registered for commandName.
// Fast path when no matching plugins are dormant: lock-free, O(1), 0 allocations.
func (l *LazyLoader) EnsureForCommand(ctx context.Context, commandName string) ([]schema.PluginID, error) {
	tbl := l.triggers.Load()
	return l.ensureEntries(ctx, tbl.byCommand[commandName])
}

// EnsureForTopic wakes any dormant plugins registered for topic.
// Fast path when no matching plugins are dormant: lock-free, O(1), 0 allocations.
func (l *LazyLoader) EnsureForTopic(ctx context.Context, topic string) ([]schema.PluginID, error) {
	tbl := l.triggers.Load()
	return l.ensureEntries(ctx, tbl.byTopic[topic])
}

// EnsureForSlot wakes any dormant plugins registered for slotID.
// Fast path when no matching plugins are dormant: lock-free, O(1), 0 allocations.
func (l *LazyLoader) EnsureForSlot(ctx context.Context, slotID schema.SlotID) ([]schema.PluginID, error) {
	tbl := l.triggers.Load()
	return l.ensureEntries(ctx, tbl.bySlot[slotID])
}

// EnsureForHook wakes any dormant plugins subscribed to an Emacs-style lifecycle
// hook (e.g. "after-turn-complete", "before-gate-open", "on-session-switch").
// Fast path when all hooked plugins are already active or none subscribe:
// lock-free O(k) atomic check with 0 heap allocations.
func (l *LazyLoader) EnsureForHook(ctx context.Context, hookName string) ([]schema.PluginID, error) {
	tbl := l.triggers.Load()
	return l.ensureEntries(ctx, tbl.byHook[hookName])
}

func (l *LazyLoader) ensureEntries(ctx context.Context, list []*lazyEntry) ([]schema.PluginID, error) {
	if len(list) == 0 {
		return nil, nil
	}
	// Lock-free fast path: if every matching plugin is already active, return
	// immediately with 0 mutex acquisitions and 0 allocations.
	needsWake := false
	for _, ent := range list {
		if !ent.activeFlag.Load() {
			needsWake = true
			break
		}
	}
	if !needsWake {
		return nil, nil
	}
	var woken []schema.PluginID
	for _, ent := range list {
		if ent.activeFlag.Load() {
			continue
		}
		panel, err := l.Wake(ctx, ent.spec.PluginID)
		if err != nil {
			return woken, err
		}
		if panel != nil {
			woken = append(woken, ent.spec.PluginID)
		}
	}
	return woken, nil
}

// Wake activates one dormant plugin on demand (with singleflight deduplication,
// SHA-256 lockfile pin verification, and O(1) bounded LRU working-set eviction).
func (l *LazyLoader) Wake(ctx context.Context, id schema.PluginID) (*InstalledPanel, error) {
	l.mu.Lock()
	ent, ok := l.entries[id]
	if !ok {
		l.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrPluginNotInIndex, id)
	}
	if !ent.spec.Enabled {
		l.mu.Unlock()
		return nil, fmt.Errorf("marketplace: plugin %s is disabled in profile", id)
	}
	if ent.state == LazyStateActive {
		ent.lastUsed = l.cfg.Now()
		if ent.inLRU {
			l.lruMoveToFrontLocked(ent)
		}
		l.mu.Unlock()
		return nil, nil
	}
	if call, busy := l.inflight[id]; busy {
		l.mu.Unlock()
		select {
		case <-call.done:
			return call.res, call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &inflightWake{done: make(chan struct{})}
	l.inflight[id] = call
	spec := ent.spec
	l.mu.Unlock()

	wakeStart := time.Now()
	panel, err := l.activateSpec(ctx, spec)
	wakeDur := time.Since(wakeStart)

	var evictedIDs []schema.PluginID
	l.mu.Lock()
	delete(l.inflight, id)
	if err == nil && panel != nil {
		if cur, still := l.entries[id]; still {
			cur.state = LazyStateActive
			cur.activeGen = panel.Instance.Scope().ActivationGeneration
			cur.wakeCount++
			cur.lastWakeDur = wakeDur
			cur.lastUsed = l.cfg.Now()
			cur.activeFlag.Store(true)
			l.totalWakes++
			if isEvictable(cur.spec) {
				l.lruPushFrontLocked(cur)
				if l.cfg.MaxActiveLazy > 0 {
					for l.lruCount > l.cfg.MaxActiveLazy && l.lruTail != nil && l.lruTail != cur {
						victim := l.lruTail
						l.lruRemoveLocked(victim)
						victim.state = LazyStateDormant
						victim.activeGen = 0
						victim.evictCount++
						victim.activeFlag.Store(false)
						l.totalEvicted++
						evictedIDs = append(evictedIDs, victim.spec.PluginID)
					}
				}
			}
		}
	}
	call.res = panel
	call.err = err
	close(call.done)
	l.mu.Unlock()

	for _, evID := range evictedIDs {
		_ = l.cfg.Host.Deactivate(evID)
	}
	return panel, err
}

// Touch marks an active plugin as freshly used in O(1) (promoting it to MRU head).
func (l *LazyLoader) Touch(id schema.PluginID) {
	l.mu.Lock()
	if ent, ok := l.entries[id]; ok && ent.state == LazyStateActive {
		ent.lastUsed = l.cfg.Now()
		if ent.inLRU {
			l.lruMoveToFrontLocked(ent)
		}
	}
	l.mu.Unlock()
}

// Sleep explicitly unloads an active plugin back to DORMANT (freeing WebView/WASM
// memory and restoring host.* fallback) while keeping its lazy triggers armed.
func (l *LazyLoader) Sleep(id schema.PluginID) error {
	l.mu.Lock()
	ent, ok := l.entries[id]
	if !ok {
		l.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrPluginNotInIndex, id)
	}
	wasActive := ent.state == LazyStateActive
	l.lruRemoveLocked(ent)
	ent.state = LazyStateDormant
	ent.activeGen = 0
	ent.activeFlag.Store(false)
	l.mu.Unlock()
	if wasActive {
		return l.cfg.Host.Deactivate(id)
	}
	return nil
}

// ReapIdle scans from the O(1) LRU tail (oldest first) and reclaims any active
// evictable plugins idle >= IdleUnloadSec. When no plugin is expired, it checks
// only the LRU tail in O(1) with 0 heap allocations.
func (l *LazyLoader) ReapIdle(now time.Time) []schema.PluginID {
	minSec := l.minIdleUnloadSec.Load()
	if minSec == 0 {
		return nil
	}
	l.mu.Lock()
	if l.lruTail == nil {
		l.mu.Unlock()
		return nil
	}
	// Fast O(1) zero-alloc exit: if the single oldest plugin hasn't reached the
	// smallest configured idle threshold, no newer plugin in the LRU can either.
	if now.Sub(l.lruTail.lastUsed) < time.Duration(minSec)*time.Second {
		l.mu.Unlock()
		return nil
	}
	var toSleep []schema.PluginID
	for cur := l.lruTail; cur != nil; {
		prev := cur.lruPrev
		if cur.spec.IdleUnloadSec > 0 && now.Sub(cur.lastUsed) >= time.Duration(cur.spec.IdleUnloadSec)*time.Second {
			l.lruRemoveLocked(cur)
			cur.state = LazyStateDormant
			cur.activeGen = 0
			cur.evictCount++
			cur.activeFlag.Store(false)
			l.totalReaped++
			toSleep = append(toSleep, cur.spec.PluginID)
		}
		cur = prev
	}
	l.mu.Unlock()
	for _, id := range toSleep {
		_ = l.cfg.Host.Deactivate(id)
	}
	slices.Sort(toSleep)
	return toSleep
}

// States returns a deterministic sorted snapshot of all profile-managed plugins.
func (l *LazyLoader) States() []LazyPluginStatus {
	l.mu.Lock()
	out := make([]LazyPluginStatus, 0, len(l.entries))
	for _, ent := range l.entries {
		out = append(out, LazyPluginStatus{
			Spec:             ent.spec,
			State:            ent.state,
			ActiveGeneration: ent.activeGen,
			WakeCount:        ent.wakeCount,
			EvictCount:       ent.evictCount,
			LastWakeDuration: ent.lastWakeDur,
			LastUsed:         ent.lastUsed,
		})
	}
	l.mu.Unlock()
	slices.SortFunc(out, func(a, b LazyPluginStatus) int {
		return strings.Compare(string(a.Spec.PluginID), string(b.Spec.PluginID))
	})
	return out
}

// Profile returns the current deterministic PluginProfile snapshot suitable for
// atomic persistence to marlin-plugins.toml.
func (l *LazyLoader) Profile() PluginProfile {
	l.mu.Lock()
	specs := make([]PluginSpec, 0, len(l.entries))
	for _, ent := range l.entries {
		specs = append(specs, ent.spec)
	}
	l.mu.Unlock()
	slices.SortFunc(specs, func(a, b PluginSpec) int {
		return strings.Compare(string(a.PluginID), string(b.PluginID))
	})
	return PluginProfile{SchemaVersion: 1, Plugins: specs}
}

// Stats returns live lazy-loader profiler metrics for Tier-0 UI rendering.
func (l *LazyLoader) Stats() LazyLoaderStats {
	l.mu.Lock()
	st := LazyLoaderStats{
		TotalDeclared:         len(l.entries),
		MaxActiveLazy:         l.cfg.MaxActiveLazy,
		TotalWakes:            l.totalWakes,
		TotalReaped:           l.totalReaped,
		TotalEvicted:          l.totalEvicted,
		LastReconcileDuration: l.lastReconcileDur,
	}
	for _, ent := range l.entries {
		switch ent.state {
		case LazyStateActive:
			st.ActiveCount++
		case LazyStateDormant:
			st.DormantCount++
		case LazyStateDisabled:
			st.DisabledCount++
		}
	}
	l.mu.Unlock()
	return st
}

// StartIdleReaper runs a low-overhead periodic O(1) idle reclamation loop until
// ctx cancels, invoking onReaped whenever one or more idle plugins are put to sleep.
func (l *LazyLoader) StartIdleReaper(ctx context.Context, interval time.Duration, onReaped func([]schema.PluginID)) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if reaped := l.ReapIdle(l.cfg.Now()); len(reaped) > 0 && onReaped != nil {
					onReaped(reaped)
				}
			}
		}
	}()
}

func isEvictable(sp PluginSpec) bool {
	return sp.Activation != schema.ActivationEager && !sp.PinnedActive
}

func (l *LazyLoader) lruPushFrontLocked(e *lazyEntry) {
	if e.inLRU {
		l.lruMoveToFrontLocked(e)
		return
	}
	e.lruPrev = nil
	e.lruNext = l.lruHead
	if l.lruHead != nil {
		l.lruHead.lruPrev = e
	} else {
		l.lruTail = e
	}
	l.lruHead = e
	e.inLRU = true
	l.lruCount++
}

func (l *LazyLoader) lruMoveToFrontLocked(e *lazyEntry) {
	if !e.inLRU || l.lruHead == e {
		return
	}
	l.lruRemoveLocked(e)
	l.lruPushFrontLocked(e)
}

func (l *LazyLoader) lruRemoveLocked(e *lazyEntry) {
	if !e.inLRU {
		return
	}
	if e.lruPrev != nil {
		e.lruPrev.lruNext = e.lruNext
	} else {
		l.lruHead = e.lruNext
	}
	if e.lruNext != nil {
		e.lruNext.lruPrev = e.lruPrev
	} else {
		l.lruTail = e.lruPrev
	}
	e.lruPrev = nil
	e.lruNext = nil
	e.inLRU = false
	l.lruCount--
}

func (l *LazyLoader) activateSpec(ctx context.Context, spec PluginSpec) (*InstalledPanel, error) {
	raw, err := l.cfg.Fetch(ctx, spec)
	if err != nil {
		return nil, err
	}
	if spec.PinnedSHA256 != "" {
		sum := sha256.Sum256(raw)
		got := hex.EncodeToString(sum[:])
		if got != spec.PinnedSHA256 {
			return nil, fmt.Errorf("%w: %s got=%s pinned=%s", ErrPinMismatch, spec.PluginID, got, spec.PinnedSHA256)
		}
	}
	gen := l.genSeq.Add(1)
	return l.cfg.Manager.InstallBundle(ctx, raw, l.cfg.Host, l.cfg.Target, gen)
}

func indexTriggersInto(tbl *triggerTable, ent *lazyEntry) {
	sp := ent.spec
	for _, h := range sp.Hooks {
		tbl.byHook[h] = append(tbl.byHook[h], ent)
	}
	switch sp.Activation {
	case schema.ActivationOnCommand:
		for _, tr := range sp.Triggers {
			tbl.byCommand[tr] = append(tbl.byCommand[tr], ent)
		}
	case schema.ActivationOnTopic:
		for _, tr := range sp.Triggers {
			tbl.byTopic[tr] = append(tbl.byTopic[tr], ent)
		}
	case schema.ActivationOnSlot:
		if sp.SlotID != "" {
			tbl.bySlot[sp.SlotID] = append(tbl.bySlot[sp.SlotID], ent)
		}
		for _, tr := range sp.Triggers {
			tbl.bySlot[schema.SlotID(tr)] = append(tbl.bySlot[schema.SlotID(tr)], ent)
		}
	}
}
