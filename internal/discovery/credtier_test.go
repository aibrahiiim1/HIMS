package discovery

import (
	"testing"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/domain"
)

// flattenGroups turns an explicit operator credential selection (ExtraGroups)
// into the resolver's Exclusive set, so a per-scan selection overrides any
// standing subnet assignment. It must preserve order and de-duplicate a
// credential that appears in more than one group.
func TestFlattenGroups(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	c := uuid.New()
	groups := []credresolver.ScopedGroup{
		{Specificity: 100, Members: []credresolver.CredRef{
			{ID: a, Kind: domain.CredONVIF},
			{ID: b, Kind: domain.CredHTTPBasic},
		}},
		{Specificity: 100, Members: []credresolver.CredRef{
			{ID: b, Kind: domain.CredHTTPBasic}, // duplicate of group-1 member
			{ID: c, Kind: domain.CredSNMPv2c},
		}},
	}

	got := flattenGroups(groups)
	if len(got) != 3 {
		t.Fatalf("want 3 de-duplicated creds, got %d: %+v", len(got), got)
	}
	want := []uuid.UUID{a, b, c}
	for i, w := range want {
		if got[i].ID != w {
			t.Errorf("position %d: want %s, got %s (order must be preserved)", i, w, got[i].ID)
		}
	}

	if flattenGroups(nil) != nil {
		t.Errorf("nil groups should flatten to nil, not an empty non-nil slice")
	}
}
