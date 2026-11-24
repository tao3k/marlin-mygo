// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

const (
	MaxBundleBytes      = 16 << 20 // 16 MiB archive ceiling
	MaxBundleFileCount  = 256
	BundleManifestFile  = "marlin-plugin.toml"
	BundleAttestation   = "attestation.json"
	BundleSignatureFile = "signature.ed25519"
	BundleAssetsPrefix  = "assets/"
)

var (
	ErrInvalidBundle  = errors.New("marketplace: invalid or malformed .marlinpkg bundle")
	ErrBundleTampered = errors.New("marketplace: bundle signature or asset digest failed verification")
)

// BundleAttestationDoc is serialized deterministically into attestation.json.
// Its SHA-256 serves as schema.ReleaseAttestation.BuildEvidenceDigest so the
// Ed25519 signature cryptographically commits to every file under assets/.
type BundleAttestationDoc struct {
	Publisher      schema.PublisherID `json:"publisher"`
	SourceRevision string             `json:"sourceRevision"`
	AssetDigests   map[string]string  `json:"assetDigests"`
}

// VerifiedBundle holds the authenticated contents extracted from a .marlinpkg.
type VerifiedBundle struct {
	Verified    *schema.VerifiedRelease
	Attestation BundleAttestationDoc
	Assets      map[string][]byte // keyed by slash-separated path relative to assets/
}

// PackBundle deterministically archives and Ed25519-signs a plugin directory
// into .marlinpkg (sorted gzipped tar with fixed epoch timestamps).
func PackBundle(
	pluginDir string,
	publisher schema.PublisherID,
	sourceRev [20]byte,
	privKey ed25519.PrivateKey,
) ([]byte, BundleAttestationDoc, error) {
	if err := schema.ValidateDotIdentifier(string(publisher)); err != nil {
		return nil, BundleAttestationDoc{}, fmt.Errorf("%w: invalid publisher %q: %v", ErrInvalidBundle, publisher, err)
	}
	if len(privKey) != ed25519.PrivateKeySize {
		return nil, BundleAttestationDoc{}, fmt.Errorf("%w: invalid Ed25519 private key length", ErrInvalidBundle)
	}
	manifestPath := filepath.Join(pluginDir, BundleManifestFile)
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, BundleAttestationDoc{}, fmt.Errorf("%w: read %s: %v", ErrInvalidBundle, BundleManifestFile, err)
	}
	if len(manifestRaw) == 0 || len(manifestRaw) > schema.MaxManifestBytes {
		return nil, BundleAttestationDoc{}, fmt.Errorf("%w: manifest size %d out of bounds", ErrInvalidBundle, len(manifestRaw))
	}

	assets := make(map[string][]byte)
	var assetPaths []string
	err = filepath.WalkDir(pluginDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == pluginDir {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlinks forbidden in bundle (%s)", ErrInvalidBundle, p)
		}
		rel, err := filepath.Rel(pluginDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == BundleManifestFile || rel == BundleAttestation || rel == BundleSignatureFile {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		assets[rel] = data
		assetPaths = append(assetPaths, rel)
		if len(assetPaths) > MaxBundleFileCount {
			return fmt.Errorf("%w: too many files (> %d)", ErrInvalidBundle, MaxBundleFileCount)
		}
		return nil
	})
	if err != nil {
		return nil, BundleAttestationDoc{}, err
	}
	slices.Sort(assetPaths)

	digests := make(map[string]string, len(assetPaths))
	for _, rel := range assetPaths {
		sum := sha256.Sum256(assets[rel])
		digests[rel] = hex.EncodeToString(sum[:])
	}
	attDoc := BundleAttestationDoc{
		Publisher:      publisher,
		SourceRevision: hex.EncodeToString(sourceRev[:]),
		AssetDigests:   digests,
	}
	attRaw, err := json.Marshal(attDoc)
	if err != nil {
		return nil, BundleAttestationDoc{}, err
	}
	attDigest := sha256.Sum256(attRaw)
	att := schema.ReleaseAttestation{
		Publisher:           publisher,
		SourceRevision:      sourceRev,
		BuildEvidenceDigest: attDigest,
	}
	sig := ed25519.Sign(privKey, schema.SigningBytes(manifestRaw, att))

	var out bytes.Buffer
	gw, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		return nil, BundleAttestationDoc{}, err
	}
	gw.Header.ModTime = time.Unix(0, 0).UTC()
	gw.Header.OS = 255 // deterministic OS byte
	tw := tar.NewWriter(gw)

	writeEntry := func(tarPath string, data []byte) error {
		hdr := &tar.Header{
			Name:     tarPath,
			Mode:     0o644,
			Size:     int64(len(data)),
			Typeflag: tar.TypeReg,
			ModTime:  time.Unix(0, 0).UTC(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}

	if err := writeEntry(BundleAttestation, attRaw); err != nil {
		return nil, BundleAttestationDoc{}, err
	}
	if err := writeEntry(BundleManifestFile, manifestRaw); err != nil {
		return nil, BundleAttestationDoc{}, err
	}
	if err := writeEntry(BundleSignatureFile, sig); err != nil {
		return nil, BundleAttestationDoc{}, err
	}
	for _, rel := range assetPaths {
		if err := writeEntry(BundleAssetsPrefix+rel, assets[rel]); err != nil {
			return nil, BundleAttestationDoc{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, BundleAttestationDoc{}, err
	}
	if err := gw.Close(); err != nil {
		return nil, BundleAttestationDoc{}, err
	}
	if out.Len() > MaxBundleBytes {
		return nil, BundleAttestationDoc{}, fmt.Errorf("%w: archive size %d exceeds %d", ErrInvalidBundle, out.Len(), MaxBundleBytes)
	}
	return out.Bytes(), attDoc, nil
}

// VerifyBundle extracts and cryptographically verifies a .marlinpkg archive
// against trust: checking path hygiene, Ed25519 signature over manifest +
// attestation digest, exact asset SHA-256 digests, and rejecting undeclared files.
func VerifyBundle(pkgBytes []byte, trust *schema.ReleaseTrust) (*VerifiedBundle, error) {
	if len(pkgBytes) == 0 || len(pkgBytes) > MaxBundleBytes {
		return nil, fmt.Errorf("%w: bundle size %d out of bounds", ErrInvalidBundle, len(pkgBytes))
	}
	if trust == nil {
		return nil, fmt.Errorf("%w: nil release trust store", ErrInvalidBundle)
	}
	gr, err := gzip.NewReader(bytes.NewReader(pkgBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: gzip open: %v", ErrInvalidBundle, err)
	}
	defer gr.Close()

	tr := tar.NewReader(io.LimitReader(gr, MaxBundleBytes+1))
	var manifestRaw, attRaw, sigRaw []byte
	assets := make(map[string][]byte)
	fileCount := 0

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: tar read: %v", ErrInvalidBundle, err)
		}
		fileCount++
		if fileCount > MaxBundleFileCount+3 {
			return nil, fmt.Errorf("%w: too many entries in bundle", ErrInvalidBundle)
		}
		if hdr.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("%w: non-regular entry %q forbidden", ErrInvalidBundle, hdr.Name)
		}
		if strings.HasPrefix(hdr.Name, "/") || strings.Contains(hdr.Name, "..") || strings.Contains(hdr.Name, "\\") {
			return nil, fmt.Errorf("%w: unsafe archive path %q", ErrInvalidBundle, hdr.Name)
		}
		if hdr.Size < 0 || hdr.Size > MaxBundleBytes {
			return nil, fmt.Errorf("%w: entry %q size %d out of bounds", ErrInvalidBundle, hdr.Name, hdr.Size)
		}
		data, err := io.ReadAll(io.LimitReader(tr, hdr.Size+1))
		if err != nil || int64(len(data)) != hdr.Size {
			return nil, fmt.Errorf("%w: truncated entry %q", ErrInvalidBundle, hdr.Name)
		}

		switch {
		case hdr.Name == BundleManifestFile:
			manifestRaw = data
		case hdr.Name == BundleAttestation:
			attRaw = data
		case hdr.Name == BundleSignatureFile:
			sigRaw = data
		case strings.HasPrefix(hdr.Name, BundleAssetsPrefix):
			rel := strings.TrimPrefix(hdr.Name, BundleAssetsPrefix)
			if rel == "" {
				return nil, fmt.Errorf("%w: empty asset path", ErrInvalidBundle)
			}
			assets[rel] = data
		default:
			return nil, fmt.Errorf("%w: unexpected top-level entry %q", ErrBundleTampered, hdr.Name)
		}
	}

	if len(manifestRaw) == 0 || len(attRaw) == 0 || len(sigRaw) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: missing manifest, attestation, or signature", ErrInvalidBundle)
	}
	var attDoc BundleAttestationDoc
	if err := json.Unmarshal(attRaw, &attDoc); err != nil {
		return nil, fmt.Errorf("%w: decode attestation: %v", ErrInvalidBundle, err)
	}
	revBytes, err := hex.DecodeString(attDoc.SourceRevision)
	if err != nil || len(revBytes) != 20 {
		return nil, fmt.Errorf("%w: invalid sourceRevision hex", ErrInvalidBundle)
	}
	var sourceRev [20]byte
	copy(sourceRev[:], revBytes)
	var sigArr [ed25519.SignatureSize]byte
	copy(sigArr[:], sigRaw)

	signed := schema.SignedRelease{
		ManifestTOML: manifestRaw,
		Attestation: schema.ReleaseAttestation{
			Publisher:           attDoc.Publisher,
			SourceRevision:      sourceRev,
			BuildEvidenceDigest: sha256.Sum256(attRaw),
		},
		Signature: sigArr,
	}
	verified, err := trust.Verify(signed)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBundleTampered, err)
	}

	// Verify exact 1:1 asset coverage and SHA-256 integrity.
	if len(assets) != len(attDoc.AssetDigests) {
		return nil, fmt.Errorf("%w: archive has %d assets, attestation declares %d", ErrBundleTampered, len(assets), len(attDoc.AssetDigests))
	}
	for rel, wantHex := range attDoc.AssetDigests {
		data, ok := assets[rel]
		if !ok {
			return nil, fmt.Errorf("%w: missing attested asset %q", ErrBundleTampered, rel)
		}
		got := sha256.Sum256(data)
		if hex.EncodeToString(got[:]) != wantHex {
			return nil, fmt.Errorf("%w: asset %q SHA-256 mismatch", ErrBundleTampered, rel)
		}
	}
	return &VerifiedBundle{
		Verified:    verified,
		Attestation: attDoc,
		Assets:      assets,
	}, nil
}
