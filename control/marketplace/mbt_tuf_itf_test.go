// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

type tufBigInt struct {
	Value string `json:"#bigint"`
}

func (b tufBigInt) Int64() int64 {
	n, _ := strconv.ParseInt(b.Value, 10, 64)
	return n
}

type tufOptionInt struct {
	Tag   string          `json:"tag"`
	Value json.RawMessage `json:"value"`
}

func (o tufOptionInt) Int64() (int64, bool) {
	if o.Tag != "Some" {
		return 0, false
	}
	var bi tufBigInt
	if err := json.Unmarshal(o.Value, &bi); err != nil {
		return 0, false
	}
	return bi.Int64(), true
}

type tufOptionBool struct {
	Tag   string          `json:"tag"`
	Value json.RawMessage `json:"value"`
}

func (o tufOptionBool) Bool() (bool, bool) {
	if o.Tag != "Some" {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(o.Value, &b); err != nil {
		return false, false
	}
	return b, true
}

type tufState struct {
	Clock                  tufBigInt `json:"clock"`
	RepoVer                tufBigInt `json:"repoVer"`
	ClientMinVer           tufBigInt `json:"clientMinVer"`
	InstalledVer           tufBigInt `json:"installedVer"`
	InstalledHash          tufBigInt `json:"installedHash"`
	InstalledSnapTargetVer tufBigInt `json:"installedSnapTargetVer"`
	InstalledMetaExp       tufBigInt `json:"installedMetaExp"`
	InstalledAtClock       tufBigInt `json:"installedAtClock"`
	// Overlay fields
	OfficialOnly      bool `json:"officialOnly"`
	PinMatched        bool `json:"pinMatched"`
	UnpinnedOrDevLeak bool `json:"unpinnedOrDevLeak"`
}

type tufNondetPicks struct {
	V             tufOptionInt  `json:"v"`
	T             tufOptionInt  `json:"t"`
	IsOfficialPub tufOptionBool `json:"isOfficialPub"`
}

type tufTraceState struct {
	H           tufState       `json:"h"`
	ActionTaken string         `json:"mbt::actionTaken"`
	NondetPicks tufNondetPicks `json:"mbt::nondetPicks"`
}

type tufTrace struct {
	States []tufTraceState `json:"states"`
}

func resolveTufModel(t *testing.T, relDEModel, relOverlay string) (bunBin, cliPath, modelPath, repoRoot string) {
	t.Helper()
	var err error
	bunBin, err = exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not on PATH; skipping live Quint TUF MBT trace generation")
	}
	repoRoot, err = filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	localDE := strings.TrimSpace(os.Getenv("DE_LOCAL_ROOT"))
	if localDE != "" {
		cand := filepath.Join(localDE, "packages/quint-modules-resolver/src/cli.ts")
		if _, err := os.Stat(cand); err == nil {
			cliPath = cand
		}
	}
	if cliPath == "" {
		cliPath = filepath.Join(repoRoot, "node_modules/@deterministic-engineering/quint-modules-resolver/src/cli.ts")
		if _, err := os.Stat(cliPath); err != nil {
			t.Skipf("quint-modules-resolver cli not found at %s", cliPath)
		}
	}
	if relOverlay != "" {
		return bunBin, cliPath, filepath.Join(repoRoot, relOverlay), repoRoot
	}
	if localDE != "" {
		cand := filepath.Join(localDE, "packages/quint-models", relDEModel)
		if _, err := os.Stat(cand); err == nil {
			return bunBin, cliPath, cand, repoRoot
		}
	}
	modelPath = filepath.Join(repoRoot, "node_modules/@deterministic-engineering/quint-models", relDEModel)
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("Quint model not found at %s", modelPath)
	}
	return bunBin, cliPath, modelPath, repoRoot
}

func generateTufMBTTraces(t *testing.T, bunBin, cliPath, modelPath, repoRoot, mainMod string, maxSteps, nTraces int) []string {
	t.Helper()
	outDir := t.TempDir()
	cmd := exec.Command(bunBin, cliPath, "mbt-audit", modelPath,
		"--out-dir", outDir,
		fmt.Sprintf("--max-steps=%d", maxSteps),
		fmt.Sprintf("--n-traces=%d", nTraces),
		"--seed=7",
		"--min-coverage=0.8",
		"--json",
	)
	cmd.Dir = repoRoot
	cmd.Env = os.Environ()
	if localDE := strings.TrimSpace(os.Getenv("DE_LOCAL_ROOT")); localDE != "" {
		resolveRoot := t.TempDir()
		scopeDir := filepath.Join(resolveRoot, "node_modules", "@deterministic-engineering")
		_ = os.MkdirAll(scopeDir, 0o755)
		_ = os.Symlink(filepath.Join(localDE, "packages/quint-models"), filepath.Join(scopeDir, "quint-models"))
		cmd.Env = append(cmd.Env, "QUINT_RESOLVE_ROOT="+resolveRoot)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mbt-audit (%s) failed: %v\n%s", mainMod, err, string(out))
	}
	var report struct {
		Passed              bool     `json:"passed"`
		DeclaredStepActions []string `json:"declaredStepActions"`
		CoveredActions      []string `json:"coveredActions"`
		ActionCoverage      float64  `json:"actionCoverage"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("parse mbt-audit JSON for %s: %v\n%s", mainMod, err, string(out))
	}
	if !report.Passed {
		t.Fatalf("mbt-audit (%s) passed=false: %+v", mainMod, report)
	}
	matches, err := filepath.Glob(filepath.Join(outDir, "*.itf.json"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no ITF traces generated in %s", outDir)
	}
	sort.Strings(matches)
	t.Logf("MBT-AUDIT-OK [%s] coverage=%d/%d (%.1f%%) traces=%d",
		mainMod, len(report.CoveredActions), len(report.DeclaredStepActions), report.ActionCoverage*100, len(matches))
	return matches
}

func makeSignedTestBundle(t *testing.T, pubID schema.PublisherID, priv ed25519.PrivateKey, ver int64) ([]byte, string) {
	t.Helper()
	dir := t.TempDir()
	manifest := fmt.Sprintf(`schema_version = 1
id = "tao3k.demo-panel"
name = "Demo Panel"
version = "%d.0.0"

[contributions.main]
kind = "ui-panel"
slot = "workbench.main"
slot_key = "demo"
runtime = "declarative-ui-ir"
entry = "panel.json"
read_scopes = ["org.tasks"]
declared_actions = ["demo.ping"]
`, ver)
	if err := os.WriteFile(filepath.Join(dir, marketplace.BundleManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "panel.json"), []byte(fmt.Sprintf(`{"type":"text","text":"v%d"}`, ver)), 0o644); err != nil {
		t.Fatal(err)
	}
	var rev [20]byte
	rev[0] = byte(ver)
	pkg, _, err := marketplace.PackBundle(dir, pubID, rev, priv)
	if err != nil {
		t.Fatalf("PackBundle v%d: %v", ver, err)
	}
	sum := sha256.Sum256(pkg)
	return pkg, hex.EncodeToString(sum[:])
}

func TestMBTReplaySupplyChainTufITF(t *testing.T) {
	bunBin, cliPath, modelPath, repoRoot := resolveTufModel(t, "threat/supply-chain/supply-chain-tuf/SupplyChainTuf.qnt", "")
	matches := generateTufMBTTraces(t, bunBin, cliPath, modelPath, repoRoot, "supply_chain_tuf", 16, 10)
	totalSteps := 0
	for _, tp := range matches {
		raw, err := os.ReadFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		var trace tufTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tp, err)
		}
		replayOneTufTrace(t, filepath.Base(tp), trace, false)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [SupplyChainTuf] replayed %d Quint ITF traces (%d transitions) against Go marketplace.TUFGuard + Ed25519", len(matches), totalSteps)
}

func TestMBTReplayMarlinMarketplaceTufOverlayITF(t *testing.T) {
	bunBin, cliPath, modelPath, repoRoot := resolveTufModel(t, "", "spec/supply-chain/MarlinMarketplaceTufOverlay.qnt")
	matches := generateTufMBTTraces(t, bunBin, cliPath, modelPath, repoRoot, "marlin_marketplace_tuf_composed", 16, 12)
	totalSteps := 0
	for _, tp := range matches {
		raw, err := os.ReadFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		var trace tufTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tp, err)
		}
		replayOneTufTrace(t, filepath.Base(tp), trace, true)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [MarlinMarketplaceTufOverlay] replayed %d Quint @quint-extends ITF traces (%d transitions) against Go TUFGuard + TrustRing + Lockfile Pin", len(matches), totalSteps)
}

func replayOneTufTrace(t *testing.T, name string, trace tufTrace, isOverlay bool) {
	t.Helper()
	offPub, offPriv, _ := ed25519.GenerateKey(rand.Reader)
	devPub, devPriv, _ := ed25519.GenerateKey(rand.Reader)
	ring := &marketplace.TrustRing{
		SchemaVersion: 1,
		Publishers: []marketplace.PublisherTrustEntry{
			{PublisherID: "tao3k.official", Tier: marketplace.TrustTierOfficial, PublicKey: offPub, AllowCatalog: true},
			{PublisherID: "community.dev", Tier: marketplace.TrustTierDev, PublicKey: devPub},
		},
	}

	bundles := make(map[int64][]byte, 3)
	hashes := make(map[int64]string, 3)
	for v := int64(1); v <= 3; v++ {
		bundles[v], hashes[v] = makeSignedTestBundle(t, "tao3k.official", offPriv, v)
	}
	devBundle, devHash := makeSignedTestBundle(t, "community.dev", devPriv, 2)

	pluginID := schema.PluginID("tao3k.demo-panel")
	guard := marketplace.NewTUFGuard(ring, true, 1)
	guard.SeedInstalled(pluginID, 1, hashes[1])

	baseTime := time.Unix(1_700_000_000, 0).UTC()
	clockUnix := func(tick int64) time.Time { return baseTime.Add(time.Duration(tick) * time.Hour) }

	for i := 1; i < len(trace.States); i++ {
		prev := trace.States[i-1].H
		st := trace.States[i]
		act := st.ActionTaken

		switch act {
		case "publishRelease", "advanceClock", "driftLockfilePin", "restoreLockfilePin":
			// Environment clock/repo/pin state progressed in Quint; enforced below.

		case "clientUpdate":
			v, ok1 := st.NondetPicks.V.Int64()
			tgt, ok2 := st.NondetPicks.T.Int64()
			if !ok1 {
				v = 1
			}
			if !ok2 {
				tgt = v
			}
			if v <= prev.RepoVer.Int64() {
				catJSON, catSig, err := marketplace.SignTUFMetadataCatalog(marketplace.CatalogIndex{
					SchemaVersion:   1,
					Publisher:       "tao3k.official",
					SourceRevision:  "0123456789abcdef0123456789abcdef01234567",
					MetadataVersion: uint64(v),
					SnapshotVersion: uint64(v),
					ExpiresAtUnix:   clockUnix(v + 1).Unix(),
					Entries: []marketplace.CatalogEntry{{
						PluginID:      pluginID,
						Version:       fmt.Sprintf("%d.0.0", v),
						PublisherID:   "tao3k.official",
						PackageSHA256: hashes[v],
					}},
				}, offPriv)
				if err != nil {
					t.Fatal(err)
				}
				_, updErr := guard.VerifyAndAdmitUpdate(clockUnix(prev.Clock.Int64()), catJSON, catSig, offPub, pluginID, uint64(tgt), bundles[tgt], "")
				quintUpdated := v >= prev.ClientMinVer.Int64() && tgt >= prev.InstalledVer.Int64() && (v+1) >= prev.Clock.Int64() && tgt == v
				if quintUpdated {
					if updErr != nil {
						t.Fatalf("[%s step %d] valid TUF update (v=%d tgt=%d clk=%d) rejected: %v", name, i, v, tgt, prev.Clock.Int64(), updErr)
					}
				} else if updErr == nil {
					t.Fatalf("[%s step %d] SECURITY VIOLATION: invalid TUF update (v=%d tgt=%d min=%d inst=%d clk=%d) admitted!",
						name, i, v, tgt, prev.ClientMinVer.Int64(), prev.InstalledVer.Int64(), prev.Clock.Int64())
				}
			}

		case "admitMarketplaceBundle":
			isOfficial, _ := st.NondetPicks.IsOfficialPub.Bool()
			curMin, curInst, _ := guard.State(pluginID)
			useVer := curMin
			if curInst > useVer {
				useVer = curInst
			}
			if useVer < 1 {
				useVer = 1
			}
			if useVer > 3 {
				useVer = 3
			}
			pkg := bundles[int64(useVer)]
			hash := hashes[int64(useVer)]
			pubID := schema.PublisherID("tao3k.official")
			if !isOfficial {
				pkg = devBundle
				hash = devHash
				pubID = "community.dev"
			}
			pin := hash
			if !prev.PinMatched {
				pin = strings.Repeat("0", 64)
			}
			catJSON, catSig, err := marketplace.SignTUFMetadataCatalog(marketplace.CatalogIndex{
				SchemaVersion:   1,
				Publisher:       "tao3k.official",
				SourceRevision:  "0123456789abcdef0123456789abcdef01234567",
				MetadataVersion: useVer,
				SnapshotVersion: useVer,
				ExpiresAtUnix:   clockUnix(prev.Clock.Int64() + 10).Unix(),
				Entries: []marketplace.CatalogEntry{{
					PluginID:      pluginID,
					Version:       fmt.Sprintf("%d.0.0", useVer),
					PublisherID:   pubID,
					PackageSHA256: hash,
				}},
			}, offPriv)
			if err != nil {
				t.Fatal(err)
			}
			_, admErr := guard.VerifyAndAdmitUpdate(clockUnix(prev.Clock.Int64()), catJSON, catSig, offPub, pluginID, useVer, pkg, pin)
			if prev.PinMatched && isOfficial {
				if admErr != nil {
					t.Fatalf("[%s step %d] valid official pinned bundle rejected: %v", name, i, admErr)
				}
			} else if admErr == nil {
				t.Fatalf("[%s step %d] SECURITY VIOLATION: unpinned or non-official bundle admitted (pinMatched=%v official=%v)",
					name, i, prev.PinMatched, isOfficial)
			}

		default:
			t.Fatalf("[%s step %d] unknown TUF MBT action %q", name, i, act)
		}

		minVer, instVer, _ := guard.State(pluginID)
		if int64(minVer) != st.H.ClientMinVer.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch clientMinVer: Go=%d Quint=%d", name, i, act, minVer, st.H.ClientMinVer.Int64())
		}
		if int64(instVer) != st.H.InstalledVer.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch installedVer: Go=%d Quint=%d", name, i, act, instVer, st.H.InstalledVer.Int64())
		}
		_ = isOverlay
	}
}

func TestMarketplaceTUFGuardExplicitAttacks(t *testing.T) {
	offPub, offPriv, _ := ed25519.GenerateKey(rand.Reader)
	devPub, devPriv, _ := ed25519.GenerateKey(rand.Reader)
	ring := &marketplace.TrustRing{
		SchemaVersion: 1,
		Publishers: []marketplace.PublisherTrustEntry{
			{PublisherID: "tao3k.official", Tier: marketplace.TrustTierOfficial, PublicKey: offPub, AllowCatalog: true},
			{PublisherID: "community.dev", Tier: marketplace.TrustTierDev, PublicKey: devPub},
		},
	}
	pkg1, h1 := makeSignedTestBundle(t, "tao3k.official", offPriv, 1)
	pkg2, h2 := makeSignedTestBundle(t, "tao3k.official", offPriv, 2)
	devPkg, devH := makeSignedTestBundle(t, "community.dev", devPriv, 2)
	pluginID := schema.PluginID("tao3k.demo-panel")
	now := time.Unix(1_700_000_000, 0).UTC()

	guard := marketplace.NewTUFGuard(ring, true, 1)
	guard.SeedInstalled(pluginID, 1, h1)

	// 1. Valid upgrade v1 -> v2 succeeds.
	cat2, sig2, _ := marketplace.SignTUFMetadataCatalog(marketplace.CatalogIndex{
		SchemaVersion: 1, Publisher: "tao3k.official", MetadataVersion: 2, SnapshotVersion: 2,
		ExpiresAtUnix: now.Add(time.Hour).Unix(),
		Entries:       []marketplace.CatalogEntry{{PluginID: pluginID, Version: "2.0.0", PublisherID: "tao3k.official", PackageSHA256: h2}},
	}, offPriv)
	if _, err := guard.VerifyAndAdmitUpdate(now, cat2, sig2, offPub, pluginID, 2, pkg2, h2); err != nil {
		t.Fatalf("valid v2 upgrade failed: %v", err)
	}

	// 2. Rollback to v1 catalog blocked!
	cat1, sig1, _ := marketplace.SignTUFMetadataCatalog(marketplace.CatalogIndex{
		SchemaVersion: 1, Publisher: "tao3k.official", MetadataVersion: 1, SnapshotVersion: 1,
		ExpiresAtUnix: now.Add(time.Hour).Unix(),
		Entries:       []marketplace.CatalogEntry{{PluginID: pluginID, Version: "1.0.0", PublisherID: "tao3k.official", PackageSHA256: h1}},
	}, offPriv)
	if _, err := guard.VerifyAndAdmitUpdate(now, cat1, sig1, offPub, pluginID, 1, pkg1, ""); !errors.Is(err, marketplace.ErrCatalogRollback) {
		t.Fatalf("expected ErrCatalogRollback, got %v", err)
	}

	// 3. Freeze attack (expired catalog) blocked!
	if _, err := guard.VerifyAndAdmitUpdate(now.Add(2*time.Hour), cat2, sig2, offPub, pluginID, 2, pkg2, h2); !errors.Is(err, marketplace.ErrCatalogExpired) {
		t.Fatalf("expected ErrCatalogExpired, got %v", err)
	}

	// 4. Mix-and-match (v2 catalog paired with v1 bundle bytes) blocked!
	if _, err := guard.VerifyAndAdmitUpdate(now, cat2, sig2, offPub, pluginID, 2, pkg1, ""); !errors.Is(err, marketplace.ErrCatalogMixAndMatch) {
		t.Fatalf("expected ErrCatalogMixAndMatch, got %v", err)
	}

	// 5. Dev-tier bundle rejected in Official-Only mode!
	catDev, sigDev, _ := marketplace.SignTUFMetadataCatalog(marketplace.CatalogIndex{
		SchemaVersion: 1, Publisher: "tao3k.official", MetadataVersion: 3, SnapshotVersion: 3,
		ExpiresAtUnix: now.Add(time.Hour).Unix(),
		Entries:       []marketplace.CatalogEntry{{PluginID: pluginID, Version: "2.0.0", PublisherID: "community.dev", PackageSHA256: devH}},
	}, offPriv)
	if _, err := guard.VerifyAndAdmitUpdate(now, catDev, sigDev, offPub, pluginID, 3, devPkg, devH); !errors.Is(err, marketplace.ErrOfficialPublisherRequired) {
		t.Fatalf("expected ErrOfficialPublisherRequired, got %v", err)
	}
}
