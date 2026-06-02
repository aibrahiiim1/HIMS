package postgres

import (
	"context"
	"net/netip"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// CredentialCandidates assembles the resolver input for a device IP +
// location: it runs the scope-resolving query and folds the rows into
// credresolver.ScopedGroup buckets (one per specificity tier). The pure
// ordering then happens in credresolver.Resolve — keeping all DB concerns
// here and all policy there.
func (s *Store) CredentialCandidates(ctx context.Context, ip netip.Addr, locationID *uuid.UUID) ([]credresolver.ScopedGroup, error) {
	rows, err := s.q.ResolveCandidatesForIP(ctx, db.ResolveCandidatesForIPParams{
		Column1:    ip,
		LocationID: locationID,
	})
	if err != nil {
		return nil, mapErr("credential_candidates", err)
	}
	// Bucket by specificity so each tier is one ScopedGroup.
	bySpec := map[int][]credresolver.CredRef{}
	for _, r := range rows {
		bySpec[int(r.Specificity)] = append(bySpec[int(r.Specificity)], credresolver.CredRef{
			ID:       r.ID,
			Kind:     domain.CredentialKind(r.Kind),
			Priority: int(r.Priority),
			Weak:     r.Weak,
		})
	}
	out := make([]credresolver.ScopedGroup, 0, len(bySpec))
	for spec, members := range bySpec {
		out = append(out, credresolver.ScopedGroup{Specificity: spec, Members: members})
	}
	return out, nil
}
