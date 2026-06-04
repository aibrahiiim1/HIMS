package migrate

import (
	"testing"

	"github.com/coralsearesorts/hims/migrations"
)

func loadTestFS() ([]Migration, error) { return Load(migrations.FS) }

func TestVersionOf(t *testing.T) {
	cases := map[string]string{
		"000034_netflow.up.sql":        "000034",
		"000001_init.up.sql":           "000001",
		"000010_operations.up.sql":     "000010",
		"weird.sql":                    "weird",
		"":                             "",
	}
	for in, want := range cases {
		if got := versionOf(in); got != want {
			t.Errorf("versionOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPending(t *testing.T) {
	all := []Migration{{Version: "000001"}, {Version: "000002"}, {Version: "000003"}}
	applied := map[string]bool{"000001": true, "000002": true}
	p := Pending(all, applied)
	if len(p) != 1 || p[0].Version != "000003" {
		t.Fatalf("pending = %+v, want [000003]", p)
	}
	// Fully applied → nothing pending.
	if len(Pending(all, map[string]bool{"000001": true, "000002": true, "000003": true})) != 0 {
		t.Error("fully-applied set should have no pending")
	}
	// Empty ledger → all pending, in order.
	full := Pending(all, map[string]bool{})
	if len(full) != 3 || full[0].Version != "000001" || full[2].Version != "000003" {
		t.Errorf("empty ledger pending = %+v", full)
	}
}

func TestLoadAndSortFromRealMigrations(t *testing.T) {
	// Load the actual embedded migrations to confirm they parse + sort.
	ms, err := loadTestFS()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) < 30 {
		t.Fatalf("expected the full migration set, got %d", len(ms))
	}
	for i := 1; i < len(ms); i++ {
		if ms[i-1].Version > ms[i].Version {
			t.Errorf("migrations not sorted at %d: %s > %s", i, ms[i-1].Version, ms[i].Version)
		}
	}
}
