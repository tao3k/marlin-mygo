// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package bench

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/richtext"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func BenchmarkComposerAttachmentValidatePNG(b *testing.B) {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 128, 128)))
	pngBytes := buf.Bytes()
	policy := composer.DefaultAttachmentPolicy()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := composer.ValidateAndStageAttachment(composer.AttachmentInput{
			Filename: "bench.png",
			Data:     pngBytes,
		}, policy); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRichTextDecodeOrgizeDTO(b *testing.B) {
	raw := []byte(`{
		"schemaVersion": 1,
		"records": [
			{
				"level": 1,
				"titleText": "Marlin Org-mode GPU Rendering SLO",
				"todo": "NEXT",
				"priority": {"raw": "[#A]", "effective": "A", "isDefault": false},
				"tags": ["marlin", "orgize"],
				"properties": [{"key": "ID", "value": "bench-org-1"}],
				"planning": {"scheduled": {"kind": "active", "raw": "<2026-11-26 Thu 10:00>"}},
				"body": [{"text": "Zero Go-side Org parser duplication."}],
				"links": [{"path": "https://orgmode.org", "description": "Org Mode"}]
			}
		]
	}`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blocks, err := richtext.DecodeDocumentAutoJSON("bench-org", raw)
		if err != nil || len(blocks) == 0 {
			b.Fatalf("DecodeDocumentAutoJSON failed: %v", err)
		}
	}
}

func BenchmarkTranscriptRenderWithRichOrgAndVoice(b *testing.B) {
	tc := mygoshell.NewTranscriptController()
	blocks, err := richtext.DecodeIRJSON("bench-1", []byte(`{
		"format": "org",
		"blocks": [
			{"kind":"heading","level":1,"is_org":true,"todo_word":"TODO","priority":"A","tags":["marlin"],"raw_text":"120Hz GPU Frame"},
			{"kind":"org-planning","is_org":true,"planning":[{"keyword":"SCHEDULED","timestamp":"<2026-11-26 Thu>"}]},
			{"kind":"table","is_org":true,"headers":["Plane","Budget"],"rows":[["GPU Frame","8.3ms"]]},
			{"kind":"code","is_org":true,"lang":"scheme","code":"(marlin-frame-budget 120)"}
		]
	}`))
	if err != nil {
		b.Fatal(err)
	}
	if _, err := tc.AppendAssistantRichBlocks(1, "Org Turn", blocks, "DONE"); err != nil {
		b.Fatal(err)
	}
	tc.Voice.Start()
	tc.Voice.PushLevel(0.6)

	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		tc.Render(c, theme, nil, nil)
	}, 1080, 720)
	tt.Frame()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Frame()
	}
}
