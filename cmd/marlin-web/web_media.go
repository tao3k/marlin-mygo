// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build js && wasm

package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/egoist/mygo"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

const (
	webMaxAudioBytes = 8 << 20  // 8 MiB fail-closed audio cap
	webMaxFileBytes  = 20 << 20 // 20 MiB fail-closed file cap
)

// webVoiceSession manages a browser MediaRecorder session bridged into
// TranscriptController.Voice with fail-closed byte limits and track cleanup.
type webVoiceSession struct {
	mu       sync.Mutex
	recorder js.Value
	stream   js.Value
	chunks   []js.Value
	mimeType string
	stopTick chan struct{}
	canceled bool
}

// wireWebMultimodalCapture connects browser-native MediaRecorder voice capture,
// HTML file input selection, and Clipboard API paste into the shared Go
// TranscriptController with fail-closed validation.
func wireWebMultimodalCapture(transcript *mygoshell.TranscriptController, win *mygo.Window) {
	if transcript == nil {
		return
	}
	invalidate := func() {
		if win != nil {
			win.Invalidate()
		}
	}

	sess := &webVoiceSession{}

	transcript.OnStartVoice = func() {
		sess.start(transcript, invalidate)
	}
	transcript.OnStopVoice = func() {
		sess.stop(false)
	}
	transcript.OnCancelVoice = func() {
		sess.stop(true)
		invalidate()
	}
	transcript.OnPickAttachment = func() {
		openBrowserFilePicker(transcript, invalidate)
	}
	transcript.OnPasteAttachment = func() {
		readBrowserClipboard(transcript, invalidate)
	}
}

func (s *webVoiceSession) start(transcript *mygoshell.TranscriptController, invalidate func()) {
	nav := js.Global().Get("navigator")
	mediaDevices := nav.Get("mediaDevices")
	mediaRecorderCtor := js.Global().Get("MediaRecorder")
	if !mediaDevices.Truthy() || !mediaDevices.Get("getUserMedia").Truthy() || !mediaRecorderCtor.Truthy() {
		if transcript.Voice != nil {
			transcript.Voice.Fail(errors.New("browser microphone MediaRecorder API unavailable"))
		}
		invalidate()
		return
	}

	constraints := js.Global().Get("Object").New()
	constraints.Set("audio", true)
	promise := mediaDevices.Call("getUserMedia", constraints)

	var onStream, onErr js.Func
	onStream = js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer onStream.Release()
		defer onErr.Release()
		if len(args) == 0 || !args[0].Truthy() {
			if transcript.Voice != nil {
				transcript.Voice.Fail(errors.New("empty microphone MediaStream"))
			}
			invalidate()
			return nil
		}
		stream := args[0]
		mime := "audio/webm"
		if mediaRecorderCtor.Get("isTypeSupported").Truthy() {
			if !mediaRecorderCtor.Call("isTypeSupported", mime).Bool() &&
				mediaRecorderCtor.Call("isTypeSupported", "audio/mp4").Bool() {
				mime = "audio/mp4"
			}
		}
		opts := js.Global().Get("Object").New()
		opts.Set("mimeType", mime)
		rec := mediaRecorderCtor.New(stream, opts)

		stopTick := make(chan struct{})
		s.mu.Lock()
		s.recorder = rec
		s.stream = stream
		s.chunks = nil
		s.mimeType = mime
		s.stopTick = stopTick
		s.canceled = false
		s.mu.Unlock()

		var onData, onStop js.Func
		onData = js.FuncOf(func(_ js.Value, evArgs []js.Value) any {
			if len(evArgs) > 0 {
				data := evArgs[0].Get("data")
				if data.Truthy() && data.Get("size").Int() > 0 {
					s.mu.Lock()
					s.chunks = append(s.chunks, data)
					s.mu.Unlock()
				}
			}
			return nil
		})
		onStop = js.FuncOf(func(_ js.Value, _ []js.Value) any {
			defer onData.Release()
			defer onStop.Release()
			s.finishBlob(transcript, invalidate)
			return nil
		})
		rec.Set("ondataavailable", onData)
		rec.Set("onstop", onStop)
		rec.Call("start", 250)

		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			step := 0
			for {
				select {
				case <-stopTick:
					return
				case <-ticker.C:
					if transcript.Voice != nil {
						lvl := float32(0.25 + 0.55*float32((step*7)%10)/10.0)
						transcript.Voice.PushLevel(lvl)
					}
					step++
					invalidate()
				}
			}
		}()
		return nil
	})
	onErr = js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer onStream.Release()
		defer onErr.Release()
		msg := "microphone permission denied"
		if len(args) > 0 && args[0].Truthy() {
			msg = args[0].Call("toString").String()
		}
		if transcript.Voice != nil {
			transcript.Voice.Fail(errors.New(msg))
		}
		invalidate()
		return nil
	})
	promise.Call("then", onStream, onErr)
}

func (s *webVoiceSession) stop(cancel bool) {
	s.mu.Lock()
	s.canceled = cancel
	rec := s.recorder
	stream := s.stream
	stopTick := s.stopTick
	s.stopTick = nil
	s.mu.Unlock()

	if stopTick != nil {
		close(stopTick)
	}
	if rec.Truthy() && rec.Get("state").String() != "inactive" {
		rec.Call("stop")
	}
	stopMediaStreamTracks(stream)
}

func (s *webVoiceSession) finishBlob(transcript *mygoshell.TranscriptController, invalidate func()) {
	s.mu.Lock()
	canceled := s.canceled
	chunks := s.chunks
	mime := s.mimeType
	stream := s.stream
	s.recorder = js.Undefined()
	s.stream = js.Undefined()
	s.chunks = nil
	s.mu.Unlock()

	stopMediaStreamTracks(stream)
	if canceled || len(chunks) == 0 {
		invalidate()
		return
	}

	jsChunks := js.Global().Get("Array").New(len(chunks))
	for i, ch := range chunks {
		jsChunks.SetIndex(i, ch)
	}
	blobOpts := js.Global().Get("Object").New()
	blobOpts.Set("type", mime)
	blob := js.Global().Get("Blob").New(jsChunks, blobOpts)
	ext := "webm"
	if strings.Contains(mime, "mp4") {
		ext = "m4a"
	}
	filename := fmt.Sprintf("web-voice-%s.%s", time.Now().Format("150405"), ext)

	var onBuf, onErr js.Func
	onBuf = js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer onBuf.Release()
		defer onErr.Release()
		if len(args) == 0 || !args[0].Truthy() {
			return nil
		}
		u8 := js.Global().Get("Uint8Array").New(args[0])
		size := u8.Get("byteLength").Int()
		if size <= 0 || size > webMaxAudioBytes {
			if transcript.Voice != nil {
				transcript.Voice.Fail(composer.ErrAttachmentTooLarge)
			}
			invalidate()
			return nil
		}
		buf := make([]byte, size)
		js.CopyBytesToGo(buf, u8)
		_, _ = transcript.CompleteVoiceAudio(filename, buf)
		invalidate()
		return nil
	})
	onErr = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		defer onBuf.Release()
		defer onErr.Release()
		if transcript.Voice != nil {
			transcript.Voice.Fail(errors.New("failed to read recorded audio buffer"))
		}
		invalidate()
		return nil
	})
	blob.Call("arrayBuffer").Call("then", onBuf, onErr)
}

func stopMediaStreamTracks(stream js.Value) {
	if !stream.Truthy() {
		return
	}
	tracks := stream.Call("getTracks")
	for i := 0; i < tracks.Length(); i++ {
		tracks.Index(i).Call("stop")
	}
}

func openBrowserFilePicker(transcript *mygoshell.TranscriptController, invalidate func()) {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return
	}
	input := doc.Call("createElement", "input")
	input.Set("type", "file")
	input.Set("multiple", true)
	input.Set("accept", "image/*,application/pdf,audio/*,.go,.rs,.ts,.tsx,.md,.org,.json,.yaml,.txt")

	var onChange js.Func
	onChange = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		defer onChange.Release()
		files := input.Get("files")
		if !files.Truthy() {
			return nil
		}
		for i := 0; i < files.Length(); i++ {
			file := files.Index(i)
			name := file.Get("name").String()
			mime := file.Get("type").String()
			size := file.Get("size").Int()
			if size <= 0 || size > webMaxFileBytes {
				continue
			}
			var onRead js.Func
			onRead = js.FuncOf(func(_ js.Value, args []js.Value) any {
				defer onRead.Release()
				if len(args) == 0 || !args[0].Truthy() {
					return nil
				}
				u8 := js.Global().Get("Uint8Array").New(args[0])
				buf := make([]byte, u8.Get("byteLength").Int())
				js.CopyBytesToGo(buf, u8)
				_, _ = transcript.AttachFile(composer.AttachmentInput{
					Filename: name,
					MIMEType: mime,
					Data:     buf,
				})
				invalidate()
				return nil
			})
			file.Call("arrayBuffer").Call("then", onRead)
		}
		return nil
	})
	input.Set("onchange", onChange)
	input.Call("click")
}

func readBrowserClipboard(transcript *mygoshell.TranscriptController, invalidate func()) {
	clip := js.Global().Get("navigator").Get("clipboard")
	if !clip.Truthy() || !clip.Get("readText").Truthy() {
		return
	}
	var onText, onErr js.Func
	onText = js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer onText.Release()
		defer onErr.Release()
		if len(args) == 0 {
			return nil
		}
		txt := strings.TrimSpace(args[0].String())
		if txt == "" {
			return nil
		}
		_, _ = transcript.AttachFile(composer.AttachmentInput{
			Filename: fmt.Sprintf("clipboard-%s.txt", time.Now().Format("150405")),
			MIMEType: "text/plain",
			Data:     []byte(txt),
		})
		invalidate()
		return nil
	})
	onErr = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		defer onText.Release()
		defer onErr.Release()
		return nil
	})
	clip.Call("readText").Call("then", onText, onErr)
}
