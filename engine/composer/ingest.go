// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package composer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrNativePickerUnavailable is returned on targets (such as headless Linux
// without zenity/kdialog or WASM) where an OS file picker binary is unavailable.
var ErrNativePickerUnavailable = errors.New("composer: native OS file picker unavailable on this platform")

// NativePickerOptions configures the OS-native multimodal file chooser dialog.
type NativePickerOptions struct {
	Title          string
	AllowMultiple  bool
	InitialDir     string
}

// PickFilesNative opens the platform's native file chooser dialog without CGO
// (macOS Finder via osascript, Linux zenity/kdialog, Windows OpenFileDialog).
// Returns (nil, nil) when the user cancels the dialog.
func PickFilesNative(ctx context.Context, opts NativePickerOptions) ([]string, error) {
	if opts.Title == "" {
		opts.Title = "Attach files to Marlin Composer"
	}
	return pickFilesPlatform(ctx, opts)
}

// ReadClipboardAttachment reads an image (preferred, e.g. pasted screenshot as
// PNG) or text snippet from the OS clipboard without CGO.
// Returns (zero, false, nil) when the clipboard holds no supported payload.
func ReadClipboardAttachment(ctx context.Context) (AttachmentInput, bool, error) {
	return readClipboardPlatform(ctx)
}

// ReadLocalFile safely reads one local regular file into an AttachmentInput
// using a fail-closed io.LimitReader cap before buffering so oversized files
// cannot exhaust frontend memory.
func ReadLocalFile(path string, policy AttachmentPolicy) (AttachmentInput, error) {
	p := policy.withDefaults()
	clean := strings.TrimSpace(path)
	if clean == "" {
		return AttachmentInput{}, ErrAttachmentEmpty
	}
	info, err := os.Stat(clean)
	if err != nil {
		return AttachmentInput{}, fmt.Errorf("composer: stat %q: %w", filepath.Base(clean), err)
	}
	if !info.Mode().IsRegular() {
		return AttachmentInput{}, fmt.Errorf("%w: %s is not a regular file", ErrAttachmentMIMEBlocked, filepath.Base(clean))
	}
	limit := max(p.MaxBinaryBytes, p.MaxTextBytes)
	if info.Size() == 0 {
		return AttachmentInput{}, ErrAttachmentEmpty
	}
	if info.Size() > int64(limit) {
		return AttachmentInput{}, fmt.Errorf("%w (%s > %s)",
			ErrAttachmentTooLarge, FormatByteSize(int(info.Size())), FormatByteSize(limit))
	}

	f, err := os.Open(clean)
	if err != nil {
		return AttachmentInput{}, fmt.Errorf("composer: open %q: %w", filepath.Base(clean), err)
	}
	defer f.Close()

	// Read at most limit+1 bytes to defend against concurrent file growth races.
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return AttachmentInput{}, fmt.Errorf("composer: read %q: %w", filepath.Base(clean), err)
	}
	if len(data) == 0 {
		return AttachmentInput{}, ErrAttachmentEmpty
	}
	if len(data) > limit {
		return AttachmentInput{}, fmt.Errorf("%w (%s > %s)",
			ErrAttachmentTooLarge, FormatByteSize(len(data)), FormatByteSize(limit))
	}
	return AttachmentInput{
		Filename: filepath.Base(clean),
		Data:     data,
	}, nil
}

// StageLocalFiles reads and validates a batch of local file paths (from OS
// file picker or drag-and-drop) and stages each valid file into state.
func StageLocalFiles(state *State, paths []string) ([]AttachmentRef, []error) {
	if state == nil {
		return nil, []error{errors.New("composer: nil State")}
	}
	var refs []AttachmentRef
	var errs []error
	for _, path := range paths {
		in, err := ReadLocalFile(path, state.policy)
		if err != nil {
			state.mu.Lock()
			state.lastError = err.Error()
			state.mu.Unlock()
			errs = append(errs, err)
			continue
		}
		ref, err := state.AddAttachment(in)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		refs = append(refs, ref)
	}
	return refs, errs
}
