// Package adimport imports Active Directory computer objects over LDAP as a
// discovery scope source — the AD-primary Windows discovery the spec calls for
// (multi-hotel fleet under one domain). It is one optional accelerator
// alongside IP-range scanning, not a dependency: HIMS works without AD.
//
// The LDAP bind/search transport can't be unit-tested without a directory, so
// the design isolates the testable part: a Searcher interface returns
// *ldap.Entry results, and ParseComputers + classifyOS map them to device
// candidates — exercised with hand-built entries in adimport_test.go. The real
// bind/search + DNS resolution + apply live in the collector's -adimport mode
// and are marked live-validation-pending.
package adimport

import (
	"strings"

	"github.com/go-ldap/ldap/v3"

	"github.com/coralsearesorts/hims/internal/domain"
)

// Searcher is the slice of *ldap.Conn the importer needs (so tests inject a
// fake returning canned entries).
type Searcher interface {
	Search(req *ldap.SearchRequest) (*ldap.SearchResult, error)
}

// Computer is a parsed AD computer object.
type Computer struct {
	Name        string                // cn / sAMAccountName (trailing $ stripped)
	DNSHostName string                // FQDN to resolve → IP
	OS          string                // operatingSystem
	OSVersion   string                // operatingSystemVersion
	Enabled     bool                  // from userAccountControl
	Category    domain.DeviceCategory // server | endpoint (from OS)
}

// ComputerFilter is the LDAP filter for enabled-or-all computer objects.
const ComputerFilter = "(objectClass=computer)"

// computerAttrs are the attributes we request.
var computerAttrs = []string{"cn", "sAMAccountName", "dNSHostName", "operatingSystem", "operatingSystemVersion", "userAccountControl"}

// OU is an Active Directory organizational unit (for the graphical browser).
type OU struct {
	DN   string // distinguishedName, e.g. "OU=Floor1,OU=HotelA,DC=corp,DC=local"
	Name string // ou / name
}

// OUFilter matches organizational units.
const OUFilter = "(objectClass=organizationalUnit)"

// SearchOUs enumerates the OU subtree under baseDN (the tree to pick from).
func SearchOUs(s Searcher, baseDN string) ([]OU, error) {
	req := ldap.NewSearchRequest(
		baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		OUFilter, []string{"ou", "name", "distinguishedName"}, nil,
	)
	res, err := s.Search(req)
	if err != nil {
		return nil, err
	}
	return ParseOUs(res.Entries), nil
}

// ParseOUs maps LDAP entries to OU records (pure; the tested core).
func ParseOUs(entries []*ldap.Entry) []OU {
	out := make([]OU, 0, len(entries))
	for _, e := range entries {
		name := e.GetAttributeValue("ou")
		if name == "" {
			name = e.GetAttributeValue("name")
		}
		dn := e.GetAttributeValue("distinguishedName")
		if dn == "" {
			dn = e.DN
		}
		if dn != "" {
			out = append(out, OU{DN: dn, Name: name})
		}
	}
	return out
}

// SearchComputers runs the computer query under baseDN (an OU or domain root)
// and parses the results. baseDN scopes the import to a site's OU subtree.
func SearchComputers(s Searcher, baseDN string) ([]Computer, error) {
	req := ldap.NewSearchRequest(
		baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		ComputerFilter, computerAttrs, nil,
	)
	res, err := s.Search(req)
	if err != nil {
		return nil, err
	}
	return ParseComputers(res.Entries), nil
}

// ParseComputers maps LDAP entries to Computer records (pure; the tested core).
func ParseComputers(entries []*ldap.Entry) []Computer {
	out := make([]Computer, 0, len(entries))
	for _, e := range entries {
		name := strings.TrimSuffix(e.GetAttributeValue("sAMAccountName"), "$")
		if name == "" {
			name = e.GetAttributeValue("cn")
		}
		if name == "" {
			continue
		}
		os := e.GetAttributeValue("operatingSystem")
		c := Computer{
			Name:        name,
			DNSHostName: strings.ToLower(e.GetAttributeValue("dNSHostName")),
			OS:          os,
			OSVersion:   e.GetAttributeValue("operatingSystemVersion"),
			Enabled:     enabledFromUAC(e.GetAttributeValue("userAccountControl")),
			Category:    classifyOS(os),
		}
		out = append(out, c)
	}
	return out
}

// classifyOS maps an AD operatingSystem string to a device category. "Server"
// in the OS name ⇒ server; anything else with a Windows/macOS/Linux OS ⇒ an
// endpoint; empty ⇒ endpoint (a domain-joined machine that hasn't reported).
func classifyOS(os string) domain.DeviceCategory {
	if strings.Contains(strings.ToLower(os), "server") {
		return domain.CatServer
	}
	return domain.CatEndpoint
}

// enabledFromUAC reports whether the account is enabled. userAccountControl
// bit 0x2 (ACCOUNTDISABLE) set ⇒ disabled. A missing/garbage value defaults to
// enabled (conservative: surface it rather than hide it).
func enabledFromUAC(uac string) bool {
	n := atoiSafe(uac)
	if n == 0 {
		return true
	}
	return n&0x2 == 0
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
