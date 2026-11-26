// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/engine/richtext"
)

// Render paints ONLY the Fenwick-virtualized visible slice of transcript rows
// plus the interactive chat composer onto MyGo's GPU scene graph.
func (tc *TranscriptController) Render(c *ui.Context, t *ui.Theme, onSend func(string), onCancel func()) {
	tc.renderMu.Lock()
	defer tc.renderMu.Unlock()

	tc.mu.RLock()
	first, last, _ := tc.heights.VisibleRange(tc.scrollY, tc.viewportH)
	totalRows := len(tc.entries)
	totalH := tc.heights.TotalHeight()
	inFlight := tc.inFlight
	autoFollow := tc.autoFollow
	deepThink := tc.deepThink
	modelLbl := tc.modelLabel
	lastCopied := tc.lastCopiedKey
	draft := tc.composerText
	prevDraft := draft
	composerRev := tc.composerRev
	resCtrl := tc.Resilience
	resScope := tc.ResilienceScope
	if resScope == "" {
		resScope = "chat:default"
	}
	retryOutboxFn := tc.OnRetryOutbox
	discardOutboxFn := tc.OnDiscardOutbox
	draftChangedFn := tc.OnDraftChanged
	searchOpen := tc.searchOpen
	searchQuery := tc.searchQuery
	searchLastQ := tc.searchLastQuery
	searchHits := append([]SessionSearchHit(nil), tc.searchHits...)
	searchHitIdx := tc.searchHitIdx
	searchFn := tc.OnSearchSession
	retryTurnFn := tc.OnRetryTurn
	forkTurnFn := tc.OnForkTurn
	mentionFn := tc.OnQueryMentions
	pickAttFn := tc.OnPickAttachment
	pasteAttFn := tc.OnPasteAttachment
	startVoiceFn := tc.OnStartVoice
	submitTurnFn := tc.OnSubmitTurn
	slashFn := tc.OnSlashCommand
	compState := tc.Composer
	voiceCtrl := tc.Voice
	ctxUsage := tc.contextUsage
	streamMetrics := tc.streamMetrics
	modelPickerOpen := tc.modelPickerOpen
	modelPickerQuery := tc.modelPickerQuery
	hasThinking := false
	anyThinkingExpanded := false
	for i := range tc.entries {
		if tc.entries[i].Role == RoleThinking && tc.entries[i].Collapsible {
			hasThinking = true
			if !tc.entries[i].Folded {
				anyThinkingExpanded = true
			}
		}
	}
	var activePreview *composer.AttachmentRef
	var activePreviewBlocks []richtext.Block
	if tc.previewAtt != nil {
		cp := *tc.previewAtt
		activePreview = &cp
		activePreviewBlocks = tc.previewBlocks
	}
	var richFoldSnap map[string]bool
	if len(tc.richFolded) > 0 {
		richFoldSnap = make(map[string]bool, len(tc.richFolded))
		for k, v := range tc.richFolded {
			richFoldSnap[k] = v
		}
	}
	tc.renderEntries = tc.renderEntries[:0]
	tc.renderIndices = tc.renderIndices[:0]
	if first >= 0 && last >= first && last < totalRows {
		for i := first; i <= last; i++ {
			tc.renderEntries = append(tc.renderEntries, tc.entries[i])
			tc.renderIndices = append(tc.renderIndices, i)
		}
	}
	tc.mu.RUnlock()

	var compSnap composer.ComposerSnapshot
	if compState != nil {
		compSnap = compState.Snapshot()
		if modelLbl == "" && compSnap.ActiveModel.Label != "" {
			modelLbl = compSnap.ActiveModel.Label
		}
	}
	if modelLbl == "" {
		modelLbl = "Marlin Core"
	}

	var voiceSnap composer.VoiceSnapshot
	if voiceCtrl != nil {
		voiceSnap = voiceCtrl.Snapshot()
	}
	streamLbl := streamMetrics.Label()
	ctxLbl := ctxUsage.Label()
	resSnap := resCtrl.Snapshot(resScope)
	resBanner := resSnap.BannerText()

	var (
		toggleRow         = -1
		submitPrompt      string
		submittedTurn     *composer.TurnSubmission
		cancelClicked     bool
		jumpLatest        bool
		toggleThink       bool
		toggleModelPicker bool
		closeModelPicker  bool
		pickedModelID     string
		pickedEffort      composer.ReasoningEffort
		startVoiceClicked bool
		stopVoiceClicked  bool
		cancelVoiceClick  bool
		openSearchNow     bool
		closeSearchNow  bool
		stepSearchDelta int
		removeAttID     string
		removeMentionK  string
		pickedSlash     *composer.SlashCommand
		pickedMention   *composer.ContextMention
		pickAttClicked  bool
		pasteAttClicked bool
		openPreviewAtt  *composer.AttachmentRef
		closePreviewNow bool
		droppedPayload  *transfer.Drop
		foldAllThinkAct int // 0=none, +1=fold all, -1=unfold all
		retryTurnOrd    uint64
		forkTurnOrd     uint64
		copiedKey       string
		copiedCode      string
		toggleRichFold  string
		toggleRichCheck *struct {
			row, blockIdx, itemIdx int
			nextCheck              string
		}
		retryOutboxNow  bool
		discardOutboxID string
	)

	dt := derivedThemeFromUITheme(t)
	visible := tc.renderEntries
	visibleIndices := tc.renderIndices
	if tc.cachedStatsStr == "" || tc.statsRows != totalRows || tc.statsVisible != len(visible) || tc.statsHeight != totalH {
		tc.statsRows = totalRows
		tc.statsVisible = len(visible)
		tc.statsHeight = totalH
		tc.cachedStatsStr = fmt.Sprintf("rows:%d visible:%d height:%dpx", totalRows, len(visible), totalH)
	}
	statsLine := tc.cachedStatsStr

	root := ui.Column(c.Key("transcript-root")).Grow(1).MinWidth(0).Padding(10).Gap(8).Radius(12).
		Background(toColor(dt.Surface)).Border(1, glassBorderColor(dt))
	if w := root.Bounds().W; w > 80 {
		tc.SyncWrapWidth(w)
	}
	root.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "LIVE TRANSCRIPT").FontSize(11).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.InkMuted))
			ui.Text(c, statsLine).FontSize(11).TextColor(toColor(dt.InkMuted))
			ui.Spacer(c)
			if hasThinking {
				lbl := "Unfold Thinking"
				act := -1
				if anyThinkingExpanded {
					lbl = "Fold Thinking"
					act = 1
				}
				if ApplePillButton(c, "transcript-fold-thinking", lbl, BtnGlass, dt, nil, true).Clicked() {
					foldAllThinkAct = act
				}
			}
			if searchFn != nil || searchOpen {
				if ApplePillButton(c, "transcript-find", "Find", BtnGlass, dt, svgIconSearch, true).Clicked() {
					openSearchNow = true
				}
			}
			if streamLbl != "" {
				AppleBadgePill(c, streamLbl, dt.Info, dt)
			}
			if inFlight {
				AppleBadgePill(c, "STREAMING", dt.Warning, dt)
			} else {
				AppleBadgePill(c, "IDLE", dt.Accent, dt)
			}
		})

		if searchOpen {
			RenderSessionSearchBar(c, dt, &searchQuery, searchHits, searchHitIdx,
				func() { stepSearchDelta = -1 },
				func() { stepSearchDelta = 1 },
				func() { closeSearchNow = true },
			)
		}

		// Live continuity & checkpoint recovery banner whenever offline, reconnecting, or outbox queued.
		if resBanner != "" {
			bannerTone := dt.Warning
			if resSnap.State == resilience.ConnOffline {
				bannerTone = dt.Danger
			} else if resSnap.State == resilience.ConnReplaying {
				bannerTone = dt.Info
			}
			ui.Column(c.Key("transcript-resilience-banner")).Padding(8, 10).Gap(6).Radius(10).
				Background(rgbaFromRGB(bannerTone, 0.10)).
				Border(1, rgbaFromRGB(bannerTone, 0.35)).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					AppleBadgePill(c, string(resSnap.State), bannerTone, dt)
					ui.Text(c, resBanner).FontSize(12).FontWeight(600).TextColor(toColor(dt.Ink))
					ui.Spacer(c)
					if len(resSnap.PendingOutbox) > 0 {
						if ApplePillButton(c, "transcript-retry-outbox", "Retry Queue", BtnTinted, dt, nil, true).Clicked() {
							retryOutboxNow = true
						}
					}
				})
				for _, qit := range resSnap.PendingOutbox {
					item := qit
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						AppleBadgePill(c, string(item.State), bannerTone, dt)
						ui.Text(c, item.Text).FontSize(11.5).TextColor(toColor(dt.InkSecondary)).MaxLines(1)
						ui.Spacer(c)
						if ApplePillButton(c, "transcript-drop-"+item.SubmissionID, "Discard", BtnGlass, dt, nil, true).Clicked() {
							discardOutboxID = item.SubmissionID
						}
					})
				}
			})
		}

		if len(visible) == 0 {
			ui.Text(c, "No turns yet. Send a message below to stream from Rust AppServer.").
				FontSize(12.5).TextColor(toColor(dt.InkMuted))
		} else {
			ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
				for idx := range visible {
					entry := &visible[idx]
					rowIdx := visibleIndices[idx]

					switch entry.Role {
					case RoleUser:
						// T3 Chat asymmetric user bubble: soft accent tint with crisp pill radius.
						ui.Row(c).Justify(ui.End).Children(func() {
							ui.Column(c).MinWidth(0).Padding(9, 13).Gap(4).Radius(16, 16, 4, 16).
								Background(rgbaFromRGB(dt.Accent, 0.14)).
								Border(1, rgbaFromRGB(dt.Accent, 0.24)).Children(func() {
								ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
									ui.Text(c, entry.headerLabel).FontSize(10.5).FontWeight(700).TextColor(toColor(dt.Accent))
									if entry.outcomeBadge != "" {
										ui.Text(c, entry.outcomeBadge).FontSize(10.5).TextColor(toColor(dt.InkMuted))
									}
								})
								if entry.Text != "" {
									ui.Text(c, entry.Text).FontSize(13).LineHeight(1.4).TextColor(toColor(dt.Ink))
								}
								if len(entry.Attachments) > 0 {
									ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
										for _, att := range entry.Attachments {
											AttachmentCardChip(c, dt, att, tc.CachedThumbnail(att.SHA256Hex), false, func(ref composer.AttachmentRef) {
												cp := ref
												openPreviewAtt = &cp
											}, nil)
										}
									})
								}
							})
						})

					case RoleThinking:
						// DeepSeek R1 calm collapsible thinking timeline (recessed + 2px left rail).
						ui.Column(c).MinWidth(0).Padding(8, 12).Gap(5).Radius(10).
							Background(toColor(dt.SurfaceRecessed)).
							BorderWidth(0, 0, 0, 2).BorderColor(toColor(dt.Accent)).Children(func() {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Text(c, entry.headerLabel).FontSize(11).FontWeight(600).TextColor(toColor(dt.InkSecondary))
								if entry.Streaming {
									AppleBadgePill(c, "THINKING...", dt.Accent, dt)
								} else if entry.outcomeBadge != "" {
									ui.Text(c, entry.outcomeBadge).FontSize(11).TextColor(toColor(dt.InkMuted))
								}
								ui.Spacer(c)
								if entry.Collapsible {
									btnLabel := entry.collapseLabel
									if entry.Folded {
										btnLabel = entry.expandLabel
									}
									if ApplePillButton(c, entry.foldKey, btnLabel, BtnGlass, dt, nil, true).Clicked() {
										toggleRow = rowIdx
									}
								}
							})
							if entry.Collapsible && entry.Folded {
								ui.Text(c, entry.foldedSummary).FontSize(11.5).TextColor(toColor(dt.InkMuted))
							} else if entry.Text != "" {
								ui.Text(c, entry.Text).FontSize(12.5).LineHeight(1.4).TextColor(toColor(dt.InkSecondary))
							}
						})

					default:
						// Assistant editorial card (with T3 code-fence blocks) / Tool / Gate cards.
						cardBg := toColor(dt.SurfaceElevated)
						borderCol := glassBorderColor(dt)
						roleColor := toColor(dt.Accent)
						if entry.Role == RoleTool {
							cardBg = toColor(dt.SurfaceRecessed)
							roleColor = toColor(dt.Info)
						} else if entry.Role == RoleGate {
							cardBg = rgbaFromRGB(dt.Warning, 0.08)
							borderCol = rgbaFromRGB(dt.Warning, 0.34)
							roleColor = toColor(dt.Warning)
						}
						ui.Column(c).MinWidth(0).Padding(10, 12).Gap(6).Radius(12).
							Background(cardBg).Border(1, borderCol).Children(func() {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Text(c, entry.headerLabel).FontSize(11).FontWeight(700).TextColor(roleColor)
								if entry.Streaming {
									AppleBadgePill(c, "[streaming...]", dt.Warning, dt)
								} else if entry.outcomeBadge != "" {
									ui.Text(c, entry.outcomeBadge).FontSize(11).FontWeight(600).TextColor(toColor(dt.InkMuted))
								}
								ui.Spacer(c)
								if entry.Collapsible {
									btnLabel := entry.collapseLabel
									if entry.Folded {
										btnLabel = entry.expandLabel
									}
									if ApplePillButton(c, entry.foldKey, btnLabel, BtnGlass, dt, nil, true).Clicked() {
										toggleRow = rowIdx
									}
								}
							})
							if entry.Collapsible && entry.Folded {
								ui.Text(c, entry.foldedSummary).FontSize(11.5).TextColor(toColor(dt.InkMuted))
							} else if len(entry.blocks) > 0 {
								curRow := rowIdx
								RenderRichBlocksInteractive(c, dt, entry.blocks, RichDocCallbacks{
									DocKey:         entry.ID,
									FoldedHeadings: richFoldSnap,
									LastCopiedKey:  lastCopied,
									OnCopyCode: func(key, code string) {
										copiedKey = key
										copiedCode = code
									},
									OnToggleFold: func(hKey string) {
										toggleRichFold = hKey
									},
									OnToggleCheck: func(bIdx, iIdx int, next string) {
										toggleRichCheck = &struct {
											row, blockIdx, itemIdx int
											nextCheck              string
										}{row: curRow, blockIdx: bIdx, itemIdx: iIdx, nextCheck: next}
									},
								})
							} else if len(entry.segments) > 0 {
								for sIdx := range entry.segments {
									seg := entry.segments[sIdx]
									if !seg.IsCode {
										ui.Text(c, seg.Content).FontSize(13).LineHeight(1.45).TextColor(toColor(dt.Ink))
										continue
									}
									ui.Column(c).MinWidth(0).Padding(8, 10).Gap(5).Radius(10).
										Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
										ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
											AppleBadgePill(c, seg.Lang, dt.Info, dt)
											ui.Spacer(c)
											copyLbl := "Copy Code"
											copyVar := BtnGlass
											if lastCopied == seg.CopyKey {
												copyLbl = "Copied"
												copyVar = BtnSuccess
											}
											if ApplePillButton(c, seg.CopyKey, copyLbl, copyVar, dt, nil, true).Clicked() {
												copiedKey = seg.CopyKey
												copiedCode = seg.Content
											}
										})
										ui.Text(c, seg.Content).FontSize(12).LineHeight(1.4).TextColor(toColor(dt.Ink))
									})
								}
							} else if entry.Text != "" {
								ui.Text(c, entry.Text).FontSize(13).LineHeight(1.45).TextColor(toColor(dt.Ink))
							}
							if entry.ToolCard != nil && !entry.Folded {
								if entry.receiptBadge != "" {
									AppleBadgePill(c, entry.receiptBadge, dt.Accent, dt)
								}
								if entry.denialBadge != "" {
									AppleBadgePill(c, entry.denialBadge, dt.Warning, dt)
								}
							}
							if entry.cancelBadge != "" {
								ui.Text(c, entry.cancelBadge).FontSize(11).FontWeight(700).TextColor(toColor(dt.Warning))
							}
							if entry.Role == RoleAssistant && !entry.Streaming && (retryTurnFn != nil || forkTurnFn != nil || (tc.OnCopyCode != nil && entry.Text != "")) {
								ui.Row(c).Gap(6).Padding(2, 0, 0, 0).AlignItems(ui.Center).Children(func() {
									if tc.OnCopyCode != nil && entry.Text != "" {
										cKey := "copy-turn-" + entry.ID
										cLbl := "Copy"
										cVar := BtnGlass
										if lastCopied == cKey {
											cLbl = "Copied"
											cVar = BtnSuccess
										}
										if ApplePillButton(c, cKey, cLbl, cVar, dt, nil, true).Clicked() {
											copiedKey = cKey
											copiedCode = entry.Text
										}
									}
									if retryTurnFn != nil && entry.TurnOrdinal > 0 {
										if ApplePillButton(c, "retry-"+entry.ID, "Retry", BtnGlass, dt, nil, true).Clicked() {
											retryTurnOrd = entry.TurnOrdinal
										}
									}
									if forkTurnFn != nil && entry.TurnOrdinal > 0 {
										if ApplePillButton(c, "fork-"+entry.ID, "Fork", BtnGlass, dt, nil, true).Clicked() {
											forkTurnOrd = entry.TurnOrdinal
										}
									}
								})
							}
						})
					}
				}
			})
		}

		// Floating T3/DeepSeek "Jump to Latest" pill whenever user scrolled up from live stream.
		if !autoFollow && totalRows > 0 {
			ui.Row(c).Justify(ui.Center).Children(func() {
				if ApplePillButton(c, "transcript-jump-latest", "Jump to Latest", BtnTinted, dt, nil, true).Clicked() {
					jumpLatest = true
				}
			})
		}

		// 2-row T3 Chat / DeepSeek floating glass capsule composer with native OS Drag-and-Drop.
		dropOpts := transfer.DropOptions{
			Formats: []transfer.Format{transfer.FileList, transfer.PNG},
		}
		composerBox := ui.Column(c.Key("transcript-composer-dropzone")).Gap(6).Padding(8, 10, 6, 12).Radius(20).
			Background(toColor(dt.SurfaceElevated)).
			Border(1, glassBorderColor(dt)).
			InsetShadow(0, 1, 0, 0, glassTopSpecular(dt))
		_, dragHover := ui.DataDragOver(composerBox, dropOpts)
		if dragHover {
			composerBox.Border(2, toColor(dt.Accent))
		}
		if dropped, ok := ui.DropData(composerBox, dropOpts); ok {
			cp := dropped
			droppedPayload = &cp
		}
		composerBox.Children(func() {
			if dragHover {
				ui.Text(c, "Drop image, PDF, audio, or code to attach (fail-closed verified)").
					FontSize(11.5).FontWeight(700).TextColor(toColor(dt.Accent))
			}
			// Interactive Model & Reasoning Effort picker popover, OR inline `/` Slash / `@` Mention popups.
			if modelPickerOpen && compState != nil {
				RenderModelPickerPopup(c, dt, compSnap, &modelPickerQuery, compState.MatchModels(modelPickerQuery),
					func(m composer.ModelOption) { pickedModelID = m.ID },
					func(eff composer.ReasoningEffort) { pickedEffort = eff },
					func() { closeModelPicker = true },
				)
			} else if compState != nil {
				if slashMatches := compState.MatchSlashCommands(draft); len(slashMatches) > 0 {
					RenderSlashCommandPopup(c, dt, slashMatches, func(cmd composer.SlashCommand) {
						cp := cmd
						pickedSlash = &cp
					})
				} else if mq, hasMention := composer.ActiveMentionQuery(draft); hasMention && mentionFn != nil {
					if cands := mentionFn(mq); len(cands) > 0 {
						RenderMentionPopup(c, dt, cands, func(m composer.ContextMention) {
							cp := m
							pickedMention = &cp
						})
					}
				}
			}

			// Live Tier-0 Voice Recording Bar whenever voice capture is active.
			if voiceSnap.Phase != composer.VoiceIdle {
				RenderVoiceRecordingBar(c, dt, voiceSnap,
					func() { cancelVoiceClick = true },
					func() { stopVoiceClicked = true },
				)
			}

			// Structured `@` context mention chips + multimodal attachment strip above input.
			if len(compSnap.Mentions) > 0 {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					for _, m := range compSnap.Mentions {
						ContextMentionChip(c, dt, m, func(key string) { removeMentionK = key })
					}
				})
			}
			if len(compSnap.Attachments) > 0 {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					for _, att := range compSnap.Attachments {
						AttachmentCardChip(c, dt, att, tc.CachedThumbnail(att.SHA256Hex), true, func(ref composer.AttachmentRef) {
							cp := ref
							openPreviewAtt = &cp
						}, func(id string) { removeAttID = id })
					}
				})
			}
			if compSnap.LastError != "" {
				ui.Text(c, compSnap.LastError).FontSize(11).FontWeight(600).TextColor(toColor(dt.Danger))
			}

			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.TextInputBase(c.Key(fmt.Sprintf("transcript-composer-%d", composerRev)), &draft).
					Placeholder("Message Marlin agent... (/ commands · @ context · attach image/PDF/code)").
					FontSize(13.5).TextColor(toColor(dt.Ink)).Grow(1).Padding(4, 2)
			})
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if pickAttFn != nil || len(compSnap.Attachments) > 0 {
					if ApplePillButton(c, "transcript-attach", "Attach", BtnGlass, dt, svgIconPaperclip, true).Clicked() {
						pickAttClicked = true
					}
				}
				if pasteAttFn != nil {
					if ApplePillButton(c, "transcript-paste", "Paste", BtnGlass, dt, svgIconClipboard, true).Clicked() {
						pasteAttClicked = true
					}
				}
				if voiceSnap.Phase == composer.VoiceIdle && (startVoiceFn != nil || voiceCtrl != nil) {
					if ApplePillButton(c, "transcript-voice", "Voice", BtnGlass, dt, svgIconMic, true).Clicked() {
						startVoiceClicked = true
					}
				}
				if compState != nil && len(compSnap.Models) > 1 {
					mVar := BtnGlass
					if modelPickerOpen {
						mVar = BtnTinted
					}
					if ApplePillButton(c, "transcript-model", modelLbl, mVar, dt, nil, true).Clicked() {
						toggleModelPicker = true
					}
				} else {
					AppleBadgePill(c, modelLbl, dt.InkMuted, dt)
				}
				thinkVar := BtnGlass
				if deepThink {
					thinkVar = BtnTinted
				}
				if ApplePillButton(c, "transcript-deep-think", "Deep Think", thinkVar, dt, svgIconSparkles, true).Clicked() {
					toggleThink = true
				}
				if ctxLbl != "" {
					ctxTone := dt.InkMuted
					if frac := ctxUsage.Fraction(); frac >= 0.90 {
						ctxTone = dt.Danger
					} else if frac >= 0.75 {
						ctxTone = dt.Warning
					}
					AppleBadgePill(c, ctxLbl, ctxTone, dt)
				}
				ui.Spacer(c)
				if inFlight && onCancel != nil {
					if ApplePillButton(c, "transcript-cancel", "Cancel Turn", BtnDestructive, dt, svgIconXMark, true).Clicked() {
						cancelClicked = true
					}
				}
				if AppleCircleIconButton(c, "transcript-send", "Send", svgIconArrowUp, 30, dt).Clicked() {
					if compState != nil {
						if sub, ok := compState.BuildAndResetSubmission(draft); ok {
							submittedTurn = &sub
							submitPrompt = sub.Prompt
							draft = ""
						}
					} else {
						trimmed := strings.TrimSpace(draft)
						if trimmed != "" {
							submitPrompt = trimmed
							draft = ""
						}
					}
				}
			})
		})
	})

	if activePreview != nil {
		RenderAttachmentPreviewModal(c, dt, *activePreview, tc.CachedThumbnail(activePreview.SHA256Hex), activePreviewBlocks, func(snippet string) {
			copiedKey = "preview-" + activePreview.ID
			copiedCode = snippet
		}, func() {
			closePreviewNow = true
		})
	}

	tc.mu.Lock()
	tc.composerText = draft
	tc.modelPickerQuery = modelPickerQuery
	if closeModelPicker {
		tc.modelPickerOpen = false
		tc.modelPickerQuery = ""
	}
	if closePreviewNow {
		tc.previewAtt = nil
		tc.previewBlocks = nil
	}
	if openSearchNow {
		tc.searchOpen = true
	}
	if closeSearchNow {
		tc.searchOpen = false
	}
	searchChanged := searchOpen && searchQuery != searchLastQ
	tc.searchQuery = searchQuery
	if jumpLatest {
		tc.autoFollow = true
		tc.scrollToBottomLocked()
	}
	if copiedKey != "" {
		tc.lastCopiedKey = copiedKey
	}
	copyCB := tc.OnCopyCode
	tc.mu.Unlock()

	if searchChanged && searchFn != nil {
		tc.runSearchQuery(searchQuery, searchFn)
	}
	if stepSearchDelta != 0 {
		tc.StepSearchHit(stepSearchDelta)
	}
	if removeAttID != "" && compState != nil {
		compState.RemoveAttachment(removeAttID)
	}
	if removeMentionK != "" && compState != nil {
		compState.RemoveMention(removeMentionK)
		tc.SyncDraftMentionsToResilience()
	}
	if pickedMention != nil && compState != nil {
		compState.AddMention(*pickedMention)
		tc.SetComposerText(composer.StripTrailingMentionToken(draft))
		tc.SyncDraftMentionsToResilience()
	}
	if pickedSlash != nil {
		tc.SetComposerText("")
		if pickedSlash.ActionID == "studio.open-search" {
			tc.OpenSearch()
		} else if slashFn != nil {
			slashFn(*pickedSlash)
		}
	}
	if openPreviewAtt != nil {
		tc.OpenAttachmentPreview(*openPreviewAtt)
	}
	if foldAllThinkAct == 1 {
		tc.SetAllThinkingFolded(true)
	} else if foldAllThinkAct == -1 {
		tc.SetAllThinkingFolded(false)
	}
	if retryTurnOrd > 0 && retryTurnFn != nil {
		retryTurnFn(retryTurnOrd)
	}
	if forkTurnOrd > 0 && forkTurnFn != nil {
		forkTurnFn(forkTurnOrd)
	}
	if droppedPayload != nil {
		_, _ = tc.HandleDroppedTransfer(*droppedPayload)
	}
	if pickAttClicked && pickAttFn != nil {
		pickAttFn()
	}
	if pasteAttClicked && pasteAttFn != nil {
		pasteAttFn()
	}
	if startVoiceClicked {
		tc.StartVoiceRecording()
	}
	if stopVoiceClicked {
		tc.StopVoiceRecording()
	}
	if cancelVoiceClick {
		tc.CancelVoiceRecording()
	}
	if toggleModelPicker {
		tc.ToggleModelPicker()
	}
	if pickedModelID != "" {
		tc.SelectModel(pickedModelID)
	}
	if pickedEffort != "" && compState != nil {
		compState.SetEffort(pickedEffort)
		tc.SetDeepThink(pickedEffort == composer.EffortDeep)
	}
	if toggleThink {
		next := !deepThink
		tc.SetDeepThink(next)
		if compState != nil {
			if next {
				compState.SetEffort(composer.EffortDeep)
			} else {
				compState.SetEffort(composer.EffortBalanced)
			}
		}
	}
	if copiedKey != "" && copyCB != nil {
		copyCB(copiedCode)
	}
	if toggleRichFold != "" {
		tc.ToggleRichHeadingFold(toggleRichFold)
	}
	if toggleRichCheck != nil {
		tc.ToggleRichChecklistItem(toggleRichCheck.row, toggleRichCheck.blockIdx, toggleRichCheck.itemIdx, toggleRichCheck.nextCheck)
	}
	if draft != prevDraft {
		if resCtrl != nil {
			resCtrl.SetDraft(resScope, draft)
		}
		if draftChangedFn != nil {
			draftChangedFn(resScope, draft)
		}
	}
	if discardOutboxID != "" {
		if resCtrl != nil && resCtrl.Outbox != nil {
			resCtrl.Outbox.Discard(discardOutboxID)
		}
		if discardOutboxFn != nil {
			discardOutboxFn(discardOutboxID)
		}
	}
	if retryOutboxNow && retryOutboxFn != nil {
		retryOutboxFn()
	}
	if toggleRow >= 0 {
		tc.ToggleFold(toggleRow)
	}
	if submittedTurn != nil && submitTurnFn != nil {
		tc.SyncDraftMentionsToResilience()
		submitTurnFn(*submittedTurn)
	} else if submitPrompt != "" && onSend != nil {
		tc.SyncDraftMentionsToResilience()
		onSend(submitPrompt)
	}
	if cancelClicked && onCancel != nil {
		onCancel()
	}
}
