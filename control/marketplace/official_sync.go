// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

const (
	// DefaultOfficialRegistryURL points to the canonical tao3k/marlin-ui-marketplace
	// 3-stream registry (plugins, themes, and Emacs layouts).
	DefaultOfficialRegistryURL = "https://raw.githubusercontent.com/tao3k/marlin-ui-marketplace/main/registry.json"

	maxOfficialAssetBytes = 1 * 1024 * 1024 // 1 MiB cap per theme/layout asset
)

var (
	ErrOfficialURLRejected = errors.New("marketplace: URL rejected by official marketplace origin policy")
	ErrThemePackInvalid    = errors.New("marketplace: invalid theme pack TOML")
	ErrLayoutPackInvalid   = errors.New("marketplace: invalid layout pack JSON")
)

// ThemePack is a validated 8-color semantic theme pack loaded from an official
// TOML theme asset (projected deterministically into all 34 --marlin-* tokens).
type ThemePack struct {
	SchemaVersion int
	ID            schema.PluginID
	Name          string
	Appearance    string // "dark" | "light"
	Palette       schema.UITheme
}

// SyncedThemePack combines a registry.json theme listing with its resolved ThemePack.
type SyncedThemePack struct {
	Entry RegistryThemeEntry
	Pack  ThemePack
}

// SyncedLayoutPack combines a registry.json layout listing with its resolved LayoutSpec.
type SyncedLayoutPack struct {
	Entry RegistryLayoutEntry
	Spec  shell.LayoutSpec
}

// OfficialRegistrySnapshot is an atomically validated snapshot synced from the
// Official UI Marketplace (`tao3k/marlin-ui-marketplace`).
type OfficialRegistrySnapshot struct {
	Manifest *RegistryManifest
	Themes   []SyncedThemePack
	Layouts  []SyncedLayoutPack
	Source   string
	SyncedAt time.Time
}

// OfficialSyncConfig configures an OfficialSyncClient.
// In production, only pinned Official HTTPS URLs (ValidateOfficialRegistryURL)
// or an explicit LocalRepoDir mirror are accepted.
type OfficialSyncConfig struct {
	RegistryURL  string
	LocalRepoDir string
	HTTPClient   *http.Client

	// allowUnsafeOriginForTest relaxes host allowlist checks strictly for in-process
	// TLS httptest servers; scheme must still be https://.
	allowUnsafeOriginForTest bool
}

// OfficialSyncClient fetches and validates the 3-stream Official UI Marketplace
// registry (plugins, themes, and Emacs layouts) fail-closed.
type OfficialSyncClient struct {
	cfg OfficialSyncConfig
}

// NewOfficialSyncClient validates configuration and returns a fail-closed sync client.
func NewOfficialSyncClient(cfg OfficialSyncConfig) (*OfficialSyncClient, error) {
	if strings.TrimSpace(cfg.LocalRepoDir) == "" {
		if strings.TrimSpace(cfg.RegistryURL) == "" {
			cfg.RegistryURL = DefaultOfficialRegistryURL
		}
		if cfg.allowUnsafeOriginForTest {
			if err := validateHTTPSOnly(cfg.RegistryURL); err != nil {
				return nil, err
			}
		} else if err := ValidateOfficialRegistryURL(cfg.RegistryURL); err != nil {
			return nil, err
		}
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &OfficialSyncClient{cfg: cfg}, nil
}

// ValidateOfficialRegistryURL enforces that remote registry fetches can ONLY
// originate from the pinned Official UI Marketplace repository over HTTPS.
func ValidateOfficialRegistryURL(rawURL string) error {
	if err := validateHTTPSOnly(rawURL); err != nil {
		return err
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOfficialURLRejected, err)
	}
	host := strings.ToLower(u.Hostname())
	cleanPath := path.Clean(u.Path)
	switch host {
	case "raw.githubusercontent.com":
		if !strings.HasPrefix(cleanPath, "/tao3k/marlin-ui-marketplace/") {
			return fmt.Errorf("%w: raw.githubusercontent.com path %q outside tao3k/marlin-ui-marketplace", ErrOfficialURLRejected, cleanPath)
		}
	case "github.com":
		if !strings.HasPrefix(cleanPath, "/tao3k/marlin-ui-marketplace/") {
			return fmt.Errorf("%w: github.com path %q outside tao3k/marlin-ui-marketplace", ErrOfficialURLRejected, cleanPath)
		}
	default:
		return fmt.Errorf("%w: untrusted host %q (only official tao3k/marlin-ui-marketplace HTTPS origins allowed)", ErrOfficialURLRejected, host)
	}
	return nil
}

func validateHTTPSOnly(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("%w: malformed URL: %v", ErrOfficialURLRejected, err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("%w: scheme %q forbidden (https required)", ErrOfficialURLRejected, u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("%w: URL userinfo forbidden", ErrOfficialURLRejected)
	}
	if strings.Contains(u.Path, "..") {
		return fmt.Errorf("%w: path traversal forbidden", ErrOfficialURLRejected)
	}
	if strings.TrimSpace(u.Host) == "" {
		return fmt.Errorf("%w: missing host", ErrOfficialURLRejected)
	}
	return nil
}

// Sync fetches and validates registry.json plus all declared theme & layout packs.
func (c *OfficialSyncClient) Sync(ctx context.Context) (*OfficialRegistrySnapshot, error) {
	if strings.TrimSpace(c.cfg.LocalRepoDir) != "" {
		return c.syncLocal(c.cfg.LocalRepoDir)
	}
	return c.syncRemote(ctx)
}

func (c *OfficialSyncClient) syncLocal(repoDir string) (*OfficialRegistrySnapshot, error) {
	regPath := filepath.Join(repoDir, "registry.json")
	m, err := LoadRegistryManifest(regPath)
	if err != nil {
		return nil, err
	}
	if err := ValidateRegistryManifest(m, repoDir); err != nil {
		return nil, err
	}

	themes := make([]SyncedThemePack, 0, len(m.Themes))
	for _, th := range m.Themes {
		if strings.TrimSpace(th.LocalPath) == "" {
			continue
		}
		raw, err := readBoundedFile(filepath.Join(repoDir, filepath.FromSlash(th.LocalPath)), maxOfficialAssetBytes)
		if err != nil {
			return nil, fmt.Errorf("%w: theme %q: %v", ErrThemePackInvalid, th.ID, err)
		}
		pack, err := ParseThemePackTOML(raw)
		if err != nil {
			return nil, fmt.Errorf("theme %q: %w", th.ID, err)
		}
		if pack.ID != th.ID {
			return nil, fmt.Errorf("%w: theme id mismatch: registry=%q file=%q", ErrThemePackInvalid, th.ID, pack.ID)
		}
		themes = append(themes, SyncedThemePack{Entry: th, Pack: pack})
	}

	layouts := make([]SyncedLayoutPack, 0, len(m.Layouts))
	for _, ly := range m.Layouts {
		if strings.TrimSpace(ly.LocalPath) == "" {
			continue
		}
		raw, err := readBoundedFile(filepath.Join(repoDir, filepath.FromSlash(ly.LocalPath)), maxOfficialAssetBytes)
		if err != nil {
			return nil, fmt.Errorf("%w: layout %q: %v", ErrLayoutPackInvalid, ly.ID, err)
		}
		spec, err := ParseLayoutPackJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("layout %q: %w", ly.ID, err)
		}
		if spec.Perspective != ly.Perspective {
			return nil, fmt.Errorf("%w: layout perspective mismatch: registry=%q file=%q", ErrLayoutPackInvalid, ly.Perspective, spec.Perspective)
		}
		layouts = append(layouts, SyncedLayoutPack{Entry: ly, Spec: spec})
	}

	return &OfficialRegistrySnapshot{
		Manifest: m,
		Themes:   themes,
		Layouts:  layouts,
		Source:   "local:" + repoDir,
		SyncedAt: time.Now().UTC(),
	}, nil
}

func (c *OfficialSyncClient) syncRemote(ctx context.Context) (*OfficialRegistrySnapshot, error) {
	rawReg, err := c.fetchBounded(ctx, c.cfg.RegistryURL, 2*1024*1024)
	if err != nil {
		return nil, err
	}
	var m RegistryManifest
	if err := json.Unmarshal(rawReg, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRegistryManifestInvalid, err)
	}
	if err := ValidateRegistryManifest(&m, ""); err != nil {
		return nil, err
	}

	baseURL, err := url.Parse(c.cfg.RegistryURL)
	if err != nil {
		return nil, err
	}

	themes := make([]SyncedThemePack, 0, len(m.Themes))
	for _, th := range m.Themes {
		if strings.TrimSpace(th.LocalPath) == "" {
			continue
		}
		assetURL, err := resolveOfficialAssetURL(baseURL, th.LocalPath, c.cfg.allowUnsafeOriginForTest)
		if err != nil {
			return nil, err
		}
		raw, err := c.fetchBounded(ctx, assetURL, maxOfficialAssetBytes)
		if err != nil {
			return nil, fmt.Errorf("fetch theme %q: %w", th.ID, err)
		}
		pack, err := ParseThemePackTOML(raw)
		if err != nil {
			return nil, fmt.Errorf("theme %q: %w", th.ID, err)
		}
		if pack.ID != th.ID {
			return nil, fmt.Errorf("%w: theme id mismatch: registry=%q remote=%q", ErrThemePackInvalid, th.ID, pack.ID)
		}
		themes = append(themes, SyncedThemePack{Entry: th, Pack: pack})
	}

	layouts := make([]SyncedLayoutPack, 0, len(m.Layouts))
	for _, ly := range m.Layouts {
		if strings.TrimSpace(ly.LocalPath) == "" {
			continue
		}
		assetURL, err := resolveOfficialAssetURL(baseURL, ly.LocalPath, c.cfg.allowUnsafeOriginForTest)
		if err != nil {
			return nil, err
		}
		raw, err := c.fetchBounded(ctx, assetURL, maxOfficialAssetBytes)
		if err != nil {
			return nil, fmt.Errorf("fetch layout %q: %w", ly.ID, err)
		}
		spec, err := ParseLayoutPackJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("layout %q: %w", ly.ID, err)
		}
		if spec.Perspective != ly.Perspective {
			return nil, fmt.Errorf("%w: layout perspective mismatch: registry=%q remote=%q", ErrLayoutPackInvalid, ly.Perspective, spec.Perspective)
		}
		layouts = append(layouts, SyncedLayoutPack{Entry: ly, Spec: spec})
	}

	return &OfficialRegistrySnapshot{
		Manifest: &m,
		Themes:   themes,
		Layouts:  layouts,
		Source:   c.cfg.RegistryURL,
		SyncedAt: time.Now().UTC(),
	}, nil
}

func resolveOfficialAssetURL(base *url.URL, relPath string, allowTestOrigin bool) (string, error) {
	if filepath.IsAbs(relPath) || strings.Contains(relPath, "..") {
		return "", fmt.Errorf("%w: unsafe asset path %q", ErrOfficialURLRejected, relPath)
	}
	ref, err := url.Parse(strings.TrimPrefix(filepath.ToSlash(relPath), "/"))
	if err != nil {
		return "", fmt.Errorf("%w: invalid asset path %q: %v", ErrOfficialURLRejected, relPath, err)
	}
	resolved := base.ResolveReference(ref).String()
	if allowTestOrigin {
		if err := validateHTTPSOnly(resolved); err != nil {
			return "", err
		}
	} else if err := ValidateOfficialRegistryURL(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func (c *OfficialSyncClient) fetchBounded(ctx context.Context, targetURL string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "marlin-mygo-official-marketplace/1.0")
	req.Header.Set("Accept", "application/json, text/plain;q=0.9")

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("official marketplace HTTP %d for %s", resp.StatusCode, targetURL)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("official marketplace asset %s exceeds %d byte cap", targetURL, maxBytes)
	}
	return raw, nil
}

func readBoundedFile(path string, maxBytes int64) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() <= 0 || st.Size() > maxBytes {
		return nil, fmt.Errorf("file size %d out of bounds", st.Size())
	}
	return os.ReadFile(path)
}

// ParseThemePackTOML parses a strict official theme pack TOML file into a ThemePack
// with all 8 semantic RGB colors validated (#rrggbb).
func ParseThemePackTOML(raw []byte) (ThemePack, error) {
	var pack ThemePack
	palette := make(map[string]schema.RGB, 8)
	section := ""

	lines := strings.Split(string(raw), "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.Index(line, "#"); idx >= 0 {
			// Preserve hex colors inside quotes ("#0f1117"); strip only trailing comments outside quotes.
			line = stripTOMLComment(line)
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return ThemePack{}, fmt.Errorf("%w: malformed line %q", ErrThemePackInvalid, line)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])

		switch section {
		case "":
			switch key {
			case "schema_version":
				n, err := strconv.Atoi(val)
				if err != nil {
					return ThemePack{}, fmt.Errorf("%w: invalid schema_version %q", ErrThemePackInvalid, val)
				}
				pack.SchemaVersion = n
			case "id":
				s, err := unquoteTOMLString(val)
				if err != nil {
					return ThemePack{}, fmt.Errorf("%w: invalid id: %v", ErrThemePackInvalid, err)
				}
				pack.ID = schema.PluginID(s)
			case "name":
				s, err := unquoteTOMLString(val)
				if err != nil {
					return ThemePack{}, fmt.Errorf("%w: invalid name: %v", ErrThemePackInvalid, err)
				}
				pack.Name = s
			case "appearance":
				s, err := unquoteTOMLString(val)
				if err != nil {
					return ThemePack{}, fmt.Errorf("%w: invalid appearance: %v", ErrThemePackInvalid, err)
				}
				pack.Appearance = s
			}
		case "palette":
			hexStr, err := unquoteTOMLString(val)
			if err != nil {
				return ThemePack{}, fmt.Errorf("%w: palette.%s: %v", ErrThemePackInvalid, key, err)
			}
			rgb, err := parseHexRGB(hexStr)
			if err != nil {
				return ThemePack{}, fmt.Errorf("%w: palette.%s: %v", ErrThemePackInvalid, key, err)
			}
			palette[key] = rgb
		}
	}

	if pack.SchemaVersion != 1 {
		return ThemePack{}, fmt.Errorf("%w: unsupported schema_version %d", ErrThemePackInvalid, pack.SchemaVersion)
	}
	if err := schema.ValidateDotIdentifier(string(pack.ID)); err != nil {
		return ThemePack{}, fmt.Errorf("%w: invalid id %q: %v", ErrThemePackInvalid, pack.ID, err)
	}
	if strings.TrimSpace(pack.Name) == "" {
		return ThemePack{}, fmt.Errorf("%w: missing theme name", ErrThemePackInvalid)
	}
	if pack.Appearance != "dark" && pack.Appearance != "light" {
		return ThemePack{}, fmt.Errorf("%w: invalid appearance %q", ErrThemePackInvalid, pack.Appearance)
	}

	required := []string{"canvas", "bar", "ink", "accent", "info", "success", "warning", "muted"}
	for _, k := range required {
		if _, ok := palette[k]; !ok {
			return ThemePack{}, fmt.Errorf("%w: missing palette.%s", ErrThemePackInvalid, k)
		}
	}
	pack.Palette = schema.UITheme{
		Canvas:  palette["canvas"],
		Bar:     palette["bar"],
		Ink:     palette["ink"],
		Accent:  palette["accent"],
		Info:    palette["info"],
		Success: palette["success"],
		Warning: palette["warning"],
		Muted:   palette["muted"],
	}
	return pack, nil
}

// ParseLayoutPackJSON parses and validates an official Emacs LayoutSpec JSON pack.
func ParseLayoutPackJSON(raw []byte) (shell.LayoutSpec, error) {
	var spec shell.LayoutSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return shell.LayoutSpec{}, fmt.Errorf("%w: %v", ErrLayoutPackInvalid, err)
	}
	if strings.TrimSpace(spec.Perspective) == "" || spec.Root == nil {
		return shell.LayoutSpec{}, fmt.Errorf("%w: perspective and root are required", ErrLayoutPackInvalid)
	}
	// Validate tree integrity using a scratch WindowTree without mutating live UI.
	probe := shell.NewWindowTree(shell.PaneBuffer{BufferID: "probe", Title: "Probe", Kind: "native-gpu"})
	if err := shell.ApplyLayoutSpec(probe, spec, nil); err != nil {
		return shell.LayoutSpec{}, fmt.Errorf("%w: %v", ErrLayoutPackInvalid, err)
	}
	return spec, nil
}

func stripTOMLComment(line string) string {
	inQuote := false
	for i := 0; i < len(line); i++ {
		if line[i] == '"' && (i == 0 || line[i-1] != '\\') {
			inQuote = !inQuote
		} else if line[i] == '#' && !inQuote {
			return strings.TrimSpace(line[:i])
		}
	}
	return strings.TrimSpace(line)
}

func unquoteTOMLString(val string) (string, error) {
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		return strconv.Unquote(val)
	}
	return "", fmt.Errorf("expected quoted string, got %q", val)
}

func parseHexRGB(s string) (schema.RGB, error) {
	s = strings.TrimSpace(s)
	if len(s) != 7 || s[0] != '#' {
		return schema.RGB{}, fmt.Errorf("expected #rrggbb hex color, got %q", s)
	}
	r, err := strconv.ParseUint(s[1:3], 16, 8)
	if err != nil {
		return schema.RGB{}, err
	}
	g, err := strconv.ParseUint(s[3:5], 16, 8)
	if err != nil {
		return schema.RGB{}, err
	}
	b, err := strconv.ParseUint(s[5:7], 16, 8)
	if err != nil {
		return schema.RGB{}, err
	}
	return schema.RGB{R: uint8(r), G: uint8(g), B: uint8(b)}, nil
}
