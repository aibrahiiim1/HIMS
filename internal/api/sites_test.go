package api

import (
	"testing"

	"github.com/google/uuid"
)

func TestResolveSite(t *testing.T) {
	group := uuid.New()
	hotel := uuid.New()
	room := uuid.New()
	orphan := uuid.New()
	parent := map[uuid.UUID]uuid.UUID{hotel: group, room: hotel}
	kind := map[uuid.UUID]string{group: "group", hotel: "hotel", room: "room", orphan: "rack"}

	// A device in a room resolves up to its hotel.
	if got := resolveSite(room, parent, kind); got != hotel {
		t.Errorf("room → %v, want hotel %v", got, hotel)
	}
	// A device directly on a hotel resolves to itself.
	if got := resolveSite(hotel, parent, kind); got != hotel {
		t.Errorf("hotel → %v, want itself", got)
	}
	// The group node is itself a site.
	if got := resolveSite(group, parent, kind); got != group {
		t.Errorf("group → %v, want itself", got)
	}
	// A node with no site ancestor resolves to Nil.
	if got := resolveSite(orphan, parent, kind); got != uuid.Nil {
		t.Errorf("orphan → %v, want Nil", got)
	}
}

func TestResolveSiteCycleGuard(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	parent := map[uuid.UUID]uuid.UUID{a: b, b: a} // cycle, no site kinds
	kind := map[uuid.UUID]string{a: "rack", b: "room"}
	if got := resolveSite(a, parent, kind); got != uuid.Nil {
		t.Errorf("cycle should resolve to Nil, got %v", got)
	}
}
