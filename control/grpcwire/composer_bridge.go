// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"fmt"
	"strings"

	"github.com/tao3k/marlin-mygo/engine/composer"
	appv1 "github.com/tao3k/marlin-mygo/gen/marlin/app/v1"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

// ArtifactUploader uploads one fail-closed validated StagedAttachment into
// Rust's content-addressed artifact store and returns its authoritative
// `marlin-artifact://<sha256>` URI.
type ArtifactUploader interface {
	UploadArtifact(ctx context.Context, att composer.StagedAttachment) (artifactURI string, err error)
}

// AttachmentStatusFn receives upload lifecycle progress updates for one
// attachment (StatusUploading -> StatusUploaded / StatusFailed).
type AttachmentStatusFn func(attID string, status composer.AttachmentStatus, progress float32, errMsg string)

// ComposerBridge is the thin frontend adapter bridging multimodal
// composer.TurnSubmission payloads and backend session summaries onto the
// existing marlin.app.v1.AppServer gRPC contract with zero local persistence.
type ComposerBridge struct {
	Turns    *TurnStreamController
	Uploader ArtifactUploader
}

// NewComposerBridge constructs a thin gRPC multimodal composer bridge.
func NewComposerBridge(turns *TurnStreamController, uploader ArtifactUploader) *ComposerBridge {
	return &ComposerBridge{
		Turns:    turns,
		Uploader: uploader,
	}
}

// PrepareTurnPrompt uploads any staged multimodal attachments via Uploader (if
// configured), emits per-attachment progress callbacks, and formats the
// structured wire prompt carrying `marlin-artifact://<sha256>` URIs and `@`
// context mentions. Fails closed if any attachment upload fails.
func (b *ComposerBridge) PrepareTurnPrompt(
	ctx context.Context,
	sub composer.TurnSubmission,
	onStatus AttachmentStatusFn,
) (string, []composer.AttachmentRef, error) {
	refs := make([]composer.AttachmentRef, 0, len(sub.Attachments))
	for _, att := range sub.Attachments {
		ref := att.Ref
		if b != nil && b.Uploader != nil {
			if onStatus != nil {
				onStatus(ref.ID, composer.StatusUploading, 0.25, "")
			}
			uri, err := b.Uploader.UploadArtifact(ctx, att)
			if err != nil {
				if onStatus != nil {
					onStatus(ref.ID, composer.StatusFailed, 0, err.Error())
				}
				return "", nil, fmt.Errorf("grpcwire: upload attachment %q failed: %w", ref.Name, err)
			}
			if strings.TrimSpace(uri) != "" {
				ref.ArtifactURI = strings.TrimSpace(uri)
			}
			ref.Status = composer.StatusUploaded
			ref.Progress = 1.0
			if onStatus != nil {
				onStatus(ref.ID, composer.StatusUploaded, 1.0, "")
			}
		}
		refs = append(refs, ref)
	}
	return FormatTurnSubmissionPrompt(sub, refs), refs, nil
}

// StartMultimodalTurn uploads staged attachments (fail-closed) and dispatches
// the structured turn to AppServer.StartSessionTurn.
func (b *ComposerBridge) StartMultimodalTurn(
	ctx context.Context,
	sessionID string,
	sub composer.TurnSubmission,
	afterTurn uint64,
	submissionID string,
	onStatus AttachmentStatusFn,
) (*appv1.SessionTurnRef, []composer.AttachmentRef, error) {
	if b == nil || b.Turns == nil {
		return nil, nil, fmt.Errorf("grpcwire: ComposerBridge requires non-nil TurnStreamController")
	}
	wirePrompt, refs, err := b.PrepareTurnPrompt(ctx, sub, onStatus)
	if err != nil {
		return nil, nil, err
	}
	turnRef, err := b.Turns.StartTurn(ctx, sessionID, wirePrompt, afterTurn, submissionID)
	if err != nil {
		return nil, nil, err
	}
	return turnRef, refs, nil
}

// FetchSessionSummary queries AppServer.GetSessionSummary and projects the
// durable Rust session metadata into a pure UI SessionSummaryView.
func (b *ComposerBridge) FetchSessionSummary(
	ctx context.Context,
	sessionID string,
	title string,
) (mygoshell.SessionSummaryView, error) {
	if b == nil || b.Turns == nil {
		return mygoshell.SessionSummaryView{}, fmt.Errorf("grpcwire: ComposerBridge requires non-nil TurnStreamController")
	}
	t, _, err := b.Turns.resolveTarget()
	if err != nil {
		return mygoshell.SessionSummaryView{}, err
	}
	resp, err := t.App().GetSessionSummary(ctx, &appv1.GetSessionSummaryRequest{
		SessionId: sessionID,
	})
	if err != nil {
		return mygoshell.SessionSummaryView{}, err
	}
	var turns uint64
	if lt := resp.GetLatestTurn(); lt != nil {
		turns = lt.GetOrdinal()
	}
	if title == "" {
		title = sessionID
	}
	return mygoshell.SessionSummaryView{
		SessionID:     sessionID,
		Title:         title,
		TurnCount:     turns,
		RecoveryState: resp.GetRecoveryState().String(),
	}, nil
}

// FormatTurnSubmissionPrompt encodes structured multimodal attachment CAS URIs
// and `@` context mentions alongside the user's prompt. When there are neither
// attachments nor mentions, the raw prompt is returned unchanged.
func FormatTurnSubmissionPrompt(sub composer.TurnSubmission, refs []composer.AttachmentRef) string {
	prompt := strings.TrimSpace(sub.Prompt)
	if len(refs) == 0 && len(sub.Mentions) == 0 {
		return prompt
	}
	var b strings.Builder
	if prompt != "" {
		b.WriteString(prompt)
	}
	if len(sub.Mentions) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		for i, m := range sub.Mentions {
			if i > 0 {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "[context @%s:%s]", m.Kind, m.Target)
		}
	}
	if len(refs) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		for i, att := range refs {
			if i > 0 {
				b.WriteByte('\n')
			}
			extra := ""
			if att.Kind == composer.AttachmentImage && att.Width > 0 && att.Height > 0 {
				extra = fmt.Sprintf(" dims=%dx%d", att.Width, att.Height)
			} else if att.LineCount > 0 {
				extra = fmt.Sprintf(" lines=%d", att.LineCount)
			}
			fmt.Fprintf(&b, "[attachment uri=%s kind=%s name=%q mime=%s bytes=%d%s]",
				att.ArtifactURI, att.Kind, att.Name, att.MIMEType, att.ByteSize, extra)
		}
	}
	return b.String()
}
