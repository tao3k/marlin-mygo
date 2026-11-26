// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package chatroom

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
	BufferIDChatRoom    = "feature:chatroom"
	CommandOpenChatRoom = "feature.open-chatroom"
)

// ViewTab selects the active workspace surface inside the Agent Chat Room.
type ViewTab string

const (
	TabTimeline ViewTab = "timeline"
	TabMatters  ViewTab = "matters"
	TabRoles    ViewTab = "roles"
	TabRooms    ViewTab = "rooms" // mobile compact channel picker
)

// View manages transient GPU UI state (draft text, active tab, compact mode)
// over a governed chatroom.Controller using Marlin's own DerivedTheme tokens.
type View struct {
	Ctrl *Controller

	mu          sync.Mutex
	activeTab   ViewTab
	draft       string
	draftRev    uint64
	threadDraft string
	threadRev   uint64
	itemDraft   string
	itemRev     uint64
}

// NewView constructs a Marlin-native GPU view bound to ctrl.
func NewView(ctrl *Controller) *View {
	if ctrl == nil {
		ctrl = NewController(nil)
	}
	return &View{
		Ctrl:      ctrl,
		activeTab: TabTimeline,
	}
}

// SetDraft programmatically updates the main room message composer draft and
// persists it into Ctrl.Resilience for power-loss continuity.
func (v *View) SetDraft(text string) {
	v.mu.Lock()
	v.draft = text
	v.draftRev++
	v.mu.Unlock()
	if v.Ctrl != nil && v.Ctrl.Resilience != nil {
		scope := "room:" + v.Ctrl.Snapshot().ActiveChannelID
		v.Ctrl.Resilience.SetDraft(scope, text)
		if v.Ctrl.OnDraftChanged != nil {
			v.Ctrl.OnDraftChanged(scope, text)
		}
	}
}

// Draft returns the current room composer text buffer.
func (v *View) Draft() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.draft
}

// SetThreadDraft programmatically updates the active Thread drawer reply draft
// and persists it into Ctrl.Resilience for power-loss continuity.
func (v *View) SetThreadDraft(text string) {
	v.mu.Lock()
	v.threadDraft = text
	v.threadRev++
	v.mu.Unlock()
	if v.Ctrl != nil && v.Ctrl.Resilience != nil {
		if tid := v.Ctrl.Snapshot().ActiveThreadID; tid != "" {
			scope := "thread:" + tid
			v.Ctrl.Resilience.SetDraft(scope, text)
			if v.Ctrl.OnDraftChanged != nil {
				v.Ctrl.OnDraftChanged(scope, text)
			}
		}
	}
}

// RestoreDraftsFromResilience restores persisted room and thread drafts after restart.
func (v *View) RestoreDraftsFromResilience() {
	if v.Ctrl == nil || v.Ctrl.Resilience == nil {
		return
	}
	snap := v.Ctrl.Snapshot()
	roomSaved := v.Ctrl.Resilience.Draft("room:" + snap.ActiveChannelID)
	threadSaved := ""
	if snap.ActiveThreadID != "" {
		threadSaved = v.Ctrl.Resilience.Draft("thread:" + snap.ActiveThreadID)
	}
	v.mu.Lock()
	if roomSaved != "" {
		v.draft = roomSaved
		v.draftRev++
	}
	if threadSaved != "" {
		v.threadDraft = threadSaved
		v.threadRev++
	}
	v.mu.Unlock()
}

// SetItemDraft programmatically updates the shared Matter creation input.
func (v *View) SetItemDraft(text string) {
	v.mu.Lock()
	v.itemDraft = text
	v.itemRev++
	v.mu.Unlock()
}

// Module implements features.FeatureModule so the Multi-Role Agent Chat Room
// compiles and registers cleanly only when opted in via build tags & `-features`.
type Module struct {
	View *View
}

// NewModule constructs an opt-in FeatureModule for Marlin's Agent Chat Room.
func NewModule(ledger *features.GovernanceLedger) *Module {
	return &Module{
		View: NewView(NewController(ledger)),
	}
}

// SetGovernanceLedger links the module's room RBAC checks into Marlin's Tier-0 audit trail.
func (m *Module) SetGovernanceLedger(ledger *features.GovernanceLedger) {
	if m != nil && m.View != nil && m.View.Ctrl != nil {
		m.View.Ctrl.SetGovernanceLedger(ledger)
	}
}

// SetResilience attaches the shared crash-durable SessionResilience store and
// immediately restores any unsent room/thread drafts after a restart.
func (m *Module) SetResilience(r *resilience.SessionResilience, onDraftChanged func(scopeID, text string)) {
	if m == nil || m.View == nil || m.View.Ctrl == nil || r == nil {
		return
	}
	m.View.Ctrl.Resilience = r
	m.View.Ctrl.OnDraftChanged = onDraftChanged
	m.View.RestoreDraftsFromResilience()
}

func (m *Module) Meta() features.FeatureMeta {
	return features.FeatureMeta{
		ID:                   features.FeatureChatRoom,
		Name:                 "Multi-Role Agent Room",
		Description:          "Shared Marlin channels, role-governed AI teammates, collaborative matters, and thread working memory",
		Category:             "collaboration",
		TrustTier:            features.Tier0HostNative,
		RequiredCapabilities: []string{"context.shared", "governance.rbac", "gate.prompt"},
		DefaultEnabled:       true,
	}
}

func (m *Module) Register(host *features.Host) error {
	host.AddBuffer(shell.PaneBuffer{
		BufferID: BufferIDChatRoom,
		Title:    "Agent Room",
		Kind:     "native-gpu",
	}, func(surface any, theme any) {
		c, ok1 := surface.(*ui.Context)
		t, ok2 := theme.(*ui.Theme)
		if ok1 && ok2 {
			m.View.Render(c, t, false)
		}
	})
	host.AddCommand(input.CommandEntry{
		Name:        CommandOpenChatRoom,
		Description: "Open Multi-Role Agent Room buffer (channels, matters, role governance)",
	})
	return nil
}

// Render paints Marlin's Multi-Role Agent Chat Room on Desktop (`compact=false`)
// or Phone (`compact=true`, zero horizontal overflow on 375–412px viewports).
func (v *View) Render(c *ui.Context, t *ui.Theme, compact bool) {
	dt := mygoshell.DeriveTheme("chatroom", "ChatRoom", themeFromUI(t))
	snap := v.Ctrl.Snapshot()

	v.mu.Lock()
	tab := v.activeTab
	draft := v.draft
	draftRev := v.draftRev
	threadDraft := v.threadDraft
	threadRev := v.threadRev
	itemDraft := v.itemDraft
	itemRev := v.itemRev
	v.mu.Unlock()

	if !compact && tab == TabRooms {
		tab = TabTimeline
	}

	partsByID := make(map[string]Participant, len(snap.Participants))
	for _, p := range snap.Participants {
		partsByID[p.ID] = p
	}
	var activeChannel Channel
	for _, ch := range snap.Channels {
		if ch.ID == snap.ActiveChannelID {
			activeChannel = ch
			break
		}
	}

	var (
		nextTab       = tab
		selectChID    string
		appendMention string
		sendMainMsg   bool
		sendThreadMsg bool
		openThreadID  string
		closeThread   bool
		reactMsgID    string
		reactTag      string
		addItemNow    bool
		transItemID   string
		transStatus   ItemStatus
		rbacTargetID  string
		rbacNextPerm  *RolePermissions
		retryRoomQNow bool
	)

	root := ui.Column(c.Key("chatroom-root")).Grow(1).MinWidth(0).Padding(10).Gap(8).Radius(12).
		Background(rgbColor(dt.Surface)).Border(1, rgbaColor(dt.Border, 0.85))
	if !compact && root.Bounds().W > 0 && root.Bounds().W < 720 {
		compact = true
	}

	root.Children(func() {
		// Top room header + mode tabs (2 rows on compact phone widths for zero horizontal overflow).
		var segs []mygoshell.SegmentedItem
		if compact {
			segs = append(segs, mygoshell.SegmentedItem{ID: string(TabRooms), Label: "Rooms"})
		}
		segs = append(segs,
			mygoshell.SegmentedItem{ID: string(TabTimeline), Label: "Timeline"},
			mygoshell.SegmentedItem{ID: string(TabMatters), Label: fmt.Sprintf("Matters (%d)", len(snap.Items))},
			mygoshell.SegmentedItem{ID: string(TabRoles), Label: "Roles"},
		)
		if compact {
			ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					mygoshell.AppleBadgePill(c, "AGENT ROOM", dt.Accent, dt)
					ui.Text(c, activeChannel.Name).FontSize(14).FontWeight(700).TextColor(rgbColor(dt.Ink))
				})
				if chosen := mygoshell.AppleSegmentedControl(c, "cr-tabs", segs, string(tab), dt, true); chosen != "" {
					nextTab = ViewTab(chosen)
				}
			})
		} else {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				mygoshell.AppleBadgePill(c, "AGENT ROOM", dt.Accent, dt)
				ui.Text(c, activeChannel.Name).FontSize(14).FontWeight(700).TextColor(rgbColor(dt.Ink))
				if activeChannel.Topic != "" {
					ui.Text(c, activeChannel.Topic).FontSize(11.5).TextColor(rgbColor(dt.InkMuted))
				}
				ui.Spacer(c)
				if chosen := mygoshell.AppleSegmentedControl(c, "cr-tabs", segs, string(tab), dt, true); chosen != "" {
					nextTab = ViewTab(chosen)
				}
			})
		}

		if snap.LastError != "" {
			ui.Text(c, snap.LastError).FontSize(11.5).FontWeight(600).TextColor(rgbColor(dt.Danger))
		}
		if banner := snap.Continuity.BannerText(); banner != "" {
			bTone := dt.Warning
			if snap.Continuity.State == resilience.ConnOffline {
				bTone = dt.Danger
			}
			ui.Row(c.Key("cr-continuity-banner")).Padding(6, 10).Gap(8).Radius(10).AlignItems(ui.Center).
				Background(rgbaColor(bTone, 0.10)).Border(1, rgbaColor(bTone, 0.35)).Children(func() {
				mygoshell.AppleBadgePill(c, string(snap.Continuity.State), bTone, dt)
				ui.Text(c, banner).FontSize(11.5).FontWeight(600).TextColor(rgbColor(dt.Ink))
				ui.Spacer(c)
				if len(snap.Continuity.PendingOutbox) > 0 {
					if mygoshell.ApplePillButton(c, "cr-retry-q", "Retry Queue", mygoshell.BtnTinted, dt, nil, true).Clicked() {
						retryRoomQNow = true
					}
				}
			})
		}

		// Main body: 3-column on Desktop, single active surface on Mobile.
		ui.Row(c).Grow(1).MinWidth(0).Gap(10).AlignItems(ui.Stretch).Children(func() {
			if !compact {
				v.renderLeftRail(c, dt, snap, &selectChID, &appendMention, false)
			}

			ui.Column(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Stretch).Children(func() {
				switch {
				case compact && snap.ActiveThreadID != "":
					v.renderThreadDrawer(c, dt, snap, partsByID, &threadDraft, threadRev, &sendThreadMsg, &closeThread, true)
				case compact && tab == TabRooms:
					v.renderLeftRail(c, dt, snap, &selectChID, &appendMention, true)
				case tab == TabMatters:
					v.renderMattersView(c, dt, snap, partsByID, &itemDraft, itemRev, &addItemNow, &transItemID, &transStatus)
				case tab == TabRoles:
					v.renderRolesGovernanceView(c, dt, snap, &rbacTargetID, &rbacNextPerm)
				default:
					v.renderTimelineView(c, dt, snap, partsByID, &draft, draftRev, &appendMention, &sendMainMsg, &openThreadID, &reactMsgID, &reactTag, compact)
				}
			})

			if !compact && snap.ActiveThreadID != "" {
				v.renderThreadDrawer(c, dt, snap, partsByID, &threadDraft, threadRev, &sendThreadMsg, &closeThread, false)
			}
		})
	})

	// Commit transient state & execute governed Controller actions outside layout closures.
	v.mu.Lock()
	v.activeTab = nextTab
	v.draft = draft
	v.threadDraft = threadDraft
	v.itemDraft = itemDraft
	if appendMention != "" {
		if strings.TrimSpace(v.draft) == "" {
			v.draft = appendMention + " "
		} else {
			v.draft = strings.TrimSpace(v.draft) + " " + appendMention + " "
		}
		v.draftRev++
	}
	v.mu.Unlock()

	if selectChID != "" {
		_ = v.Ctrl.SelectChannel(selectChID)
		if compact {
			v.mu.Lock()
			v.activeTab = TabTimeline
			v.mu.Unlock()
		}
	}
	if openThreadID != "" {
		_ = v.Ctrl.OpenThread(openThreadID)
	}
	if closeThread {
		v.Ctrl.CloseThread()
	}
	if reactMsgID != "" && reactTag != "" {
		_ = v.Ctrl.ToggleReaction(reactMsgID, snap.CurrentActorID, reactTag)
	}
	if sendMainMsg {
		if _, err := v.Ctrl.PostMessage(snap.ActiveChannelID, snap.CurrentActorID, draft, ""); err == nil {
			v.SetDraft("")
		}
	}
	if sendThreadMsg && snap.ActiveThreadID != "" {
		if _, err := v.Ctrl.PostMessage(snap.ActiveChannelID, snap.CurrentActorID, threadDraft, snap.ActiveThreadID); err == nil {
			v.SetThreadDraft("")
		}
	}
	if addItemNow {
		if _, err := v.Ctrl.CreateItem(snap.ActiveChannelID, snap.CurrentActorID, itemDraft, snap.CurrentActorID); err == nil {
			v.SetItemDraft("")
		}
	}
	if transItemID != "" {
		_ = v.Ctrl.TransitionItem(transItemID, snap.CurrentActorID, transStatus)
	}
	if rbacTargetID != "" && rbacNextPerm != nil {
		_ = v.Ctrl.UpdateRolePermissions(rbacTargetID, snap.CurrentActorID, *rbacNextPerm)
	}
	if retryRoomQNow && v.Ctrl.OnRetryOutbox != nil {
		v.Ctrl.OnRetryOutbox(snap.ActiveChannelID)
	}
}

func (v *View) renderLeftRail(
	c *ui.Context,
	dt mygoshell.DerivedTheme,
	snap RoomSnapshot,
	selectChID *string,
	appendMention *string,
	fullWidth bool,
) {
	rail := ui.Column(c.Key("cr-rail")).Padding(8).Gap(8).Radius(12).
		Background(rgbColor(dt.SurfaceRecessed)).Border(1, rgbaColor(dt.Border, 0.7)).AlignItems(ui.Stretch)
	if fullWidth {
		rail.Grow(1).MinWidth(0)
	} else {
		rail.Width(204)
	}
	rail.Children(func() {
		ui.Text(c, "CHANNELS").FontSize(10).FontWeight(700).LetterSpacing(0.5).TextColor(rgbColor(dt.InkMuted))
		for _, ch := range snap.Channels {
			channel := ch
			variant := mygoshell.BtnGlass
			if channel.ID == snap.ActiveChannelID {
				variant = mygoshell.BtnTinted
			}
			lbl := channel.Name
			if channel.UnreadCount > 0 {
				lbl = fmt.Sprintf("%s (%d)", channel.Name, channel.UnreadCount)
			}
			if mygoshell.ApplePillButton(c, "cr-ch-"+channel.ID, lbl, variant, dt, nil, true).Clicked() {
				*selectChID = channel.ID
			}
		}

		ui.Text(c, "AGENT ROLES & TEAM").FontSize(10).FontWeight(700).LetterSpacing(0.5).TextColor(rgbColor(dt.InkMuted))
		for _, p := range snap.Participants {
			part := p
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(7, 7).Radius(3.5).Background(rgbColor(presenceTone(part.Presence, dt)))
				if mygoshell.ApplePillButton(c, "cr-part-"+part.ID, part.Handle, mygoshell.BtnGlass, dt, nil, true).Clicked() {
					*appendMention = part.Handle
				}
				ui.Spacer(c)
				ui.Text(c, part.RoleTitle).FontSize(10).TextColor(rgbColor(dt.InkMuted))
			})
		}
	})
}

func (v *View) renderTimelineView(
	c *ui.Context,
	dt mygoshell.DerivedTheme,
	snap RoomSnapshot,
	parts map[string]Participant,
	draft *string,
	draftRev uint64,
	appendMention *string,
	sendNow *bool,
	openThreadID *string,
	reactMsgID *string,
	reactTag *string,
	compact bool,
) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Stretch).Children(func() {
		for _, m := range snap.Messages {
			msg := m
			author := parts[msg.AuthorID]
			replies := v.Ctrl.ThreadReplyCount(msg.ID)
			mygoshell.AppleGlassCard(c, "cr-msg-"+msg.ID, dt, 12, 10, 5, func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					name := author.DisplayName
					if name == "" {
						name = msg.AuthorID
					}
					ui.Text(c, name).FontSize(12.5).FontWeight(700).TextColor(rgbColor(dt.Ink))
					roleTone := dt.Info
					if author.Kind == ParticipantAgent {
						roleTone = dt.Accent
					}
					if author.RoleTitle != "" {
						mygoshell.AppleBadgePill(c, author.RoleTitle, roleTone, dt)
					}
					if author.HostNode != "" && !compact {
						mygoshell.AppleBadgePill(c, author.HostNode, dt.InkMuted, dt)
					}
					if msg.Pinned {
						mygoshell.AppleBadgePill(c, "PINNED", dt.Warning, dt)
					}
					if msg.Delivery == resilience.DeliveryQueued || msg.Delivery == resilience.DeliveryFailed {
						dTone := dt.Warning
						if msg.Delivery == resilience.DeliveryFailed {
							dTone = dt.Danger
						}
						mygoshell.AppleBadgePill(c, string(msg.Delivery), dTone, dt)
					}
				})
				ui.Text(c, msg.Body).FontSize(13).LineHeight(1.4).TextColor(rgbColor(dt.Ink))
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ackCount := len(msg.Reactions["Ack"])
					ackLbl := "Ack"
					if ackCount > 0 {
						ackLbl = fmt.Sprintf("Ack (%d)", ackCount)
					}
					if mygoshell.ApplePillButton(c, "cr-ack-"+msg.ID, ackLbl, mygoshell.BtnGlass, dt, nil, true).Clicked() {
						*reactMsgID = msg.ID
						*reactTag = "Ack"
					}
					thLbl := "Reply in Thread"
					if replies > 0 {
						thLbl = fmt.Sprintf("Thread (%d)", replies)
					}
					if mygoshell.ApplePillButton(c, "cr-th-"+msg.ID, thLbl, mygoshell.BtnTinted, dt, nil, true).Clicked() {
						*openThreadID = msg.ID
					}
				})
			})
		}
		ui.Spacer(c)

		// Room composer with quick @role mention chips.
		ui.Column(c).Padding(8, 10).Gap(6).Radius(16).
			Background(rgbColor(dt.SurfaceElevated)).Border(1, rgbaColor(dt.Border, 0.85)).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				for _, p := range snap.Participants {
					if p.Kind != ParticipantAgent {
						continue
					}
					handle := p.Handle
					if mygoshell.ApplePillButton(c, "cr-q-"+p.ID, handle, mygoshell.BtnGlass, dt, nil, true).Clicked() {
						*appendMention = handle
					}
				}
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInputBase(c.Key(fmt.Sprintf("cr-input-%d", draftRev)), draft).
					Placeholder("Message room or @mention an agent role...").
					FontSize(13).TextColor(rgbColor(dt.Ink)).Grow(1).Padding(4, 2)
				if mygoshell.ApplePillButton(c, "cr-send", "Post", mygoshell.BtnPrimary, dt, nil, true).Clicked() {
					*sendNow = true
				}
			})
		})
	})
}

func (v *View) renderThreadDrawer(
	c *ui.Context,
	dt mygoshell.DerivedTheme,
	snap RoomSnapshot,
	parts map[string]Participant,
	threadDraft *string,
	threadRev uint64,
	sendNow *bool,
	closeNow *bool,
	fullWidth bool,
) {
	drawer := ui.Column(c.Key("cr-thread")).Padding(10).Gap(8).Radius(12).
		Background(rgbColor(dt.SurfaceRecessed)).Border(1, rgbaColor(dt.Accent, 0.35)).AlignItems(ui.Stretch)
	if fullWidth {
		drawer.Grow(1).MinWidth(0)
	} else {
		drawer.Width(280)
	}
	drawer.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			mygoshell.AppleBadgePill(c, "THREAD MEMORY", dt.Accent, dt)
			ui.Spacer(c)
			if mygoshell.ApplePillButton(c, "cr-th-close", "Close Thread", mygoshell.BtnGlass, dt, nil, true).Clicked() {
				*closeNow = true
			}
		})
		for _, rep := range snap.ThreadReplies {
			author := parts[rep.AuthorID]
			mygoshell.AppleGlassCard(c, "cr-rep-"+rep.ID, dt, 10, 8, 3, func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, author.DisplayName).FontSize(12).FontWeight(700).TextColor(rgbColor(dt.Ink))
					if author.RoleTitle != "" {
						mygoshell.AppleBadgePill(c, author.RoleTitle, dt.Info, dt)
					}
				})
				ui.Text(c, rep.Body).FontSize(12).LineHeight(1.38).TextColor(rgbColor(dt.InkSecondary))
			})
		}
		ui.Spacer(c)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.TextInputBase(c.Key(fmt.Sprintf("cr-th-in-%d", threadRev)), threadDraft).
				Placeholder("Reply in thread...").
				FontSize(12.5).TextColor(rgbColor(dt.Ink)).Grow(1).Padding(4, 2)
			if mygoshell.ApplePillButton(c, "cr-th-send", "Reply", mygoshell.BtnPrimary, dt, nil, true).Clicked() {
				*sendNow = true
			}
		})
	})
}

func (v *View) renderMattersView(
	c *ui.Context,
	dt mygoshell.DerivedTheme,
	snap RoomSnapshot,
	parts map[string]Participant,
	itemDraft *string,
	itemRev uint64,
	addNow *bool,
	transID *string,
	transNext *ItemStatus,
) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInputBase(c.Key(fmt.Sprintf("cr-item-in-%d", itemRev)), itemDraft).
				Placeholder("Add shared matter for humans & agent roles...").
				FontSize(13).TextColor(rgbColor(dt.Ink)).Grow(1).Padding(6, 10)
			if mygoshell.ApplePillButton(c, "cr-item-add", "Add Matter", mygoshell.BtnPrimary, dt, nil, true).Clicked() {
				*addNow = true
			}
		})
		for _, it := range snap.Items {
			item := it
			owner := parts[item.OwnerID]
			mygoshell.AppleGlassCard(c, "cr-it-"+item.ID, dt, 12, 10, 5, func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					mygoshell.AppleBadgePill(c, string(item.Status), itemStatusTone(item.Status, dt), dt)
					ui.Text(c, item.Title).FontSize(13).FontWeight(600).TextColor(rgbColor(dt.Ink))
					ui.Spacer(c)
					if item.Status != ItemDone {
						if mygoshell.ApplePillButton(c, "cr-done-"+item.ID, "Mark Done", mygoshell.BtnSuccess, dt, nil, true).Clicked() {
							*transID = item.ID
							*transNext = ItemDone
						}
					}
				})
				ui.Textf(c, "Owner: %s (%s)", owner.DisplayName, owner.RoleTitle).FontSize(11).TextColor(rgbColor(dt.InkMuted))
			})
		}
	})
}

func (v *View) renderRolesGovernanceView(
	c *ui.Context,
	dt mygoshell.DerivedTheme,
	snap RoomSnapshot,
	targetID *string,
	nextPerm **RolePermissions,
) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Stretch).Children(func() {
		ui.Text(c, "ROLE-BASED PERMISSION MATRIX (HUMAN-GOVERNED · AGENTS NEVER APPROVE GATES)").
			FontSize(10.5).FontWeight(700).LetterSpacing(0.4).TextColor(rgbColor(dt.InkMuted))
		for _, p := range snap.Participants {
			part := p
			mygoshell.AppleGlassCard(c, "cr-rbac-"+part.ID, dt, 12, 10, 6, func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, part.DisplayName).FontSize(13).FontWeight(700).TextColor(rgbColor(dt.Ink))
					mygoshell.AppleBadgePill(c, part.Handle, dt.Accent, dt)
					mygoshell.AppleBadgePill(c, part.RoleTitle, dt.Info, dt)
					ui.Spacer(c)
					if part.Kind == ParticipantAgent {
						mygoshell.AppleBadgePill(c, "GATE APPROVE: HUMAN ONLY", dt.Warning, dt)
					} else {
						mygoshell.AppleBadgePill(c, "GATE APPROVER", dt.Success, dt)
					}
				})
				if part.Kind == ParticipantAgent {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						postLbl := permToggleLabel("Post", part.Permissions.CanPost)
						if mygoshell.ApplePillButton(c, "rbac-post-"+part.ID, part.ID+" "+postLbl, permVariant(part.Permissions.CanPost), dt, nil, true).Clicked() {
							cp := part.Permissions
							cp.CanPost = !cp.CanPost
							*targetID = part.ID
							*nextPerm = &cp
						}
						itemLbl := permToggleLabel("Matters", part.Permissions.CanManageItems)
						if mygoshell.ApplePillButton(c, "rbac-items-"+part.ID, part.ID+" "+itemLbl, permVariant(part.Permissions.CanManageItems), dt, nil, true).Clicked() {
							cp := part.Permissions
							cp.CanManageItems = !cp.CanManageItems
							*targetID = part.ID
							*nextPerm = &cp
						}
						toolLbl := permToggleLabel("Tools", part.Permissions.CanInvokeTools)
						if mygoshell.ApplePillButton(c, "rbac-tools-"+part.ID, part.ID+" "+toolLbl, permVariant(part.Permissions.CanInvokeTools), dt, nil, true).Clicked() {
							cp := part.Permissions
							cp.CanInvokeTools = !cp.CanInvokeTools
							*targetID = part.ID
							*nextPerm = &cp
						}
					})
				}
			})
		}
	})
}

func permToggleLabel(name string, on bool) string {
	if on {
		return name + ": ON"
	}
	return name + ": OFF"
}

func permVariant(on bool) mygoshell.AppleButtonVariant {
	if on {
		return mygoshell.BtnTinted
	}
	return mygoshell.BtnGlass
}

func presenceTone(p PresenceState, dt mygoshell.DerivedTheme) schema.RGB {
	switch p {
	case PresenceOnline:
		return dt.Success
	case PresenceWorking, PresenceThinking:
		return dt.Accent
	case PresenceWaitingGate:
		return dt.Warning
	default:
		return dt.InkMuted
	}
}

func itemStatusTone(st ItemStatus, dt mygoshell.DerivedTheme) schema.RGB {
	switch st {
	case ItemDone:
		return dt.Success
	case ItemBlocked:
		return dt.Danger
	case ItemInProgress:
		return dt.Accent
	default:
		return dt.Info
	}
}

func themeFromUI(t *ui.Theme) schema.UITheme {
	if t == nil {
		return schema.DefaultUITheme()
	}
	// Preserve light/dark luminance from MyGo ui.Theme into Marlin DerivedTheme tokens.
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
