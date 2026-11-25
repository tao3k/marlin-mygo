// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package richtext

import (
	"strings"
	"testing"
)

func FuzzDecodeIRJSON(f *testing.F) {
	f.Add([]byte(`{"format":"org","blocks":[{"kind":"heading","level":1,"is_org":true,"todo_word":"TODO","priority":"A","tags":["marlin"],"raw_text":"Task"}]}`))
	f.Add([]byte(`{"format":"markdown","blocks":[{"kind":"paragraph","spans":[{"kind":"link","text":"xss","url":"javascript:alert(1)"},{"kind":"link","text":"ok","url":"https://example.com"}]}]}`))
	f.Add([]byte(`{"blocks":[{"kind":"table","headers":["A","B"],"rows":[["1","2"]]},{"kind":"list","items":[{"check":"-","raw_text":"partial"}]}]}`))
	f.Add([]byte(`{"blocks":[{"kind":"unknown"}]}`))
	f.Add([]byte(`not json`))

	lim := AdmissionLimits{
		MaxPayloadBytes:  64 * 1024,
		MaxBlocks:        64,
		MaxTableRows:     32,
		MaxTableCols:     16,
		MaxListItems:     64,
		MaxSpansPerBlock: 32,
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		blocks, err := DecodeIRJSONWithLimits("fuzz", raw, lim)
		if err != nil {
			return
		}
		if len(blocks) > lim.MaxBlocks {
			t.Fatalf("block ceiling exceeded: %d > %d", len(blocks), lim.MaxBlocks)
		}
		for _, b := range blocks {
			for _, sp := range b.Spans {
				if sp.Kind == SpanLink {
					u := strings.ToLower(sp.URL)
					if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") &&
						!strings.HasPrefix(u, "mailto:") && !strings.HasPrefix(u, "file://") {
						t.Fatalf("unsafe link URL admitted across IR boundary: %q", sp.URL)
					}
				}
			}
			for _, item := range b.Items {
				for _, sp := range item.Spans {
					if sp.Kind == SpanLink {
						u := strings.ToLower(sp.URL)
						if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") &&
							!strings.HasPrefix(u, "mailto:") && !strings.HasPrefix(u, "file://") {
							t.Fatalf("unsafe list link URL admitted: %q", sp.URL)
						}
					}
				}
			}
		}
	})
}
