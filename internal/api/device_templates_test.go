package api

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

func i32p(v int32) *int32   { return &v }
func strp(s string) *string { return &s }

func TestPlanChecks_SkipsExistingAndInvalid(t *testing.T) {
	existing := []db.MonitoringCheck{
		{Kind: "tcp", TargetPort: i32p(443)},
		{Kind: "snmp", Oid: strp("1.3.6.1.2.1.1.3.0")},
	}
	checks := []templateCheck{
		{Kind: "tcp", Port: 443},                  // already present → skip
		{Kind: "tcp", Port: 22},                   // new → create
		{Kind: "snmp", OID: "1.3.6.1.2.1.1.3.0"},  // already present → skip
		{Kind: "snmp", OID: "1.3.6.1.2.1.2.2.1.10.1"}, // new → create
		{Kind: "tcp", Port: 0},                    // invalid → skip
		{Kind: "snmp", OID: ""},                   // invalid → skip
		{Kind: "telnet", Port: 23},                // invalid kind → skip
	}
	toCreate, skipped := planChecks(existing, checks)
	if len(toCreate) != 2 {
		t.Fatalf("expected 2 to create, got %d: %+v", len(toCreate), toCreate)
	}
	if skipped != 5 {
		t.Errorf("expected 5 skipped, got %d", skipped)
	}
	// Re-applying the freshly-planned set against an updated existing list must
	// be a no-op (idempotency).
	updated := append(existing,
		db.MonitoringCheck{Kind: "tcp", TargetPort: i32p(22)},
		db.MonitoringCheck{Kind: "snmp", Oid: strp("1.3.6.1.2.1.2.2.1.10.1")},
	)
	again, _ := planChecks(updated, checks)
	if len(again) != 0 {
		t.Errorf("re-apply should create nothing, got %d", len(again))
	}
}

func TestTemplateCheckWithDefaults(t *testing.T) {
	c := templateCheck{Kind: "tcp", Port: 80}.withDefaults()
	if c.IntervalSeconds != 60 || c.DownThreshold != 2 {
		t.Errorf("defaults not applied: %+v", c)
	}
	c2 := templateCheck{Kind: "tcp", Port: 80, IntervalSeconds: 30, DownThreshold: 5}.withDefaults()
	if c2.IntervalSeconds != 30 || c2.DownThreshold != 5 {
		t.Errorf("explicit values overwritten: %+v", c2)
	}
}
