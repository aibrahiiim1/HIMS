package credtest

import (
	"strings"
	"testing"
)

func TestProtocolForKind(t *testing.T) {
	cases := map[string]string{
		"snmp_v2c": "snmp", "snmp_v3": "snmp", "SNMP": "snmp",
		"ssh": "ssh", "cli": "ssh",
		"winrm":      "winrm",
		"onvif":      "onvif",
		"http_basic": "http", "vendor_api": "http",
		"weird": "",
	}
	for kind, want := range cases {
		if got := ProtocolForKind(kind); got != want {
			t.Errorf("ProtocolForKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestSplitUserPass(t *testing.T) {
	u, p := SplitUserPass("admin:s3cr3t:with:colons")
	if u != "admin" || p != "s3cr3t:with:colons" {
		t.Errorf("split = %q/%q (password keeps later colons)", u, p)
	}
	u, p = SplitUserPass("community-only")
	if u != "community-only" || p != "" {
		t.Errorf("no-colon split = %q/%q", u, p)
	}
}

func TestCategorizeErr(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"ssh handshake: ssh: unable to authenticate", CatAuthFailed},
		{"HTTP 401 Unauthorized", CatAuthFailed},
		{"permission denied", CatAuthFailed},
		{"dial 10.0.0.1:22: connect: connection refused", CatUnreachable},
		{"context deadline exceeded", CatUnreachable},
		{"no such host", CatUnreachable},
		{"some weird protocol fault", CatError},
	}
	for _, c := range cases {
		if got, _ := categorizeErr(c.in); got != c.want {
			t.Errorf("categorizeErr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The tester must NEVER leak the secret into Outcome.Detail. categorizeErr only
// receives error strings, but guard the contract: feed a "secret-bearing" error
// and confirm Detail is a fixed non-secret note.
func TestDetailNeverEchoesSecret(t *testing.T) {
	secret := "SuperSecretPassw0rd!"
	_, detail := categorizeErr("ssh: handshake failed for user with password " + secret)
	if strings.Contains(detail, secret) {
		t.Errorf("Detail leaked the secret: %q", detail)
	}
}

func TestUnsupportedKind(t *testing.T) {
	o := Test(nil, "telnet", "x:y", "127.0.0.1", Options{})
	if o.Category != CatUnsupported {
		t.Errorf("unsupported kind → %q, want unsupported", o.Category)
	}
}
