// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package nativebridge defines platform-neutral Go contracts and wire codecs
// paired with `//mygo:native` Swift (iOS) and Kotlin (Android) OS adapters in
// ios/Sources/MarlinDeviceBridge/MarlinDeviceBridge.swift and
// android/src/main/kotlin/org/tao3k/marlin/device/MarlinDeviceBridge.kt.
package nativebridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tao3k/marlin-mygo/runtime/device"
)

// ErrNativeBridgeUnavailable is returned when invoked on a host OS without a
// compiled Swift/Kotlin native bridge module.
var ErrNativeBridgeUnavailable = errors.New("nativebridge: OS native module not linked on this target")

// NativeCallTransport abstracts the `//mygo:native` FFI invocation channel so
// Swift (iOS) and Kotlin (Android) adapters share identical typed JSON codecs
// and remain 100% testable under `CGO_ENABLED=0` on desktop CI.
type NativeCallTransport interface {
	Call(ctx context.Context, method string, argsJSON []byte) ([]byte, error)
}

// Adapter wraps a NativeCallTransport into device.PhotosProtocol,
// device.NotificationProtocol, and device.CaptureProtocol implementations.
type Adapter struct {
	Transport NativeCallTransport
	Platform  string // "ios" or "android"
}

func (a *Adapter) call(ctx context.Context, method string, req any, resp any) error {
	if a == nil || a.Transport == nil {
		return ErrNativeBridgeUnavailable
	}
	var payload []byte
	var err error
	if req != nil {
		payload, err = json.Marshal(req)
		if err != nil {
			return fmt.Errorf("nativebridge marshal %s: %w", method, err)
		}
	}
	raw, err := a.Transport.Call(ctx, method, payload)
	if err != nil {
		return err
	}
	if resp != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, resp); err != nil {
			return fmt.Errorf("nativebridge unmarshal %s: %w", method, err)
		}
	}
	return nil
}

func (a *Adapter) PermissionState(ctx context.Context) (device.OsPermissionState, error) {
	var out struct {
		State device.OsPermissionState `json:"state"`
	}
	if err := a.call(ctx, "permissionState", map[string]string{"platform": a.Platform}, &out); err != nil {
		return device.PermissionUnsupported, err
	}
	if out.State == "" {
		return device.PermissionPrompt, nil
	}
	return out.State, nil
}

func (a *Adapter) RecentScreenshots(ctx context.Context, limit int) ([]device.PhotoItem, error) {
	if limit <= 0 || limit > 32 {
		limit = 10
	}
	var out struct {
		Items []device.PhotoItem `json:"items"`
	}
	if err := a.call(ctx, "photos.recentScreenshots", map[string]int{"limit": limit}, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (a *Adapter) ExtractTextOCR(ctx context.Context, localID string) (string, error) {
	var out struct {
		Text string `json:"text"`
	}
	if err := a.call(ctx, "vision.extractOCR", map[string]string{"localId": localID}, &out); err != nil {
		return "", err
	}
	return out.Text, nil
}

func (a *Adapter) SendNotification(ctx context.Context, req device.NotificationRequest) error {
	return a.call(ctx, "notify.send", req, nil)
}

func (a *Adapter) DrainPendingCaptures(ctx context.Context) ([]device.CapturePayload, error) {
	var out struct {
		Captures []device.CapturePayload `json:"captures"`
	}
	if err := a.call(ctx, "capture.drain", nil, &out); err != nil {
		return nil, err
	}
	return out.Captures, nil
}
