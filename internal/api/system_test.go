package api

import (
	"strings"
	"testing"
)

func TestRedactDBURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"postgres://hims:hims@localhost:5433/hims?sslmode=disable", "postgres://hims:****@localhost:5433/hims?sslmode=disable"},
		{"postgres://hims@localhost:5433/hims", "postgres://hims@localhost:5433/hims"},
		{"postgres://localhost:5433/hims", "postgres://localhost:5433/hims"},
	}
	for _, c := range cases {
		if got := redactDBURL(c.in); got != c.want {
			t.Errorf("redactDBURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRedactDBURLNeverLeaksPassword(t *testing.T) {
	const pw = "sup3r-s3cr3t-pw"
	got := redactDBURL("postgres://user:" + pw + "@db.internal:5432/x?sslmode=require")
	if strings.Contains(got, pw) {
		t.Fatalf("redacted URL leaked password: %q", got)
	}
	if !strings.Contains(got, "****") {
		t.Fatalf("redacted URL did not mask the password: %q", got)
	}
}
