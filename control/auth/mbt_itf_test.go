// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type itfBigInt struct {
	Value string `json:"#bigint"`
}

func (b itfBigInt) Int64() int64 {
	n, _ := strconv.ParseInt(b.Value, 10, 64)
	return n
}

type itfSetInt struct {
	Items []itfBigInt `json:"#set"`
}

func (s itfSetInt) Contains(target int64) bool {
	for _, item := range s.Items {
		if item.Int64() == target {
			return true
		}
	}
	return false
}

type itfOptionBool struct {
	Tag   string          `json:"tag"`
	Value json.RawMessage `json:"value"`
}

func (o itfOptionBool) Bool() (bool, bool) {
	if o.Tag != "Some" {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(o.Value, &b); err != nil {
		return false, false
	}
	return b, true
}

type itfOptionInt struct {
	Tag   string          `json:"tag"`
	Value json.RawMessage `json:"value"`
}

func (o itfOptionInt) Int64() (int64, bool) {
	if o.Tag != "Some" {
		return 0, false
	}
	var bi itfBigInt
	if err := json.Unmarshal(o.Value, &bi); err != nil {
		return 0, false
	}
	return bi.Int64(), true
}

func resolveDEModelAndCLI(t *testing.T, relModelPath string) (bunBin, cliPath, modelPath string) {
	t.Helper()
	var err error
	bunBin, err = exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not on PATH; skipping live Quint MBT trace generation")
	}
	repoRoot, _ := filepath.Abs("../..")
	deRoot := "/Users/guangtao/ghq/github.com/hardenedlinux/deterministic-engineering-qnt"

	cliPath = filepath.Join(deRoot, "packages/quint-modules-resolver/src/cli.ts")
	if _, err := os.Stat(cliPath); err != nil {
		cliPath = filepath.Join(repoRoot, "node_modules/@deterministic-engineering/quint-modules-resolver/src/cli.ts")
	}
	if _, err := os.Stat(cliPath); err != nil {
		t.Skipf("quint-modules-resolver cli not found at %s", cliPath)
	}

	modelPath = filepath.Join(deRoot, "packages/quint-models", relModelPath)
	if _, err := os.Stat(modelPath); err != nil {
		modelPath = filepath.Join(repoRoot, "node_modules/@deterministic-engineering/quint-models", relModelPath)
	}
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("Quint model not found at %s", modelPath)
	}
	return bunBin, cliPath, modelPath
}

func generateMBTTraces(t *testing.T, bunBin, cliPath, modelPath, mainMod string, maxSteps, nTraces int) []string {
	t.Helper()
	tmpDir := t.TempDir()
	itfBase := filepath.Join(tmpDir, mainMod+".itf.json")
	cmd := exec.Command(
		bunBin, cliPath, "run", modelPath,
		"--main="+mainMod,
		"--invariant=safety",
		"--max-steps="+strconv.Itoa(maxSteps),
		"--max-samples=40",
		"--n-traces="+strconv.Itoa(nTraces),
		"--seed=7",
		"--mbt",
		"--out-itf="+itfBase,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("quint-modules-resolver run --mbt (%s) failed: %v\n%s", mainMod, err, string(out))
	}
	matches, err := filepath.Glob(filepath.Join(tmpDir, "*.itf.json"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("expected generated MBT .itf.json traces for %s in %s, got %v (err=%v)", mainMod, tmpDir, matches, err)
	}
	return matches
}

// ---------------------------------------------------------------------------
// 1. OAuthTokenLifecycle.qnt -> real Go AuthManager + concurrent refresh race
// ---------------------------------------------------------------------------

type oauthLifecycleState struct {
	AuthEpoch           itfBigInt `json:"authEpoch"`
	CodeIssued          bool      `json:"codeIssued"`
	CodeConsumed        bool      `json:"codeConsumed"`
	SignedIn            bool      `json:"signedIn"`
	VaultHasToken       bool      `json:"vaultHasToken"`
	RefreshInFlight     bool      `json:"refreshInFlight"`
	RefreshStartedEpoch itfBigInt `json:"refreshStartedEpoch"`
	Revoked             bool      `json:"revoked"`
	GrantedScopeLevel   itfBigInt `json:"grantedScopeLevel"`
	ActiveScopeLevel    itfBigInt `json:"activeScopeLevel"`
	Exchanges           itfBigInt `json:"exchanges"`
}

type oauthNondetPicks struct {
	PkceOk   itfOptionBool `json:"pkceOk"`
	NonceOk  itfOptionBool `json:"nonceOk"`
	NewScope itfOptionInt  `json:"newScope"`
}

type oauthMbtTraceState struct {
	H           oauthLifecycleState `json:"h"`
	ActionTaken string              `json:"mbt::actionTaken"`
	NondetPicks oauthNondetPicks    `json:"mbt::nondetPicks"`
}

type oauthMbtTrace struct {
	Vars   []string             `json:"vars"`
	States []oauthMbtTraceState `json:"states"`
}

type mbtExchanger struct {
	mu            sync.Mutex
	nextPkceOk    bool
	nextNonceOk   bool
	refreshStart  chan struct{}
	refreshResume chan int64
}

func (e *mbtExchanger) ExchangeCode(_ context.Context, _ ProviderConfig, _, _, _, _ string) (TokenSet, AccountProfile, error) {
	e.mu.Lock()
	pkceOk := e.nextPkceOk
	nonceOk := e.nextNonceOk
	e.mu.Unlock()
	if !pkceOk {
		return TokenSet{}, AccountProfile{}, errors.New("auth: PKCE verifier mismatch")
	}
	if !nonceOk {
		return TokenSet{}, AccountProfile{}, ErrNonceMismatch
	}
	return TokenSet{
			AccessToken:  "mbt-at-1",
			RefreshToken: "mbt-rt-1",
			ExpiresAt:    time.Now().Add(-time.Minute),
			Scopes:       []string{"openid"},
		}, AccountProfile{
			ID:            "oidc:mbt-user",
			Subject:       "mbt-user",
			Email:         "mbt@example.com",
			EmailVerified: true,
			Scopes:        []string{"openid"},
		}, nil
}

func (e *mbtExchanger) RefreshToken(ctx context.Context, _ ProviderConfig, _ string) (TokenSet, error) {
	select {
	case e.refreshStart <- struct{}{}:
	case <-ctx.Done():
		return TokenSet{}, ctx.Err()
	}
	var newScope int64
	select {
	case newScope = <-e.refreshResume:
	case <-ctx.Done():
		return TokenSet{}, ctx.Err()
	}
	scopes := []string{"openid"}
	if newScope >= 2 {
		scopes = []string{"openid", "admin:escalated"}
	}
	return TokenSet{
		AccessToken:  "mbt-at-refreshed",
		RefreshToken: "mbt-rt-2",
		ExpiresAt:    time.Now().Add(-time.Minute),
		Scopes:       scopes,
	}, nil
}

func (e *mbtExchanger) RevokeToken(_ context.Context, _ ProviderConfig, _ string) error {
	return nil
}

func TestMBTReplayOAuthTokenLifecycleITF(t *testing.T) {
	bunBin, cliPath, qntModel := resolveDEModelAndCLI(t, "threat/identity/oauth-token-lifecycle/OAuthTokenLifecycle.qnt")
	matches := generateMBTTraces(t, bunBin, cliPath, qntModel, "oauth_token_lifecycle", 20, 5)

	totalSteps := 0
	for _, tracePath := range matches {
		raw, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		var trace oauthMbtTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tracePath, err)
		}
		replayOneOAuthTrace(t, filepath.Base(tracePath), trace)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [OAuthTokenLifecycle] replayed %d Quint ITF traces (%d transitions) against Go AuthManager", len(matches), totalSteps)
}

func replayOneOAuthTrace(t *testing.T, name string, trace oauthMbtTrace) {
	t.Helper()
	ctx := context.Background()
	vault := NewMemoryVault()
	ex := &mbtExchanger{
		refreshStart:  make(chan struct{}, 4),
		refreshResume: make(chan int64, 4),
	}
	mgr := NewManager(ManagerConfig{Vault: vault, Exchanger: ex})
	cfg := ProviderConfig{
		ID: ProviderOIDC, DisplayName: "MBT OIDC", Method: MethodOAuthPKCE,
		AuthURL: "https://idp.example.com/auth", TokenURL: "https://idp.example.com/token",
		ClientID: "marlin-mbt", Scopes: []string{"openid"}, SupportsRefresh: true,
	}
	if err := mgr.RegisterProvider(cfg); err != nil {
		t.Fatal(err)
	}

	var (
		pendingAttemptID string
		pendingState     string
		refreshDone      chan error
		refreshRunning   bool
	)
	defer func() {
		if refreshRunning {
			ex.refreshResume <- 1
			<-refreshDone
		}
	}()

	for i := 1; i < len(trace.States); i++ {
		prev := trace.States[i-1].H
		st := trace.States[i]
		act := st.ActionTaken

		switch act {
		case "beginOAuth":
			if !prev.CodeIssued && prev.AuthEpoch.Int64() < 3 {
				flow, err := mgr.BeginOAuth(ProviderOIDC, "http://127.0.0.1:8080/cb")
				if err != nil {
					t.Fatalf("[%s step %d] BeginOAuth failed: %v", name, i, err)
				}
				pendingAttemptID = flow.AttemptID
				mgr.mu.RLock()
				pendingState = mgr.oauthAttempts[flow.AttemptID].state
				mgr.mu.RUnlock()
			}
		case "completeCallback":
			pkceOk, _ := st.NondetPicks.PkceOk.Bool()
			nonceOk, _ := st.NondetPicks.NonceOk.Bool()
			if prev.CodeIssued && !prev.CodeConsumed && pkceOk && nonceOk && prev.AuthEpoch.Int64() < 3 {
				ex.mu.Lock()
				ex.nextPkceOk = pkceOk
				ex.nextNonceOk = nonceOk
				ex.mu.Unlock()
				if _, err := mgr.CompleteOAuthCallback(ctx, pendingAttemptID, LoopbackCallback{
					Code: "code-1", State: pendingState,
				}); err != nil {
					t.Fatalf("[%s step %d] valid CompleteOAuthCallback failed: %v", name, i, err)
				}
			}
		case "startRefresh":
			if prev.SignedIn && prev.VaultHasToken && !prev.RefreshInFlight && !refreshRunning {
				refreshRunning = true
				refreshDone = make(chan error, 1)
				go func() {
					_, rErr := mgr.ValidAccessToken(ctx, "oidc:mbt-user")
					refreshDone <- rErr
				}()
				<-ex.refreshStart
			}
		case "finishRefresh":
			newScope, ok := st.NondetPicks.NewScope.Int64()
			if !ok {
				newScope = 1
			}
			if prev.RefreshInFlight && refreshRunning {
				ex.refreshResume <- newScope
				<-refreshDone
				refreshRunning = false
			}
		case "signOut":
			if prev.SignedIn && prev.AuthEpoch.Int64() < 3 {
				if err := mgr.SignOut(ctx, "oidc:mbt-user"); err != nil {
					t.Fatalf("[%s step %d] SignOut failed: %v", name, i, err)
				}
			}
		default:
			t.Fatalf("[%s step %d] unknown MBT action %q", name, i, act)
		}

		snap := mgr.Snapshot()
		goSignedIn := len(snap.Accounts) > 0
		_, goVaultHasToken, _ := vault.Load(ctx, "oidc:mbt-user")
		var goScopeLevel int64
		if goSignedIn {
			goScopeLevel = 1
			for _, sc := range snap.Accounts[0].Scopes {
				if sc == "admin:escalated" {
					goScopeLevel = 2
				}
			}
		}

		if goSignedIn != st.H.SignedIn {
			t.Fatalf("[%s step %d after %s] refinement mismatch signedIn: Go=%v Quint=%v", name, i, act, goSignedIn, st.H.SignedIn)
		}
		if goVaultHasToken != st.H.VaultHasToken {
			t.Fatalf("[%s step %d after %s] refinement mismatch vaultHasToken: Go=%v Quint=%v", name, i, act, goVaultHasToken, st.H.VaultHasToken)
		}
		if goScopeLevel != st.H.ActiveScopeLevel.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch activeScopeLevel: Go=%d Quint=%d", name, i, act, goScopeLevel, st.H.ActiveScopeLevel.Int64())
		}
	}
}

// ---------------------------------------------------------------------------
// 2. DpopSenderConstrain.qnt -> real Go RFC 9449 DPoPKeyPair + DPoPVerifier
// ---------------------------------------------------------------------------

type dpopState struct {
	TokenIssued        bool      `json:"tokenIssued"`
	TokenBoundKey      itfBigInt `json:"tokenBoundKey"`
	AttackerStoleToken bool      `json:"attackerStoleToken"`
	NextJti            itfBigInt `json:"nextJti"`
	UsedJtis           itfSetInt `json:"usedJtis"`
	LastProofJti       itfBigInt `json:"lastProofJti"`
	AcceptedActors     itfSetInt `json:"acceptedActors"`
	TotalAccepted      itfBigInt `json:"totalAccepted"`
}

type dpopMbtTraceState struct {
	H           dpopState `json:"h"`
	ActionTaken string    `json:"mbt::actionTaken"`
}

type dpopMbtTrace struct {
	Vars   []string            `json:"vars"`
	States []dpopMbtTraceState `json:"states"`
}

func TestMBTReplayDpopSenderConstrainITF(t *testing.T) {
	bunBin, cliPath, qntModel := resolveDEModelAndCLI(t, "threat/protocol/dpop-sender-constrain/DpopSenderConstrain.qnt")
	matches := generateMBTTraces(t, bunBin, cliPath, qntModel, "dpop_sender_constrain", 16, 5)

	totalSteps := 0
	for _, tracePath := range matches {
		raw, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		var trace dpopMbtTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tracePath, err)
		}
		replayOneDpopTrace(t, filepath.Base(tracePath), trace)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [DpopSenderConstrain] replayed %d Quint ITF traces (%d transitions) against Go RFC 9449 DPoPVerifier", len(matches), totalSteps)
}

func replayOneDpopTrace(t *testing.T, name string, trace dpopMbtTrace) {
	t.Helper()
	honestKey, err := GenerateDPoPKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	attackerKey, err := GenerateDPoPKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	verifier := NewDPoPVerifier(60*time.Second, 5*time.Minute)
	now := time.Now().UTC().Truncate(time.Second)
	htu := "https://rs.example.com/v1/resource"

	var (
		boundToken      string
		boundJKT        string
		stolenToken     string
		lastHonestProof string
	)

	for i := 1; i < len(trace.States); i++ {
		prev := trace.States[i-1].H
		st := trace.States[i]
		act := st.ActionTaken

		switch act {
		case "issueBoundToken":
			if !prev.TokenIssued {
				boundToken = "dpop-at-bound-to-honest-key"
				boundJKT = honestKey.Thumbprint()
			}
		case "stealAccessToken":
			if prev.TokenIssued && !prev.AttackerStoleToken {
				stolenToken = boundToken
			}
		case "honestSendFresh":
			if prev.TokenIssued && prev.NextJti.Int64() <= 3 {
				proof, _, err := honestKey.CreateProof("POST", htu, boundToken, "", now)
				if err != nil {
					t.Fatalf("[%s step %d] honest CreateProof failed: %v", name, i, err)
				}
				lastHonestProof = proof
				if _, err := verifier.VerifyProof(proof, DPoPVerifyRequest{
					ExpectedHTM: "POST", ExpectedHTU: htu, AccessToken: boundToken, ExpectedJKT: boundJKT, Now: now,
				}); err != nil {
					t.Fatalf("[%s step %d] honest fresh proof rejected: %v", name, i, err)
				}
			}
		case "attackerTryOwnKey":
			if prev.AttackerStoleToken && prev.NextJti.Int64() <= 3 {
				proof, _, err := attackerKey.CreateProof("POST", htu, stolenToken, "", now)
				if err != nil {
					t.Fatalf("[%s step %d] attacker CreateProof failed: %v", name, i, err)
				}
				if _, err := verifier.VerifyProof(proof, DPoPVerifyRequest{
					ExpectedHTM: "POST", ExpectedHTU: htu, AccessToken: boundToken, ExpectedJKT: boundJKT, Now: now,
				}); !errors.Is(err, ErrDPoPThumbprintMismatch) {
					t.Fatalf("[%s step %d] expected ErrDPoPThumbprintMismatch for stolen token, got %v", name, i, err)
				}
			}
		case "attackerReplayProof":
			if prev.LastProofJti.Int64() > 0 {
				if _, err := verifier.VerifyProof(lastHonestProof, DPoPVerifyRequest{
					ExpectedHTM: "POST", ExpectedHTU: htu, AccessToken: boundToken, ExpectedJKT: boundJKT, Now: now,
				}); !errors.Is(err, ErrDPoPProofReplay) {
					t.Fatalf("[%s step %d] expected ErrDPoPProofReplay on replayed proof, got %v", name, i, err)
				}
			}
		default:
			t.Fatalf("[%s step %d] unknown DPoP MBT action %q", name, i, act)
		}

		goAccepted, goUniqueJTIs, byKey := verifier.Stats()
		if int64(goAccepted) != st.H.TotalAccepted.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch totalAccepted: Go=%d Quint=%d", name, i, act, goAccepted, st.H.TotalAccepted.Int64())
		}
		if goUniqueJTIs != len(st.H.UsedJtis.Items) {
			t.Fatalf("[%s step %d after %s] refinement mismatch usedJtis cardinality: Go=%d Quint=%d", name, i, act, goUniqueJTIs, len(st.H.UsedJtis.Items))
		}
		if byKey[attackerKey.Thumbprint()] != 0 || st.H.AcceptedActors.Contains(2) {
			t.Fatalf("[%s step %d after %s] attacker actor 2 was accepted!", name, i, act)
		}
	}
}

// ---------------------------------------------------------------------------
// 3. ZeroTrustPepPdp.qnt -> real Go Cedar Authorizer (PDP+PEP) + AuthManager
// ---------------------------------------------------------------------------

type zeroTrustState struct {
	PolicyEpoch            itfBigInt `json:"policyEpoch"`
	PolicyAllowed          bool      `json:"policyAllowed"`
	Revoked                bool      `json:"revoked"`
	Decided                bool      `json:"decided"`
	DecidedEpoch           itfBigInt `json:"decidedEpoch"`
	DecidedAllowed         bool      `json:"decidedAllowed"`
	Executed               itfBigInt `json:"executed"`
	LastEnforcedEpoch      itfBigInt `json:"lastEnforcedEpoch"`
	LastEnforcedPolicyEp   itfBigInt `json:"lastEnforcedPolicyEpoch"`
	LastEnforcedAllowed    bool      `json:"lastEnforcedAllowed"`
	LastEnforcedRevoked    bool      `json:"lastEnforcedRevoked"`
}

type zeroTrustMbtTraceState struct {
	H           zeroTrustState `json:"h"`
	ActionTaken string         `json:"mbt::actionTaken"`
}

type zeroTrustMbtTrace struct {
	Vars   []string                 `json:"vars"`
	States []zeroTrustMbtTraceState `json:"states"`
}

func TestMBTReplayZeroTrustPepPdpITF(t *testing.T) {
	bunBin, cliPath, qntModel := resolveDEModelAndCLI(t, "threat/zero-trust/zero-trust-pep-pdp/ZeroTrustPepPdp.qnt")
	matches := generateMBTTraces(t, bunBin, cliPath, qntModel, "zero_trust_pep_pdp", 16, 5)

	totalSteps := 0
	for _, tracePath := range matches {
		raw, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		var trace zeroTrustMbtTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tracePath, err)
		}
		replayOneZeroTrustTrace(t, filepath.Base(tracePath), trace)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [ZeroTrustPepPdp] replayed %d Quint ITF traces (%d transitions) against Go Cedar Authorizer", len(matches), totalSteps)
}

func replayOneZeroTrustTrace(t *testing.T, name string, trace zeroTrustMbtTrace) {
	t.Helper()
	ctx := context.Background()
	vault := NewMemoryVault()
	ex := &mbtExchanger{refreshStart: make(chan struct{}, 1), refreshResume: make(chan int64, 1)}
	ex.nextPkceOk = true
	ex.nextNonceOk = true
	mgr := NewManager(ManagerConfig{Vault: vault, Exchanger: ex})
	if err := mgr.RegisterProvider(ProviderConfig{
		ID: ProviderOIDC, DisplayName: "ZT OIDC", Method: MethodOAuthPKCE,
		AuthURL: "https://idp.example.com/auth", TokenURL: "https://idp.example.com/token",
		ClientID: "marlin-zt", Scopes: []string{"openid"},
	}); err != nil {
		t.Fatal(err)
	}
	flow, err := mgr.BeginOAuth(ProviderOIDC, "http://127.0.0.1:8080/cb")
	if err != nil {
		t.Fatal(err)
	}
	mgr.mu.RLock()
	stToken := mgr.oauthAttempts[flow.AttemptID].state
	mgr.mu.RUnlock()
	if _, err := mgr.CompleteOAuthCallback(ctx, flow.AttemptID, LoopbackCallback{Code: "zt-c", State: stToken}); err != nil {
		t.Fatal(err)
	}

	authz := NewAuthorizer(mgr)
	baseEpoch, err := authz.LoadPolicyBytes("init-allow", []byte(`permit(principal, action, resource);`))
	if err != nil {
		t.Fatal(err)
	}

	var (
		hasPendingTicket bool
		pendingTicket    AuthzTicket
		goExecuted       int64
	)

	for i := 1; i < len(trace.States); i++ {
		prev := trace.States[i-1].H
		st := trace.States[i]
		act := st.ActionTaken

		switch act {
		case "pdpEvaluate":
			if !prev.Decided {
				ticket, evalErr := authz.Evaluate("oidc:mbt-user", "deploy", "prod", nil)
				if evalErr != nil {
					// Account already revoked/signed out -> denied ticket matching Quint decidedAllowed=false.
					ticket = AuthzTicket{Allowed: false, PolicyEpoch: authz.PolicyEpoch()}
				}
				pendingTicket = ticket
				hasPendingTicket = true
			}
		case "policyDeny":
			if prev.PolicyEpoch.Int64() < 3 {
				if _, err := authz.LoadPolicyBytes("deny", []byte(`forbid(principal, action, resource);`)); err != nil {
					t.Fatal(err)
				}
			}
		case "policyAllow":
			if prev.PolicyEpoch.Int64() < 3 {
				if _, err := authz.LoadPolicyBytes("allow", []byte(`permit(principal, action, resource);`)); err != nil {
					t.Fatal(err)
				}
			}
		case "revokeSubject":
			if !prev.Revoked {
				if err := mgr.SignOut(ctx, "oidc:mbt-user"); err != nil {
					t.Fatalf("[%s step %d] SignOut failed: %v", name, i, err)
				}
			}
		case "pepEnforce":
			if hasPendingTicket {
				enforceErr := authz.Enforce(pendingTicket)
				quintEnforced := st.H.Executed.Int64() > prev.Executed.Int64()
				if quintEnforced {
					if enforceErr != nil {
						t.Fatalf("[%s step %d] Quint enforced valid ticket but Go Authorizer rejected: %v", name, i, enforceErr)
					}
					goExecuted++
					hasPendingTicket = false
				} else if enforceErr == nil {
					t.Fatalf("[%s step %d] SECURITY VIOLATION: Quint blocked stale/revoked PEP enforce but Go Authorizer allowed it!", name, i)
				}
			}
		default:
			t.Fatalf("[%s step %d] unknown ZeroTrust MBT action %q", name, i, act)
		}

		goRelPolicyEpoch := int64(authz.PolicyEpoch() - baseEpoch)
		if goRelPolicyEpoch != st.H.PolicyEpoch.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch policyEpoch: Go=%d Quint=%d", name, i, act, goRelPolicyEpoch, st.H.PolicyEpoch.Int64())
		}
		if goExecuted != st.H.Executed.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch executed: Go=%d Quint=%d", name, i, act, goExecuted, st.H.Executed.Int64())
		}
		goRevoked := len(mgr.Snapshot().Accounts) == 0
		if goRevoked != st.H.Revoked {
			t.Fatalf("[%s step %d after %s] refinement mismatch revoked: Go=%v Quint=%v", name, i, act, goRevoked, st.H.Revoked)
		}
	}
}

// ---------------------------------------------------------------------------
// 4. DeviceAuthorization.qnt -> real Go RFC 8628 Device Flow + Multi-Account PEP
// ---------------------------------------------------------------------------

type deviceAuthState struct {
	FlowEpoch            itfBigInt `json:"flowEpoch"`
	DeviceActive         bool      `json:"deviceActive"`
	DeviceExpired        bool      `json:"deviceExpired"`
	DeviceCancelled      bool      `json:"deviceCancelled"`
	PollInFlight         bool      `json:"pollInFlight"`
	PollStartedEpoch     itfBigInt `json:"pollStartedEpoch"`
	Admitted             bool      `json:"admitted"`
	ActiveAccount        itfBigInt `json:"activeAccount"`
	TicketAccount        itfBigInt `json:"ticketAccount"`
	AttachedTokenAccount itfBigInt `json:"attachedTokenAccount"`
}

type deviceAuthMbtTraceState struct {
	H           deviceAuthState `json:"h"`
	ActionTaken string          `json:"mbt::actionTaken"`
}

type deviceAuthMbtTrace struct {
	Vars   []string                  `json:"vars"`
	States []deviceAuthMbtTraceState `json:"states"`
}

type mbtDeviceBackend struct {
	pollStart  chan struct{}
	pollResume chan bool
	seq        int
	mu         sync.Mutex
}

func (b *mbtDeviceBackend) RequestDeviceCode(_ context.Context, _ ProviderConfig) (DeviceCodeResponse, error) {
	b.mu.Lock()
	b.seq++
	code := fmt.Sprintf("dev-code-%d", b.seq)
	b.mu.Unlock()
	return DeviceCodeResponse{
		DeviceCode:      code,
		UserCode:        "ABCD-1234",
		VerificationURI: "https://github.com/login/device",
		ExpiresIn:       5 * time.Minute,
		Interval:        time.Second,
	}, nil
}

func (b *mbtDeviceBackend) PollDeviceToken(ctx context.Context, _ ProviderConfig, _ string) (TokenSet, AccountProfile, bool, error) {
	select {
	case b.pollStart <- struct{}{}:
	case <-ctx.Done():
		return TokenSet{}, AccountProfile{}, false, ctx.Err()
	}
	var ready bool
	select {
	case ready = <-b.pollResume:
	case <-ctx.Done():
		return TokenSet{}, AccountProfile{}, false, ctx.Err()
	}
	if !ready {
		return TokenSet{}, AccountProfile{}, false, nil
	}
	return TokenSet{
			AccessToken: "dev-at-1",
			ExpiresAt:   time.Now().Add(time.Hour),
			Scopes:      []string{"read:user"},
		}, AccountProfile{
			ID:      "github:dev-user",
			Subject: "dev-user",
			Email:   "dev@example.com",
			Scopes:  []string{"read:user"},
		}, true, nil
}

func TestMBTReplayDeviceAuthorizationITF(t *testing.T) {
	bunBin, cliPath, qntModel := resolveDEModelAndCLI(t, "threat/identity/device-authorization/DeviceAuthorization.qnt")
	matches := generateMBTTraces(t, bunBin, cliPath, qntModel, "device_authorization", 18, 5)

	totalSteps := 0
	for _, tracePath := range matches {
		raw, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		var trace deviceAuthMbtTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tracePath, err)
		}
		replayOneDeviceAuthTrace(t, filepath.Base(tracePath), trace)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [DeviceAuthorization] replayed %d Quint ITF traces (%d transitions) against Go RFC 8628 AuthManager + Authorizer", len(matches), totalSteps)
}

func replayOneDeviceAuthTrace(t *testing.T, name string, trace deviceAuthMbtTrace) {
	t.Helper()
	ctx := context.Background()
	vault := NewMemoryVault()
	devBackend := &mbtDeviceBackend{
		pollStart:  make(chan struct{}, 4),
		pollResume: make(chan bool, 4),
	}
	var clockMu sync.Mutex
	fakeNow := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	nowFn := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return fakeNow
	}

	mgr := NewManager(ManagerConfig{
		Vault:      vault,
		DeviceFlow: devBackend,
		Now:        nowFn,
	})
	if err := mgr.RegisterProvider(ProviderConfig{
		ID: ProviderGitHub, DisplayName: "GitHub Device", Method: MethodDeviceCode,
		DeviceAuthURL: "https://github.com/login/device/code",
		TokenURL:      "https://github.com/login/oauth/access_token",
		ClientID:      "marlin-dev", Scopes: []string{"read:user"},
	}); err != nil {
		t.Fatal(err)
	}
	// Pre-populate two signed-in accounts (1 and 2) to verify ticket-to-account Bearer binding.
	acct1 := AccountID("oidc:acct-1")
	acct2 := AccountID("oidc:acct-2")
	oidcCfg := ProviderConfig{ID: ProviderOIDC, DisplayName: "OIDC", Method: MethodOAuthPKCE}
	if _, err := mgr.admitSignedInAccount(ctx, oidcCfg, TokenSet{AccessToken: "tok-acct-1", ExpiresAt: fakeNow.Add(24 * time.Hour)}, AccountProfile{ID: acct1, Subject: "u1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.admitSignedInAccount(ctx, oidcCfg, TokenSet{AccessToken: "tok-acct-2", ExpiresAt: fakeNow.Add(24 * time.Hour)}, AccountProfile{ID: acct2, Subject: "u2"}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SwitchAccount(acct1); err != nil {
		t.Fatal(err)
	}

	authz := NewAuthorizer(mgr)
	if _, err := authz.LoadPolicyBytes("permit", []byte(`permit(principal, action, resource);`)); err != nil {
		t.Fatal(err)
	}

	var (
		pendingDevCode   string
		pollRunning      bool
		pollDone         chan error
		hasTicket        bool
		activeTicket     AuthzTicket
		attachedAcctNum  int64
	)
	defer func() {
		if pollRunning {
			devBackend.pollResume <- false
			<-pollDone
		}
	}()

	for i := 1; i < len(trace.States); i++ {
		prev := trace.States[i-1].H
		st := trace.States[i]
		act := st.ActionTaken

		switch act {
		case "beginDeviceFlow":
			if !prev.DeviceActive && !prev.Admitted && prev.FlowEpoch.Int64() < 3 {
				_, code, err := mgr.BeginDeviceAuth(ctx, ProviderGitHub)
				if err != nil {
					t.Fatalf("[%s step %d] BeginDeviceAuth failed: %v", name, i, err)
				}
				pendingDevCode = code
			}
		case "startDevicePoll":
			if prev.DeviceActive && !prev.DeviceExpired && !prev.PollInFlight && !pollRunning {
				pollRunning = true
				pollDone = make(chan error, 1)
				codeCopy := pendingDevCode
				go func() {
					_, _, pErr := mgr.CompleteDeviceAuth(ctx, ProviderGitHub, codeCopy)
					pollDone <- pErr
				}()
				<-devBackend.pollStart
			}
		case "expireDeviceCode":
			if prev.DeviceActive && !prev.DeviceExpired {
				clockMu.Lock()
				fakeNow = fakeNow.Add(15 * time.Minute)
				clockMu.Unlock()
			}
		case "cancelDeviceFlow":
			if prev.DeviceActive && prev.FlowEpoch.Int64() < 3 {
				mgr.CancelPending()
			}
		case "finishDevicePoll":
			if prev.PollInFlight && pollRunning {
				devBackend.pollResume <- true
				<-pollDone
				pollRunning = false
			}
		case "issueTicketForActive":
			activeID := mgr.Snapshot().ActiveAccountID
			if activeID != acct1 && activeID != acct2 {
				activeID = acct1
			}
			tk, err := authz.Evaluate(activeID, "call", "api", nil)
			if err != nil || !tk.Allowed {
				t.Fatalf("[%s step %d] Evaluate failed: tk=%+v err=%v", name, i, tk, err)
			}
			activeTicket = tk
			hasTicket = true
			attachedAcctNum = 0
		case "switchAccount":
			cur := mgr.Snapshot().ActiveAccountID
			next := acct2
			if cur == acct2 {
				next = acct1
			}
			if err := mgr.SwitchAccount(next); err != nil {
				t.Fatalf("[%s step %d] SwitchAccount(%s) failed: %v", name, i, next, err)
			}
		case "attachBearerForTicket":
			if hasTicket {
				hdr, err := authz.AuthorizedBearerHeader(ctx, activeTicket)
				if err != nil {
					t.Fatalf("[%s step %d] AuthorizedBearerHeader failed: %v", name, i, err)
				}
				switch hdr {
				case "Bearer tok-acct-1":
					attachedAcctNum = 1
				case "Bearer tok-acct-2":
					attachedAcctNum = 2
				default:
					t.Fatalf("[%s step %d] unexpected Bearer header %q", name, i, hdr)
				}
			}
		default:
			t.Fatalf("[%s step %d] unknown DeviceAuthorization MBT action %q", name, i, act)
		}

		snap := mgr.Snapshot()
		goDevAdmitted := false
		for _, a := range snap.Accounts {
			if a.ID == "github:dev-user" {
				goDevAdmitted = true
			}
		}
		if goDevAdmitted != st.H.Admitted {
			t.Fatalf("[%s step %d after %s] refinement mismatch device admitted: Go=%v Quint=%v (expired=%v cancelled=%v)",
				name, i, act, goDevAdmitted, st.H.Admitted, st.H.DeviceExpired, st.H.DeviceCancelled)
		}
		if attachedAcctNum != st.H.AttachedTokenAccount.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch attachedTokenAccount: Go=%d Quint=%d",
				name, i, act, attachedAcctNum, st.H.AttachedTokenAccount.Int64())
		}
	}
}

// ---------------------------------------------------------------------------
// 5. OidcFederation.qnt -> real Go VerifyIDToken + UserInfo sub + HTTPJWKSCache
// ---------------------------------------------------------------------------

type oidcFedState struct {
	JwksEpoch        itfBigInt `json:"jwksEpoch"`
	JwksRefreshes    itfBigInt `json:"jwksRefreshes"`
	JwksCooldown     bool      `json:"jwksCooldown"`
	IdTokenVerified  bool      `json:"idTokenVerified"`
	IdTokenSub       itfBigInt `json:"idTokenSub"`
	MultiAud         bool      `json:"multiAud"`
	AzpMatchesClient bool      `json:"azpMatchesClient"`
	UserInfoFetched  bool      `json:"userInfoFetched"`
	UserInfoSub      itfBigInt `json:"userInfoSub"`
	Admitted         bool      `json:"admitted"`
}

type oidcFedNondetPicks struct {
	Sub        itfOptionInt  `json:"sub"`
	IsMultiAud itfOptionBool `json:"isMultiAud"`
	AzpOk      itfOptionBool `json:"azpOk"`
	AlgSafe    itfOptionBool `json:"algSafe"`
	UiSub      itfOptionInt  `json:"uiSub"`
}

type oidcFedMbtTraceState struct {
	H           oidcFedState       `json:"h"`
	ActionTaken string             `json:"mbt::actionTaken"`
	NondetPicks oidcFedNondetPicks `json:"mbt::nondetPicks"`
}

type oidcFedMbtTrace struct {
	Vars   []string               `json:"vars"`
	States []oidcFedMbtTraceState `json:"states"`
}

func signTestES256IDToken(t *testing.T, priv *ecdsa.PrivateKey, sub int64, multiAud, azpOk, algSafe bool) string {
	t.Helper()
	alg := "ES256"
	if !algSafe {
		alg = "HS256"
	}
	hdrJSON, _ := json.Marshal(map[string]any{"alg": alg, "kid": "k1", "typ": "JWT"})
	now := time.Now().Unix()
	var aud any = "marlin-client"
	if multiAud {
		aud = []string{"marlin-client", "other-service"}
	}
	azp := "evil-client"
	if azpOk {
		azp = "marlin-client"
	}
	claims := map[string]any{
		"iss": "https://idp.example.com",
		"sub": fmt.Sprintf("user-%d", sub),
		"aud": aud,
		"azp": azp,
		"exp": now + 300,
		"iat": now,
	}
	clmJSON, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(hdrJSON) + "." + base64.RawURLEncoding.EncodeToString(clmJSON)
	if !algSafe {
		return input + ".AAAA"
	}
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestMBTReplayOidcFederationITF(t *testing.T) {
	bunBin, cliPath, qntModel := resolveDEModelAndCLI(t, "threat/identity/oidc-federation/OidcFederation.qnt")
	matches := generateMBTTraces(t, bunBin, cliPath, qntModel, "oidc_federation", 16, 5)

	totalSteps := 0
	for _, tracePath := range matches {
		raw, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		var trace oidcFedMbtTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tracePath, err)
		}
		replayOneOidcFedTrace(t, filepath.Base(tracePath), trace)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [OidcFederation] replayed %d Quint ITF traces (%d transitions) against Go VerifyIDToken + HTTPJWKSCache", len(matches), totalSteps)
}

func replayOneOidcFedTrace(t *testing.T, name string, trace oidcFedMbtTrace) {
	t.Helper()
	ctx := context.Background()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	xb := make([]byte, 32)
	yb := make([]byte, 32)
	priv.PublicKey.X.FillBytes(xb)
	priv.PublicKey.Y.FillBytes(yb)
	jwksBody, _ := json.Marshal(map[string]any{
		"keys": []map[string]any{{
			"kty": "EC", "crv": "P-256", "kid": "k1", "alg": "ES256", "use": "sig",
			"x": base64.RawURLEncoding.EncodeToString(xb),
			"y": base64.RawURLEncoding.EncodeToString(yb),
		}},
	})

	var uiSubMu sync.Mutex
	nextUiSub := int64(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/jwks"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(jwksBody)
		case strings.HasSuffix(r.URL.Path, "/userinfo"):
			uiSubMu.Lock()
			s := nextUiSub
			uiSubMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"sub":"user-%d","email":"u@example.com"}`, s)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ex := NewHTTPOAuthExchanger(srv.Client())
	ex.JWKS.RefreshCooldown = time.Hour
	jwksURL := srv.URL + "/jwks"
	keySet, err := ex.JWKS.Get(ctx, jwksURL, true, false)
	if err != nil {
		t.Fatal(err)
	}

	var (
		goIdTokenVerified bool
		goIdTokenSub      string
		goAdmitted        bool
		goJwksRefreshes   int64
	)

	for i := 1; i < len(trace.States); i++ {
		prev := trace.States[i-1].H
		st := trace.States[i]
		act := st.ActionTaken

		switch act {
		case "verifyIdToken":
			if !prev.IdTokenVerified {
				sub, _ := st.NondetPicks.Sub.Int64()
				isMultiAud, _ := st.NondetPicks.IsMultiAud.Bool()
				azpOk, _ := st.NondetPicks.AzpOk.Bool()
				algSafe, _ := st.NondetPicks.AlgSafe.Bool()
				rawJWT := signTestES256IDToken(t, priv, sub, isMultiAud, azpOk, algSafe)
				claims, vErr := VerifyIDToken(rawJWT, keySet, IDTokenVerifyConfig{
					ExpectedIssuer:   "https://idp.example.com",
					ExpectedAudience: "marlin-client",
				})
				if vErr == nil {
					goIdTokenVerified = true
					goIdTokenSub = claims.Subject
				} else if !algSafe && !errors.Is(vErr, ErrUnsupportedAlgorithm) {
					t.Fatalf("[%s step %d] expected ErrUnsupportedAlgorithm, got %v", name, i, vErr)
				} else if algSafe && isMultiAud && !azpOk && !errors.Is(vErr, ErrAZPMismatch) {
					t.Fatalf("[%s step %d] expected ErrAZPMismatch, got %v", name, i, vErr)
				}
			}
		case "fetchUserInfoAndAdmit":
			if prev.IdTokenVerified && !prev.Admitted {
				uiSub, _ := st.NondetPicks.UiSub.Int64()
				uiSubMu.Lock()
				nextUiSub = uiSub
				uiSubMu.Unlock()
				uiProf, uErr := ex.fetchUserInfo(ctx, srv.URL+"/userinfo", "at-1")
				if uErr != nil {
					t.Fatalf("[%s step %d] fetchUserInfo failed: %v", name, i, uErr)
				}
				if uiProf.Subject == goIdTokenSub {
					goAdmitted = true
				}
			}
		case "refreshJwks":
			if prev.JwksRefreshes.Int64() < 2 {
				_, rErr := ex.JWKS.Get(ctx, jwksURL, true, true)
				if prev.JwksCooldown {
					if !errors.Is(rErr, ErrJWKSRefreshCooldown) {
						t.Fatalf("[%s step %d] expected ErrJWKSRefreshCooldown while on cooldown, got %v", name, i, rErr)
					}
				} else {
					if rErr != nil {
						t.Fatalf("[%s step %d] expected JWKS force-refresh to succeed, got %v", name, i, rErr)
					}
					goJwksRefreshes++
				}
			}
		case "cooldownTick":
			if prev.JwksCooldown {
				ex.JWKS.mu.Lock()
				ex.JWKS.lastForceRefresh[jwksURL] = time.Now().Add(-2 * time.Hour)
				ex.JWKS.mu.Unlock()
			}
		default:
			t.Fatalf("[%s step %d] unknown OidcFederation MBT action %q", name, i, act)
		}

		if goIdTokenVerified != st.H.IdTokenVerified {
			t.Fatalf("[%s step %d after %s] refinement mismatch idTokenVerified: Go=%v Quint=%v", name, i, act, goIdTokenVerified, st.H.IdTokenVerified)
		}
		if goAdmitted != st.H.Admitted {
			t.Fatalf("[%s step %d after %s] refinement mismatch admitted: Go=%v Quint=%v", name, i, act, goAdmitted, st.H.Admitted)
		}
		if goJwksRefreshes != st.H.JwksRefreshes.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch jwksRefreshes: Go=%d Quint=%d", name, i, act, goJwksRefreshes, st.H.JwksRefreshes.Int64())
		}
	}
}

// ---------------------------------------------------------------------------
// 6. OAuthRevocationRecovery.qnt -> Crash-Safe Durable SignOut across Restart
// ---------------------------------------------------------------------------

type revRecoveryState struct {
	ParRegistered       bool `json:"parRegistered"`
	SessionActive       bool `json:"sessionActive"`
	SessionPersisted    bool `json:"sessionPersisted"`
	RevokeRequested     bool `json:"revokeRequested"`
	RevokeIntentDurable bool `json:"revokeIntentDurable"`
	Crashed             bool `json:"crashed"`
	RevokedOnServer     bool `json:"revokedOnServer"`
}

type revRecoveryMbtTraceState struct {
	H           revRecoveryState `json:"h"`
	ActionTaken string           `json:"mbt::actionTaken"`
}

type revRecoveryMbtTrace struct {
	Vars   []string                   `json:"vars"`
	States []revRecoveryMbtTraceState `json:"states"`
}

type mbtCrashRevokeExchanger struct {
	revokeEntered chan struct{}
	revokeRelease chan struct{}
}

func (e *mbtCrashRevokeExchanger) ExchangeCode(_ context.Context, _ ProviderConfig, _, _, _, _ string) (TokenSet, AccountProfile, error) {
	return TokenSet{
			AccessToken:  "durable-at-1",
			RefreshToken: "durable-rt-1",
			ExpiresAt:    time.Now().Add(time.Hour),
			Scopes:       []string{"openid"},
		}, AccountProfile{
			ID:            "oidc:crash-user",
			Subject:       "crash-user",
			Email:         "crash@example.com",
			EmailVerified: true,
			Scopes:        []string{"openid"},
		}, nil
}

func (e *mbtCrashRevokeExchanger) RefreshToken(_ context.Context, _ ProviderConfig, _ string) (TokenSet, error) {
	return TokenSet{}, ErrRefreshUnavailable
}

func (e *mbtCrashRevokeExchanger) RevokeToken(ctx context.Context, _ ProviderConfig, _ string) error {
	select {
	case e.revokeEntered <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-e.revokeRelease:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestMBTReplayOAuthRevocationRecoveryITF(t *testing.T) {
	bunBin, cliPath, qntModel := resolveDEModelAndCLI(t, "threat/identity/oauth-revocation-recovery/OAuthRevocationRecovery.qnt")
	matches := generateMBTTraces(t, bunBin, cliPath, qntModel, "oauth_revocation_recovery", 16, 5)

	totalSteps := 0
	for _, tracePath := range matches {
		raw, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		var trace revRecoveryMbtTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tracePath, err)
		}
		replayOneRevRecoveryTrace(t, filepath.Base(tracePath), trace)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [OAuthRevocationRecovery] replayed %d Quint ITF traces (%d transitions) against Go AuthManager + EncryptedFileVault crash-restart", len(matches), totalSteps)
}

func replayOneRevRecoveryTrace(t *testing.T, name string, trace revRecoveryMbtTrace) {
	t.Helper()
	vaultPath := filepath.Join(t.TempDir(), "vault.enc")
	masterKey := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i + 1)
	}
	cfg := ProviderConfig{
		ID: ProviderOIDC, DisplayName: "Crash OIDC", Method: MethodOAuthPKCE,
		AuthURL: "https://idp.example.com/auth", TokenURL: "https://idp.example.com/token",
		RevokeURL: "https://idp.example.com/revoke", ClientID: "marlin-crash",
		Scopes: []string{"openid"}, SupportsRefresh: true,
	}

	bootHost := func(ex *mbtCrashRevokeExchanger) *AuthManager {
		v, err := NewEncryptedFileVault(vaultPath, masterKey)
		if err != nil {
			t.Fatal(err)
		}
		m := NewManager(ManagerConfig{Vault: v, Exchanger: ex})
		if err := m.RegisterProvider(cfg); err != nil {
			t.Fatal(err)
		}
		return m
	}

	ex := &mbtCrashRevokeExchanger{
		revokeEntered: make(chan struct{}, 4),
		revokeRelease: make(chan struct{}, 4),
	}
	mgr := bootHost(ex)
	signOutCtx, cancelSignOut := context.WithCancel(context.Background())
	var (
		pendingAttemptID string
		pendingState     string
		signOutDone      chan error
		signOutInFlight  bool
	)
	defer func() {
		cancelSignOut()
		if signOutInFlight {
			<-signOutDone
		}
	}()

	for i := 1; i < len(trace.States); i++ {
		prev := trace.States[i-1].H
		st := trace.States[i]
		act := st.ActionTaken

		switch act {
		case "registerPar":
			if !prev.ParRegistered && !prev.SessionActive && !prev.RevokeRequested && mgr != nil {
				flow, err := mgr.BeginOAuth(ProviderOIDC, "http://127.0.0.1:8080/cb")
				if err != nil {
					t.Fatalf("[%s step %d] BeginOAuth failed: %v", name, i, err)
				}
				pendingAttemptID = flow.AttemptID
				mgr.mu.RLock()
				pendingState = mgr.oauthAttempts[flow.AttemptID].state
				mgr.mu.RUnlock()
			}
		case "admitViaPar":
			if prev.ParRegistered && !prev.SessionActive && !prev.RevokeRequested && !prev.Crashed && mgr != nil {
				if _, err := mgr.CompleteOAuthCallback(context.Background(), pendingAttemptID, LoopbackCallback{
					Code: "par-code-1", State: pendingState,
				}); err != nil {
					t.Fatalf("[%s step %d] CompleteOAuthCallback failed: %v", name, i, err)
				}
			}
		case "requestRevokeDurable":
			if prev.SessionActive && !prev.RevokeRequested && !prev.Crashed && mgr != nil {
				signOutInFlight = true
				signOutDone = make(chan error, 1)
				go func(m *AuthManager) {
					signOutDone <- m.SignOut(signOutCtx, "oidc:crash-user")
				}(mgr)
				// Wait until SignOut has durably scrubbed disk vault and entered remote RevokeToken I/O.
				<-ex.revokeEntered
			}
		case "crashHost":
			if !prev.Crashed {
				// Simulate sudden SIGKILL while remote RevokeToken may still be in flight.
				cancelSignOut()
				if signOutInFlight {
					<-signOutDone
					signOutInFlight = false
				}
				mgr = nil
			}
		case "recoverHost":
			if prev.Crashed {
				ex = &mbtCrashRevokeExchanger{
					revokeEntered: make(chan struct{}, 4),
					revokeRelease: make(chan struct{}, 4),
				}
				signOutCtx, cancelSignOut = context.WithCancel(context.Background())
				mgr = bootHost(ex)
				if _, err := mgr.RestoreFromVault(context.Background()); err != nil {
					t.Fatalf("[%s step %d] RestoreFromVault failed: %v", name, i, err)
				}
				// RFC 9126 PAR request_uri survives host restart before admission; re-attach local handle.
				if prev.ParRegistered && !prev.SessionPersisted && !prev.RevokeRequested {
					flow, err := mgr.BeginOAuth(ProviderOIDC, "http://127.0.0.1:8080/cb")
					if err != nil {
						t.Fatalf("[%s step %d] re-attach PAR BeginOAuth after crash failed: %v", name, i, err)
					}
					pendingAttemptID = flow.AttemptID
					mgr.mu.RLock()
					pendingState = mgr.oauthAttempts[flow.AttemptID].state
					mgr.mu.RUnlock()
				}
			}
		case "completeRevoke":
			if prev.RevokeIntentDurable && !prev.Crashed && !prev.RevokedOnServer && signOutInFlight {
				ex.revokeRelease <- struct{}{}
				<-signOutDone
				signOutInFlight = false
			}
		default:
			t.Fatalf("[%s step %d] unknown OAuthRevocationRecovery MBT action %q", name, i, act)
		}

		if !st.H.Crashed && mgr != nil {
			goSessionActive := len(mgr.Snapshot().Accounts) > 0
			if goSessionActive != st.H.SessionActive {
				t.Fatalf("[%s step %d after %s] CRASH-RECOVERY REFINEMENT VIOLATION sessionActive: Go=%v Quint=%v (revoked=%v)",
					name, i, act, goSessionActive, st.H.SessionActive, st.H.RevokeRequested)
			}
		}
	}
}
