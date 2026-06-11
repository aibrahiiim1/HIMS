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

// SubnetScopedCredentials returns the EXCLUSIVE credential set for the IP's
// site subnet when that subnet has assigned credentials, plus a human label
// ("CCTV Cameras 172.21.210.0/24") for reporting. An empty slice means the IP
// is in no scoped subnet → the caller falls back to normal resolution. The
// returned CredRefs carry no group priority/weak flag (the operator chose them
// explicitly), so they sort by kind then id within the exclusive tier.
func (s *Store) SubnetScopedCredentials(ctx context.Context, ip netip.Addr, locationID *uuid.UUID) ([]credresolver.CredRef, string, error) {
	rows, err := s.q.SubnetScopedCredentialsForIP(ctx, db.SubnetScopedCredentialsForIPParams{
		LocationID: locationID,
		Ip:         ip,
	})
	if err != nil {
		return nil, "", mapErr("subnet_scoped_credentials", err)
	}
	if len(rows) == 0 {
		return nil, "", nil
	}
	creds := make([]credresolver.CredRef, 0, len(rows))
	for _, r := range rows {
		creds = append(creds, credresolver.CredRef{
			ID:   r.ID,
			Kind: domain.CredentialKind(r.Kind),
		})
	}
	label := rows[0].Cidr
	if rows[0].SubnetName != nil && *rows[0].SubnetName != "" {
		label = *rows[0].SubnetName + " " + rows[0].Cidr
	}
	return creds, label, nil
}
