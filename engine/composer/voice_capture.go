// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package composer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// ErrAudioCaptureUnavailable is returned when no CGO-free OS microphone capture
// binary (rec/sox, ffmpeg, arecord) is installed on the host.
var ErrAudioCaptureUnavailable = errors.New("composer: native microphone capture tool (sox/rec, ffmpeg, or arecord) not found")

const (
	defaultMaxVoiceDuration = 120 * time.Second
	defaultMaxVoiceBytes    = 8 << 20 // 8 MiB WAV ceiling
)

// NativeAudioRecorder captures bounded 16kHz mono WAV audio from the host
// microphone without CGO using platform-native capture tools, feeding live
// elapsed duration and pulse levels into a VoiceController.
type NativeAudioRecorder struct {
	MaxDuration time.Duration
	MaxBytes    int

	mu      sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	tmpPath string
	doneCh  chan struct{}
}

// NewNativeAudioRecorder creates a fail-closed bounded OS audio recorder.
func NewNativeAudioRecorder() *NativeAudioRecorder {
	return &NativeAudioRecorder{
		MaxDuration: defaultMaxVoiceDuration,
		MaxBytes:    defaultMaxVoiceBytes,
	}
}

// Start begins recording to a 0600 temporary WAV file and streams periodic
// level ticks into vc (calling onTick so the UI invalidates smoothly).
func (r *NativeAudioRecorder) Start(ctx context.Context, vc *VoiceController, onTick func()) error {
	r.mu.Lock()
	if r.cmd != nil {
		r.mu.Unlock()
		return errors.New("composer: audio recording already in progress")
	}
	maxDur := r.MaxDuration
	if maxDur <= 0 {
		maxDur = defaultMaxVoiceDuration
	}
	tmp, err := os.CreateTemp("", "marlin-voice-*.wav")
	if err != nil {
		r.mu.Unlock()
		return fmt.Errorf("composer: create temp wav: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Chmod(0o600)
	_ = tmp.Close()

	recCtx, cancel := context.WithTimeout(ctx, maxDur)
	cmd, err := buildAudioCaptureCmd(recCtx, tmpPath)
	if err != nil {
		cancel()
		_ = os.Remove(tmpPath)
		r.mu.Unlock()
		return err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.Remove(tmpPath)
		r.mu.Unlock()
		return fmt.Errorf("composer: start audio capture: %w", err)
	}

	doneCh := make(chan struct{})
	r.cmd = cmd
	r.cancel = cancel
	r.tmpPath = tmpPath
	r.doneCh = doneCh
	r.mu.Unlock()

	go func() {
		_ = cmd.Wait()
		close(doneCh)
	}()

	// Emit smooth 10Hz elapsed & gentle activity pulse ticks while recording.
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		step := 0
		for {
			select {
			case <-doneCh:
				return
			case <-recCtx.Done():
				return
			case <-ticker.C:
				if vc != nil {
					lvl := float32(0.25 + 0.55*float32((step*7)%10)/10.0)
					vc.PushLevel(lvl)
				}
				step++
				if onTick != nil {
					onTick()
				}
			}
		}
	}()
	return nil
}

// Stop terminates recording cleanly, reads the bounded WAV payload, removes
// the temporary file, and returns the raw WAV bytes for fail-closed staging.
func (r *NativeAudioRecorder) Stop() (filename string, wavBytes []byte, err error) {
	r.mu.Lock()
	cmd := r.cmd
	cancel := r.cancel
	tmpPath := r.tmpPath
	doneCh := r.doneCh
	maxBytes := r.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxVoiceBytes
	}
	r.cmd = nil
	r.cancel = nil
	r.tmpPath = ""
	r.doneCh = nil
	r.mu.Unlock()

	if tmpPath == "" {
		return "", nil, ErrAttachmentEmpty
	}
	defer os.Remove(tmpPath)

	if cmd != nil && cmd.Process != nil {
		_ = signalGracefulAudioStop(cmd)
	}
	if doneCh != nil {
		select {
		case <-doneCh:
		case <-time.After(1500 * time.Millisecond):
			if cancel != nil {
				cancel()
			}
			<-doneCh
		}
	}
	if cancel != nil {
		cancel()
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		return "", nil, fmt.Errorf("composer: read captured wav: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil {
		return "", nil, err
	}
	if len(data) == 0 {
		return "", nil, ErrAttachmentEmpty
	}
	if len(data) > maxBytes {
		return "", nil, fmt.Errorf("%w (%s > %s)", ErrAttachmentTooLarge, FormatByteSize(len(data)), FormatByteSize(maxBytes))
	}
	return fmt.Sprintf("voice-%s.wav", time.Now().Format("150405")), data, nil
}

// Cancel aborts recording immediately and deletes the temporary WAV file.
func (r *NativeAudioRecorder) Cancel() {
	r.mu.Lock()
	cancel := r.cancel
	tmpPath := r.tmpPath
	doneCh := r.doneCh
	r.cmd = nil
	r.cancel = nil
	r.tmpPath = ""
	r.doneCh = nil
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if doneCh != nil {
		select {
		case <-doneCh:
		case <-time.After(500 * time.Millisecond):
		}
	}
	if tmpPath != "" {
		_ = os.Remove(tmpPath)
	}
}
