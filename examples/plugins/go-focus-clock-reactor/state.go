// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package main

import (
	"encoding/json"
	"fmt"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	gouir "github.com/tao3k/marlin-mygo/sdk/go-ui-ir"
)

const (
	defaultFocusSec = 25 * 60
	maxQuickLogs    = 4
	dailyBuckets    = 16
)

// orgClockProjection mirrors the canonical `tao3k/orgize` OrgizeClockRollupResponseDto
// fields plus optional live host tick updates (zero Org syntax parsing in the plugin).
type orgClockProjection struct {
	Records []struct {
		Title      string `json:"title"`
		LocalClock struct {
			RunningEntries int    `json:"runningEntries"`
			TotalSeconds   uint64 `json:"totalSeconds"`
		} `json:"localClock"`
		Effort struct {
			Status string `json:"status"`
		} `json:"effort"`
	} `json:"records"`
	// Optional incremental host focus tick or quick-note event fields.
	TickSeconds int    `json:"tickSeconds,omitempty"`
	ActiveTitle string `json:"activeTitle,omitempty"`
	QuickNote   string `json:"quickNote,omitempty"`
	HourBucket  int    `json:"hourBucket,omitempty"`
}

// orgAgendaProjection mirrors the canonical `tao3k/orgize` OrgizeAgendaViewResponseDto.
type orgAgendaProjection struct {
	Cards []struct {
		DisplayDate string `json:"displayDate"`
		Time        string `json:"time"`
		Title       string `json:"title"`
		TodoState   string `json:"todoState"`
		Urgency     struct {
			Total int `json:"total"`
		} `json:"urgency"`
	} `json:"cards"`
}

// FocusClockState holds pure, deterministic state for the personal focus alarm,
// Org clock effort status, next scheduled agenda card, and quick-capture log strip.
type FocusClockState struct {
	FrameSeq        uint64
	ActiveTitle     string
	EffortStatus    string
	ClockedTotalSec uint64
	DurationSec     int
	RemainingSec    int
	Running         bool
	CompletedBlocks int
	NextAgendaTitle string
	NextAgendaTime  string
	NextUrgency     int
	QuickLogs       []string
	HourlyMinutes   [dailyBuckets]uint16
}

// NewFocusClockState initializes the focus alarm reactor with sensible defaults.
func NewFocusClockState() FocusClockState {
	return FocusClockState{
		ActiveTitle:     "Deep Work Block",
		EffortStatus:    "underEffort",
		DurationSec:     defaultFocusSec,
		RemainingSec:    defaultFocusSec,
		NextAgendaTitle: "Review org.agenda projection",
		NextAgendaTime:  "14:00",
		NextUrgency:     80,
		HourlyMinutes:   [dailyBuckets]uint16{25, 50, 35, 20, 45, 55, 15, 0, 30, 50, 40, 25, 0, 0, 0, 0},
	}
}

// ApplyEvent ingests a host-scoped `org.clock` or `org.agenda` projection event
// and reports whether a focus block just completed (so main.go can emit `org.clock.out`).
func (s *FocusClockState) ApplyEvent(ev gouir.Event) (alarmCompleted bool, err error) {
	switch ev.Topic {
	case "org.agenda":
		var ag orgAgendaProjection
		if err := json.Unmarshal(ev.Payload, &ag); err != nil {
			return false, err
		}
		for _, c := range ag.Cards {
			if c.TodoState != "done" && c.Title != "" {
				s.NextAgendaTitle = c.Title
				if c.Time != "" {
					s.NextAgendaTime = c.Time
				} else {
					s.NextAgendaTime = c.DisplayDate
				}
				s.NextUrgency = c.Urgency.Total
				break
			}
		}
		return false, nil

	case "org.clock", "":
		var clk orgClockProjection
		if err := json.Unmarshal(ev.Payload, &clk); err != nil {
			return false, err
		}
		if len(clk.Records) > 0 {
			var total uint64
			running := false
			for _, rec := range clk.Records {
				total += rec.LocalClock.TotalSeconds
				if rec.LocalClock.RunningEntries > 0 {
					running = true
					if rec.Title != "" {
						s.ActiveTitle = rec.Title
					}
					if rec.Effort.Status != "" {
						s.EffortStatus = rec.Effort.Status
					}
				}
			}
			s.ClockedTotalSec = total
			if running {
				s.Running = true
			}
		}
		if clk.ActiveTitle != "" {
			s.ActiveTitle = clk.ActiveTitle
			s.Running = true
		}
		if clk.QuickNote != "" {
			s.AppendQuickLog(clk.QuickNote)
		}
		if clk.TickSeconds > 0 {
			s.Running = true
			s.ClockedTotalSec += uint64(clk.TickSeconds)
			if clk.HourBucket >= 0 && clk.HourBucket < dailyBuckets {
				s.HourlyMinutes[clk.HourBucket] += uint16(max(1, clk.TickSeconds/60))
			}
			if clk.TickSeconds >= s.RemainingSec {
				s.RemainingSec = 0
				s.Running = false
				s.CompletedBlocks++
				return true, nil
			}
			s.RemainingSec -= clk.TickSeconds
		}
		return false, nil

	default:
		return false, nil
	}
}

// AppendQuickLog pushes a quick note into a bounded 4-entry FIFO strip.
func (s *FocusClockState) AppendQuickLog(note string) {
	if note == "" {
		return
	}
	if len(s.QuickLogs) >= maxQuickLogs {
		copy(s.QuickLogs, s.QuickLogs[1:])
		s.QuickLogs[len(s.QuickLogs)-1] = note
		return
	}
	s.QuickLogs = append(s.QuickLogs, note)
}

// FormatCountdown formats seconds as MM:SS without heap allocations beyond the string.
func FormatCountdown(sec int) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%02d:%02d", sec/60, sec%60)
}

// BuildUITree builds the bounded schema.UIElement tree rendered directly on MyGo's GPU.
func (s *FocusClockState) BuildUITree() schema.UIElement {
	s.FrameSeq++
	statusLabel := "IDLE"
	statusTone := schema.ToneMuted
	if s.Running {
		statusLabel = "RUNNING"
		statusTone = schema.ToneSuccess
	} else if s.RemainingSec == 0 {
		statusLabel = "RINGING"
		statusTone = schema.ToneWarning
	}

	effortTone := schema.ToneInfo
	if s.EffortStatus == "overEffort" {
		effortTone = schema.ToneWarning
	}

	lastNote := "No quick logs yet"
	if n := len(s.QuickLogs); n > 0 {
		lastNote = s.QuickLogs[n-1]
	}

	clockInArgs := fmt.Sprintf(`{"title":%q,"durationSec":%d}`, s.NextAgendaTitle, defaultFocusSec)
	clockOutArgs := fmt.Sprintf(`{"title":%q,"completedBlocks":%d}`, s.ActiveTitle, s.CompletedBlocks)
	captureArgs := fmt.Sprintf(`{"kind":"note","title":%q,"target":{"kind":"datetree","insertPosition":"append"}}`, "Quick focus check-in: "+s.ActiveTitle)

	return gouir.Panel(
		"FOCUS CLOCK DOCK · GO 1.27 WASI GPU (0 MB WEBVIEW RAM)",
		schema.ToneInfo,
		gouir.Rows(
			gouir.Text(
				gouir.Span(FormatCountdown(s.RemainingSec)+"  ", schema.ToneAccent),
				gouir.Span(statusLabel+"  ·  ", statusTone),
				gouir.Span(s.ActiveTitle+" ", schema.ToneNormal),
				gouir.Span("["+s.EffortStatus+"]  ·  ", effortTone),
				gouir.Span(fmt.Sprintf("%d blocks · %dm clocked", s.CompletedBlocks, s.ClockedTotalSec/60), schema.ToneMuted),
			),
			gouir.Columns(
				gouir.ActionButton("Clock In 25m", "org.clock.in", clockInArgs, schema.ToneAccent),
				gouir.ActionButton("Clock Out", "org.clock.out", clockOutArgs, schema.ToneWarning),
				gouir.ActionButton("Quick Note", "org.capture.plan", captureArgs, schema.ToneInfo),
				gouir.Text(
					gouir.Span("   Next: ", schema.ToneInfo),
					gouir.Span(fmt.Sprintf("%s %s (U:%d)", s.NextAgendaTime, s.NextAgendaTitle, s.NextUrgency), schema.ToneNormal),
					gouir.Span("   ·   Log: ", schema.ToneMuted),
					gouir.Span(lastNote, schema.ToneMuted),
				),
			),
		),
	)
}

// BuildDrawList populates zero-JSON binary GPU commands painting the focus progress
// bar and 16-bucket daily focus intensity sparkline (<= 64 DrawCmds, 0 allocations).
func (s *FocusClockState) BuildDrawList(dst *[64]schema.DrawCmd) []schema.DrawCmd {
	s.FrameSeq++
	n := 0

	// 1. Focus countdown progress track + active fill.
	dst[n] = schema.DrawCmd{
		Op: schema.DrawOpFillRect, Radius: 4,
		X: 12, Y: 10, W: 280, H: 10,
		RGBA: 0x2c323cFF, Extra0: uint32(s.FrameSeq),
	}
	n++

	ratio := float32(0)
	if s.DurationSec > 0 {
		elapsed := s.DurationSec - s.RemainingSec
		if elapsed < 0 {
			elapsed = 0
		}
		if elapsed > s.DurationSec {
			elapsed = s.DurationSec
		}
		ratio = float32(elapsed) / float32(s.DurationSec)
	}
	fillColor := uint32(0x98be65FF) // green while running/normal
	if s.RemainingSec == 0 {
		fillColor = 0xecbe7bFF // warm amber when ringing
	} else if !s.Running {
		fillColor = 0xc678ddFF // accent purple when idle
	}
	dst[n] = schema.DrawCmd{
		Op: schema.DrawOpFillRect, Radius: 4,
		X: 12, Y: 10, W: 280 * ratio, H: 10,
		RGBA: fillColor, Extra0: uint32(s.FrameSeq), Extra1: uint32(s.CompletedBlocks),
	}
	n++

	// 2. 16-bucket daily focus intensity bars (8am..midnight).
	for i := 0; i < dailyBuckets && n < len(dst); i++ {
		mins := s.HourlyMinutes[i]
		if mins > 60 {
			mins = 60
		}
		barH := float32(4) + float32(mins)*0.6
		color := uint32(0x51afefCC)
		if mins >= 45 {
			color = 0x98be65FF
		} else if mins == 0 {
			color = 0x3e4451AA
		}
		dst[n] = schema.DrawCmd{
			Op: schema.DrawOpFillRect, Radius: 3,
			X:      12 + float32(i)*17.5,
			Y:      68 - barH,
			W:      13,
			H:      barH,
			RGBA:   color,
			Extra0: uint32(i),
			Extra1: uint32(mins),
		}
		n++
	}
	return dst[:n]
}
