// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package composer

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

func TestReadLocalFileAndStageLocalFilesFailClosed(t *testing.T) {
	dir := t.TempDir()

	// 1. Valid PNG + Go source files stage cleanly with SHA-256 URIs.
	img := image.NewRGBA(image.Rect(0, 0, 16, 12))
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	pngPath := filepath.Join(dir, "capture.png")
	if err := os.WriteFile(pngPath, pngBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	goPath := filepath.Join(dir, "handler.go")
	if err := os.WriteFile(goPath, []byte("package demo\nfunc Run() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 2. Oversized file exceeding custom 128-byte cap must fail closed before full buffer.
	bigPath := filepath.Join(dir, "huge.txt")
	if err := os.WriteFile(bigPath, bytes.Repeat([]byte("A"), 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	// 3. Directory path must fail closed.
	subDir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subDir, 0o700); err != nil {
		t.Fatal(err)
	}

	policy := AttachmentPolicy{
		MaxCount:          8,
		MaxBinaryBytes:    1024,
		MaxTextBytes:      1024,
		MaxTotalTurnBytes: 4096,
	}
	st := NewState(policy)
	refs, errs := StageLocalFiles(st, []string{pngPath, goPath, bigPath, subDir})
	if len(refs) != 2 {
		t.Fatalf("expected 2 staged refs, got %d (errs=%v)", len(refs), errs)
	}
	if len(errs) != 2 {
		t.Fatalf("expected 2 fail-closed errors for oversized file and directory, got %v", errs)
	}
	if refs[0].Width != 16 || refs[0].Height != 12 || refs[1].Kind != AttachmentCode {
		t.Fatalf("unexpected staged metadata: %+v", refs)
	}
}

func FuzzValidateAndStageAttachment(f *testing.F) {
	// Seed valid multimodal payloads + adversarial/malformed payloads.
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	f.Add("ok.png", "image/png", pngBuf.Bytes())
	f.Add("doc.pdf", "application/pdf", []byte("%PDF-1.7\n1 0 obj\nendobj\n"))
	f.Add("clip.wav", "audio/wav", append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 24)...))
	f.Add("main.go", "text/plain", []byte("package main\nfunc main(){}\n"))
	f.Add("../../etc/passwd.png", "image/png", []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00})
	f.Add("xss.svg", "image/svg+xml", []byte(`<svg onload="alert(1)"></svg>`))
	f.Add("page.html", "text/html", []byte(`<!doctype html><script>alert(1)</script>`))
	f.Add("bad.jpg", "image/jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00})
	f.Add("bad.webp", "image/webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "))
	f.Add("nul.txt", "text/plain", []byte("hello\x00world"))

	policy := AttachmentPolicy{
		MaxCount:          8,
		MaxBinaryBytes:    64 * 1024,
		MaxTextBytes:      64 * 1024,
		MaxTotalTurnBytes: 256 * 1024,
	}
	f.Fuzz(func(t *testing.T, filename, mime string, data []byte) {
		staged, err := ValidateAndStageAttachment(AttachmentInput{
			Filename: filename,
			MIMEType: mime,
			Data:     data,
		}, policy)
		if err != nil {
			return
		}
		// Security invariants for EVERY admitted attachment:
		if staged.Ref.Name == "" || strings.ContainsAny(staged.Ref.Name, `/\`) || strings.Contains(staged.Ref.Name, "..") {
			t.Fatalf("unsafe filename admitted: %q", staged.Ref.Name)
		}
		if len(staged.Ref.SHA256Hex) != 64 || !strings.HasPrefix(staged.Ref.ArtifactURI, "marlin-artifact://") {
			t.Fatalf("invalid CAS URI/digest: %+v", staged.Ref)
		}
		if staged.Ref.MIMEType == "image/svg+xml" || staged.Ref.MIMEType == "text/html" {
			t.Fatalf("XSS MIME type admitted: %q", staged.Ref.MIMEType)
		}
		if (len(data) >= 4 && (bytes.HasPrefix(data, []byte{0x7f, 'E', 'L', 'F'}) ||
			bytes.HasPrefix(data, []byte{0xCF, 0xFA, 0xED, 0xFE}))) ||
			(len(data) >= 64 && bytes.HasPrefix(data, []byte{'M', 'Z'})) {
			t.Fatalf("executable magic admitted as %s (%s)", staged.Ref.Kind, staged.Ref.MIMEType)
		}
	})
}

func FuzzSanitizeFilename(f *testing.F) {
	f.Add("../../etc/passwd")
	f.Add(`C:\Windows\System32\cmd.exe`)
	f.Add("hello\x00\r\nworld.png")
	f.Add("...")
	f.Add(strings.Repeat("a", 500) + ".tar.gz")
	f.Fuzz(func(t *testing.T, raw string) {
		out := SanitizeFilename(raw)
		if out == "" || len(out) > 128 {
			t.Fatalf("SanitizeFilename length violation: %q (len=%d)", out, len(out))
		}
		if strings.ContainsAny(out, `/\`) || strings.Contains(out, "..") {
			t.Fatalf("SanitizeFilename path traversal leak: %q", out)
		}
		for _, r := range out {
			if unicode.IsControl(r) {
				t.Fatalf("SanitizeFilename control rune leak: %q", out)
			}
		}
	})
}

func FuzzActiveMentionQuery(f *testing.F) {
	f.Add("review @client.go")
	f.Add("email user@example.com")
	f.Add("@")
	f.Add("check @git:diff")
	f.Fuzz(func(t *testing.T, draft string) {
		q, ok := ActiveMentionQuery(draft)
		if ok {
			stripped := StripTrailingMentionToken(draft)
			if len(stripped) > len(draft) {
				t.Fatalf("StripTrailingMentionToken grew input: %q -> %q (q=%q)", draft, stripped, q)
			}
		}
	})
}
