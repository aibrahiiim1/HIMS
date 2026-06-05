package api

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

func TestBuildAccessMap_MergeAndSourcePriority(t *testing.T) {
	d1, d2, d3 := uuid.New(), uuid.New(), uuid.New()
	rows := []db.ListDeviceAccessSignalsRow{
		// d1: bound snmp + evidence ssh (two protocols)
		{DeviceID: d1, Protocol: "snmp_v2c", Source: "bound_credential"},
		{DeviceID: d1, Protocol: "ssh", Source: "evidence"},
		// d2: same protocol from both sources — bound must win regardless of order
		{DeviceID: d2, Protocol: "winrm", Source: "evidence"},
		{DeviceID: d2, Protocol: "winrm", Source: "bound_credential"},
		// d3: only evidence
		{DeviceID: d3, Protocol: "onvif", Source: "evidence"},
	}
	m := buildAccessMap(rows)

	if !m[d1].managed() || len(m[d1].protocols) != 2 {
		t.Fatalf("d1 should have 2 protocols, got %+v", m[d1])
	}
	if m[d1].protocols["snmp_v2c"] != "bound_credential" || m[d1].protocols["ssh"] != "evidence" {
		t.Errorf("d1 sources wrong: %+v", m[d1].protocols)
	}
	if m[d2].protocols["winrm"] != "bound_credential" {
		t.Errorf("d2 winrm source = %q, want bound_credential (bound must win)", m[d2].protocols["winrm"])
	}
	if !m[d3].has("onvif") || m[d3].protocols["onvif"] != "evidence" {
		t.Errorf("d3 onvif wrong: %+v", m[d3])
	}
	// A device with no signals is absent → unmanaged.
	var nilAccess *deviceAccess
	if nilAccess.managed() {
		t.Error("nil deviceAccess must be unmanaged")
	}
}

func TestFilterDevicesByAccess(t *testing.T) {
	managedID, unmanagedID := uuid.New(), uuid.New()
	credID := uuid.New()
	rows := []db.Device{
		{ID: managedID, CredentialID: &credID},
		{ID: unmanagedID, CredentialID: nil},
	}
	am := map[uuid.UUID]*deviceAccess{
		managedID: {protocols: map[string]string{"snmp_v2c": "bound_credential"}},
		// unmanagedID absent → unmanaged
	}

	only := func(got []db.Device, want uuid.UUID, label string) {
		if len(got) != 1 || got[0].ID != want {
			t.Errorf("%s: got %d rows %v, want [%s]", label, len(got), ids(got), want)
		}
	}

	only(filterDevicesByAccess(rows, am, "managed", "", ""), managedID, "access=managed")
	only(filterDevicesByAccess(rows, am, "unmanaged", "", ""), unmanagedID, "access=unmanaged")
	only(filterDevicesByAccess(rows, am, "", "snmp_v2c", ""), managedID, "accessProtocol=snmp_v2c")
	only(filterDevicesByAccess(rows, am, "", "", "no_credential_bound"), unmanagedID, "accessIssue=no_credential_bound")

	// Unknown protocol → no matches.
	if got := filterDevicesByAccess(rows, am, "", "winrm", ""); len(got) != 0 {
		t.Errorf("accessProtocol=winrm should match nothing, got %d", len(got))
	}
	// Non-derivable issue → nothing (never guess).
	if got := filterDevicesByAccess(rows, am, "", "", "credential_failed"); len(got) != 0 {
		t.Errorf("accessIssue=credential_failed should match nothing, got %d", len(got))
	}
	// No params → unchanged.
	if got := filterDevicesByAccess(rows, am, "", "", ""); len(got) != 2 {
		t.Errorf("no params should pass all, got %d", len(got))
	}
}

func ids(ds []db.Device) []uuid.UUID {
	out := make([]uuid.UUID, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}

func TestProtocolLabel(t *testing.T) {
	cases := map[string]string{"snmp_v2c": "SNMP v2c", "winrm": "WinRM", "onvif": "ONVIF", "http_basic": "HTTP Basic", "weird": "weird"}
	for in, want := range cases {
		if got := protocolLabel(in); got != want {
			t.Errorf("protocolLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
