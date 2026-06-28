package credtest

import "testing"

// TestIsHandshakeAlgoError pins which SSH errors trigger the automatic legacy-algorithm
// retry: only algorithm-negotiation failures (KEX / cipher / host-key / signature algo).
// An auth rejection, connection refusal, or timeout must NOT (a legacy retry can't help
// and only adds load).
func TestIsHandshakeAlgoError(t *testing.T) {
	retry := []string{
		"ssh: handshake failed: no common algorithm for key exchange",
		"ssh: handshake failed: ssh: no common algorithm for host key",
		`ssh: handshake failed: ssh: invalid signature algorithm "rsa-sha2-256", expected "ssh-rsa"`,
		"ssh: handshake failed: no common algorithm for client to server cipher",
	}
	for _, e := range retry {
		if !isHandshakeAlgoError(e) {
			t.Errorf("isHandshakeAlgoError(%q) = false, want true (should trigger legacy retry)", e)
		}
	}
	noRetry := []string{
		"ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password]", // auth — not algo
		"dial tcp 10.0.0.5:22: connect: connection refused",                                     // refused
		"dial 10.0.0.5:22: i/o timeout",                                                         // timeout
		"ssh: unable to authenticate",                                                           // auth, no "handshake"
	}
	for _, e := range noRetry {
		if isHandshakeAlgoError(e) {
			t.Errorf("isHandshakeAlgoError(%q) = true, want false (no legacy retry)", e)
		}
	}
}
