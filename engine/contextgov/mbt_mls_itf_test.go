// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package contextgov_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tao3k/marlin-mygo/engine/contextgov"
	"github.com/tao3k/marlin-mygo/engine/features"
)

type mlsRatchetState struct {
	Epoch                  itfBigInt `json:"epoch"`
	KeyGen                 itfBigInt `json:"keyGen"`
	PastMsgSent            bool      `json:"pastMsgSent"`
	PastMsgKeyGen          itfBigInt `json:"pastMsgKeyGen"`
	PastKeyRetained        bool      `json:"pastKeyRetained"`
	Compromised            bool      `json:"compromised"`
	AttackerKeyGen         itfBigInt `json:"attackerKeyGen"`
	Healed                 bool      `json:"healed"`
	PostHealMsgSent        bool      `json:"postHealMsgSent"`
	PostHealMsgKeyGen      itfBigInt `json:"postHealMsgKeyGen"`
	BobRemoved             bool      `json:"bobRemoved"`
	BobKeyGen              itfBigInt `json:"bobKeyGen"`
	PostRemoveMsgSent      bool      `json:"postRemoveMsgSent"`
	PostRemoveMsgKeyGen    itfBigInt `json:"postRemoveMsgKeyGen"`
	PastMsgDecrypted       bool      `json:"pastMsgDecrypted"`
	PostHealMsgDecrypted   bool      `json:"postHealMsgDecrypted"`
	RemovedMemberDecrypted bool      `json:"removedMemberDecrypted"`
	// MarlinMlsContextOverlay fields
	ACLEpoch            itfBigInt `json:"aclEpoch"`
	UnrotatedRemoveLeak bool      `json:"unrotatedRemoveLeak"`
}

type mlsRatchetTraceState struct {
	H           mlsRatchetState `json:"h"`
	ActionTaken string          `json:"mbt::actionTaken"`
}

type mlsRatchetTrace struct {
	States []mlsRatchetTraceState `json:"states"`
}

func TestMBTReplayMlsRatchetPcsITF(t *testing.T) {
	bunBin, cliPath, modelPath, repoRoot := resolveContextModel(t, "threat/protocol/mls-ratchet-pcs/MlsRatchetPcs.qnt", "")
	matches := generateContextMBTTraces(t, bunBin, cliPath, modelPath, repoRoot, "mls_ratchet_pcs", 18, 12)
	totalSteps := 0
	for _, tp := range matches {
		raw, err := os.ReadFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		var trace mlsRatchetTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tp, err)
		}
		replayOneMlsTrace(t, filepath.Base(tp), trace, false)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [MlsRatchetPcs] replayed %d Quint ITF traces (%d transitions) against Go AES-256-GCM ChannelKeyRatchet", len(matches), totalSteps)
}

func TestMBTReplayMarlinMlsContextOverlayITF(t *testing.T) {
	bunBin, cliPath, modelPath, repoRoot := resolveContextModel(t, "", "spec/context/MarlinMlsContextOverlay.qnt")
	matches := generateContextMBTTraces(t, bunBin, cliPath, modelPath, repoRoot, "marlin_mls_context_composed", 18, 12)
	totalSteps := 0
	for _, tp := range matches {
		raw, err := os.ReadFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		var trace mlsRatchetTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tp, err)
		}
		replayOneMlsTrace(t, filepath.Base(tp), trace, true)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [MarlinMlsContextOverlay] replayed %d Quint @quint-extends ITF traces (%d transitions) against Go ChannelKeyRatchet + GovernanceStore", len(matches), totalSteps)
}

func replayOneMlsTrace(t *testing.T, name string, trace mlsRatchetTrace, isOverlay bool) {
	t.Helper()
	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	store := contextgov.NewGovernanceStore(ledger)
	ratchet, err := contextgov.NewChannelKeyRatchet("tenant-1", "ch-e2ee", store, "alice", "bob")
	if err != nil {
		t.Fatal(err)
	}

	var (
		hasPastMsg       bool
		pastMsg          contextgov.ChannelEpochCiphertext
		hasPostHealMsg   bool
		postHealMsg      contextgov.ChannelEpochCiphertext
		hasPostRemoveMsg bool
		postRemoveMsg    contextgov.ChannelEpochCiphertext
		hasAttackerKey   bool
		attackerKey      [32]byte
		hasBobFrozenKey  bool
		bobFrozenKey     [32]byte
		aclSynced        bool
	)

	for i := 1; i < len(trace.States); i++ {
		prev := trace.States[i-1].H
		st := trace.States[i]
		act := st.ActionTaken

		switch act {
		case "sendPastMessage":
			if prev.Epoch.Int64() == 1 && !prev.PastMsgSent {
				ct, err := ratchet.Seal([]byte("epoch-1-secret"))
				if err != nil {
					t.Fatalf("[%s step %d] Seal past msg failed: %v", name, i, err)
				}
				pastMsg = ct
				hasPastMsg = true
			}
		case "advanceEpochRegular":
			if prev.Epoch.Int64() < 5 && prev.PastMsgSent && !prev.Compromised {
				ratchet.AdvanceEpoch()
			}
		case "compromiseCurrentState":
			if prev.Epoch.Int64() >= 2 && !prev.Compromised {
				attackerKey, _, _ = ratchet.CompromiseCurrentKey()
				hasAttackerKey = true
			}
		case "ratchetHealPcs":
			if prev.Compromised && !prev.Healed && prev.Epoch.Int64() < 5 {
				if _, _, err := ratchet.HealPCS(); err != nil {
					t.Fatalf("[%s step %d] HealPCS failed: %v", name, i, err)
				}
			}
		case "sendPostHealMessage":
			if prev.Healed && !prev.PostHealMsgSent {
				ct, err := ratchet.Seal([]byte("post-heal-secret"))
				if err != nil {
					t.Fatalf("[%s step %d] Seal post-heal msg failed: %v", name, i, err)
				}
				postHealMsg = ct
				hasPostHealMsg = true
			}
		case "removeBobCommit":
			if !prev.BobRemoved && prev.Epoch.Int64() < 5 {
				_, _, frozen, err := ratchet.RemoveMemberAndRotate("bob")
				if err != nil {
					t.Fatalf("[%s step %d] RemoveMemberAndRotate failed: %v", name, i, err)
				}
				bobFrozenKey = frozen
				hasBobFrozenKey = true
			}
		case "sendPostRemoveMessage":
			if prev.BobRemoved && !prev.PostRemoveMsgSent {
				ct, err := ratchet.Seal([]byte("post-remove-secret"))
				if err != nil {
					t.Fatalf("[%s step %d] Seal post-remove msg failed: %v", name, i, err)
				}
				postRemoveMsg = ct
				hasPostRemoveMsg = true
				// Active member Alice MUST still decrypt cleanly!
				pt, err := ratchet.OpenAsMember("alice", ct)
				if err != nil || !bytes.Equal(pt, []byte("post-remove-secret")) {
					t.Fatalf("[%s step %d] active member Alice decrypt failed: pt=%q err=%v", name, i, pt, err)
				}
			}
		case "attackerDecryptPast":
			if prev.Compromised && prev.PastMsgSent && hasAttackerKey && hasPastMsg {
				if pt, err := ratchet.OpenWithKey(attackerKey, pastMsg); err == nil {
					t.Fatalf("[%s step %d] FORWARD SECRECY VIOLATION: compromised epoch>=2 key decrypted epoch-1 past msg %q!", name, i, pt)
				}
			}
		case "attackerDecryptPostHeal":
			if prev.Compromised && prev.PostHealMsgSent && hasAttackerKey && hasPostHealMsg {
				if pt, err := ratchet.OpenWithKey(attackerKey, postHealMsg); err == nil {
					t.Fatalf("[%s step %d] PCS VIOLATION: pre-heal compromised key decrypted post-heal msg %q!", name, i, pt)
				}
			}
		case "bobDecryptPostRemove":
			if prev.BobRemoved && prev.PostRemoveMsgSent && hasBobFrozenKey && hasPostRemoveMsg {
				if pt, err := ratchet.OpenWithKey(bobFrozenKey, postRemoveMsg); err == nil {
					t.Fatalf("[%s step %d] REMOVAL ISOLATION VIOLATION: removed Bob key decrypted post-remove msg %q!", name, i, pt)
				}
				if _, err := ratchet.OpenAsMember("bob", postRemoveMsg); err == nil {
					t.Fatalf("[%s step %d] REMOVAL VIOLATION: OpenAsMember succeeded for removed Bob!", name, i)
				}
			}
		case "syncAclOnRemove":
			if prev.BobRemoved && prev.ACLEpoch.Int64() < 5 {
				if store.IsChannelMember("tenant-1", "ch-e2ee", "bob") {
					t.Fatalf("[%s step %d] expected GovernanceStore to have removed bob on removeBobCommit", name, i)
				}
				aclSynced = true
			}
		default:
			t.Fatalf("[%s step %d] unknown MLS MBT action %q", name, i, act)
		}

		ep, kg := ratchet.State()
		if int64(ep) != st.H.Epoch.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch epoch: Go=%d Quint=%d", name, i, act, ep, st.H.Epoch.Int64())
		}
		if int64(kg) != st.H.KeyGen.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch keyGen: Go=%d Quint=%d", name, i, act, kg, st.H.KeyGen.Int64())
		}
		_ = isOverlay
		_ = aclSynced
	}
}
