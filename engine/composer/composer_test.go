// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package composer_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/tao3k/marlin-mygo/engine/composer"
)

func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 88, G: 166, B: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMultimodalAttachmentValidationSecurityAndDeduplication(t *testing.T) {
	st := composer.NewState(composer.AttachmentPolicy{
		MaxCount:          4,
		MaxBinaryBytes:    1 << 20,
		MaxTextBytes:      64 << 10,
		MaxTotalTurnBytes: 2 << 20,
	})

	// 1. Real PNG image decodes exact dimensions and generates marlin-artifact://<sha256>!
	pngBytes := makeTestPNG(t, 320, 180)
	imgRef, err := st.AddAttachment(composer.AttachmentInput{
		Filename: "../../evil/path/screenshot.png",
		MIMEType: "image/png",
		Data:     pngBytes,
	})
	if err != nil {
		t.Fatalf("expected valid PNG attachment to succeed, got %v", err)
	}
	if imgRef.Kind != composer.AttachmentImage || imgRef.Width != 320 || imgRef.Height != 180 {
		t.Fatalf("unexpected PNG metadata: %+v", imgRef)
	}
	if imgRef.Name != "screenshot.png" || !strings.HasPrefix(imgRef.ArtifactURI, "marlin-artifact://") {
		t.Fatalf("expected sanitized filename and CAS URI, got %+v", imgRef)
	}

	// Attaching identical PNG bytes deduplicates without consuming extra slot or byte budget!
	dupRef, err := st.AddAttachment(composer.AttachmentInput{Filename: "copy.png", Data: pngBytes})
	if err != nil || dupRef.SHA256Hex != imgRef.SHA256Hex || len(st.Snapshot().Attachments) != 1 {
		t.Fatalf("expected SHA-256 deduplication to keep 1 attachment, got ref=%+v len=%d", dupRef, len(st.Snapshot().Attachments))
	}

	// 2. Valid PDF, WAV audio, and Go source code all stage with accurate metadata!
	pdfRef, err := st.AddAttachment(composer.AttachmentInput{
		Filename: "architecture.pdf",
		Data:     []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n"),
	})
	if err != nil || pdfRef.Kind != composer.AttachmentPDF {
		t.Fatalf("expected valid PDF attachment, got ref=%+v err=%v", pdfRef, err)
	}

	wavRef, err := st.AddAttachment(composer.AttachmentInput{
		Filename: "voice-note.wav",
		Data:     append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 32)...),
	})
	if err != nil || wavRef.Kind != composer.AttachmentAudio {
		t.Fatalf("expected valid WAV audio attachment, got ref=%+v err=%v", wavRef, err)
	}

	codeRef, err := st.AddAttachment(composer.AttachmentInput{
		Filename: "client.go",
		Data:     []byte("package grpcwire\n\nfunc Dial() {}\n"),
	})
	if err != nil || codeRef.Kind != composer.AttachmentCode || codeRef.LineCount != 4 {
		t.Fatalf("expected valid Go code attachment (4 lines), got ref=%+v err=%v", codeRef, err)
	}

	// 3. Adversarial payloads MUST fail-closed!
	// 3a. Mach-O / ELF binary renamed to `.png`!
	if _, err := st.AddAttachment(composer.AttachmentInput{
		Filename: "trojan.png",
		MIMEType: "image/png",
		Data:     []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0},
	}); !errors.Is(err, composer.ErrAttachmentMIMEBlocked) {
		t.Fatalf("expected ELF binary disguised as PNG to be blocked, got %v", err)
	}
	// 3b. SVG XSS blocked fail-closed!
	if _, err := st.AddAttachment(composer.AttachmentInput{
		Filename: "xss.svg",
		MIMEType: "image/svg+xml",
		Data:     []byte(`<svg onload="alert(1)"/>`),
	}); !errors.Is(err, composer.ErrAttachmentMIMEBlocked) {
		t.Fatalf("expected SVG XSS to be blocked, got %v", err)
	}
	// 3c. Spoofed PDF with fake text bytes fails magic-byte check!
	if _, err := st.AddAttachment(composer.AttachmentInput{
		Filename: "fake.pdf",
		MIMEType: "application/pdf",
		Data:     []byte("not really a pdf"),
	}); !errors.Is(err, composer.ErrAttachmentMagicBad) {
		t.Fatalf("expected ErrAttachmentMagicBad for spoofed PDF, got %v", err)
	}
	// 3d. Invalid binary NUL byte inside code file rejected!
	if _, err := st.AddAttachment(composer.AttachmentInput{
		Filename: "bad.go",
		MIMEType: "text/plain",
		Data:     []byte("package x\x00evil"),
	}); !errors.Is(err, composer.ErrAttachmentInvalidUTF8) {
		t.Fatalf("expected ErrAttachmentInvalidUTF8 for NUL byte in text, got %v", err)
	}
}

func TestSlashCommandsMentionsAndTurnSubmissionReset(t *testing.T) {
	st := composer.NewState(composer.DefaultAttachmentPolicy())

	// Slash command fuzzy matching.
	matches := st.MatchSlashCommands("/pl")
	if len(matches) == 0 || matches[0].Name != "/plan" {
		t.Fatalf("expected /plan match for '/pl', got %+v", matches)
	}
	if len(st.MatchSlashCommands("hello /plan")) != 0 {
		t.Fatal("expected slash popup only when draft starts with /")
	}

	// Trailing @-mention extraction & stripping.
	q, ok := composer.ActiveMentionQuery("inspect @grpc")
	if !ok || q != "grpc" {
		t.Fatalf("expected active mention query 'grpc', got %q ok=%v", q, ok)
	}
	if stripped := composer.StripTrailingMentionToken("inspect @grpc"); stripped != "inspect" {
		t.Fatalf("expected 'inspect', got %q", stripped)
	}

	// Stage attachment + mention + cycle model & effort -> BuildAndResetSubmission packages cleanly!
	pngBytes := makeTestPNG(t, 64, 64)
	_, err := st.AddAttachment(composer.AttachmentInput{Filename: "ui.png", Data: pngBytes})
	if err != nil {
		t.Fatal(err)
	}
	st.AddMention(composer.ContextMention{Kind: "file", Target: "control/grpcwire/client.go", Label: "client.go"})
	st.CycleModel()
	st.CycleEffort()

	sub, ok := st.BuildAndResetSubmission("Audit this screenshot and client.go")
	if !ok {
		t.Fatal("expected valid TurnSubmission")
	}
	if len(sub.Attachments) != 1 || len(sub.Mentions) != 1 || sub.ModelID != "marlin-fast" || sub.ReasoningEffort != composer.EffortDeep {
		t.Fatalf("unexpected TurnSubmission: %+v", sub)
	}
	// Staged buffers MUST be reset after submission.
	snap := st.Snapshot()
	if len(snap.Attachments) != 0 || len(snap.Mentions) != 0 || snap.TotalBytes != 0 {
		t.Fatalf("expected composer state cleared after submission, got %+v", snap)
	}
}
