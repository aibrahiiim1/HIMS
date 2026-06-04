package ssh

import (
	"testing"
	"time"
)

func TestBuildConfigDefaultsNoLegacy(t *testing.T) {
	base := buildConfig(Creds{Username: "admin", Password: "secret"}, false, 10*time.Second)
	// Without legacy KEX, KeyExchanges is left as the library default (nil/empty),
	// so no weak group1-sha1 is offered.
	for _, kex := range base.KeyExchanges {
		if kex == "diffie-hellman-group1-sha1" {
			t.Error("group1-sha1 should not be offered when legacyKEX is false")
		}
	}
	if base.User != "admin" {
		t.Errorf("User = %q, want admin", base.User)
	}
	if base.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", base.Timeout)
	}
}

func TestBuildConfigLegacyAddsWeakAlgorithms(t *testing.T) {
	cfg := buildConfig(Creds{Username: "admin", Password: "x"}, true, time.Second)
	hasGroup1 := false
	for _, kex := range cfg.KeyExchanges {
		if kex == "diffie-hellman-group1-sha1" {
			hasGroup1 = true
		}
	}
	if !hasGroup1 {
		t.Error("legacyKEX should add diffie-hellman-group1-sha1 for old switches")
	}
	hasCBC := false
	for _, c := range cfg.Ciphers {
		if c == "aes128-cbc" || c == "3des-cbc" {
			hasCBC = true
		}
	}
	if !hasCBC {
		t.Error("legacyKEX should add CBC ciphers for old switches")
	}
}
