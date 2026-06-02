// Package credresolver turns "where a device lives + what it is" into an
// ordered list of credentials to try — so the operator never picks a
// credential per scan or per device (a hard lesson from NIMS). The ordering
// logic is pure and unit-tested here; the DB lookups that assemble the
// inputs (subnets containing the IP → bindings → groups → members) live in
// the storage layer and feed this function.
package credresolver

import (
	"sort"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/domain"
)

// Fingerprint expresses which credential kinds are worth trying for a host,
// derived from light discovery (open ports / banners). The resolver filters
// candidates to the kinds a host can actually speak.
type Fingerprint struct {
	SNMP  bool // 161 open → snmp_v2c / snmp_v3
	SSH   bool // 22 open → ssh
	WinRM bool // 5985/5986 open → winrm
	HTTP  bool // 80/443/8000 open → http_basic / onvif / vendor_api
	LDAP  bool // 389/636 open → ldap
}

// Allows reports whether a credential kind is viable for this fingerprint.
func (f Fingerprint) Allows(k domain.CredentialKind) bool {
	switch k {
	case domain.CredSNMPv2c, domain.CredSNMPv3:
		return f.SNMP
	case domain.CredSSH:
		return f.SSH
	case domain.CredWinRM:
		return f.WinRM
	case domain.CredHTTPBasic, domain.CredONVIF, domain.CredVendorAPI:
		return f.HTTP
	case domain.CredLDAP:
		return f.LDAP
	}
	return false
}

// Specificity ranks how narrowly a binding is scoped. A subnet binding is
// more specific than a location binding and outranks it.
const (
	SpecLocation = 1
	SpecSubnet   = 2
)

// CredRef is one candidate credential as seen by the resolver.
type CredRef struct {
	ID       uuid.UUID
	Kind     domain.CredentialKind
	Priority int  // group-member try-order (lower first)
	Weak     bool // default/guessable (e.g. SNMP "public")
}

// ScopedGroup is a credential group in scope for the device, tagged with
// the specificity of the binding that brought it in.
type ScopedGroup struct {
	Specificity int
	Members     []CredRef
}

// Input is everything the resolver needs, pre-loaded by the storage layer.
type Input struct {
	Fingerprint Fingerprint
	// BoundCredentialID is the credential that last authenticated this
	// device (bind-on-success); tried first when still viable.
	BoundCredentialID *uuid.UUID
	Groups            []ScopedGroup
}

// Resolve returns the ordered, de-duplicated candidate list to try.
// Ordering: (1) the bound credential, (2) non-weak before weak, (3) more
// specific scope first, (4) lower group priority first, (5) stable by id.
// Candidates whose kind the fingerprint can't speak are dropped — so an
// SSH-only host never gets an SNMP community thrown at it, and a weak
// "public" community is never tried first unless it's the proven binding.
func Resolve(in Input) []CredRef {
	type cand struct {
		ref     CredRef
		spec    int
		isBound bool
	}
	best := map[uuid.UUID]*cand{}
	for _, g := range in.Groups {
		for _, m := range g.Members {
			if !in.Fingerprint.Allows(m.Kind) {
				continue
			}
			isBound := in.BoundCredentialID != nil && *in.BoundCredentialID == m.ID
			c := &cand{ref: m, spec: g.Specificity, isBound: isBound}
			// De-dup: a credential bound at multiple scopes keeps its most
			// specific (and bound-flagged) appearance.
			if prev, ok := best[m.ID]; ok {
				if c.spec > prev.spec {
					prev.spec = c.spec
				}
				if c.isBound {
					prev.isBound = true
				}
				if c.ref.Priority < prev.ref.Priority {
					prev.ref.Priority = c.ref.Priority
				}
				continue
			}
			best[m.ID] = c
		}
	}

	cands := make([]*cand, 0, len(best))
	for _, c := range best {
		cands = append(cands, c)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.isBound != b.isBound {
			return a.isBound // bound first
		}
		if a.ref.Weak != b.ref.Weak {
			return !a.ref.Weak // non-weak before weak
		}
		if a.spec != b.spec {
			return a.spec > b.spec // more specific first
		}
		if a.ref.Priority != b.ref.Priority {
			return a.ref.Priority < b.ref.Priority // lower priority first
		}
		return a.ref.ID.String() < b.ref.ID.String() // stable
	})

	out := make([]CredRef, len(cands))
	for i, c := range cands {
		out[i] = c.ref
	}
	return out
}
