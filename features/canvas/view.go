// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package canvas

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

const (
	BufferIDCanvas    = "feature:canvas"
	CommandOpenCanvas = "feature.open-canvas"
)

// View renders Marlin's pinned Live Artifact & Version Canvas on Desktop or Mobile.
type View struct {
	Ctrl       *Controller
	Resilience *resilience.SessionResilience

	mu             sync.Mutex
	revDraft       string
	revDraftID     uint64
	folded         map[string]bool
	copiedKey      string
	onDraftChanged func(scopeID, text string)
}

// NewView constructs a Live Canvas GPU view bound to ctrl.
func NewView(ctrl *Controller) *View {
	if ctrl == nil {
		ctrl = NewController(nil)
	}
	return &View{
		Ctrl:   ctrl,
		folded: make(map[string]bool),
	}
}

func (v *View) draftScope() string {
	if v.Ctrl != nil {
		if id := strings.TrimSpace(v.Ctrl.Snapshot().ActiveDocID); id != "" {
			return "canvas:" + id
		}
	}
	return "canvas:default"
}

// SetRevisionDraft programmatically sets the inline artifact revision prompt
// and persists it into the shared SessionResilience store.
func (v *View) SetRevisionDraft(text string) {
	v.mu.Lock()
	v.revDraft = text
	v.revDraftID++
	res := v.Resilience
	cb := v.onDraftChanged
	v.mu.Unlock()
	if res != nil {
		scope := v.draftScope()
		res.SetDraft(scope, text)
		if cb != nil {
			cb(scope, text)
		}
	}
}

// RevisionDraft returns the active inline artifact revision draft buffer.
func (v *View) RevisionDraft() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.revDraft
}

// RestoreDraftFromResilience restores any power-loss persisted revision prompt
// for the active canvas document.
func (v *View) RestoreDraftFromResilience() string {
	v.mu.Lock()
	res := v.Resilience
	v.mu.Unlock()
	if res == nil {
		return ""
	}
	saved := res.Draft(v.draftScope())
	if saved != "" {
		v.mu.Lock()
		v.revDraft = saved
		v.revDraftID++
		v.mu.Unlock()
	}
	return saved
}

// Module implements features.FeatureModule for opt-in build-tag compilation.
type Module struct {
	View *View
}

// NewModule constructs an opt-in FeatureModule for Live Artifact Canvas.
func NewModule(ledger *features.GovernanceLedger) *Module {
	return &Module{
		View: NewView(NewController(ledger)),
	}
}

// SetGovernanceLedger links revision requests into Marlin's Tier-0 audit ledger.
func (m *Module) SetGovernanceLedger(ledger *features.GovernanceLedger) {
	if m != nil && m.View != nil && m.View.Ctrl != nil {
		m.View.Ctrl.SetGovernanceLedger(ledger)
	}
}

// SetResilience attaches the shared crash-durable SessionResilience store and
// immediately restores any unsent artifact revision draft after a restart.
func (m *Module) SetResilience(r *resilience.SessionResilience, onDraftChanged func(scopeID, text string)) {
	if m == nil || m.View == nil || r == nil {
		return
	}
	m.View.mu.Lock()
	m.View.Resilience = r
	m.View.onDraftChanged = onDraftChanged
	m.View.mu.Unlock()
	m.View.RestoreDraftFromResilience()
}

// SetRevisionHandler wires a transport-free callback invoked when a user requests an artifact revision.
func (m *Module) SetRevisionHandler(fn func(docID string, version int, instruction string)) {
	if m != nil && m.View != nil && m.View.Ctrl != nil {
		m.View.Ctrl.OnRequestRevision = fn
	}
}

func (m *Module) Meta() features.FeatureMeta {
	return features.FeatureMeta{
		ID:                   features.FeatureCanvas,
		Name:                 "Live Artifact Canvas",
		Description:          "Pinned code, diff, and Org-RZ1/Markdown Document IR canvas with immutable v1..vN snapshots",
		Category:             "workspace",
		TrustTier:            features.Tier0HostNative,
		RequiredCapabilities: []string{"artifact.read", "richtext.ir"},
		DefaultEnabled:       false,
	}
}

func (m *Module) Register(host *features.Host) error {
	host.AddBuffer(shell.PaneBuffer{
		BufferID: BufferIDCanvas,
		Title:    "Live Canvas",
		Kind:     "native-gpu",
	}, func(surface any, theme any) {
		c, ok1 := surface.(*ui.Context)
		t, ok2 := theme.(*ui.Theme)
		if ok1 && ok2 {
			m.View.Render(c, t, false)
		}
	})
	host.AddCommand(input.CommandEntry{
		Name:        CommandOpenCanvas,
		Description: "Open Live Artifact & Document Version Canvas buffer",
	})
	return nil
}

// Render paints the Live Artifact Canvas on Desktop (`compact=false`) or Mobile (`compact=true`).
func (v *View) Render(c *ui.Context, t *ui.Theme, compact bool) {
	dt := mygoshell.DeriveTheme("canvas", "Canvas", themeFromUI(t))
	snap := v.Ctrl.Snapshot()

	v.mu.Lock()
	draft := v.revDraft
	prevDraft := draft
	draftRev := v.revDraftID
	copiedKey := v.copiedKey
	res := v.Resilience
	draftCB := v.onDraftChanged
	foldedSnap := make(map[string]bool, len(v.folded))
	for k, val := range v.folded {
		foldedSnap[k] = val
	}
	v.mu.Unlock()
	curScope := "canvas:" + snap.ActiveDocID
	if snap.ActiveDocID == "" {
		curScope = "canvas:default"
	}

	var activeDoc ArtifactDoc
	for _, d := range snap.Docs {
		if d.ID == snap.ActiveDocID {
			activeDoc = d
			break
		}
	}
	var activeVer ArtifactVersion
	var prevVer *ArtifactVersion
	if activeDoc.ActiveVersion >= 1 && activeDoc.ActiveVersion <= len(activeDoc.Versions) {
		activeVer = activeDoc.Versions[activeDoc.ActiveVersion-1]
		if activeDoc.ActiveVersion >= 2 {
			p := activeDoc.Versions[activeDoc.ActiveVersion-2]
			prevVer = &p
		}
	}

	var (
		selectDocID   string
		selectVer     int
		toggleDiff    bool
		copyBodyNow   bool
		toggleFoldKey string
		sendRevNow    bool
	)

	root := ui.Column(c.Key("canvas-root")).Grow(1).MinWidth(0).Padding(10).Gap(8).Radius(12).
		Background(rgbColor(dt.Surface)).Border(1, rgbaColor(dt.Border, 0.85))
	if !compact && root.Bounds().W > 0 && root.Bounds().W < 720 {
		compact = true
	}

	root.Children(func() {
		// Header: artifact switcher + version pills + Diff toggle (2 rows on compact mobile).
		if compact {
			ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					mygoshell.AppleBadgePill(c, "LIVE CANVAS", dt.Accent, dt)
					ui.Text(c, activeDoc.Title).FontSize(13.5).FontWeight(700).TextColor(rgbColor(dt.Ink))
					ui.Spacer(c)
					if prevVer != nil {
						dVar := mygoshell.BtnGlass
						if snap.DiffMode {
							dVar = mygoshell.BtnTinted
						}
						if mygoshell.ApplePillButton(c, "cv-diff", "Diff", dVar, dt, nil, true).Clicked() {
							toggleDiff = true
						}
					}
				})
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					for _, d := range snap.Docs {
						doc := d
						dVar := mygoshell.BtnGlass
						if doc.ID == activeDoc.ID {
							dVar = mygoshell.BtnTinted
						}
						if mygoshell.ApplePillButton(c, "cv-doc-"+doc.ID, doc.Title, dVar, dt, nil, true).Clicked() {
							selectDocID = doc.ID
						}
					}
					ui.Spacer(c)
					for _, ver := range activeDoc.Versions {
						vv := ver.Version
						vVar := mygoshell.BtnGlass
						if vv == activeDoc.ActiveVersion {
							vVar = mygoshell.BtnPrimary
						}
						if mygoshell.ApplePillButton(c, fmt.Sprintf("cv-v-%d", vv), fmt.Sprintf("v%d", vv), vVar, dt, nil, true).Clicked() {
							selectVer = vv
						}
					}
				})
			})
		} else {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				mygoshell.AppleBadgePill(c, "LIVE CANVAS", dt.Accent, dt)
				for _, d := range snap.Docs {
					doc := d
					dVar := mygoshell.BtnGlass
					if doc.ID == activeDoc.ID {
						dVar = mygoshell.BtnTinted
					}
					if mygoshell.ApplePillButton(c, "cv-doc-"+doc.ID, doc.Title, dVar, dt, nil, true).Clicked() {
						selectDocID = doc.ID
					}
				}
				ui.Spacer(c)
				for _, ver := range activeDoc.Versions {
					vv := ver.Version
					vVar := mygoshell.BtnGlass
					if vv == activeDoc.ActiveVersion {
						vVar = mygoshell.BtnPrimary
					}
					if mygoshell.ApplePillButton(c, fmt.Sprintf("cv-v-%d", vv), fmt.Sprintf("v%d", vv), vVar, dt, nil, true).Clicked() {
						selectVer = vv
					}
				}
				if prevVer != nil {
					dVar := mygoshell.BtnGlass
					if snap.DiffMode {
						dVar = mygoshell.BtnTinted
					}
					if mygoshell.ApplePillButton(c, "cv-diff", "Diff", dVar, dt, nil, true).Clicked() {
						toggleDiff = true
					}
				}
				if mygoshell.ApplePillButton(c, "cv-copy", "Copy", mygoshell.BtnGlass, dt, nil, true).Clicked() {
					copyBodyNow = true
				}
			})
		}

		if activeVer.Summary != "" {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				mygoshell.AppleBadgePill(c, fmt.Sprintf("v%d · %s", activeVer.Version, activeVer.AuthorRole), dt.Info, dt)
				ui.Text(c, activeVer.Summary).FontSize(11.5).TextColor(rgbColor(dt.InkMuted))
			})
		}
		if res != nil {
			resSnap := res.Snapshot(curScope)
			if banner := resSnap.BannerText(); banner != "" {
				bTone := dt.Warning
				if resSnap.State == resilience.ConnOffline {
					bTone = dt.Danger
				}
				ui.Row(c.Key("cv-continuity-banner")).Padding(5, 9).Gap(6).Radius(10).AlignItems(ui.Center).
					Background(rgbaColor(bTone, 0.10)).Border(1, rgbaColor(bTone, 0.35)).Children(func() {
					mygoshell.AppleBadgePill(c, string(resSnap.State), bTone, dt)
					ui.Text(c, banner).FontSize(11).FontWeight(600).TextColor(rgbColor(dt.Ink))
				})
			}
		}

		// Artifact Content Surface.
		mygoshell.AppleGlassCard(c, "cv-body-"+activeDoc.ID, dt, 12, 12, 6, func() {
			if snap.DiffMode && prevVer != nil {
				mygoshell.AppleBadgePill(c, fmt.Sprintf("VERSION DIFF v%d -> v%d", prevVer.Version, activeVer.Version), dt.Warning, dt)
				for _, line := range ComputeLineDiff(prevVer.RawText, activeVer.RawText) {
					col := rgbColor(dt.InkSecondary)
					if strings.HasPrefix(line, "+ ") {
						col = rgbColor(dt.Success)
					} else if strings.HasPrefix(line, "- ") {
						col = rgbColor(dt.Danger)
					}
					ui.Text(c, line).FontSize(12).LineHeight(1.38).TextColor(col)
				}
			} else if len(activeVer.RichBlocks) > 0 {
				mygoshell.RenderRichBlocksInteractive(c, dt, activeVer.RichBlocks, mygoshell.RichDocCallbacks{
					DocKey:         fmt.Sprintf("%s-v%d", activeDoc.ID, activeVer.Version),
					FoldedHeadings: foldedSnap,
					LastCopiedKey:  copiedKey,
					OnToggleFold: func(hKey string) {
						toggleFoldKey = hKey
					},
				})
			} else {
				for idx, line := range strings.Split(strings.TrimRight(activeVer.RawText, "\n"), "\n") {
					ui.Textf(c, "%2d | %s", idx+1, line).FontSize(12).LineHeight(1.4).TextColor(rgbColor(dt.Ink))
				}
			}
		})

		if snap.LastError != "" {
			ui.Text(c, snap.LastError).FontSize(11.5).FontWeight(600).TextColor(rgbColor(dt.Danger))
		}

		ui.Spacer(c)

		// Inline revision prompt bar.
		ui.Row(c).Gap(8).Padding(7, 10).Radius(16).AlignItems(ui.Center).
			Background(rgbColor(dt.SurfaceElevated)).Border(1, rgbaColor(dt.Border, 0.85)).Children(func() {
			ui.TextInputBase(c.Key(fmt.Sprintf("cv-rev-%d", draftRev)), &draft).
				Placeholder("Request revision on pinned artifact...").
				FontSize(13).TextColor(rgbColor(dt.Ink)).Grow(1).Padding(4, 2)
			if mygoshell.ApplePillButton(c, "cv-rev-send", "Revise", mygoshell.BtnPrimary, dt, nil, true).Clicked() {
				sendRevNow = true
			}
		})
	})

	v.mu.Lock()
	v.revDraft = draft
	if toggleFoldKey != "" {
		if v.folded[toggleFoldKey] {
			delete(v.folded, toggleFoldKey)
		} else {
			v.folded[toggleFoldKey] = true
		}
	}
	v.mu.Unlock()
	if draft != prevDraft && res != nil {
		res.SetDraft(curScope, draft)
		if draftCB != nil {
			draftCB(curScope, draft)
		}
	}

	if selectDocID != "" {
		_ = v.Ctrl.SelectDoc(selectDocID)
		v.RestoreDraftFromResilience()
	}
	if selectVer > 0 {
		_ = v.Ctrl.SelectVersion(activeDoc.ID, selectVer)
	}
	if toggleDiff {
		v.Ctrl.ToggleDiffMode()
	}
	if copyBodyNow && v.Ctrl.OnCopyArtifact != nil {
		v.Ctrl.OnCopyArtifact(activeVer.RawText)
	}
	if sendRevNow {
		if err := v.Ctrl.RequestRevision(draft); err == nil {
			v.SetRevisionDraft("")
		}
	}
}

func themeFromUI(t *ui.Theme) schema.UITheme {
	if t == nil {
		return schema.DefaultUITheme()
	}
	if int(t.Background.R)+int(t.Background.G)+int(t.Background.B) > 380 {
		return mygoshell.ThemeByID("paper").Base
	}
	return mygoshell.ThemeByID("midnight").Base
}

func rgbColor(c schema.RGB) ui.Color {
	return ui.RGB(c.R, c.G, c.B)
}

func rgbaColor(c schema.RGB, a float32) ui.Color {
	return ui.RGBA(c.R, c.G, c.B, a)
}
