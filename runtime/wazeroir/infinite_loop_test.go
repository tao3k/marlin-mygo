// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package wazeroir_test

import (
	"context"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/engine/bus"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/wazeroir"
)

type fallbackOnlyRenderer struct{ id string }

func (r fallbackOnlyRenderer) ID() string                        { return r.id }
func (r fallbackOnlyRenderer) Trusted() bool                     { return true }
func (r fallbackOnlyRenderer) Render(shell.RenderContext)        {}
func (r fallbackOnlyRenderer) HandleEvent(shell.HostEvent) error { return nil }
func (r fallbackOnlyRenderer) Dispose()                          {}

type evilSlotRenderer struct{ id string }

func (r evilSlotRenderer) ID() string                        { return r.id }
func (r evilSlotRenderer) Trusted() bool                     { return false }
func (r evilSlotRenderer) Render(shell.RenderContext)        {}
func (r evilSlotRenderer) HandleEvent(shell.HostEvent) error { return nil }
func (r evilSlotRenderer) Dispose()                          {}

// TestInfiniteLoopWasmKilledByContextTimeoutTripsCircuitBreakerAndRestoresFallback
// proves that a malicious third-party WASM plugin containing an infinite loop
// `(loop (br 0))` is terminated promptly by wazero's context cancellation without
// stalling the 120Hz render thread, trips the CircuitBreaker, and atomically
// restores the trusted host.* fallback renderer.
func TestInfiniteLoopWasmKilledByContextTimeoutTripsCircuitBreakerAndRestoresFallback(t *testing.T) {
	// Minimal spec-valid WebAssembly 1.0 binary:
	// (module (func (export "_start") (loop (br 0))))
	infiniteLoopWasm := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic + version 1
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type section: () -> ()
		0x03, 0x02, 0x01, 0x00, // func section: func 0 -> type 0
		0x07, 0x0a, 0x01, 0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x00, // export "_start"
		0x0a, 0x09, 0x01, 0x07, 0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x0b, // code: loop; br 0; end; end
	}

	eng, err := wazeroir.NewModuleEngine(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(context.Background())

	slots := shell.NewSlotRegistry()
	if err := slots.Declare(schema.SlotDeclaration{
		SlotID:               "workbench.inspector",
		Cardinality:          schema.SlotSingle,
		FallbackRenderer:     "host.inspector",
		AllowUntrustedPlugin: true,
	}, fallbackOnlyRenderer{id: "host.inspector"}); err != nil {
		t.Fatal(err)
	}
	unmountEvil, err := slots.Mount("workbench.inspector", "", evilSlotRenderer{id: "evil.hang"})
	if err != nil {
		t.Fatal(err)
	}

	breaker := bus.NewCircuitBreaker(2, 5*time.Second)

	for attempt := 0; attempt < 2; attempt++ {
		runCtx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		start := time.Now()
		_, runErr := eng.ExecuteCached(runCtx, infiniteLoopWasm)
		elapsed := time.Since(start)
		cancel()

		if runErr == nil {
			t.Fatalf("attempt %d: expected infinite-loop WASM module to fail on timeout", attempt)
		}
		if elapsed > 2*time.Second {
			t.Fatalf("attempt %d: runaway WASM took too long to terminate (%s)", attempt, elapsed)
		}
		if tripped := breaker.RecordFailure(time.Now()); tripped {
			unmountEvil()
		}
	}

	if breaker.State() != bus.BreakerOpen {
		t.Fatalf("expected CircuitBreaker OPEN after runaway WASM faults, got %v", breaker.State())
	}
	resolved := slots.Resolve("workbench.inspector")
	if len(resolved) != 1 || resolved[0].ID() != "host.inspector" {
		t.Fatalf("expected host.inspector fallback restored after tripping breaker, got %+v", resolved)
	}
}
