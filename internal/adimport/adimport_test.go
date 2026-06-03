package adimport

import (
	"testing"

	"github.com/go-ldap/ldap/v3"

	"github.com/coralsearesorts/hims/internal/domain"
)

// entry builds an *ldap.Entry from attr→value pairs.
func entry(dn string, attrs map[string]string) *ldap.Entry {
	e := ldap.NewEntry(dn, nil)
	for k, v := range attrs {
		e.Attributes = append(e.Attributes, &ldap.EntryAttribute{Name: k, Values: []string{v}})
	}
	return e
}

func find(cs []Computer, name string) (Computer, bool) {
	for _, c := range cs {
		if c.Name == name {
			return c, true
		}
	}
	return Computer{}, false
}

func TestParseComputers_ServerVsEndpoint(t *testing.T) {
	entries := []*ldap.Entry{
		entry("CN=DC01,OU=Servers,DC=hotel,DC=local", map[string]string{
			"sAMAccountName": "DC01$", "dNSHostName": "DC01.hotel.local",
			"operatingSystem": "Windows Server 2022 Standard", "userAccountControl": "4096",
		}),
		entry("CN=FRONT-PC07,OU=Workstations,DC=hotel,DC=local", map[string]string{
			"sAMAccountName": "FRONT-PC07$", "dNSHostName": "front-pc07.hotel.local",
			"operatingSystem": "Windows 11 Pro", "userAccountControl": "4098", // disabled bit set
		}),
	}
	cs := ParseComputers(entries)
	if len(cs) != 2 {
		t.Fatalf("got %d computers; want 2", len(cs))
	}
	dc, _ := find(cs, "DC01")
	if dc.Category != domain.CatServer || dc.DNSHostName != "dc01.hotel.local" || !dc.Enabled {
		t.Fatalf("DC01 wrong: %+v", dc)
	}
	pc, _ := find(cs, "FRONT-PC07")
	if pc.Category != domain.CatEndpoint || pc.Enabled { // 4098 has 0x2 → disabled
		t.Fatalf("FRONT-PC07 wrong: %+v", pc)
	}
}

func TestParseComputers_FallbacksAndSkips(t *testing.T) {
	entries := []*ldap.Entry{
		entry("CN=NoSam", map[string]string{"cn": "NoSam", "operatingSystem": ""}), // cn fallback, empty OS→endpoint, enabled default
		entry("CN=Nameless", map[string]string{"operatingSystem": "Windows 10"}),   // no name → skipped
	}
	cs := ParseComputers(entries)
	if len(cs) != 1 {
		t.Fatalf("got %d; want 1 (nameless skipped)", len(cs))
	}
	if cs[0].Name != "NoSam" || cs[0].Category != domain.CatEndpoint || !cs[0].Enabled {
		t.Fatalf("NoSam wrong: %+v", cs[0])
	}
}

// fakeSearcher returns canned entries.
type fakeSearcher struct {
	entries []*ldap.Entry
	gotBase string
}

func (f *fakeSearcher) Search(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	f.gotBase = req.BaseDN
	return &ldap.SearchResult{Entries: f.entries}, nil
}

func TestSearchComputers_UsesBaseDN(t *testing.T) {
	fs := &fakeSearcher{entries: []*ldap.Entry{
		entry("CN=SRV", map[string]string{"sAMAccountName": "SRV$", "operatingSystem": "Windows Server 2019"}),
	}}
	cs, err := SearchComputers(fs, "OU=HotelA,DC=hotel,DC=local")
	if err != nil {
		t.Fatal(err)
	}
	if fs.gotBase != "OU=HotelA,DC=hotel,DC=local" {
		t.Fatalf("baseDN not passed: %q", fs.gotBase)
	}
	if len(cs) != 1 || cs[0].Category != domain.CatServer {
		t.Fatalf("search→parse wrong: %+v", cs)
	}
}

func TestEnabledFromUAC(t *testing.T) {
	if !enabledFromUAC("512") || enabledFromUAC("514") || !enabledFromUAC("") || !enabledFromUAC("garbage") {
		t.Fatal("UAC enabled-bit logic wrong")
	}
}
