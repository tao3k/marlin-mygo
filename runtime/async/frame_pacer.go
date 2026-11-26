// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package async

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// FramePacer provides coalesced invalidation, 120Hz frame-budget telemetry
// (8.33ms budget), and demand-driven idle tick gating to prevent macOS
// transparent-window GPU power drain when unfocused or on static buffers.
type FramePacer struct {
	pending    atomic.Bool
	focused    atomic.Bool
	clockLive  atomic.Bool
	triggerFn  func()
	slowCount  atomic.Uint64
	frameCount atomic.Uint64
	maxNanos   atomic.Int64
	budget     time.Duration
}

// NewFramePacer constructs a FramePacer with default 120Hz frame budget.
func NewFramePacer(trigger func(), budget time.Duration) *FramePacer {
	if budget <= 0 {
		budget = 8333 * time.Microsecond
	}
	p := &FramePacer{triggerFn: trigger, budget: budget}
	p.focused.Store(true)
	return p
}

// RequestFrame coalesces bursts into a single pending invalidation until consumed.
func (p *FramePacer) RequestFrame() bool {
	if !p.pending.CompareAndSwap(false, true) {
		return false
	}
	if p.triggerFn != nil {
		p.triggerFn()
	}
	return true
}

// MarkFrameStart clears the coalesced pending flag when a render frame begins.
func (p *FramePacer) MarkFrameStart() time.Time {
	p.pending.Store(false)
	return time.Now()
}

// RecordFrame records render duration and slow-frame exceedances.
func (p *FramePacer) RecordFrame(dur time.Duration) {
	p.frameCount.Add(1)
	ns := dur.Nanoseconds()
	for {
		cur := p.maxNanos.Load()
		if ns <= cur || p.maxNanos.CompareAndSwap(cur, ns) {
			break
		}
	}
	if dur > p.budget {
		p.slowCount.Add(1)
	}
}

// SetWindowFocused records OS window activation state.
func (p *FramePacer) SetWindowFocused(focused bool) { p.focused.Store(focused) }

// SetLiveClockVisible records whether a live-ticking view (e.g. Dashboard) is shown.
func (p *FramePacer) SetLiveClockVisible(visible bool) { p.clockLive.Store(visible) }

// ShouldTickIdle returns true only when focused and displaying live-ticking UI.
func (p *FramePacer) ShouldTickIdle() bool {
	return p.focused.Load() && p.clockLive.Load()
}

// Telemetry returns (frames, slowFrames, maxFrameDuration).
func (p *FramePacer) Telemetry() (uint64, uint64, time.Duration) {
	return p.frameCount.Load(), p.slowCount.Load(), time.Duration(p.maxNanos.Load())
}

// WebViewState enumerates hybrid WebView lifecycle states.
type WebViewState uint8

const (
	WebViewUnmounted WebViewState = iota
	WebViewWarmingAsync
	WebViewReady
	WebViewCrashed
)

// TrustTier classifies hybrid WebView capabilities.
type TrustTier uint8

const (
	TrustTier0FirstParty TrustTier = 0
	TrustTier2Untrusted  TrustTier = 2
)

// ErrUntrustedRPCForbidden denies Tier-0 host RPC access to Tier-2 islands.
var ErrUntrustedRPCForbidden = errors.New("async: untrusted Tier-2 WebView island cannot invoke Tier-0 RPC")

// WebViewLifecycle enforces async off-render mount, focus revocation on hide,
// crash recovery, and Tier-0/Tier-2 capability isolation.
type WebViewLifecycle struct {
	mu      sync.Mutex
	state   WebViewState
	shown   bool
	focused bool
	tier    TrustTier
}

// NewWebViewLifecycle initializes an unmounted lifecycle for tier.
func NewWebViewLifecycle(tier TrustTier) *WebViewLifecycle {
	return &WebViewLifecycle{state: WebViewUnmounted, tier: tier}
}

// RequestAsyncMount transitions Unmounted -> WarmingAsync without blocking render.
func (w *WebViewLifecycle) RequestAsyncMount() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state != WebViewUnmounted {
		return false
	}
	w.state = WebViewWarmingAsync
	w.shown = true
	return true
}

// MarkReady transitions WarmingAsync -> Ready.
func (w *WebViewLifecycle) MarkReady() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state != WebViewWarmingAsync {
		return false
	}
	w.state = WebViewReady
	return true
}

// Focus grants keyboard focus only when Ready and Shown.
func (w *WebViewLifecycle) Focus() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state != WebViewReady || !w.shown {
		return false
	}
	w.focused = true
	return true
}

// HideTab hides the tab and revokes keyboard focus back to the native GPU frame.
func (w *WebViewLifecycle) HideTab() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.shown = false
	w.focused = false
}

// ShowTab shows a Ready tab.
func (w *WebViewLifecycle) ShowTab() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state != WebViewReady {
		return false
	}
	w.shown = true
	return true
}

// MarkCrashed transitions Ready -> Crashed and revokes focus.
func (w *WebViewLifecycle) MarkCrashed() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state == WebViewReady {
		w.state = WebViewCrashed
		w.focused = false
	}
}

// Recover transitions Crashed -> WarmingAsync.
func (w *WebViewLifecycle) Recover() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state != WebViewCrashed {
		return false
	}
	w.state = WebViewWarmingAsync
	return true
}

// CheckHostRPC enforces the Tier-0 capability gate.
func (w *WebViewLifecycle) CheckHostRPC() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.tier != TrustTier0FirstParty {
		return ErrUntrustedRPCForbidden
	}
	if w.state != WebViewReady {
		return errors.New("async: webview not ready")
	}
	return nil
}

// Snapshot returns (state, shown, focused, tier).
func (w *WebViewLifecycle) Snapshot() (WebViewState, bool, bool, TrustTier) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state, w.shown, w.focused, w.tier
}
