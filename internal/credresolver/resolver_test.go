package credresolver

import (
	"testing"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/domain"
)

func ref(kind domain.CredentialKind, prio int, weak bool) CredRef {
	return CredRef{ID: uuid.New(), Kind: kind, Priority: prio, Weak: weak}
}

func ids(refs []CredRef) []uuid.UUID {
	out := make([]uuid.UUID, len(refs))
	for i, r := range refs {
		out[i] = r.ID
	}
	return out
}

func TestResolve_FiltersByFingerprint(t *testing.T) {
	snmp := ref(domain.CredSNMPv2c, 100, false)
	ssh := ref(domain.CredSSH, 100, false)
	winrm := ref(domain.CredWinRM, 100, false)
	out := Resolve(Input{
		Fingerprint: Fingerprint{SNMP: true, SSH: true}, // no WinRM
		Groups: []ScopedGroup{{
			Specificity: SpecSubnet,
			Members:     []CredRef{snmp, ssh, winrm},
		}},
	})
	if len(out) != 2 {
		t.Fatalf("WinRM should be filtered out (no WinRM surface), got %d: %+v", len(out), out)
	}
	for _, r := range out {
		if r.Kind == domain.CredWinRM {
			t.Fatal("winrm credential leaked past the fingerprint filter")
		}
	}
}

func TestResolve_BoundCredentialFirst(t *testing.T) {
	a := ref(domain.CredSNMPv2c, 10, false)
	b := ref(domain.CredSNMPv2c, 5, false) // lower priority would normally win
	out := Resolve(Input{
		Fingerprint:       Fingerprint{SNMP: true},
		BoundCredentialID: &a.ID,
		Groups:            []ScopedGroup{{Specificity: SpecSubnet, Members: []CredRef{a, b}}},
	})
	if len(out) != 2 || out[0].ID != a.ID {
		t.Fatalf("bound credential must be first, got %v", ids(out))
	}
}

func TestResolve_SubnetBeatsLocation(t *testing.T) {
	loc := ref(domain.CredSNMPv2c, 1, false)   // better priority but broader scope
	sub := ref(domain.CredSNMPv2c, 100, false) // worse priority but more specific
	out := Resolve(Input{
		Fingerprint: Fingerprint{SNMP: true},
		Groups: []ScopedGroup{
			{Specificity: SpecLocation, Members: []CredRef{loc}},
			{Specificity: SpecSubnet, Members: []CredRef{sub}},
		},
	})
	if out[0].ID != sub.ID {
		t.Fatalf("subnet-scoped credential should outrank location-scoped, got %v", ids(out))
	}
}

func TestResolve_WeakSinksUnlessBound(t *testing.T) {
	strong := ref(domain.CredSNMPv2c, 100, false)
	weak := ref(domain.CredSNMPv2c, 1, true) // "public": great priority but weak
	out := Resolve(Input{
		Fingerprint: Fingerprint{SNMP: true},
		Groups:      []ScopedGroup{{Specificity: SpecSubnet, Members: []CredRef{weak, strong}}},
	})
	if out[0].ID != strong.ID || out[1].ID != weak.ID {
		t.Fatalf("weak credential must sink below non-weak, got %v", ids(out))
	}

	// But if the weak one is the proven binding, it leads.
	out2 := Resolve(Input{
		Fingerprint:       Fingerprint{SNMP: true},
		BoundCredentialID: &weak.ID,
		Groups:            []ScopedGroup{{Specificity: SpecSubnet, Members: []CredRef{weak, strong}}},
	})
	if out2[0].ID != weak.ID {
		t.Fatalf("a proven (bound) weak credential should still lead, got %v", ids(out2))
	}
}

func TestResolve_PriorityOrderWithinTier(t *testing.T) {
	p100 := ref(domain.CredSSH, 100, false)
	p10 := ref(domain.CredSSH, 10, false)
	p50 := ref(domain.CredSSH, 50, false)
	out := Resolve(Input{
		Fingerprint: Fingerprint{SSH: true},
		Groups:      []ScopedGroup{{Specificity: SpecSubnet, Members: []CredRef{p100, p10, p50}}},
	})
	if out[0].ID != p10.ID || out[1].ID != p50.ID || out[2].ID != p100.ID {
		t.Fatalf("expected priority order 10,50,100; got %v", ids(out))
	}
}

func TestResolve_DedupesAcrossScopes(t *testing.T) {
	shared := ref(domain.CredSNMPv2c, 100, false)
	out := Resolve(Input{
		Fingerprint: Fingerprint{SNMP: true},
		Groups: []ScopedGroup{
			{Specificity: SpecLocation, Members: []CredRef{shared}},
			{Specificity: SpecSubnet, Members: []CredRef{shared}},
		},
	})
	if len(out) != 1 {
		t.Fatalf("a credential bound at two scopes should appear once, got %d", len(out))
	}
}

func TestResolve_EmptyWhenNothingViable(t *testing.T) {
	out := Resolve(Input{
		Fingerprint: Fingerprint{SSH: true}, // host speaks SSH only
		Groups: []ScopedGroup{{
			Specificity: SpecSubnet,
			Members:     []CredRef{ref(domain.CredSNMPv2c, 1, false)}, // only SNMP creds in scope
		}},
	})
	if len(out) != 0 {
		t.Fatalf("expected no viable candidates, got %v", ids(out))
	}
}

func TestResolve_ExclusiveSubnetIgnoresGlobalGroups(t *testing.T) {
	// The IP's subnet is scoped to two CCTV creds. The global groups carry an
	// SSH switch credential and an SNMP community — neither must be tried.
	onvif := ref(domain.CredONVIF, 100, false)
	webBasic := ref(domain.CredHTTPBasic, 100, false)
	switchSSH := ref(domain.CredSSH, 1, false)
	community := ref(domain.CredSNMPv2c, 1, false)
	out := Resolve(Input{
		// Camera surface: HTTP + SNMP open. Even though SNMP is "open", the
		// exclusive set has no SNMP cred, so no community is sprayed.
		Fingerprint: Fingerprint{HTTP: true, SNMP: true, SSH: true},
		Groups: []ScopedGroup{
			{Specificity: SpecLocation, Members: []CredRef{switchSSH, community}},
			{Specificity: SpecSubnet, Members: []CredRef{switchSSH}},
		},
		Exclusive: []CredRef{onvif, webBasic},
	})
	if len(out) != 2 {
		t.Fatalf("only the 2 subnet-scoped creds should be tried, got %d: %v", len(out), ids(out))
	}
	for _, r := range out {
		if r.ID == switchSSH.ID || r.ID == community.ID {
			t.Fatalf("a non-subnet credential (%s) leaked past exclusivity: %v", r.Kind, ids(out))
		}
		if r.Kind != domain.CredONVIF && r.Kind != domain.CredHTTPBasic {
			t.Fatalf("unexpected kind %s in exclusive result", r.Kind)
		}
	}
}

func TestResolve_ExclusiveStillFingerprintFiltered(t *testing.T) {
	// An RTSP-only camera (no HTTP surface) gets none of its subnet's web creds.
	onvif := ref(domain.CredONVIF, 100, false)
	out := Resolve(Input{
		Fingerprint: Fingerprint{SNMP: true}, // no HTTP
		Exclusive:   []CredRef{onvif},
	})
	if len(out) != 0 {
		t.Fatalf("an ONVIF cred must not be tried against a host with no web surface, got %v", ids(out))
	}
}

func TestResolve_ExclusiveBoundCredentialFirst(t *testing.T) {
	a := ref(domain.CredHTTPBasic, 100, false)
	b := ref(domain.CredHTTPBasic, 1, false) // better priority but not the bound one
	out := Resolve(Input{
		Fingerprint:       Fingerprint{HTTP: true},
		BoundCredentialID: &a.ID,
		Exclusive:         []CredRef{a, b},
	})
	if len(out) != 2 || out[0].ID != a.ID {
		t.Fatalf("the bound credential must lead even within the exclusive set, got %v", ids(out))
	}
}

func TestResolve_EmptyExclusiveFallsBackToGroups(t *testing.T) {
	// Empty Exclusive ⇒ current behavior: the global/scope groups are used.
	g := ref(domain.CredSSH, 10, false)
	out := Resolve(Input{
		Fingerprint: Fingerprint{SSH: true},
		Groups:      []ScopedGroup{{Specificity: SpecSubnet, Members: []CredRef{g}}},
		Exclusive:   nil,
	})
	if len(out) != 1 || out[0].ID != g.ID {
		t.Fatalf("with no subnet scope, normal group resolution must still work, got %v", ids(out))
	}
}

func TestFingerprint_Allows(t *testing.T) {
	f := Fingerprint{SNMP: true, HTTP: true}
	cases := map[domain.CredentialKind]bool{
		domain.CredSNMPv2c:   true,
		domain.CredSNMPv3:    true,
		domain.CredONVIF:     true,
		domain.CredVendorAPI: true,
		domain.CredHTTPBasic: true,
		domain.CredSSH:       false,
		domain.CredWinRM:     false,
		domain.CredLDAP:      false,
	}
	for k, want := range cases {
		if got := f.Allows(k); got != want {
			t.Errorf("Allows(%s) = %v, want %v", k, got, want)
		}
	}
}
