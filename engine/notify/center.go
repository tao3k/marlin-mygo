// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package notify

import (
	"sync"
	"time"
)

// CenterConfig tunes capacity, toast visibility bounds, and plugin rate limits.
type CenterConfig struct {
	MaxHistory          int           // default 100
	MaxFloatingToasts   int           // default 3
	PluginRateBurst     int           // default 4 notifications per PluginRateWindow
	PluginRateWindow    time.Duration // default 10s
	Now                 func() time.Time
}

type pluginBucket struct {
	windowStart time.Time
	count       int
}

// Center owns the Tier-0 notification lifecycle: publishing, toast-storm
// deduplication, TTL expiration, Do-Not-Disturb filtering, per-plugin rate
// limiting, and version-cached snapshots for 120Hz UI presentation.
type Center struct {
	maxHistory  int
	maxFloating int
	pluginBurst int
	pluginWin   time.Duration
	now         func() time.Time

	mu         sync.RWMutex
	nextID     uint64
	version    uint64
	dnd        bool
	items      []Item // newest first
	buckets    map[string]*pluginBucket
	nextSub    uint64
	subs       map[uint64]func(Snapshot)
	onAction   func(item Item, actionID string)
}

// NewCenter creates a bounded, thread-safe Notification Center.
func NewCenter(cfg CenterConfig) *Center {
	if cfg.MaxHistory <= 0 {
		cfg.MaxHistory = 100
	}
	if cfg.MaxFloatingToasts <= 0 {
		cfg.MaxFloatingToasts = 3
	}
	if cfg.PluginRateBurst <= 0 {
		cfg.PluginRateBurst = 4
	}
	if cfg.PluginRateWindow <= 0 {
		cfg.PluginRateWindow = 10 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Center{
		maxHistory:  cfg.MaxHistory,
		maxFloating: cfg.MaxFloatingToasts,
		pluginBurst: cfg.PluginRateBurst,
		pluginWin:   cfg.PluginRateWindow,
		now:         cfg.Now,
		buckets:     make(map[string]*pluginBucket),
		subs:        make(map[uint64]func(Snapshot)),
	}
}

// SetActionHandler registers a callback invoked when the user clicks a toast or
// Activity Center CTA button.
func (c *Center) SetActionHandler(fn func(item Item, actionID string)) {
	c.mu.Lock()
	c.onAction = fn
	c.mu.Unlock()
}

// Publish emits a Tier-0 Host/GRPC/Auth/Gate notification (or validated plugin
// notification). Duplicate active DedupeKeys collapse into a single item with
// incremented Count, refreshed message/progress, and extended TTL.
func (c *Center) Publish(spec NotificationSpec) (Item, error) {
	if spec.Source == "" {
		spec.Source = SourceHost
	}
	if spec.Severity == "" {
		spec.Severity = SeverityInfo
	}
	if spec.Source == SourcePlugin {
		return c.publishPluginInternal(spec)
	}
	return c.publishInternal(spec)
}

// PublishFromPlugin is the sandboxed entry point for untrusted WebView/WASM
// plugins. It strictly forces SourcePlugin, rejects host-plane spoofing, strips
// control characters, caps actions, and enforces a per-plugin rate limit.
func (c *Center) PublishFromPlugin(pluginID string, spec NotificationSpec) (Item, error) {
	if spec.Source != "" && spec.Source != SourcePlugin {
		return Item{}, ErrSpoofedHostSource
	}
	spec.Source = SourcePlugin
	spec.PluginID = pluginID
	// Untrusted plugins cannot create un-dismissable persistent system alerts.
	spec.Persistent = false
	if spec.TTL <= 0 || spec.TTL > 10*time.Second {
		spec.TTL = 5 * time.Second
	}
	if spec.Severity == "" {
		spec.Severity = SeverityInfo
	}
	return c.publishPluginInternal(spec)
}

func (c *Center) publishPluginInternal(spec NotificationSpec) (Item, error) {
	pid := sanitizeText(spec.PluginID, 64)
	if pid == "" {
		return Item{}, ErrPluginIDRequired
	}
	spec.PluginID = pid
	if spec.DedupeKey != "" {
		// Scope plugin dedupe keys to the plugin so one plugin cannot overwrite another's toast.
		spec.DedupeKey = "plugin:" + pid + ":" + sanitizeText(spec.DedupeKey, 64)
	}

	now := c.now()
	c.mu.Lock()
	b, ok := c.buckets[pid]
	if !ok || now.Sub(b.windowStart) >= c.pluginWin {
		b = &pluginBucket{windowStart: now, count: 0}
		c.buckets[pid] = b
	}
	if b.count >= c.pluginBurst {
		c.mu.Unlock()
		return Item{}, ErrPluginRateLimited
	}
	b.count++
	c.mu.Unlock()

	return c.publishInternal(spec)
}

func (c *Center) publishInternal(spec NotificationSpec) (Item, error) {
	title := sanitizeText(spec.Title, MaxTitleBytes)
	if title == "" {
		return Item{}, ErrEmptyTitle
	}
	msg := sanitizeText(spec.Message, MaxMessageBytes)
	dedupe := sanitizeText(spec.DedupeKey, 96)

	var actions []Action
	for i := 0; i < len(spec.Actions) && i < MaxActions; i++ {
		lbl := sanitizeText(spec.Actions[i].Label, 32)
		id := sanitizeText(spec.Actions[i].ID, 48)
		if lbl != "" && id != "" {
			actions = append(actions, Action{
				ID:      id,
				Label:   lbl,
				Primary: spec.Actions[i].Primary,
			})
		}
	}

	now := c.now()
	persistent := spec.Persistent
	var expiresAt time.Time
	if spec.TTL < 0 {
		persistent = true
	} else if !persistent {
		ttl := spec.TTL
		if ttl == 0 {
			defTTL, defPersist := defaultTTLFor(spec.Severity, spec.Source)
			persistent = defPersist
			ttl = defTTL
		}
		if !persistent && ttl > 0 {
			expiresAt = now.Add(ttl)
		}
	}

	progress := spec.Progress
	if progress < 0 {
		progress = 0
	} else if progress > 1 {
		progress = 1
	}

	c.mu.Lock()
	// Deduplicate against an existing non-dismissed item with the same DedupeKey.
	if dedupe != "" {
		for i := range c.items {
			if c.items[i].DedupeKey == dedupe && !c.items[i].Dismissed {
				it := c.items[i]
				it.Severity = spec.Severity
				it.Title = title
				it.Message = msg
				it.Count++
				it.UpdatedAt = now
				it.ExpiresAt = expiresAt
				it.Persistent = persistent
				it.Read = false
				it.HasProgress = spec.HasProgress
				it.Progress = progress
				if len(actions) > 0 {
					it.Actions = actions
				}
				// Move updated item to head (newest first).
				copy(c.items[1:i+1], c.items[0:i])
				c.items[0] = it
				c.version++
				c.mu.Unlock()
				c.emit()
				return cloneItem(it), nil
			}
		}
	}

	c.nextID++
	item := Item{
		ID:          c.nextID,
		Severity:    spec.Severity,
		Source:      spec.Source,
		PluginID:    spec.PluginID,
		Title:       title,
		Message:     msg,
		DedupeKey:   dedupe,
		Count:       1,
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   expiresAt,
		Persistent:  persistent,
		HasProgress: spec.HasProgress,
		Progress:    progress,
		Actions:     actions,
	}
	c.items = append([]Item{item}, c.items...)
	if len(c.items) > c.maxHistory {
		c.items = c.items[:c.maxHistory]
	}
	c.version++
	c.mu.Unlock()
	c.emit()
	return cloneItem(item), nil
}

// UpdateProgress updates a live progress toast in place (e.g. Marketplace sync or download).
func (c *Center) UpdateProgress(id uint64, progress float32, message string) bool {
	if progress < 0 {
		progress = 0
	} else if progress > 1 {
		progress = 1
	}
	c.mu.Lock()
	for i := range c.items {
		if c.items[i].ID == id {
			c.items[i].HasProgress = true
			c.items[i].Progress = progress
			if message != "" {
				c.items[i].Message = sanitizeText(message, MaxMessageBytes)
			}
			c.items[i].UpdatedAt = c.now()
			c.version++
			c.mu.Unlock()
			c.emit()
			return true
		}
	}
	c.mu.Unlock()
	return false
}

// DismissToast hides one notification from the floating stack while retaining
// it in the Activity Center history.
func (c *Center) DismissToast(id uint64) {
	c.mu.Lock()
	changed := false
	for i := range c.items {
		if c.items[i].ID == id && !c.items[i].Dismissed {
			c.items[i].Dismissed = true
			changed = true
			break
		}
	}
	if changed {
		c.version++
	}
	c.mu.Unlock()
	if changed {
		c.emit()
	}
}

// TriggerAction marks the notification read, dismisses its floating toast, and
// invokes the registered ActionHandler.
func (c *Center) TriggerAction(id uint64, actionID string) {
	c.mu.Lock()
	var target Item
	found := false
	for i := range c.items {
		if c.items[i].ID == id {
			c.items[i].Read = true
			c.items[i].Dismissed = true
			target = cloneItem(c.items[i])
			found = true
			c.version++
			break
		}
	}
	handler := c.onAction
	c.mu.Unlock()
	if found {
		c.emit()
		if handler != nil {
			handler(target, actionID)
		}
	}
}

// MarkRead marks one notification as read.
func (c *Center) MarkRead(id uint64) {
	c.mu.Lock()
	changed := false
	for i := range c.items {
		if c.items[i].ID == id && !c.items[i].Read {
			c.items[i].Read = true
			changed = true
			break
		}
	}
	if changed {
		c.version++
	}
	c.mu.Unlock()
	if changed {
		c.emit()
	}
}

// MarkAllRead marks every notification in history as read.
func (c *Center) MarkAllRead() {
	c.mu.Lock()
	changed := false
	for i := range c.items {
		if !c.items[i].Read {
			c.items[i].Read = true
			changed = true
		}
	}
	if changed {
		c.version++
	}
	c.mu.Unlock()
	if changed {
		c.emit()
	}
}

// ClearHistory removes all non-persistent-gate history entries and floating toasts.
func (c *Center) ClearHistory() {
	c.mu.Lock()
	kept := make([]Item, 0)
	for _, it := range c.items {
		if it.Source == SourceGate && !it.Read {
			kept = append(kept, it)
		}
	}
	c.items = kept
	c.version++
	c.mu.Unlock()
	c.emit()
}

// SetDoNotDisturb enables or disables Do-Not-Disturb mode. Under DND, routine
// Info/Success/Plugin floating toasts are muted (still logged in history), while
// Tier-0 Gate & Error alerts always break through.
func (c *Center) SetDoNotDisturb(enabled bool) {
	c.mu.Lock()
	if c.dnd == enabled {
		c.mu.Unlock()
		return
	}
	c.dnd = enabled
	c.version++
	c.mu.Unlock()
	c.emit()
}

// DoNotDisturb reports whether Do-Not-Disturb mode is active.
func (c *Center) DoNotDisturb() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dnd
}

// Version returns the monotonic mutation version for zero-alloc UI caching.
func (c *Center) Version() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

// Snapshot returns an immutable view of active floating toasts (respecting TTL
// and DND) plus the bounded Activity Center history.
func (c *Center) Snapshot() Snapshot {
	now := c.now()
	c.mu.RLock()
	defer c.mu.RUnlock()

	history := make([]Item, len(c.items))
	floating := make([]Item, 0, c.maxFloating)
	unread := 0
	alerts := 0

	for i, it := range c.items {
		cp := cloneItem(it)
		history[i] = cp
		if !cp.Read {
			unread++
			if cp.Severity == SeverityWarning || cp.Severity == SeverityError || cp.Source == SourceGate {
				alerts++
			}
		}
		if len(floating) >= c.maxFloating || cp.Dismissed {
			continue
		}
		if !cp.Persistent && !cp.ExpiresAt.IsZero() && !now.Before(cp.ExpiresAt) {
			continue
		}
		if c.dnd && !breaksThroughDND(cp) {
			continue
		}
		floating = append(floating, cp)
	}

	return Snapshot{
		Version:        c.version,
		DoNotDisturb:   c.dnd,
		UnreadCount:    unread,
		AlertCount:     alerts,
		FloatingToasts: floating,
		History:        history,
	}
}

// FilterHistory returns a filtered slice of items matching mode.
func FilterHistory(items []Item, mode FilterMode) []Item {
	if mode == "" || mode == FilterAll {
		return items
	}
	out := make([]Item, 0, len(items))
	for _, it := range items {
		switch mode {
		case FilterUnread:
			if !it.Read {
				out = append(out, it)
			}
		case FilterAlerts:
			if it.Severity == SeverityWarning || it.Severity == SeverityError || it.Source == SourceGate {
				out = append(out, it)
			}
		case FilterPlugins:
			if it.Source == SourcePlugin {
				out = append(out, it)
			}
		}
	}
	return out
}

// Subscribe registers a listener notified whenever notifications change.
func (c *Center) Subscribe(fn func(Snapshot)) func() {
	if fn == nil {
		return func() {}
	}
	c.mu.Lock()
	c.nextSub++
	id := c.nextSub
	c.subs[id] = fn
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.subs, id)
		c.mu.Unlock()
	}
}

func (c *Center) emit() {
	snap := c.Snapshot()
	c.mu.RLock()
	listeners := make([]func(Snapshot), 0, len(c.subs))
	for _, fn := range c.subs {
		listeners = append(listeners, fn)
	}
	c.mu.RUnlock()
	for _, fn := range listeners {
		fn(snap)
	}
}

func breaksThroughDND(it Item) bool {
	return it.Source == SourceGate || it.Severity == SeverityError
}

func cloneItem(it Item) Item {
	if len(it.Actions) > 0 {
		it.Actions = append([]Action(nil), it.Actions...)
	}
	return it
}
