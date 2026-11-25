// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package composer

import (
	"fmt"
	"sync"
	"time"
)

// VoicePhase identifies the live recording lifecycle of the composer voice bar.
type VoicePhase string

const (
	VoiceIdle       VoicePhase = "idle"
	VoiceRecording  VoicePhase = "recording"
	VoiceProcessing VoicePhase = "processing"
)

const voiceLevelRingSize = 24

// VoiceSnapshot is an immutable 120Hz-safe view of active voice capture state.
type VoiceSnapshot struct {
	Phase     VoicePhase
	Elapsed   time.Duration
	Levels    []float32 // 0.0..1.0 normalized amplitude bars
	LastError string
}

// FormatElapsed returns MM:SS duration display for the voice recording bar.
func (v VoiceSnapshot) FormatElapsed() string {
	totalSec := int(v.Elapsed.Seconds())
	if totalSec < 0 {
		totalSec = 0
	}
	return fmt.Sprintf("%02d:%02d", totalSec/60, totalSec%60)
}

// VoiceController manages real-time voice recording state, normalized waveform
// levels, elapsed duration, and fail-closed conversion of captured audio bytes
// into a content-addressed AttachmentAudio in composer.State.
type VoiceController struct {
	mu        sync.RWMutex
	phase     VoicePhase
	startedAt time.Time
	elapsed   time.Duration
	levels    []float32
	lastErr   string
}

// NewVoiceController constructs an idle VoiceController.
func NewVoiceController() *VoiceController {
	return &VoiceController{
		phase:  VoiceIdle,
		levels: make([]float32, 0, voiceLevelRingSize),
	}
}

// Start transitions the voice recorder into VoiceRecording and resets waveform bars.
func (vc *VoiceController) Start() {
	vc.mu.Lock()
	vc.phase = VoiceRecording
	vc.startedAt = time.Now()
	vc.elapsed = 0
	vc.levels = vc.levels[:0]
	vc.lastErr = ""
	vc.mu.Unlock()
}

// PushLevel appends one clamped [0.0, 1.0] microphone RMS amplitude sample into
// the bounded 24-bar ring for real-time 120Hz GPU waveform visualization.
func (vc *VoiceController) PushLevel(level float32) {
	if level < 0 {
		level = 0
	} else if level > 1 {
		level = 1
	}
	vc.mu.Lock()
	if vc.phase == VoiceRecording {
		if len(vc.levels) >= voiceLevelRingSize {
			copy(vc.levels, vc.levels[1:])
			vc.levels[len(vc.levels)-1] = level
		} else {
			vc.levels = append(vc.levels, level)
		}
		if !vc.startedAt.IsZero() {
			vc.elapsed = time.Since(vc.startedAt)
		}
	}
	vc.mu.Unlock()
}

// SetElapsed explicitly overrides elapsed duration (for external native audio clocks or tests).
func (vc *VoiceController) SetElapsed(d time.Duration) {
	vc.mu.Lock()
	vc.elapsed = d
	vc.mu.Unlock()
}

// BeginProcessing transitions from VoiceRecording to VoiceProcessing while audio
// encoding or backend transcription finalizes.
func (vc *VoiceController) BeginProcessing() {
	vc.mu.Lock()
	if vc.phase == VoiceRecording {
		if !vc.startedAt.IsZero() && vc.elapsed == 0 {
			vc.elapsed = time.Since(vc.startedAt)
		}
		vc.phase = VoiceProcessing
	}
	vc.mu.Unlock()
}

// CompleteWithAudio validates captured audio bytes (WAV/OGG/MP3/MP4/WebM)
// through State.AddAttachment (fail-closed magic check + SHA-256 CAS URI) and
// returns to VoiceIdle.
func (vc *VoiceController) CompleteWithAudio(filename string, audioBytes []byte, state *State) (AttachmentRef, error) {
	if filename == "" {
		filename = "voice-note.wav"
	}
	if state == nil {
		vc.Fail(ErrAttachmentEmpty)
		return AttachmentRef{}, ErrAttachmentEmpty
	}
	ref, err := state.AddAttachment(AttachmentInput{
		Filename: filename,
		Data:     audioBytes,
	})
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.phase = VoiceIdle
	vc.levels = vc.levels[:0]
	if err != nil {
		vc.lastErr = err.Error()
		return AttachmentRef{}, err
	}
	vc.lastErr = ""
	return ref, nil
}

// Cancel aborts recording and clears transient waveform samples.
func (vc *VoiceController) Cancel() {
	vc.mu.Lock()
	vc.phase = VoiceIdle
	vc.elapsed = 0
	vc.levels = vc.levels[:0]
	vc.lastErr = ""
	vc.mu.Unlock()
}

// Fail records a microphone/capture error and returns to VoiceIdle.
func (vc *VoiceController) Fail(err error) {
	vc.mu.Lock()
	vc.phase = VoiceIdle
	vc.levels = vc.levels[:0]
	if err != nil {
		vc.lastErr = err.Error()
	}
	vc.mu.Unlock()
}

// Snapshot returns a thread-safe copy of the live voice recorder state.
func (vc *VoiceController) Snapshot() VoiceSnapshot {
	if vc == nil {
		return VoiceSnapshot{Phase: VoiceIdle}
	}
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	el := vc.elapsed
	if vc.phase == VoiceRecording && !vc.startedAt.IsZero() && el == 0 {
		el = time.Since(vc.startedAt)
	}
	lvls := make([]float32, len(vc.levels))
	copy(lvls, vc.levels)
	return VoiceSnapshot{
		Phase:     vc.phase,
		Elapsed:   el,
		Levels:    lvls,
		LastError: vc.lastErr,
	}
}
