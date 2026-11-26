// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/notify"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

// wireFederationTargetSwitching connects Federation Cockpit Switch and
// Reconnect actions to the shared SwitchableTargetProvider so desktop stream
// pumps (SessionDriver, ProjectionStreamBridge, TurnStreamController) hot-swap
// cleanly across local sidecar and remote gRPC agents.
func wireFederationTargetSwitching(
	ctx context.Context,
	app *mygoshell.WorkbenchApp,
	targets *transport.TargetRegistry,
	federation *transport.FederationSupervisor,
	switcher *grpcwire.SwitchableTargetProvider,
	notifyCenter *notify.Center,
	win *mygo.Window,
) {
	if app == nil || app.FederationUI == nil {
		return
	}
	invalidate := func() {
		if win != nil {
			win.Invalidate()
		}
	}
	publish := func(sev notify.Severity, title, msg string) {
		if notifyCenter == nil {
			return
		}
		_, _ = notifyCenter.Publish(notify.NotificationSpec{
			Severity: sev,
			Source:   notify.SourceHost,
			Title:    title,
			Message:  msg,
		})
	}

	app.FederationUI.OnSelectTarget = func(targetID string) {
		if targets != nil {
			_, _ = targets.Switch(targetID)
		}
		switchedLive := false
		if switcher != nil {
			if ok, _ := switcher.SwitchTo(targetID); ok {
				switchedLive = true
			} else if ok, _ := switcher.ResolveFromRegistry(targets, targetID); ok {
				switchedLive = true
			}
		}
		msg := fmt.Sprintf("Active target set to %s", targetID)
		if switchedLive {
			msg = fmt.Sprintf("Live gRPC streams hot-swapped to %s", targetID)
		}
		publish(notify.SeveritySuccess, "Switched Target", msg)
		if federation != nil {
			go func() {
				federation.ProbeAll(ctx, 2*time.Second)
				invalidate()
			}()
		}
		invalidate()
	}

	app.FederationUI.OnReconnectTarget = func(targetID string) {
		if switcher != nil {
			if switcher.ActiveID() == targetID {
				switcher.ReconnectActive()
			} else {
				switcher.ResolveFromRegistry(targets, targetID)
			}
		}
		publish(notify.SeverityInfo, "Reconnecting Target", fmt.Sprintf("Probing & re-establishing streams for %s", targetID))
		if federation != nil {
			go func() {
				federation.ProbeAll(ctx, 2*time.Second)
				invalidate()
			}()
		}
		invalidate()
	}
}
