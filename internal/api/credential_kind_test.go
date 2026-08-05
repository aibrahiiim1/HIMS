package api

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The Go allowlist and the DB CHECK constraint are two copies of the same fact.
// When they disagree the operator gets a raw driver error (SQLSTATE 23514) that
// names neither the bad value nor the good ones — which is exactly what happened
// when 'winrm' was retired in the DB but left as the create form's default.
//
// This parses the CHECK out of the newest migration that redefines it and
// compares, so adding a kind in SQL without adding it here fails the build.
func TestCredentialKindsMatchMigration(t *testing.T) {
	dir := filepath.Join("..", "..", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	names := []string{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // sequential NNNNNN_ prefix => lexical order is apply order

	// Last migration that redefines the constraint wins.
	var lastSQL, lastFile string
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		if strings.Contains(string(b), "credentials_kind_check") && strings.Contains(string(b), "CHECK") {
			lastSQL, lastFile = string(b), n
		}
	}
	if lastSQL == "" {
		t.Fatal("no migration defines credentials_kind_check")
	}

	// Grab the quoted values inside the final CHECK (...) of that file.
	idx := strings.LastIndex(lastSQL, "CHECK")
	quoted := regexp.MustCompile(`'([a-z0-9_]+)'`).FindAllStringSubmatch(lastSQL[idx:], -1)
	fromSQL := map[string]bool{}
	for _, m := range quoted {
		fromSQL[m[1]] = true
	}
	if len(fromSQL) == 0 {
		t.Fatalf("parsed no kinds out of %s", lastFile)
	}

	fromGo := map[string]bool{}
	for _, k := range credentialKinds {
		fromGo[k] = true
	}
	for k := range fromSQL {
		if !fromGo[k] {
			t.Errorf("%s allows kind %q but credentialKinds does not — creates will be rejected by the API", lastFile, k)
		}
	}
	for k := range fromGo {
		if !fromSQL[k] {
			t.Errorf("credentialKinds allows %q but %s does not — creates will fail the DB CHECK (SQLSTATE 23514)", k, lastFile)
		}
	}
}

func TestValidCredentialKind(t *testing.T) {
	if ok, _ := validCredentialKind("windows"); !ok {
		t.Error("'windows' must be accepted — it is the unified Windows credential kind")
	}
	// Retired kinds must be rejected, and the message must point at the successor
	// rather than leaving the operator to guess.
	for _, k := range []string{"winrm", "wmi"} {
		ok, msg := validCredentialKind(k)
		if ok {
			t.Errorf("%q was retired by migration 000089 and must be rejected", k)
		}
		if !strings.Contains(msg, "windows") {
			t.Errorf("rejection of %q should name 'windows' as the replacement, got: %s", k, msg)
		}
	}
	ok, msg := validCredentialKind("nonsense")
	if ok {
		t.Error("unknown kind must be rejected")
	}
	if !strings.Contains(msg, "snmp_v2c") {
		t.Errorf("rejection should list the valid kinds, got: %s", msg)
	}
}
