package osinv

import (
	"context"
	"testing"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// mockWriter records calls so we can assert Persist's behaviour without a DB.
type mockWriter struct {
	inv          *db.UpsertOSInventoryParams
	disks        int
	services     int
	software     int
	rolesAdded   []string
	rolesDeleted int
	staleCalls   int
}

func (m *mockWriter) UpsertOSInventory(_ context.Context, a db.UpsertOSInventoryParams) (db.OsInventory, error) {
	m.inv = &a
	return db.OsInventory{}, nil
}
func (m *mockWriter) UpsertOSDisk(context.Context, db.UpsertOSDiskParams) error { m.disks++; return nil }
func (m *mockWriter) DeleteStaleOSDisks(context.Context, db.DeleteStaleOSDisksParams) error {
	m.staleCalls++
	return nil
}
func (m *mockWriter) UpsertOSNic(context.Context, db.UpsertOSNicParams) error { return nil }
func (m *mockWriter) DeleteStaleOSNics(context.Context, db.DeleteStaleOSNicsParams) error {
	m.staleCalls++
	return nil
}
func (m *mockWriter) UpsertOSService(context.Context, db.UpsertOSServiceParams) error {
	m.services++
	return nil
}
func (m *mockWriter) DeleteStaleOSServices(context.Context, db.DeleteStaleOSServicesParams) error {
	m.staleCalls++
	return nil
}
func (m *mockWriter) UpsertOSProcess(context.Context, db.UpsertOSProcessParams) error { return nil }
func (m *mockWriter) DeleteStaleOSProcesses(context.Context, db.DeleteStaleOSProcessesParams) error {
	m.staleCalls++
	return nil
}
func (m *mockWriter) UpsertOSSoftware(context.Context, db.UpsertOSSoftwareParams) error {
	m.software++
	return nil
}
func (m *mockWriter) DeleteStaleOSSoftware(context.Context, db.DeleteStaleOSSoftwareParams) error {
	m.staleCalls++
	return nil
}
func (m *mockWriter) UpsertOSRole(_ context.Context, a db.UpsertOSRoleParams) error {
	m.rolesAdded = append(m.rolesAdded, a.Role)
	return nil
}
func (m *mockWriter) DeleteStaleOSRoles(context.Context, db.DeleteStaleOSRolesParams) error {
	m.rolesDeleted++
	return nil
}

func TestPersist_Windows(t *testing.T) {
	rep, err := CollectWindows(context.Background(), winMock{})
	if err != nil {
		t.Fatal(err)
	}
	m := &mockWriter{}
	if err := Persist(context.Background(), m, uuid.New(), rep, time.Now()); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if m.inv == nil || m.inv.CollectionMethod != "winrm" {
		t.Fatalf("inventory not upserted with winrm method")
	}
	if m.inv.Hostname == nil || *m.inv.Hostname != "DC01" {
		t.Errorf("hostname not mapped")
	}
	if m.inv.RamTotalBytes == nil || *m.inv.RamTotalBytes != 68719476736 {
		t.Errorf("ram not mapped")
	}
	if m.disks != 1 || m.services != 4 || m.software != 1 {
		t.Errorf("collection upserts wrong: disks=%d svc=%d sw=%d", m.disks, m.services, m.software)
	}
	if m.staleCalls != 5 { // one DeleteStale per collection (disks,nics,services,processes,software)
		t.Errorf("expected 5 delete-stale calls, got %d", m.staleCalls)
	}
	if m.rolesDeleted != 1 {
		t.Errorf("roles should be refreshed (deleted-by-source once), got %d", m.rolesDeleted)
	}
	// DC + DNS + SQL roles from services.
	if len(m.rolesAdded) != 3 {
		t.Errorf("expected 3 roles, got %v", m.rolesAdded)
	}
}

func TestBuildOSInventoryParams_EmptyStaysNull(t *testing.T) {
	// A near-empty report: scalar fields must map to nil (NULL), not "" / 0.
	p := buildOSInventoryParams(uuid.New(), Report{Method: "ssh", Identity: Identity{Hostname: "h"}})
	if p.Hostname == nil || *p.Hostname != "h" {
		t.Error("set field should be non-nil")
	}
	if p.Domain != nil || p.OsCaption != nil || p.RamTotalBytes != nil || p.CpuCores != nil {
		t.Error("empty/zero fields must map to nil (NULL → 'Not collected')")
	}
	if p.InstallDate != nil {
		t.Error("empty date must be nil, not a zero time")
	}
}

func TestParseTimePtr(t *testing.T) {
	if parseTimePtr("") != nil || parseTimePtr("not-a-date") != nil {
		t.Error("empty/garbage date must be nil")
	}
	if tp := parseTimePtr("2026-06-01T02:00:00.000+00:00"); tp == nil {
		t.Error("ISO date should parse")
	}
}
