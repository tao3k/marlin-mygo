// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/egoist/mygo"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/notify"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

// wireNativeVoiceCapture connects the TranscriptController voice controls to a
// CGO-free OS microphone recorder (rec/sox, ffmpeg, or arecord), failing closed
// with a user-visible Toast whenever no host audio capture tool is installed.
func wireNativeVoiceCapture(
	ctx context.Context,
	transcript *mygoshell.TranscriptController,
	notifyCenter *notify.Center,
	win *mygo.Window,
) func() {
	if transcript == nil {
		return func() {}
	}
	audioRec := composer.NewNativeAudioRecorder()

	invalidate := func() {
		if win != nil {
			win.Invalidate()
		}
	}
	publishVoiceToast := func(sev notify.Severity, title, msg string) {
		if notifyCenter == nil {
			return
		}
		_, _ = notifyCenter.Publish(notify.NotificationSpec{
			Severity: sev,
			Source:   notify.SourceHost,
			Title:    title,
			Message:  msg,
		})
	}

	transcript.OnStartVoice = func() {
		go func() {
			if err := audioRec.Start(ctx, transcript.Voice, invalidate); err != nil {
				log.Printf("native voice capture start failed: %v", err)
				if transcript.Voice != nil {
					transcript.Voice.Fail(err)
				}
				publishVoiceToast(notify.SeverityWarning, "Microphone Capture Unavailable", err.Error())
				invalidate()
			}
		}()
	}

	transcript.OnStopVoice = func() {
		go func() {
			name, wavBytes, err := audioRec.Stop()
			if err != nil {
				log.Printf("native voice capture stop failed: %v", err)
				if transcript.Voice != nil {
					transcript.Voice.Fail(err)
				}
				publishVoiceToast(notify.SeverityWarning, "Voice Capture Failed", err.Error())
				invalidate()
				return
			}
			ref, stageErr := transcript.CompleteVoiceAudio(name, wavBytes)
			if stageErr != nil {
				publishVoiceToast(notify.SeverityWarning, "Voice Attachment Rejected", stageErr.Error())
				invalidate()
				return
			}
			publishVoiceToast(
				notify.SeveritySuccess,
				"Voice Note Attached",
				fmt.Sprintf("%s (%s)", ref.Name, composer.FormatByteSize(ref.ByteSize)),
			)
			invalidate()
		}()
	}

	transcript.OnCancelVoice = func() {
		go func() {
			audioRec.Cancel()
			invalidate()
		}()
	}

	return audioRec.Cancel
}
