package api

import "testing"

// TestMalformedUserPassSecret pins the guard that rejects a user:password credential
// stored without a password — the malformed-credential class that left the 150.0.0.0/24
// ESXi host uncollectable ("C0r@lSe@" → user="C0r@lSe@", pass=""). SNMP communities
// (no colon, the whole secret IS the community) must NOT be flagged.
func TestMalformedUserPassSecret(t *testing.T) {
	cases := []struct {
		kind, secret string
		want         bool
	}{
		{"vendor_api", "C0r@lSe@", true},           // the real ESXi bug: no colon → empty password
		{"vendor_api", "root:", true},              // colon but blank password
		{"vendor_api", "root:C0r@lSe@#$", false},   // well-formed
		{"ssh", "linux", true},                     // no password
		{"ssh", "linux:linux", false},              // well-formed
		{"windows", "administrator:", true},        // blank password
		{"windows", "dpm@x.com:p@ss", false},       // well-formed
		{"onvif", "admin", true},                   // no password
		{"http_basic", "admin:secret", false},      // well-formed
		{"snmp_v2c", "public", false},              // community-only — never flagged
		{"snmp_v2c", "my-community-string", false}, // community-only — never flagged
	}
	for _, c := range cases {
		if got := malformedUserPassSecret(c.kind, c.secret); got != c.want {
			t.Errorf("malformedUserPassSecret(%q, <secret>) = %v, want %v", c.kind, got, c.want)
		}
	}
}
