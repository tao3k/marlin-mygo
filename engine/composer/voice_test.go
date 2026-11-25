// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package composer

import (
	"testing"
	"time"
)

func TestVoiceControllerLifecycleWaveformAndModelSelection(t *testing.T) {
	vc := NewVoiceController()
	st := NewState(DefaultAttachmentPolicy())

	// 1. Voice recording lifecycle + clamped 24-bar ring + duration formatting.
	vc.Start()
	for i := 0; i < 30; i++ {
		vc.PushLevel(float32(i) / 20.0)
	}
	vc.SetElapsed(75 * time.Second)
	snap := vc.Snapshot()
	if snap.Phase != VoiceRecording || len(snap.Levels) != voiceLevelRingSize || snap.FormatElapsed() != "01:15" {
		t.Fatalf("unexpected voice snapshot: %+v", snap)
	}
	if snap.Levels[len(snap.Levels)-1] != 1.0 {
		t.Fatalf("expected level clamped to 1.0, got %f", snap.Levels[len(snap.Levels)-1])
	}

	vc.BeginProcessing()
	if vc.Snapshot().Phase != VoiceProcessing {
		t.Fatalf("expected VoiceProcessing phase")
	}

	wav := make([]byte, 44)
	copy(wav[0:4], "RIFF")
	copy(wav[8:12], "WAVE")
	copy(wav[12:16], "fmt ")
	ref, err := vc.CompleteWithAudio("meeting.wav", wav, st)
	if err != nil {
		t.Fatalf("CompleteWithAudio failed: %v", err)
	}
	if ref.Kind != AttachmentAudio || vc.Snapshot().Phase != VoiceIdle {
		t.Fatalf("expected staged AttachmentAudio & idle recorder, got ref=%+v snap=%+v", ref, vc.Snapshot())
	}

	// 2. Direct model selection & query filtering.
	matches := st.MatchModels("pro")
	if len(matches) != 1 || matches[0].ID != "marlin-pro" {
		t.Fatalf("expected MatchModels('pro') -> marlin-pro, got %+v", matches)
	}
	chosen, ok := st.SetActiveModel("marlin-pro")
	if !ok || chosen.ID != "marlin-pro" || st.Snapshot().ActiveModel.ID != "marlin-pro" {
		t.Fatalf("expected SetActiveModel('marlin-pro') active, got %+v", chosen)
	}
}
