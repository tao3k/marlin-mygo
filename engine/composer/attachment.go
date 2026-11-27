// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package composer implements Marlin's multimodal turn-composer state engine:
// fail-closed attachment validation (magic-byte sniffing, image dimension decoding,
// SHA-256 content-addressed deduplication, dangerous MIME blocking), structured
// @-context mentions, /-slash command completion, and model/reasoning selection.
package composer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// AttachmentKind classifies the multimodal media family of an attached artifact.
type AttachmentKind string

const (
	AttachmentImage AttachmentKind = "image"
	AttachmentPDF   AttachmentKind = "pdf"
	AttachmentAudio AttachmentKind = "audio"
	AttachmentCode  AttachmentKind = "code"
	AttachmentText  AttachmentKind = "text"
)

// AttachmentStatus tracks the upload/staging lifecycle to the Rust CAS backend.
type AttachmentStatus string

const (
	StatusReady     AttachmentStatus = "ready"
	StatusUploading AttachmentStatus = "uploading"
	StatusUploaded  AttachmentStatus = "uploaded"
	StatusFailed    AttachmentStatus = "failed"
)

var (
	ErrAttachmentEmpty        = errors.New("composer: attachment payload is empty")
	ErrAttachmentTooLarge     = errors.New("composer: attachment exceeds maximum byte budget")
	ErrAttachmentTotalBudget  = errors.New("composer: turn attachments exceed total byte budget")
	ErrAttachmentLimitReached = errors.New("composer: maximum attachments per turn reached")
	ErrAttachmentMIMEBlocked  = errors.New("composer: attachment media type is blocked by security policy")
	ErrAttachmentMagicBad     = errors.New("composer: attachment content bytes do not match declared media type")
	ErrAttachmentInvalidUTF8  = errors.New("composer: text/code attachment must be valid UTF-8 without NUL bytes")
)

// AttachmentPolicy configures fail-closed size and media constraints.
type AttachmentPolicy struct {
	MaxCount          int // default 10 attachments per turn
	MaxBinaryBytes    int // default 20 MiB per image/PDF/audio
	MaxTextBytes      int // default 2 MiB per text/code file
	MaxTotalTurnBytes int // default 48 MiB combined across all turn attachments
}

// DefaultAttachmentPolicy returns production safety ceilings for multimodal turns.
func DefaultAttachmentPolicy() AttachmentPolicy {
	return AttachmentPolicy{
		MaxCount:          10,
		MaxBinaryBytes:    20 << 20, // 20 MiB
		MaxTextBytes:      2 << 20,  // 2 MiB
		MaxTotalTurnBytes: 48 << 20, // 48 MiB
	}
}

func (p AttachmentPolicy) withDefaults() AttachmentPolicy {
	def := DefaultAttachmentPolicy()
	if p.MaxCount <= 0 {
		p.MaxCount = def.MaxCount
	}
	if p.MaxBinaryBytes <= 0 {
		p.MaxBinaryBytes = def.MaxBinaryBytes
	}
	if p.MaxTextBytes <= 0 {
		p.MaxTextBytes = def.MaxTextBytes
	}
	if p.MaxTotalTurnBytes <= 0 {
		p.MaxTotalTurnBytes = def.MaxTotalTurnBytes
	}
	return p
}

// AttachmentRef is the lightweight, immutable, content-addressed descriptor
// rendered in the Tier-0 UI and forwarded to the Rust App Server on Send.
// Raw binary bytes are kept out of UI snapshots and addressed via SHA-256 URI.
type AttachmentRef struct {
	ID           string
	Name         string
	Kind         AttachmentKind
	MIMEType     string
	ByteSize     int
	SHA256Hex    string
	ArtifactURI  string // marlin-artifact://<sha256-hex>
	Width        int    // populated for AttachmentImage
	Height       int    // populated for AttachmentImage
	LineCount    int    // populated for AttachmentCode / AttachmentText
	PreviewText  string // bounded excerpt for code/text attachments
	Status       AttachmentStatus
	Progress     float32 // 0.0..1.0 while StatusUploading
	ErrorMessage string
}

// ShortDigest returns the first 8 hex characters of the SHA-256 digest for compact pills.
func (a AttachmentRef) ShortDigest() string {
	if len(a.SHA256Hex) >= 8 {
		return a.SHA256Hex[:8]
	}
	return a.SHA256Hex
}

// FormatByteSize renders a human-readable size badge (e.g. "420 B", "142 KB", "3.4 MB").
func FormatByteSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// AttachmentInput carries raw bytes from a file picker, drag-drop, or clipboard paste.
type AttachmentInput struct {
	Filename string
	MIMEType string // optional; inferred from magic bytes / extension if empty
	Data     []byte
}

// StagedAttachment pairs the immutable UI metadata with its verified bytes
// for handing off to the Rust CAS uploader on submit.
type StagedAttachment struct {
	Ref  AttachmentRef
	Data []byte
}

var blockedMIMEs = map[string]bool{
	"text/html":                    true,
	"application/xhtml+xml":        true,
	"image/svg+xml":                true, // blocks embedded SVG script XSS
	"application/javascript":       true,
	"text/javascript":              true,
	"application/x-executable":     true,
	"application/x-mach-binary":    true,
	"application/x-dosexec":        true,
	"application/x-msdownload":     true,
	"application/x-sh":             true,
	"application/vnd.microsoft.portable-executable": true,
}

var codeExtensions = map[string]bool{
	".go": true, ".rs": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".py": true, ".ss": true, ".scm": true, ".c": true, ".h": true, ".cpp": true,
	".hpp": true, ".java": true, ".kt": true, ".swift": true, ".sh": true,
	".toml": true, ".yaml": true, ".yml": true, ".json": true, ".sql": true,
	".proto": true, ".nix": true, ".diff": true, ".patch": true, ".css": true,
}

// ValidateAndStageAttachment performs fail-closed security validation:
//   - Sanitizes filename (strips path traversal & control chars)
//   - Rejects blocked/executable/XSS MIME types & executable magic headers (ELF/Mach-O/MZ)
//   - Verifies magic bytes match image/PDF/audio declarations
//   - Decodes image dimensions (PNG/JPEG/GIF/WebP)
//   - Validates UTF-8 & counts lines for text/code attachments
//   - Computes SHA-256 content-addressed URI (`marlin-artifact://<sha256>`)
func ValidateAndStageAttachment(in AttachmentInput, policy AttachmentPolicy) (StagedAttachment, error) {
	policy = policy.withDefaults()
	if len(in.Data) == 0 {
		return StagedAttachment{}, ErrAttachmentEmpty
	}

	safeName := SanitizeFilename(in.Filename)
	if safeName == "" {
		safeName = "attachment"
	}

	// Hard fail-closed check against executable binaries regardless of claimed filename or MIME!
	if looksLikeExecutableBinary(in.Data) {
		return StagedAttachment{}, ErrAttachmentMIMEBlocked
	}

	declaredMIME := normalizeMIME(in.MIMEType)
	if blockedMIMEs[declaredMIME] {
		return StagedAttachment{}, ErrAttachmentMIMEBlocked
	}

	kind, canonicalMIME, width, height, err := classifyAndVerifyPayload(safeName, declaredMIME, in.Data)
	if err != nil {
		return StagedAttachment{}, err
	}

	maxBytes := policy.MaxBinaryBytes
	if kind == AttachmentCode || kind == AttachmentText {
		maxBytes = policy.MaxTextBytes
	}
	if len(in.Data) > maxBytes {
		return StagedAttachment{}, fmt.Errorf("%w (%s > %s)", ErrAttachmentTooLarge, FormatByteSize(len(in.Data)), FormatByteSize(maxBytes))
	}

	var lineCount int
	var preview string
	if kind == AttachmentCode || kind == AttachmentText {
		if bytes.IndexByte(in.Data, 0) >= 0 || !utf8.Valid(in.Data) {
			return StagedAttachment{}, ErrAttachmentInvalidUTF8
		}
		lineCount = bytes.Count(in.Data, []byte{'\n'}) + 1
		preview = buildTextPreview(string(in.Data), 1200)
	}

	sum := sha256.Sum256(in.Data)
	digest := hex.EncodeToString(sum[:])

	copied := make([]byte, len(in.Data))
	copy(copied, in.Data)

	ref := AttachmentRef{
		ID:          "att-" + digest[:12],
		Name:        safeName,
		Kind:        kind,
		MIMEType:    canonicalMIME,
		ByteSize:    len(copied),
		SHA256Hex:   digest,
		ArtifactURI: "marlin-artifact://" + digest,
		Width:       width,
		Height:      height,
		LineCount:   lineCount,
		PreviewText: preview,
		Status:      StatusReady,
	}
	return StagedAttachment{Ref: ref, Data: copied}, nil
}

// SanitizeFilename strips directory paths, traversal sequences, and control bytes.
func SanitizeFilename(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "attachment"
	}
	s = strings.ReplaceAll(s, "\\", "/")
	s = filepath.Base(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			continue
		}
		if b.Len()+utf8.RuneLen(r) > 120 {
			break
		}
		b.WriteRune(r)
	}
	out := b.String()
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", ".")
	}
	out = strings.Trim(out, " .")
	if out == "" {
		return "attachment"
	}
	return out
}

func normalizeMIME(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if semi := strings.IndexByte(s, ';'); semi >= 0 {
		s = strings.TrimSpace(s[:semi])
	}
	return s
}

func looksLikeExecutableBinary(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	// ELF binary (\x7fELF)
	if bytes.HasPrefix(data, []byte{0x7f, 'E', 'L', 'F'}) {
		return true
	}
	// Windows PE / DOS MZ executable
	if data[0] == 'M' && data[1] == 'Z' && len(data) >= 64 {
		return true
	}
	// Mach-O 32/64 & universal fat binaries
	m := binary.BigEndian.Uint32(data[:4])
	switch m {
	case 0xFEEDFACE, 0xCEFAEDFE, 0xFEEDFACF, 0xCFFAEDFE, 0xCAFEBABE, 0xBEBAFECA:
		return true
	}
	return false
}

func classifyAndVerifyPayload(name, declaredMIME string, data []byte) (AttachmentKind, string, int, int, error) {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".svg" || ext == ".html" || ext == ".htm" || ext == ".exe" || ext == ".dll" || ext == ".dylib" {
		return "", "", 0, 0, ErrAttachmentMIMEBlocked
	}

	// Infer MIME from magic bytes / extension if not declared.
	mime := declaredMIME
	if mime == "" {
		mime = sniffSafeMIME(ext, data)
	}
	if blockedMIMEs[mime] {
		return "", "", 0, 0, ErrAttachmentMIMEBlocked
	}

	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		w, h, ok := verifyAndDecodeImage(mime, data)
		if !ok {
			return "", "", 0, 0, fmt.Errorf("%w: invalid %s header", ErrAttachmentMagicBad, mime)
		}
		return AttachmentImage, mime, w, h, nil

	case "application/pdf":
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return "", "", 0, 0, fmt.Errorf("%w: missing %%PDF- header", ErrAttachmentMagicBad)
		}
		return AttachmentPDF, "application/pdf", 0, 0, nil

	case "audio/wav", "audio/mpeg", "audio/ogg", "audio/mp4", "audio/webm":
		if !verifyAudioMagic(mime, data) {
			return "", "", 0, 0, fmt.Errorf("%w: invalid %s header", ErrAttachmentMagicBad, mime)
		}
		return AttachmentAudio, mime, 0, 0, nil

	case "text/plain", "text/markdown", "text/csv", "text/x-diff", "application/json", "application/toml", "text/x-code":
		if codeExtensions[ext] || mime == "application/json" || mime == "text/x-diff" || mime == "application/toml" || mime == "text/x-code" {
			return AttachmentCode, mime, 0, 0, nil
		}
		return AttachmentText, mime, 0, 0, nil
	}

	return "", "", 0, 0, fmt.Errorf("%w: %q", ErrAttachmentMIMEBlocked, mime)
}

func sniffSafeMIME(ext string, data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return "image/png"
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return "application/pdf"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE")):
		return "audio/wav"
	case bytes.HasPrefix(data, []byte("OggS")):
		return "audio/ogg"
	case bytes.HasPrefix(data, []byte("ID3")) || (len(data) >= 2 && data[0] == 0xFF && (data[1]&0xE0) == 0xE0):
		return "audio/mpeg"
	}
	switch ext {
	case ".md", ".markdown":
		return "text/markdown"
	case ".csv":
		return "text/csv"
	case ".diff", ".patch":
		return "text/x-diff"
	case ".json":
		return "application/json"
	case ".toml":
		return "application/toml"
	case ".txt", ".org", ".log":
		return "text/plain"
	}
	if codeExtensions[ext] {
		return "text/x-code"
	}
	if bytes.IndexByte(data, 0) < 0 && utf8.Valid(data) {
		return "text/plain"
	}
	return "application/octet-stream"
}

func verifyAndDecodeImage(mime string, data []byte) (int, int, bool) {
	switch mime {
	case "image/png":
		if !bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
			return 0, 0, false
		}
	case "image/jpeg":
		if len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 || data[2] != 0xFF {
			return 0, 0, false
		}
	case "image/gif":
		if !bytes.HasPrefix(data, []byte("GIF87a")) && !bytes.HasPrefix(data, []byte("GIF89a")) {
			return 0, 0, false
		}
	case "image/webp":
		return decodeWebPDimensions(data)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

func decodeWebPDimensions(data []byte) (int, int, bool) {
	if len(data) < 30 || !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WEBP")) {
		return 0, 0, false
	}
	chunk := string(data[12:16])
	switch chunk {
	case "VP8X":
		w := 1 + int(data[24]) | int(data[25])<<8 | int(data[26])<<16
		h := 1 + int(data[27]) | int(data[28])<<8 | int(data[29])<<16
		return w, h, w > 0 && h > 0
	case "VP8 ":
		w := int(binary.LittleEndian.Uint16(data[26:28])) & 0x3FFF
		h := int(binary.LittleEndian.Uint16(data[28:30])) & 0x3FFF
		return w, h, w > 0 && h > 0
	case "VP8L":
		bits := binary.LittleEndian.Uint32(data[21:25])
		w := int(bits&0x3FFF) + 1
		h := int((bits>>14)&0x3FFF) + 1
		return w, h, w > 0 && h > 0
	default:
		return 0, 0, false
	}
}

func verifyAudioMagic(mime string, data []byte) bool {
	switch mime {
	case "audio/wav":
		return len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE"))
	case "audio/ogg":
		return bytes.HasPrefix(data, []byte("OggS"))
	case "audio/mpeg":
		return bytes.HasPrefix(data, []byte("ID3")) || (len(data) >= 2 && data[0] == 0xFF && (data[1]&0xE0) == 0xE0)
	case "audio/mp4":
		return len(data) >= 8 && bytes.Equal(data[4:8], []byte("ftyp"))
	case "audio/webm":
		return len(data) >= 4 && data[0] == 0x1A && data[1] == 0x45 && data[2] == 0xDF && data[3] == 0xA3
	default:
		return false
	}
}

func buildTextPreview(text string, maxRunes int) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	trimmed = strings.ReplaceAll(trimmed, "\r", "\n")
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes-1]) + "…"
	}
	return trimmed
}
