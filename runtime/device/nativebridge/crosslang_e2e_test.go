// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package nativebridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
)

// subprocessJSONTransport drives a compiled Swift or Kotlin probe binary over
// newline-delimited JSON RPC frames so Go device.Bridge executes real Swift and
// Kotlin MarlinDeviceBridge implementations end-to-end without simulators.
type subprocessJSONTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	mu     sync.Mutex
}

func startSubprocessTransport(t *testing.T, name string, args ...string) *subprocessJSONTransport {
	t.Helper()
	cmd := exec.Command(name, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start probe %s %v: %v", name, args, err)
	}
	tr := &subprocessJSONTransport{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})
	return tr
}

func (s *subprocessJSONTransport) Call(_ context.Context, method string, argsJSON []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	envelope, err := json.Marshal(map[string]string{
		"method": method,
		"args":   string(argsJSON),
	})
	if err != nil {
		return nil, err
	}
	if _, err := s.stdin.Write(append(envelope, '\n')); err != nil {
		return nil, fmt.Errorf("write probe frame: %w", err)
	}
	line, err := s.reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read probe frame: %w", err)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Data  string `json:"data"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode probe response %q: %w", string(line), err)
	}
	if !resp.OK {
		return nil, errors.New(resp.Error)
	}
	return []byte(resp.Data), nil
}

func runFullCrossLanguageProtocolSuite(
	t *testing.T,
	kind schema.TargetKind,
	expectedPlatform string,
	tr NativeCallTransport,
	capPhotos schema.CapabilityID,
	capOCR schema.CapabilityID,
	capVoice schema.CapabilityID,
	capContacts schema.CapabilityID,
	capShare schema.CapabilityID,
	capNotify schema.CapabilityID,
	expectedLocalID string,
) {
	t.Helper()
	ctx := context.Background()

	adapter := &Adapter{Transport: tr, Platform: expectedPlatform}
	caps, err := adapter.Capabilities(ctx)
	if err != nil {
		t.Fatalf("Capabilities() failed: %v", err)
	}
	if caps.Platform != expectedPlatform || len(caps.Families) != 5 || len(caps.Methods) != 8 {
		t.Fatalf("unexpected capabilities from %s bridge: %+v", expectedPlatform, caps)
	}

	br := device.NewBridge(kind)
	if err := RegisterAllProtocols(ctx, br, tr); err != nil {
		t.Fatalf("RegisterAllProtocols(%s) failed: %v", expectedPlatform, err)
	}
	descs, err := br.SnapshotDescriptors(ctx)
	if err != nil || len(descs) != 6 {
		t.Fatalf("expected 6 registered capability descriptors, got %d err=%v", len(descs), err)
	}

	// 1. Photos recentScreenshots
	res, err := br.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "inv-photos",
		CapabilityID: capPhotos,
		Args:         json.RawMessage(`{"limit":5}`),
	})
	if err != nil || !res.OK {
		t.Fatalf("photos dispatch failed: res=%+v err=%v", res, err)
	}
	var photos []device.PhotoItem
	if err := json.Unmarshal(res.Result, &photos); err != nil || len(photos) != 1 || photos[0].LocalID != expectedLocalID {
		t.Fatalf("unexpected photos payload: %s err=%v", string(res.Result), err)
	}

	// 2. Vision OCR
	res, err = br.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "inv-ocr",
		CapabilityID: capOCR,
		Args:         json.RawMessage(fmt.Sprintf(`{"localId":%q}`, expectedLocalID)),
	})
	if err != nil || !res.OK {
		t.Fatalf("ocr dispatch failed: res=%+v err=%v", res, err)
	}
	var ocrOut map[string]string
	if err := json.Unmarshal(res.Result, &ocrOut); err != nil || ocrOut["text"] == "" {
		t.Fatalf("unexpected ocr payload: %s err=%v", string(res.Result), err)
	}

	// 3. Voice transcript
	res, err = br.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "inv-voice",
		CapabilityID: capVoice,
	})
	if err != nil || !res.OK {
		t.Fatalf("voice dispatch failed: res=%+v err=%v", res, err)
	}
	var voiceOut device.VoiceTranscript
	if err := json.Unmarshal(res.Result, &voiceOut); err != nil || voiceOut.Text == "" || voiceOut.DurationMS <= 0 {
		t.Fatalf("unexpected voice payload: %s err=%v", string(res.Result), err)
	}

	// 4. Contacts search (PII-bounded)
	res, err = br.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "inv-contacts",
		CapabilityID: capContacts,
		Args:         json.RawMessage(`{"query":"Ada","limit":5}`),
	})
	if err != nil || !res.OK {
		t.Fatalf("contacts dispatch failed: res=%+v err=%v", res, err)
	}
	var contacts []device.ContactSummary
	if err := json.Unmarshal(res.Result, &contacts); err != nil || len(contacts) != 1 || contacts[0].DisplayName != "Ada Lovelace" {
		t.Fatalf("unexpected contacts payload: %s err=%v", string(res.Result), err)
	}

	// 5. Share capture FIFO + atomic drain (second drain empty)
	res, err = br.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "inv-share-1",
		CapabilityID: capShare,
	})
	if err != nil || !res.OK {
		t.Fatalf("share drain 1 failed: res=%+v err=%v", res, err)
	}
	var captures []device.CapturePayload
	if err := json.Unmarshal(res.Result, &captures); err != nil || len(captures) != 1 {
		t.Fatalf("expected 1 capture on first drain, got %s err=%v", string(res.Result), err)
	}
	res2, err := br.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "inv-share-2",
		CapabilityID: capShare,
	})
	if err != nil || !res2.OK {
		t.Fatalf("share drain 2 failed: res=%+v err=%v", res2, err)
	}
	var captures2 []device.CapturePayload
	if err := json.Unmarshal(res2.Result, &captures2); err != nil || len(captures2) != 0 {
		t.Fatalf("expected 0 captures on second drain, got %s err=%v", string(res2.Result), err)
	}

	// 6. Mutating notification enforces one-shot Rust GateReceipt (deny -> claim -> replay-deny)
	if _, err := br.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "inv-notify-no-gate",
		CapabilityID: capNotify,
		Args:         json.RawMessage(`{"title":"Marlin","body":"Blocked without gate"}`),
	}); !errors.Is(err, device.ErrGateReceiptMissing) {
		t.Fatalf("expected ErrGateReceiptMissing, got %v", err)
	}

	receiptID := "rcpt-crosslang-" + expectedPlatform
	if res, err := br.Dispatch(ctx, device.InvocationRequest{
		InvocationID:  "inv-notify-ok",
		CapabilityID:  capNotify,
		GateReceiptID: receiptID,
		Args:          json.RawMessage(`{"title":"Marlin","body":"Approved over cross-language bridge"}`),
	}); err != nil || !res.OK {
		t.Fatalf("expected gated notify success, got res=%+v err=%v", res, err)
	}

	if _, err := br.Dispatch(ctx, device.InvocationRequest{
		InvocationID:  "inv-notify-replay",
		CapabilityID:  capNotify,
		GateReceiptID: receiptID,
		Args:          json.RawMessage(`{"title":"Marlin","body":"Replay must fail"}`),
	}); !errors.Is(err, device.ErrGateReceiptReplayed) {
		t.Fatalf("expected ErrGateReceiptReplayed on receipt reuse, got %v", err)
	}
}

func TestCrossLanguageGoToSwiftProtocolE2E(t *testing.T) {
	bin := os.Getenv("MARLIN_SWIFT_PROBE_BIN")
	if bin == "" {
		t.Skip("set MARLIN_SWIFT_PROBE_BIN (via `just test-swift`) to run real Go <-> Swift protocol E2E")
	}
	tr := startSubprocessTransport(t, bin)
	runFullCrossLanguageProtocolSuite(
		t,
		schema.TargetMobileIOS,
		"ios",
		tr,
		device.CapIOSPhotosRecent,
		device.CapIOSOCR,
		device.CapIOSVoiceTranscribe,
		device.CapIOSContactsSearch,
		device.CapIOSShareDrain,
		device.CapIOSNotifySend,
		"ios-ph-1",
	)
}

func TestCrossLanguageGoToKotlinProtocolE2E(t *testing.T) {
	jar := os.Getenv("MARLIN_KOTLIN_PROBE_JAR")
	if jar == "" {
		t.Skip("set MARLIN_KOTLIN_PROBE_JAR (via `just test-kotlin`) to run real Go <-> Kotlin protocol E2E")
	}
	tr := startSubprocessTransport(t, "java", "-jar", jar)
	runFullCrossLanguageProtocolSuite(
		t,
		schema.TargetMobileAndroid,
		"android",
		tr,
		device.CapAndroidPhotosRecent,
		device.CapAndroidOCR,
		device.CapAndroidVoiceTranscribe,
		device.CapAndroidContactsSearch,
		device.CapAndroidShareDrain,
		device.CapAndroidNotifySend,
		"and-media-1",
	)
}
