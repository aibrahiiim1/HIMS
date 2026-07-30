package api

import "testing"

// The password policy is shared by the self-service change and the admin reset.
// If these drift apart, one path silently accepts what the other rejects.
func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		ok   bool
	}{
		{"empty", "", false},
		{"one under the floor", "1234567", false},
		{"exactly the floor", "12345678", true},
		{"long and mixed", "Correct-Horse-Battery-9", true},
		{"whitespace only at floor length", "        ", false},
		{"unicode counted as runes not bytes", "pässwörd", true}, // 8 runes, 11 bytes
		{"unicode one rune short", "pässwör", false},             // 7 runes, 10 bytes
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := validatePassword(tc.pw)
			if tc.ok && msg != "" {
				t.Fatalf("validatePassword(%q) rejected with %q, want accepted", tc.pw, msg)
			}
			if !tc.ok && msg == "" {
				t.Fatalf("validatePassword(%q) accepted, want rejected", tc.pw)
			}
		})
	}
}
