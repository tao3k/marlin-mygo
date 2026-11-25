// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func FuzzParsePluginProfileTOML(f *testing.F) {
	f.Add([]byte("schema_version = 1\n[plugins.\"tao3k.demo\"]\nenabled = true\nactivation = \"on-slot\"\nslot_id = \"workbench.main\"\nidle_unload_sec = 300\n"))
	f.Add([]byte("schema_version = 1\n[plugins.\"evil.gate\"]\nenabled = true\nslot_id = \"approval.gate\"\n"))
	f.Add([]byte("schema_version = 1\n[plugins.\"acme.hook\"]\nenabled = true\nactivation = \"on-command\"\ntriggers = [\"a\", \"b\"]\nhooks = [\"after-turn-complete\"]\n[plugins.\"acme.hook\".config]\ntheme = \"nord\"\n"))
	f.Add([]byte(""))
	f.Add([]byte("[plugins.\"broken\"]\nactivation = \"never\"\n"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		prof, err := marketplace.ParsePluginProfileTOML(raw)
		if err != nil {
			return
		}
		for _, sp := range prof.Plugins {
			if _, idErr := schema.ParsePluginID(string(sp.PluginID)); idErr != nil {
				t.Fatalf("accepted invalid PluginID %q: %v", sp.PluginID, idErr)
			}
			if schema.IsRestrictedSlot(sp.SlotID) {
				t.Fatalf("accepted restricted slot %q in marlin-plugins.toml", sp.SlotID)
			}
			if !schema.ValidActivationPolicy(sp.Activation) {
				t.Fatalf("accepted invalid ActivationPolicy %q", sp.Activation)
			}
		}
		// Every accepted profile MUST marshal deterministically and parse back without error.
		roundTrip := marketplace.MarshalPluginProfileTOML(*prof)
		reparsed, err := marketplace.ParsePluginProfileTOML(roundTrip)
		if err != nil {
			t.Fatalf("marshaled profile failed round-trip parse: %v\n%s", err, string(roundTrip))
		}
		if len(reparsed.Plugins) != len(prof.Plugins) {
			t.Fatalf("round-trip plugin count mismatch: got %d want %d", len(reparsed.Plugins), len(prof.Plugins))
		}
	})
}

func FuzzParseTrustRingTOML(f *testing.F) {
	zeroKey := strings.Repeat("aa", ed25519.PublicKeySize)
	f.Add([]byte("schema_version = 1\n[publishers.\"tao3k.official\"]\ntier = \"official\"\nallow_catalog = true\ned25519_pub = \"" + zeroKey + "\"\n"))
	f.Add([]byte("[publishers.\"bad.tier\"]\ntier = \"superuser\"\ned25519_pub = \"" + zeroKey + "\"\n"))
	f.Add([]byte("[publishers.\"short.key\"]\ntier = \"verified\"\ned25519_pub = \"abcd\"\n"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, raw []byte) {
		ring, err := marketplace.ParseTrustRingTOML(raw)
		if err != nil {
			return
		}
		if len(ring.Publishers) == 0 {
			t.Fatal("accepted empty TrustRing")
		}
		for _, p := range ring.Publishers {
			if len(p.PublicKey) != ed25519.PublicKeySize {
				t.Fatalf("accepted invalid Ed25519 key length %d for %s", len(p.PublicKey), p.PublisherID)
			}
			switch p.Tier {
			case marketplace.TrustTierOfficial, marketplace.TrustTierVerified, marketplace.TrustTierDev:
			default:
				t.Fatalf("accepted unknown TrustTier %q", p.Tier)
			}
		}
	})
}
