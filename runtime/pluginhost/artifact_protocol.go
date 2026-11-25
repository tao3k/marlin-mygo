// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// ArtifactScheme is registered with mygo.Protocol.Handle(ArtifactScheme, handler)
// to serve content-addressed, plugin-granted binary artifacts (screenshots, diffs,
// bounded logs) at "marlin-artifact://<sha256-hex>" without bloating event streams.
const (
	ArtifactScheme        = "marlin-artifact"
	DefaultMaxArtifactLen = 16 << 20 // 16 MiB per artifact
	DefaultMaxCASBytes    = 64 << 20 // 64 MiB total resident CAS budget
)

var (
	ErrArtifactTooLarge    = errors.New("artifact: payload exceeds maximum byte budget")
	ErrArtifactDigestBad   = errors.New("artifact: invalid or mismatched sha256 digest")
	ErrArtifactMIMEBlocked = errors.New("artifact: mime type not in safe allowlist")
	ErrArtifactNotFound    = errors.New("artifact: digest not found in CAS")
)

var allowedArtifactMIMEs = map[string]string{
	"image/png":                "image/png",
	"image/jpeg":               "image/jpeg",
	"image/webp":               "image/webp",
	"text/plain":               "text/plain; charset=utf-8",
	"text/plain; charset=utf-8": "text/plain; charset=utf-8",
	"application/json":         "application/json",
	"text/x-diff":              "text/plain; charset=utf-8",
}

// ArtifactEntry is one verified immutable blob held inside ArtifactCAS.
type ArtifactEntry struct {
	SHA256Hex string
	MIMEType  string
	Data      []byte
}

// ArtifactCAS is a thread-safe, bounded, SHA-256 verified content-addressed
// artifact store with FIFO eviction when the total byte ceiling is exceeded.
type ArtifactCAS struct {
	maxItemBytes  int
	maxTotalBytes int

	mu         sync.RWMutex
	entries    map[string]ArtifactEntry
	order      []string
	totalBytes int
}

func NewArtifactCAS(maxItemBytes, maxTotalBytes int) *ArtifactCAS {
	if maxItemBytes <= 0 {
		maxItemBytes = DefaultMaxArtifactLen
	}
	if maxTotalBytes <= 0 {
		maxTotalBytes = DefaultMaxCASBytes
	}
	if maxTotalBytes < maxItemBytes {
		maxTotalBytes = maxItemBytes
	}
	return &ArtifactCAS{
		maxItemBytes:  maxItemBytes,
		maxTotalBytes: maxTotalBytes,
		entries:       make(map[string]ArtifactEntry),
	}
}

// NormalizeArtifactDigest validates a 64-char lowercase hex SHA-256 digest.
func NormalizeArtifactDigest(raw string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if len(s) != sha256.Size*2 {
		return "", false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", false
	}
	return s, true
}

// Put hashes, validates MIME allowlist + byte caps, and stores an immutable copy.
func (c *ArtifactCAS) Put(mimeType string, data []byte) (ArtifactEntry, error) {
	canonicalMIME, ok := allowedArtifactMIMEs[strings.ToLower(strings.TrimSpace(mimeType))]
	if !ok {
		return ArtifactEntry{}, ErrArtifactMIMEBlocked
	}
	if len(data) == 0 || len(data) > c.maxItemBytes {
		return ArtifactEntry{}, ErrArtifactTooLarge
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])

	copied := make([]byte, len(data))
	copy(copied, data)
	entry := ArtifactEntry{
		SHA256Hex: digest,
		MIMEType:  canonicalMIME,
		Data:      copied,
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, exists := c.entries[digest]; exists {
		return existing, nil
	}
	for c.totalBytes+len(copied) > c.maxTotalBytes && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		if prev, ok := c.entries[oldest]; ok {
			c.totalBytes -= len(prev.Data)
			delete(c.entries, oldest)
		}
	}
	c.entries[digest] = entry
	c.order = append(c.order, digest)
	c.totalBytes += len(copied)
	return entry, nil
}

// PutVerified stores data only if its SHA-256 matches expectedDigestHex exactly.
func (c *ArtifactCAS) PutVerified(expectedDigestHex, mimeType string, data []byte) (ArtifactEntry, error) {
	norm, ok := NormalizeArtifactDigest(expectedDigestHex)
	if !ok {
		return ArtifactEntry{}, ErrArtifactDigestBad
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != norm {
		return ArtifactEntry{}, ErrArtifactDigestBad
	}
	return c.Put(mimeType, data)
}

// Get retrieves an artifact by validated SHA-256 hex digest.
func (c *ArtifactCAS) Get(digestHex string) (ArtifactEntry, error) {
	norm, ok := NormalizeArtifactDigest(digestHex)
	if !ok {
		return ArtifactEntry{}, ErrArtifactDigestBad
	}
	c.mu.RLock()
	entry, exists := c.entries[norm]
	c.mu.RUnlock()
	if !exists {
		return ArtifactEntry{}, ErrArtifactNotFound
	}
	return entry, nil
}

// TotalBytes returns the current resident CAS footprint in bytes.
func (c *ArtifactCAS) TotalBytes() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.totalBytes
}

// ArtifactProtocolHandler serves "marlin-artifact://<sha256-hex>" strictly to
// identified plugin pages that have been explicitly granted that digest.
type ArtifactProtocolHandler struct {
	resolveCaller CallerResolver
	cas           *ArtifactCAS

	mu     sync.RWMutex
	grants map[schema.PluginID]map[string]struct{}
}

func NewArtifactProtocolHandler(resolve CallerResolver, cas *ArtifactCAS) *ArtifactProtocolHandler {
	if cas == nil {
		cas = NewArtifactCAS(DefaultMaxArtifactLen, DefaultMaxCASBytes)
	}
	return &ArtifactProtocolHandler{
		resolveCaller: resolve,
		cas:           cas,
		grants:        make(map[schema.PluginID]map[string]struct{}),
	}
}

func (h *ArtifactProtocolHandler) CAS() *ArtifactCAS { return h.cas }

// Grant authorizes pluginID to read a specific SHA-256 artifact via marlin-artifact://.
func (h *ArtifactProtocolHandler) Grant(pluginID schema.PluginID, digestHex string) error {
	norm, ok := NormalizeArtifactDigest(digestHex)
	if !ok {
		return ErrArtifactDigestBad
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.grants[pluginID]
	if set == nil {
		set = make(map[string]struct{})
		h.grants[pluginID] = set
	}
	set[norm] = struct{}{}
	return nil
}

// RevokePlugin removes all artifact grants owned by a deactivated plugin.
func (h *ArtifactProtocolHandler) RevokePlugin(pluginID schema.PluginID) {
	h.mu.Lock()
	delete(h.grants, pluginID)
	h.mu.Unlock()
}

func (h *ArtifactProtocolHandler) isGranted(pluginID schema.PluginID, normDigest string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	set := h.grants[pluginID]
	if set == nil {
		return false
	}
	_, ok := set[normDigest]
	return ok
}

// ServeHTTP enforces CallerPage identity, strict per-plugin SHA-256 grant checks,
// safe read-only methods (GET/HEAD), and nosniff/immutable security headers.
func (h *ArtifactProtocolHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.resolveCaller == nil {
		http.Error(w, "caller resolver not configured", http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	callerID, ok := h.resolveCaller(r.Context())
	if !ok || callerID == "" {
		http.Error(w, "unidentified plugin page", http.StatusForbidden)
		return
	}
	rawHost := r.URL.Host
	if rawHost == "" {
		rawHost = r.Host
	}
	if strings.Contains(rawHost, ":") {
		http.Error(w, "invalid artifact digest host", http.StatusBadRequest)
		return
	}
	normDigest, valid := NormalizeArtifactDigest(rawHost)
	if !valid {
		http.Error(w, "invalid artifact digest", http.StatusBadRequest)
		return
	}
	if !h.isGranted(callerID, normDigest) {
		http.Error(w, "artifact access not granted to plugin", http.StatusForbidden)
		return
	}
	entry, err := h.cas.Get(normDigest)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", entry.MIMEType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("ETag", `"`+entry.SHA256Hex+`"`)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(entry.Data)
	}
}

// ArtifactURL formats the canonical marlin-artifact://<sha256-hex> URI for WebViews.
func ArtifactURL(digestHex string) (string, error) {
	norm, ok := NormalizeArtifactDigest(digestHex)
	if !ok {
		return "", ErrArtifactDigestBad
	}
	return ArtifactScheme + "://" + norm + "/", nil
}

// NewContextWithArtifactCaller attaches a plugin caller ID in tests/harnesses.
type artifactCallerKey struct{}

func WithArtifactCaller(ctx context.Context, id schema.PluginID) context.Context {
	return context.WithValue(ctx, artifactCallerKey{}, id)
}

func ArtifactCallerFromContext(ctx context.Context) (schema.PluginID, bool) {
	id, ok := ctx.Value(artifactCallerKey{}).(schema.PluginID)
	return id, ok && id != ""
}
