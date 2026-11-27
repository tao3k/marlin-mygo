// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/notify"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

var (
	svgIconBell = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/></svg>`,
	))
	svgIconBellOff = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M13.73 21a2 2 0 0 1-3.46 0"/><path d="M18.63 13A17.89 17.89 0 0 1 18 8"/><path d="M6.26 6.26A5.86 5.86 0 0 0 6 8c0 7-3 9-3 9h14"/><path d="M18 8a6 6 0 0 0-9.33-5"/><line x1="1" y1="1" x2="23" y2="23"/></svg>`,
	))
)

// NotificationController bridges engine/notify.Center into Tier-0 Liquid Glass
// floating toasts and the interactive Activity Center drawer.
type NotificationController struct {
	Center    *notify.Center
	OnChanged func()

	mu            sync.Mutex
	filter        notify.FilterMode
	cachedVer     uint64
	cachedBellLbl string
	cachedBellDND bool
}

// NewNotificationController wraps a notify.Center for Tier-0 GPU rendering.
func NewNotificationController(center *notify.Center) *NotificationController {
	if center == nil {
		center = notify.NewCenter(notify.CenterConfig{})
	}
	return &NotificationController{
		Center: center,
		filter: notify.FilterAll,
	}
}

// BellLabel returns a concise, cached status-bar label for the Activity Center pill.
func (nc *NotificationController) BellLabel() string {
	if nc == nil || nc.Center == nil {
		return "Activity"
	}
	snap := nc.Center.Snapshot()
	nc.mu.Lock()
	defer nc.mu.Unlock()
	if nc.cachedBellLbl != "" && nc.cachedVer == snap.Version && nc.cachedBellDND == snap.DoNotDisturb {
		return nc.cachedBellLbl
	}
	nc.cachedVer = snap.Version
	nc.cachedBellDND = snap.DoNotDisturb
	switch {
	case snap.UnreadCount > 0 && snap.DoNotDisturb:
		nc.cachedBellLbl = fmt.Sprintf("DND · %d", snap.UnreadCount)
	case snap.UnreadCount > 0:
		nc.cachedBellLbl = fmt.Sprintf("Activity · %d", snap.UnreadCount)
	case snap.DoNotDisturb:
		nc.cachedBellLbl = "DND"
	default:
		nc.cachedBellLbl = "Activity"
	}
	return nc.cachedBellLbl
}

// BellIcon returns the crisp vector bell or muted-bell icon for DND state.
func (nc *NotificationController) BellIcon() *ui.SVG {
	if nc != nil && nc.Center != nil && nc.Center.DoNotDisturb() {
		return svgIconBellOff
	}
	return svgIconBell
}

// RenderFloatingToasts renders up to MaxFloatingToasts non-blocking Liquid Glass
// toast cards anchored to the bottom-right above workspace panes and WebViews.
func (nc *NotificationController) RenderFloatingToasts(c *ui.Context, dt DerivedTheme) {
	if nc == nil || nc.Center == nil {
		return
	}
	snap := nc.Center.Snapshot()
	if len(snap.FloatingToasts) == 0 {
		return
	}

	ui.Column(c.Key("floating-toasts")).Absolute().Right(16).Bottom(40).ZIndex(900).
		Width(368).Gap(8).AlignItems(ui.Stretch).Children(func() {
		for _, it := range snap.FloatingToasts {
			item := it
			nc.renderNotificationCard(c, dt, item, true)
		}
	})
}

// RenderActivityCenter renders the full Tier-0 Activity Center modal drawer with
// category filters, Do-Not-Disturb toggle, and actionable history cards.
func (nc *NotificationController) RenderActivityCenter(c *ui.Context, _ *ui.Theme, dt DerivedTheme, onClose func()) {
	if nc == nil || nc.Center == nil {
		ui.Text(c, "Notification center unavailable.").TextColor(toColor(dt.InkMuted))
		return
	}
	snap := nc.Center.Snapshot()
	nc.mu.Lock()
	filter := nc.filter
	if filter == "" {
		filter = notify.FilterAll
		nc.filter = filter
	}
	nc.mu.Unlock()

	ui.Column(c).Width(560).Gap(12).AlignItems(ui.Stretch).Children(func() {
		// Header row: Title + Unread pill + DND toggle + Close.
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			AppleBadgePill(c, "TIER-0 ACTIVITY", dt.Accent, dt)
			ui.Text(c, "Notifications & System Activity").FontSize(16).FontWeight(700).TextColor(toColor(dt.Ink))
			if snap.UnreadCount > 0 {
				AppleBadgePill(c, fmt.Sprintf("%d UNREAD", snap.UnreadCount), dt.Warning, dt)
			}
			ui.Spacer(c)
			dndLbl := "DND: Off"
			dndVar := BtnGlass
			dndIcon := svgIconBell
			if snap.DoNotDisturb {
				dndLbl = "DND: On"
				dndVar = BtnTinted
				dndIcon = svgIconBellOff
			}
			if ApplePillButton(c, "notify-dnd", dndLbl, dndVar, dt, dndIcon, true).Clicked() {
				nc.Center.SetDoNotDisturb(!snap.DoNotDisturb)
				nc.notifyChanged()
			}
			if onClose != nil {
				if ApplePillButton(c, "notify-close", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
					onClose()
				}
			}
		})

		// Filter segmented control + bulk actions.
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			if chosen := AppleSegmentedControl(c, "notify-filter", []SegmentedItem{
				{ID: string(notify.FilterAll), Label: "All"},
				{ID: string(notify.FilterUnread), Label: "Unread"},
				{ID: string(notify.FilterAlerts), Label: "Alerts"},
				{ID: string(notify.FilterPlugins), Label: "Plugins"},
			}, string(filter), dt, true); chosen != "" {
				nc.mu.Lock()
				nc.filter = notify.FilterMode(chosen)
				nc.mu.Unlock()
			}
			ui.Spacer(c)
			if snap.UnreadCount > 0 {
				if ApplePillButton(c, "notify-mark-all", "Mark All Read", BtnGlass, dt, svgIconCheck, true).Clicked() {
					nc.Center.MarkAllRead()
					nc.notifyChanged()
				}
			}
			if len(snap.History) > 0 {
				if ApplePillButton(c, "notify-clear", "Clear History", BtnGlass, dt, nil, true).Clicked() {
					nc.Center.ClearHistory()
					nc.notifyChanged()
				}
			}
		})

		if snap.DoNotDisturb {
			ui.Row(c).Padding(7, 10).Radius(10).Gap(8).AlignItems(ui.Center).
				Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
				ui.Icon(c, svgIconBellOff).Size(13, 13).TextColor(toColor(dt.Warning))
				ui.Text(c, "Do Not Disturb active: routine toasts muted; Tier-0 Gates & Errors still break through.").
					FontSize(11).TextColor(toColor(dt.InkSecondary))
			})
		}

		filtered := notify.FilterHistory(snap.History, filter)
		if len(filtered) == 0 {
			ui.Column(c).Padding(24).Gap(6).AlignItems(ui.Center).Radius(12).
				Background(toColor(dt.SurfaceRecessed)).Children(func() {
				ui.Text(c, "All caught up").FontSize(14).FontWeight(700).TextColor(toColor(dt.Ink))
				ui.Text(c, "No matching system, gRPC, auth, or plugin notifications.").
					FontSize(12).TextColor(toColor(dt.InkMuted))
			})
			return
		}

		ui.Column(c).Gap(8).AlignItems(ui.Stretch).Children(func() {
			limit := min(len(filtered), 12)
			for i := 0; i < limit; i++ {
				nc.renderNotificationCard(c, dt, filtered[i], false)
			}
		})
	})
}

func (nc *NotificationController) renderNotificationCard(
	c *ui.Context,
	dt DerivedTheme,
	item notify.Item,
	floating bool,
) {
	accent := notificationToneColor(dt, item.Severity, item.Source)
	sourceLbl := strings.ToUpper(string(item.Source))
	if item.Source == notify.SourcePlugin && item.PluginID != "" {
		sourceLbl = "PLUGIN · " + item.PluginID
	}

	keyPrefix := fmt.Sprintf("ncard-%d", item.ID)
	if floating {
		keyPrefix = fmt.Sprintf("toast-%d", item.ID)
	}

	bg := toColor(dt.SurfaceElevated)
	if floating {
		bg = toColor(dt.Surface)
	}

	ui.Row(c.Key(keyPrefix)).Radius(12).Background(bg).
		Border(1, rgbaFromRGB(accent, 0.35)).
		Shadow(0, 6, 18, -4, ambientShadowColor(dt)).
		AlignItems(ui.Stretch).Children(func() {
		// Left semantic accent stripe.
		ui.Box(c).Width(4).Radius(12, 0, 0, 12).Background(toColor(accent))

		ui.Column(c).Grow(1).MinWidth(0).Padding(10, 12).Gap(5).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				if !item.Read {
					ui.Box(c).Size(6, 6).Radius(3).Background(toColor(accent))
				}
				AppleBadgePill(c, sourceLbl, accent, dt)
				if item.Count > 1 {
					AppleBadgePill(c, fmt.Sprintf("x%d", item.Count), dt.InkMuted, dt)
				}
				ui.Spacer(c)
				if floating {
					if ApplePillButton(c, keyPrefix+"-dismiss", "Dismiss", BtnGlass, dt, svgIconXMark, true).Clicked() {
						nc.Center.DismissToast(item.ID)
						nc.notifyChanged()
					}
				} else if !item.Read {
					if ApplePillButton(c, keyPrefix+"-read", "Mark Read", BtnGlass, dt, svgIconCheck, true).Clicked() {
						nc.Center.MarkRead(item.ID)
						nc.notifyChanged()
					}
				}
			})

			ui.Text(c, item.Title).FontSize(13).FontWeight(700).TextColor(toColor(dt.Ink))
			if item.Message != "" {
				ui.Text(c, item.Message).FontSize(11.5).TextColor(toColor(dt.InkSecondary))
			}

			if item.HasProgress {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Progress(c, float64(item.Progress)).Grow(1).Height(5)
					ui.Textf(c, "%d%%", int(item.Progress*100)).FontSize(10.5).FontWeight(600).TextColor(toColor(dt.InkMuted))
				})
			}

			if len(item.Actions) > 0 {
				ui.Row(c).Gap(6).Justify(ui.End).Children(func() {
					for _, act := range item.Actions {
						a := act
						v := BtnGlass
						if a.Primary {
							v = BtnPrimary
						}
						if ApplePillButton(c, keyPrefix+"-act-"+a.ID, a.Label, v, dt, nil, true).Clicked() {
							nc.Center.TriggerAction(item.ID, a.ID)
							nc.notifyChanged()
						}
					}
				})
			}
		})
	})
}

func notificationToneColor(dt DerivedTheme, sev notify.Severity, src notify.SourceKind) schema.RGB {
	if src == notify.SourceGate {
		return dt.Warning
	}
	switch sev {
	case notify.SeveritySuccess:
		return dt.Success
	case notify.SeverityWarning:
		return dt.Warning
	case notify.SeverityError:
		return dt.Danger
	default:
		return dt.Info
	}
}

func (nc *NotificationController) notifyChanged() {
	if nc.OnChanged != nil {
		nc.OnChanged()
	}
}
