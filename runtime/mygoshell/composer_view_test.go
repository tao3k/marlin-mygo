// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 10, G: 132, B: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode failed: %v", err)
	}
	return buf.Bytes()
}

func makeTestWAV() []byte {
	wav := make([]byte, 44)
	copy(wav[0:4], "RIFF")
	copy(wav[8:12], "WAVE")
	copy(wav[12:16], "fmt ")
	return wav
}

func TestMultimodalComposer_AttachmentsSlashMentionsModelAndSubmit(t *testing.T) {
	tc := mygoshell.NewTranscriptController()

	// 1. Stage PNG image, PDF document, WAV audio, and Go source file.
	pngRef, err := tc.AttachFile(composer.AttachmentInput{
		Filename: "../../screenshots/architecture.png",
		Data:     makeTestPNG(t, 320, 180),
	})
	if err != nil {
		t.Fatalf("attach PNG failed: %v", err)
	}
	if pngRef.Name != "architecture.png" || pngRef.Width != 320 || pngRef.Height != 180 {
		t.Fatalf("unexpected PNG ref: %+v", pngRef)
	}

	pdfRef, err := tc.AttachFile(composer.AttachmentInput{
		Filename: "rfc-design.pdf",
		Data:     []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"),
	})
	if err != nil {
		t.Fatalf("attach PDF failed: %v", err)
	}
	if pdfRef.Kind != composer.AttachmentPDF {
		t.Fatalf("expected PDF kind, got %s", pdfRef.Kind)
	}

	if _, err := tc.AttachFile(composer.AttachmentInput{
		Filename: "voice-memo.wav",
		Data:     makeTestWAV(),
	}); err != nil {
		t.Fatalf("attach WAV failed: %v", err)
	}

	if _, err := tc.AttachFile(composer.AttachmentInput{
		Filename: "client.go",
		Data:     []byte("package grpcwire\n\nfunc Ping() bool { return true }\n"),
	}); err != nil {
		t.Fatalf("attach Go file failed: %v", err)
	}

	// Verify blocked SVG XSS sets visible fail-closed error in composer without removing staged files.
	if _, err := tc.AttachFile(composer.AttachmentInput{
		Filename: "exploit.svg",
		Data:     []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`),
	}); err == nil {
		t.Fatal("expected SVG XSS attachment to be rejected")
	}

	var submitted []composer.TurnSubmission
	var slashPicked []composer.SlashCommand
	var pastedClicks int
	var copiedSnippets []string
	tc.OnSubmitTurn = func(sub composer.TurnSubmission) {
		submitted = append(submitted, sub)
	}
	tc.OnSlashCommand = func(cmd composer.SlashCommand) {
		slashPicked = append(slashPicked, cmd)
	}
	tc.OnPasteAttachment = func() {
		pastedClicks++
	}
	tc.OnCopyCode = func(code string) {
		copiedSnippets = append(copiedSnippets, code)
	}
	tc.OnQueryMentions = func(query string) []composer.ContextMention {
		if strings.Contains("client.go", strings.ToLower(query)) {
			return []composer.ContextMention{
				{Kind: "file", Target: "control/grpcwire/client.go", Label: "client.go"},
			}
		}
		return nil
	}

	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		tc.Render(c, theme, nil, nil)
	}, 1100, 760)
	tt.Frame()

	// Assert all 4 multimodal attachment chips + pixel dims + short SHA-256 + fail-closed error banner render.
	for _, want := range []string{
		"architecture.png", "IMAGE", "320x180", "#" + pngRef.ShortDigest(),
		"rfc-design.pdf", "PDF",
		"voice-memo.wav", "AUDIO",
		"client.go", "CODE",
		"blocked",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected multimodal composer to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// 1b. Click Paste button and verify OnPasteAttachment fires; click attachment chip to open Lightbox Preview!
	if err := tt.Click("Paste"); err != nil {
		t.Fatalf("click Paste attachment button failed: %v", err)
	}
	if pastedClicks != 1 {
		t.Fatalf("expected 1 Paste click, got %d", pastedClicks)
	}
	if err := tt.Click("Inspect client.go"); err != nil {
		t.Fatalf("click Inspect client.go failed: %v", err)
	}
	tt.Frame()
	if !tc.IsAttachmentPreviewOpen() || !tt.HasText("MAGIC VERIFIED · FAIL-CLOSED") || !tt.HasText("1 | package grpcwire") {
		t.Fatalf("expected Attachment Preview Lightbox with numbered Go code, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Copy Snippet"); err != nil {
		t.Fatalf("click Copy Snippet in Lightbox failed: %v", err)
	}
	if len(copiedSnippets) != 1 || !strings.Contains(copiedSnippets[0], "func Ping()") {
		t.Fatalf("expected Copy Snippet to copy Go preview, got %+v", copiedSnippets)
	}
	if err := tt.Click("Close Preview"); err != nil {
		t.Fatalf("click Close Preview failed: %v", err)
	}
	tt.Frame()
	if tc.IsAttachmentPreviewOpen() {
		t.Fatal("expected Attachment Preview Lightbox closed")
	}

	// 2. Test inline `/` Slash Command popup and click `/auth`.
	tc.SetComposerText("/au")
	tt.Frame()
	if !tt.HasText("SLASH COMMANDS") {
		t.Fatalf("expected SLASH COMMANDS popup when typing /au, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("/auth"); err != nil {
		t.Fatalf("click /auth slash command failed: %v", err)
	}
	tt.Frame()
	if len(slashPicked) != 1 || slashPicked[0].ActionID != "studio.open-auth" {
		t.Fatalf("expected /auth to invoke studio.open-auth, got %+v", slashPicked)
	}

	// 3. Test inline `@` Context Mention popup, pick `@file:client.go`, verify token stripped + chip added.
	tc.SetComposerText("Review @cl")
	tt.Frame()
	if !tt.HasText("ATTACH CONTEXT (@)") {
		t.Fatalf("expected ATTACH CONTEXT (@) popup when typing @cl, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("@file:client.go"); err != nil {
		t.Fatalf("click @file:client.go mention failed: %v", err)
	}
	tt.Frame()
	if got := tc.ComposerText(); got != "Review" {
		t.Fatalf("expected trailing @cl stripped from draft, got %q", got)
	}
	if !tt.HasText("@file:client.go") {
		t.Fatalf("expected @file:client.go mention chip rendered above input, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 4. Cycle Model selector pill (`Marlin Core` -> `Marlin Fast`) and submit full multimodal turn.
	if err := tt.Click("Marlin Core"); err != nil {
		t.Fatalf("click model selector pill 'Marlin Core' failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Marlin Fast") {
		t.Fatalf("expected model selector to cycle to 'Marlin Fast', got %q", strings.Join(tt.Texts(), " | "))
	}

	if err := tt.Click("Send"); err != nil {
		t.Fatalf("click Send failed: %v", err)
	}
	tt.Frame()

	if len(submitted) != 1 {
		t.Fatalf("expected 1 typed TurnSubmission, got %d", len(submitted))
	}
	sub := submitted[0]
	if sub.Prompt != "Review" || sub.ModelID != "marlin-fast" || len(sub.Attachments) != 4 || len(sub.Mentions) != 1 {
		t.Fatalf("unexpected TurnSubmission payload: %+v", sub)
	}
	if snap := tc.Composer.Snapshot(); len(snap.Attachments) != 0 || len(snap.Mentions) != 0 {
		t.Fatalf("expected composer attachments & mentions reset after Send, got %+v", snap)
	}

	// 5. Render User Bubble with read-only multimodal attachment chips & verify clicking User Bubble chip opens Image Lightbox + ESC closes it.
	refs := make([]composer.AttachmentRef, 0, len(sub.Attachments))
	for _, s := range sub.Attachments {
		refs = append(refs, s.Ref)
	}
	tc.AppendUserWithAttachments("Analyze these artifacts", refs)
	tt.Frame()
	if !tt.HasText("Analyze these artifacts") || !tt.HasText("architecture.png") || !tt.HasText("rfc-design.pdf") {
		t.Fatalf("expected User Bubble to render prompt text and read-only attachment chips, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Inspect architecture.png"); err != nil {
		t.Fatalf("click Inspect architecture.png in User Bubble failed: %v", err)
	}
	tt.Frame()
	if !tc.IsAttachmentPreviewOpen() || !tt.HasText("Verified Raster Image · 320 x 180 px") {
		t.Fatalf("expected Image Lightbox from User Bubble click, got %q", strings.Join(tt.Texts(), " | "))
	}
	tc.CloseAttachmentPreview()
}

func TestSessionSearchBarAndMultiSessionSidebar(t *testing.T) {
	targets := transport.NewTargetRegistry()
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)

	tc := mygoshell.NewTranscriptController()
	tc.AppendUser("Explain OAuth PKCE")
	thinkRow := tc.AppendThinking("PKCE Analysis", "Checking S256 verifier length and nonce validation rules", true)
	tc.BeginAssistantTurn(1)
	tc.ApplyCheckpoint(1, "ev-1", "PKCE S256 binds the authorization code to a 43-char high-entropy verifier.")
	tc.CompleteTurn(1, "COMPLETED")
	app.Transcript = tc

	// Wire thin backend-delegated search callback (Rust backend owns actual index).
	var searchQueries []string
	tc.OnSearchSession = func(q string) []mygoshell.SessionSearchHit {
		searchQueries = append(searchQueries, q)
		if strings.Contains(strings.ToLower(q), "s256") {
			return []mygoshell.SessionSearchHit{
				{Row: thinkRow, Snippet: "Checking S256 verifier length..."},
				{Row: thinkRow + 1, Snippet: "PKCE S256 binds the authorization code..."},
			}
		}
		return nil
	}

	var selectedSession string
	var newSessionClicks int
	app.Sessions = []mygoshell.SessionSummaryView{
		{SessionID: "sess-1", Title: "OAuth Security Audit", TurnCount: 4},
		{SessionID: "sess-2", Title: "Multimodal Composer", TurnCount: 2},
	}
	app.ActiveSessionID = "sess-1"
	app.OnSelectSession = func(id string) { selectedSession = id }
	app.OnNewSession = func() { newSessionClicks++ }

	tt := ui.NewTester(app.View, 1180, 760)
	tt.Frame()

	// Verify multi-session sidebar switcher.
	if !tt.HasText("SESSIONS") {
		t.Fatalf("expected SESSIONS heading in left sidebar, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Multimodal Composer (2)"); err != nil {
		t.Fatalf("click session item failed: %v", err)
	}
	if err := tt.Click("+ New"); err != nil {
		t.Fatalf("click + New session button failed: %v", err)
	}
	tt.Frame()
	if selectedSession != "sess-2" || app.ActiveSessionID != "sess-2" {
		t.Fatalf("expected active session switched to sess-2, got selected=%q active=%q", selectedSession, app.ActiveSessionID)
	}
	if newSessionClicks != 1 {
		t.Fatalf("expected 1 + New click, got %d", newSessionClicks)
	}

	// Open thin Cmd+F backend-delegated search bar, run query, step hits, and verify folded row unfolds.
	tc.OpenSearch()
	tc.SetSearchQuery("S256")
	tt.Frame()

	if len(searchQueries) == 0 {
		t.Fatal("expected OnSearchSession backend callback to be invoked")
	}
	if !tt.HasText("1 / 2") {
		t.Fatalf("expected search bar hit counter '1 / 2', got %q", strings.Join(tt.Texts(), " | "))
	}
	if !tt.HasText("Checking S256 verifier length") {
		t.Fatalf("expected JumpToRow to unfold collapsed thinking block for hit #1, got %q", strings.Join(tt.Texts(), " | "))
	}

	if err := tt.Click("Next"); err != nil {
		t.Fatalf("click Next in search bar failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("2 / 2") {
		t.Fatalf("expected hit counter to advance to '2 / 2', got %q", strings.Join(tt.Texts(), " | "))
	}

	// Global ESC DismissTopOverlay closes the session search bar cleanly.
	if !app.DismissTopOverlay() {
		t.Fatal("expected DismissTopOverlay to close open session search bar")
	}
	if tc.IsSearchOpen() {
		t.Fatal("expected session search bar closed after DismissTopOverlay")
	}
}

func TestAttachmentThumbCacheDragDropAndMobileSheetLightbox(t *testing.T) {
	// 1. Verify GPU Thumbnail Cache + Native OS Drag-and-Drop (FileList + raw PNG transfer).
	tc := mygoshell.NewTranscriptController()
	pngBytes := makeTestPNG(t, 128, 96)
	dir := t.TempDir()
	droppedFile := filepath.Join(dir, "dropped.go")
	if err := os.WriteFile(droppedFile, []byte("package drop\nfunc Ok() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fileData, err := transfer.FileData(droppedFile)
	if err != nil {
		t.Fatal(err)
	}
	refs, errs := tc.HandleDroppedTransfer(transfer.Drop{Data: fileData})
	if len(refs) != 1 || len(errs) != 0 || refs[0].Kind != composer.AttachmentCode {
		t.Fatalf("expected dropped .go file staged cleanly, got refs=%+v errs=%v", refs, errs)
	}

	pngDrop := transfer.Drop{Data: transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, pngBytes)))}
	imgRefs, imgErrs := tc.HandleDroppedTransfer(pngDrop)
	if len(imgRefs) != 1 || len(imgErrs) != 0 || imgRefs[0].Width != 128 || imgRefs[0].Height != 96 {
		t.Fatalf("expected dropped PNG image staged cleanly, got refs=%+v errs=%v", imgRefs, imgErrs)
	}
	if tc.CachedThumbnail(imgRefs[0].SHA256Hex) == nil {
		t.Fatal("expected pre-decoded GPU ImageSource cached for dropped PNG attachment")
	}

	// 2. Verify MobileCompanionApp (iOS/Android) Photo/Camera/File hooks, compact bottom-sheet Lightbox, and attachment submit.
	mob := mygoshell.NewMobileCompanionAppForPlatform(mygoshell.MobilePlatformIOS, "iphone16", "")
	mob.Transcript = tc
	var photoClicks, cameraClicks, docClicks int
	var mobSubmitted []composer.TurnSubmission
	mob.OnPickPhoto = func() { photoClicks++ }
	mob.OnTakePhoto = func() { cameraClicks++ }
	mob.OnPickDocument = func() { docClicks++ }
	tc.OnSubmitTurn = func(sub composer.TurnSubmission) { mobSubmitted = append(mobSubmitted, sub) }

	tt := ui.NewTester(mob.View, 393, 852)
	tt.Frame()

	for _, btn := range []string{"Photo", "Camera", "File"} {
		if err := tt.Click(btn); err != nil {
			t.Fatalf("click mobile %q button failed: %v", btn, err)
		}
	}
	if photoClicks != 1 || cameraClicks != 1 || docClicks != 1 {
		t.Fatalf("expected mobile capture hooks fired once each, got photo=%d camera=%d doc=%d", photoClicks, cameraClicks, docClicks)
	}

	// Tap staged image chip in mobile Chat -> opens thumb-friendly bottom-sheet Lightbox!
	if err := tt.Click("Inspect dropped-image.png"); err != nil {
		t.Fatalf("click Inspect dropped-image.png on mobile failed: %v", err)
	}
	tt.Frame()
	if !tc.IsAttachmentPreviewOpen() || !tt.HasText("MAGIC VERIFIED · FAIL-CLOSED") || !tt.HasText("Verified Raster Image · 128 x 96 px") {
		t.Fatalf("expected mobile bottom-sheet Lightbox, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Done"); err != nil {
		t.Fatalf("click Done on mobile Lightbox sheet failed: %v", err)
	}
	tt.Frame()
	if tc.IsAttachmentPreviewOpen() {
		t.Fatal("expected mobile Lightbox sheet closed after Done")
	}

	// Tap Send on mobile -> submits staged multimodal attachments and resets composer!
	if err := tt.Click("Send"); err != nil {
		t.Fatalf("click Send on mobile failed: %v", err)
	}
	tt.Frame()
	if len(mobSubmitted) != 1 || len(mobSubmitted[0].Attachments) != 2 {
		t.Fatalf("expected mobile submit with 2 multimodal attachments, got %+v", mobSubmitted)
	}
}
