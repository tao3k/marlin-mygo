// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package device

import (
	"context"
	"fmt"

	"github.com/egoist/mygo"
)

const maxDesktopClipboardBytes = 64 * 1024

// MyGoDesktopClipboard implements ClipboardProtocol directly on MyGo's native
// zero-cgo OS clipboard module (macOS NSPasteboard, Linux Wayland/X11, Windows).
type MyGoDesktopClipboard struct{}

func (MyGoDesktopClipboard) PermissionState(context.Context) (OsPermissionState, error) {
	return PermissionGranted, nil
}

func (MyGoDesktopClipboard) ReadText(context.Context) (string, error) {
	txt := mygo.Clipboard.ReadText()
	if len(txt) > maxDesktopClipboardBytes {
		txt = txt[:maxDesktopClipboardBytes]
	}
	return txt, nil
}

// MyGoDesktopNotification implements NotificationProtocol directly on MyGo's
// native OS notification center (requires a Rust GateReceipt on Bridge.Dispatch).
type MyGoDesktopNotification struct{}

func (MyGoDesktopNotification) PermissionState(context.Context) (OsPermissionState, error) {
	if !mygo.NotificationsSupported() {
		return PermissionUnsupported, nil
	}
	return PermissionGranted, nil
}

func (MyGoDesktopNotification) SendNotification(_ context.Context, req NotificationRequest) error {
	if !mygo.NotificationsSupported() {
		return fmt.Errorf("%w: desktop notifications require a packaged MyGo app bundle", ErrPermissionDenied)
	}
	n := mygo.NewNotification(mygo.NotificationOptions{
		Title: req.Title,
		Body:  req.Body,
		Group: "marlin.studio",
	})
	n.Show()
	return nil
}

// RegisterDesktopMyGoAdapters binds real zero-cgo MyGo desktop clipboard and
// notification providers onto bridge under device.desktop.* capabilities.
func RegisterDesktopMyGoAdapters(ctx context.Context, bridge *Bridge) error {
	if bridge == nil {
		return fmt.Errorf("device: nil bridge")
	}
	return bridge.RegisterProvider(ctx, &DesktopProvider{
		Clipboard: MyGoDesktopClipboard{},
		Notify:    MyGoDesktopNotification{},
	})
}
