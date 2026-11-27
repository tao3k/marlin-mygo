// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

const (
	defaultMaxCatalogBytes int64 = 4 * 1024 * 1024
	defaultMaxPackageBytes int64 = 32 * 1024 * 1024
)

var (
	ErrRegistrySourceRequired = errors.New("marketplace: either BaseURL or LocalDir is required")
	ErrPayloadTooLarge        = errors.New("marketplace: registry payload exceeds safety limit")
	ErrRegistryHTTPStatus     = errors.New("marketplace: unexpected HTTP status from registry")
)

// RegistryConfig configures a fail-closed, Ed25519-verifying Marketplace
// catalog & .marlinpkg client with a local SHA-256 content-addressed cache.
type RegistryConfig struct {
	BaseURL         string // e.g. "https://marketplace.example.com" or "http://127.0.0.1:8787"
	LocalDir        string // optional local directory containing catalog.json, catalog.json.sig, *.marlinpkg
	CacheDir        string // optional local CAS disk cache for offline resilience
	CatalogPubKey   ed25519.PublicKey
	HTTPClient      *http.Client
	MaxCatalogBytes int64
	MaxPackageBytes int64
}

// RegistryClient fetches Ed25519-signed Marketplace catalogs and SHA-256
// verified .marlinpkg archives over HTTP/TLS or local disk with offline CAS fallback.
type RegistryClient struct {
	cfg RegistryConfig
	mu  sync.RWMutex
	idx *CatalogIndex
}

func NewRegistryClient(cfg RegistryConfig) (*RegistryClient, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" && strings.TrimSpace(cfg.LocalDir) == "" {
		return nil, ErrRegistrySourceRequired
	}
	if len(cfg.CatalogPubKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: invalid catalog Ed25519 public key", ErrCatalogInvalid)
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.MaxCatalogBytes <= 0 {
		cfg.MaxCatalogBytes = defaultMaxCatalogBytes
	}
	if cfg.MaxPackageBytes <= 0 {
		cfg.MaxPackageBytes = defaultMaxPackageBytes
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	return &RegistryClient{cfg: cfg}, nil
}

// RefreshCatalog fetches catalog.json + catalog.json.sig, verifies the Ed25519
// signature fail-closed, persists the verified snapshot to CacheDir, and falls
// back to the last signed cached catalog if the remote registry is offline.
func (c *RegistryClient) RefreshCatalog(ctx context.Context) (*CatalogIndex, error) {
	rawJSON, sigBytes, fetchErr := c.fetchSignedCatalogRaw(ctx)
	if fetchErr == nil {
		idx, err := VerifySignedCatalog(rawJSON, sigBytes, c.cfg.CatalogPubKey)
		if err != nil {
			// Tampered remote response MUST fail closed (never silently downgrade to cache).
			return nil, err
		}
		if c.cfg.CacheDir != "" {
			_ = c.writeCachedCatalog(rawJSON, sigBytes)
		}
		c.mu.Lock()
		c.idx = idx
		c.mu.Unlock()
		return idx, nil
	}

	// Transport/network failure: attempt fail-closed offline fallback from CAS cache.
	if c.cfg.CacheDir != "" {
		cachedJSON, cachedSig, cacheErr := c.readCachedCatalog()
		if cacheErr == nil {
			idx, verifyErr := VerifySignedCatalog(cachedJSON, cachedSig, c.cfg.CatalogPubKey)
			if verifyErr == nil {
				c.mu.Lock()
				c.idx = idx
				c.mu.Unlock()
				return idx, nil
			}
		}
	}
	return nil, fetchErr
}

// CachedCatalog returns the last authenticated CatalogIndex in memory.
func (c *RegistryClient) CachedCatalog() *CatalogIndex {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.idx
}

// LookupEntry searches the last authenticated catalog for pluginID.
func (c *RegistryClient) LookupEntry(pluginID schema.PluginID) (CatalogEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.idx == nil {
		return CatalogEntry{}, false
	}
	for _, e := range c.idx.Entries {
		if e.PluginID == pluginID {
			return e, true
		}
	}
	return CatalogEntry{}, false
}

// FetchPackage retrieves a .marlinpkg archive from local CAS cache or the
// configured registry source, enforcing exact SHA-256 equality with entry.PackageSHA256.
func (c *RegistryClient) FetchPackage(ctx context.Context, entry CatalogEntry) ([]byte, error) {
	if entry.PackageFile == "" {
		return nil, fmt.Errorf("%w: entry %q has empty packageFile", ErrInvalidBundle, entry.PluginID)
	}
	cleanName := filepath.Base(entry.PackageFile)
	if cleanName != entry.PackageFile || !strings.HasSuffix(cleanName, ".marlinpkg") {
		return nil, fmt.Errorf("%w: unsafe package filename %q", ErrInvalidBundle, entry.PackageFile)
	}

	// 1. Check SHA-256 content-addressed cache first (re-verifying digest on read).
	if c.cfg.CacheDir != "" && entry.PackageSHA256 != "" {
		if raw, ok := c.readCASBlob(entry.PackageSHA256); ok {
			return raw, nil
		}
	}

	// 2. Fetch from remote HTTP/TLS or LocalDir.
	var raw []byte
	var err error
	if c.cfg.BaseURL != "" {
		targetURL := c.cfg.BaseURL + "/" + cleanName
		if entry.DownloadURL != "" && (strings.HasPrefix(entry.DownloadURL, "https://") || strings.HasPrefix(entry.DownloadURL, "http://")) {
			targetURL = entry.DownloadURL
		}
		raw, err = c.httpGetBounded(ctx, targetURL, c.cfg.MaxPackageBytes)
	} else {
		raw, err = readFileBounded(filepath.Join(c.cfg.LocalDir, cleanName), c.cfg.MaxPackageBytes)
	}
	if err != nil {
		return nil, err
	}

	gotSum := sha256.Sum256(raw)
	gotHex := hex.EncodeToString(gotSum[:])
	if entry.PackageSHA256 != "" && gotHex != entry.PackageSHA256 {
		return nil, fmt.Errorf("%w: package %q sha256=%s want=%s", ErrBundleTampered, entry.PluginID, gotHex, entry.PackageSHA256)
	}
	if c.cfg.CacheDir != "" {
		_ = c.writeCASBlob(gotHex, raw)
	}
	return raw, nil
}

// ServeCatalogHandler returns a hardened read-only HTTP handler serving
// catalog.json, catalog.json.sig, and *.marlinpkg archives from dir with
// path-traversal protection and strict Content-Type headers.
func ServeCatalogHandler(dir string) http.Handler {
	absDir, _ := filepath.Abs(dir)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rel := strings.TrimPrefix(r.URL.Path, "/")
		if rel == "" || strings.Contains(rel, "/") || strings.Contains(rel, "\\") || strings.Contains(rel, "..") {
			http.NotFound(w, r)
			return
		}
		var contentType string
		switch {
		case rel == "catalog.json":
			contentType = "application/json; charset=utf-8"
		case rel == "catalog.json.sig":
			contentType = "text/plain; charset=utf-8"
		case strings.HasSuffix(rel, ".marlinpkg"):
			contentType = "application/octet-stream"
		default:
			http.NotFound(w, r)
			return
		}
		fullPath := filepath.Join(absDir, rel)
		info, err := os.Stat(fullPath)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeFile(w, r, fullPath)
	})
}

func (c *RegistryClient) fetchSignedCatalogRaw(ctx context.Context) ([]byte, []byte, error) {
	var rawJSON, rawSig []byte
	var err error
	if c.cfg.BaseURL != "" {
		rawJSON, err = c.httpGetBounded(ctx, c.cfg.BaseURL+"/catalog.json", c.cfg.MaxCatalogBytes)
		if err != nil {
			return nil, nil, err
		}
		rawSig, err = c.httpGetBounded(ctx, c.cfg.BaseURL+"/catalog.json.sig", 4096)
		if err != nil {
			return nil, nil, err
		}
	} else {
		rawJSON, err = readFileBounded(filepath.Join(c.cfg.LocalDir, "catalog.json"), c.cfg.MaxCatalogBytes)
		if err != nil {
			return nil, nil, err
		}
		rawSig, err = readFileBounded(filepath.Join(c.cfg.LocalDir, "catalog.json.sig"), 4096)
		if err != nil {
			return nil, nil, err
		}
	}
	sigBytes, err := decodeSignatureBytes(rawSig)
	if err != nil {
		return nil, nil, err
	}
	return rawJSON, sigBytes, nil
}

func decodeSignatureBytes(raw []byte) ([]byte, error) {
	if len(raw) == ed25519.SignatureSize {
		return raw, nil
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(decoded) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: malformed catalog.json.sig", ErrCatalogInvalid)
	}
	return decoded, nil
}

func (c *RegistryClient) httpGetBounded(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: GET %s -> %d", ErrRegistryHTTPStatus, url, resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes", ErrPayloadTooLarge, url, maxBytes)
	}
	return data, nil
}

func readFileBounded(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%w: %s (%d bytes > %d)", ErrPayloadTooLarge, path, info.Size(), maxBytes)
	}
	return os.ReadFile(path)
}

func (c *RegistryClient) writeCachedCatalog(rawJSON, sig []byte) error {
	if err := os.MkdirAll(c.cfg.CacheDir, 0o755); err != nil {
		return err
	}
	if err := atomicWriteFile(filepath.Join(c.cfg.CacheDir, "catalog.json"), rawJSON, 0o644); err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(c.cfg.CacheDir, "catalog.json.sig"), []byte(hex.EncodeToString(sig)+"\n"), 0o644)
}

func (c *RegistryClient) readCachedCatalog() ([]byte, []byte, error) {
	rawJSON, err := readFileBounded(filepath.Join(c.cfg.CacheDir, "catalog.json"), c.cfg.MaxCatalogBytes)
	if err != nil {
		return nil, nil, err
	}
	rawSig, err := readFileBounded(filepath.Join(c.cfg.CacheDir, "catalog.json.sig"), 4096)
	if err != nil {
		return nil, nil, err
	}
	sig, err := decodeSignatureBytes(rawSig)
	if err != nil {
		return nil, nil, err
	}
	return rawJSON, sig, nil
}

func (c *RegistryClient) blobPath(shaHex string) string {
	return filepath.Join(c.cfg.CacheDir, "blobs", shaHex+".marlinpkg")
}

func (c *RegistryClient) readCASBlob(expectedSHA string) ([]byte, bool) {
	raw, err := readFileBounded(c.blobPath(expectedSHA), c.cfg.MaxPackageBytes)
	if err != nil {
		return nil, false
	}
	got := sha256.Sum256(raw)
	if hex.EncodeToString(got[:]) != expectedSHA {
		return nil, false
	}
	return raw, true
}

func (c *RegistryClient) writeCASBlob(shaHex string, data []byte) error {
	dir := filepath.Join(c.cfg.CacheDir, "blobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return atomicWriteFile(c.blobPath(shaHex), data, 0o644)
}

func atomicWriteFile(dest string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".marlin-cas-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, dest)
}
