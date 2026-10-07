// Package reconcile checks the indexed read model against authoritative
// contract state and repairs any drift.
//
// The indexer builds its view purely from events. That is correct as long as
// it sees every event, but it can miss history: the Soroban RPC only retains
// a limited window, so a service that was stopped long enough has to skip
// forward, leaving a hole. Reconciliation closes that hole by reading the
// contracts directly.
package reconcile

import (
	"context"
	"errors"
	"fmt"

	"github.com/tetedu/tuitio-backend/internal/chain"
	"github.com/tetedu/tuitio-backend/internal/store"
)

// EscrowSource reads escrow state from the chain.
type EscrowSource interface {
	NextGrantID(ctx context.Context) (uint64, error)
	Grant(ctx context.Context, id uint64) (chain.GrantState, error)
	Term(ctx context.Context, grantID uint64, index uint32) (chain.TermState, error)
}

// RegistrySource reads registry state from the chain.
type RegistrySource interface {
	Institution(ctx context.Context, address string) (chain.InstitutionState, error)
}

// Repo is the read model being checked and repaired.
type Repo interface {
	Grant(ctx context.Context, grantID int64) (store.Grant, error)
	Terms(ctx context.Context, grantID int64) ([]store.Term, error)
	Institutions(ctx context.Context) ([]store.Institution, error)
	Institution(ctx context.Context, address string) (store.Institution, error)
	ReconcileGrant(ctx context.Context, g store.Grant) error
	ReconcileTerm(ctx context.Context, t store.Term) error
	ReconcileInstitution(ctx context.Context, i store.Institution) error
}

// Report summarises what a pass found.
type Report struct {
	GrantsChecked        int      `json:"grants_checked"`
	GrantsRepaired       int      `json:"grants_repaired"`
	TermsChecked         int      `json:"terms_checked"`
	TermsRepaired        int      `json:"terms_repaired"`
	InstitutionsChecked  int      `json:"institutions_checked"`
	InstitutionsRepaired int      `json:"institutions_repaired"`
	Drift                []string `json:"drift"`
}

// Clean reports whether the read model already matched the chain.
func (r Report) Clean() bool {
	return r.GrantsRepaired == 0 && r.TermsRepaired == 0 && r.InstitutionsRepaired == 0
}

func (r Report) String() string {
	return fmt.Sprintf(
		"grants %d/%d repaired, terms %d/%d repaired, institutions %d/%d repaired",
		r.GrantsRepaired, r.GrantsChecked,
		r.TermsRepaired, r.TermsChecked,
		r.InstitutionsRepaired, r.InstitutionsChecked)
}

// Run walks every grant the escrow has issued, compares it and its terms with
// the read model, and overwrites any row that disagrees. Institutions are
// reconciled for every address the read model or a grant references, since the
// registry cannot be enumerated on chain yet.
func Run(ctx context.Context, esc EscrowSource, reg RegistrySource, repo Repo) (Report, error) {
	var rep Report

	total, err := esc.NextGrantID(ctx)
	if err != nil {
		return rep, fmt.Errorf("read next_grant_id: %w", err)
	}

	institutions := map[string]struct{}{}

	for id := uint64(0); id < total; id++ {
		onChain, err := esc.Grant(ctx, id)
		if err != nil {
			if errors.Is(err, chain.ErrNotFound) {
				// The id was allocated but the entry is gone (archived state).
				rep.Drift = append(rep.Drift, fmt.Sprintf("grant %d missing from contract state", id))
				continue
			}
			return rep, fmt.Errorf("read grant %d: %w", id, err)
		}
		rep.GrantsChecked++
		institutions[onChain.Institution] = struct{}{}

		want := store.Grant{
			GrantID:     int64(id),
			Sponsor:     onChain.Sponsor,
			Beneficiary: onChain.Beneficiary,
			Institution: onChain.Institution,
			Token:       onChain.Token,
			TermAmount:  onChain.TermAmount,
			TermsTotal:  int(onChain.TermsTotal),
			NextTerm:    int(onChain.NextTerm),
			Status:      chain.GrantStatusName(onChain.Status),
			CreatedAt:   int64(onChain.CreatedAt),
		}

		got, err := repo.Grant(ctx, int64(id))
		switch {
		case err != nil:
			rep.Drift = append(rep.Drift, fmt.Sprintf("grant %d absent from read model", id))
			if err := repo.ReconcileGrant(ctx, want); err != nil {
				return rep, fmt.Errorf("repair grant %d: %w", id, err)
			}
			rep.GrantsRepaired++
		case !grantEqual(got, want):
			rep.Drift = append(rep.Drift, describeGrantDrift(got, want))
			if err := repo.ReconcileGrant(ctx, want); err != nil {
				return rep, fmt.Errorf("repair grant %d: %w", id, err)
			}
			rep.GrantsRepaired++
		}

		// Terms are only meaningful once the grant row exists.
		dbTerms, err := repo.Terms(ctx, int64(id))
		if err != nil {
			return rep, fmt.Errorf("read terms for grant %d: %w", id, err)
		}
		byIndex := make(map[int]store.Term, len(dbTerms))
		for _, t := range dbTerms {
			byIndex[t.TermIndex] = t
		}

		for i := uint32(0); i < onChain.TermsTotal; i++ {
			chainTerm, err := esc.Term(ctx, id, i)
			if err != nil {
				if errors.Is(err, chain.ErrNotFound) {
					continue
				}
				return rep, fmt.Errorf("read grant %d term %d: %w", id, i, err)
			}
			rep.TermsChecked++

			wantTerm := store.Term{
				GrantID:      int64(id),
				TermIndex:    int(i),
				Status:       chain.TermStatusName(chainTerm.Status),
				AttestedAt:   int64(chainTerm.AttestedAt),
				ReleaseAfter: int64(chainTerm.ReleaseAfter),
			}
			gotTerm, present := byIndex[int(i)]
			if present && termEqual(gotTerm, wantTerm) {
				continue
			}
			rep.Drift = append(rep.Drift, fmt.Sprintf(
				"grant %d term %d: read model %q, chain %q",
				id, i, statusOf(gotTerm, present), wantTerm.Status))
			if err := repo.ReconcileTerm(ctx, wantTerm); err != nil {
				return rep, fmt.Errorf("repair grant %d term %d: %w", id, i, err)
			}
			rep.TermsRepaired++
		}
	}

	// Everything the read model already knows about, plus every institution a
	// grant points at.
	known, err := repo.Institutions(ctx)
	if err != nil {
		return rep, fmt.Errorf("read institutions: %w", err)
	}
	for _, i := range known {
		institutions[i.Address] = struct{}{}
	}

	for addr := range institutions {
		onChain, err := reg.Institution(ctx, addr)
		if err != nil {
			if errors.Is(err, chain.ErrNotFound) {
				rep.Drift = append(rep.Drift, fmt.Sprintf("institution %s not in registry", addr))
				continue
			}
			return rep, fmt.Errorf("read institution %s: %w", addr, err)
		}
		rep.InstitutionsChecked++

		want := store.Institution{
			Address:      addr,
			Payout:       onChain.Payout,
			Name:         onChain.Name,
			Country:      onChain.Country,
			Status:       chain.InstitutionStatusName(onChain.Status),
			RegisteredAt: int64(onChain.RegisteredAt),
		}
		got, err := repo.Institution(ctx, addr)
		if err != nil || !institutionEqual(got, want) {
			if err != nil {
				rep.Drift = append(rep.Drift, fmt.Sprintf("institution %s absent from read model", addr))
			} else {
				rep.Drift = append(rep.Drift, fmt.Sprintf(
					"institution %s: read model %q, chain %q", addr, got.Status, want.Status))
			}
			if err := repo.ReconcileInstitution(ctx, want); err != nil {
				return rep, fmt.Errorf("repair institution %s: %w", addr, err)
			}
			rep.InstitutionsRepaired++
		}
	}

	return rep, nil
}

func statusOf(t store.Term, present bool) string {
	if !present {
		return "absent"
	}
	return t.Status
}

// LockedAmount is derived, so it is excluded from the comparison.
func grantEqual(a, b store.Grant) bool {
	return a.GrantID == b.GrantID &&
		a.Sponsor == b.Sponsor &&
		a.Beneficiary == b.Beneficiary &&
		a.Institution == b.Institution &&
		a.Token == b.Token &&
		a.TermAmount == b.TermAmount &&
		a.TermsTotal == b.TermsTotal &&
		a.NextTerm == b.NextTerm &&
		a.Status == b.Status &&
		a.CreatedAt == b.CreatedAt
}

func termEqual(a, b store.Term) bool {
	return a.GrantID == b.GrantID &&
		a.TermIndex == b.TermIndex &&
		a.Status == b.Status &&
		a.AttestedAt == b.AttestedAt &&
		a.ReleaseAfter == b.ReleaseAfter
}

func institutionEqual(a, b store.Institution) bool {
	return a.Address == b.Address &&
		a.Payout == b.Payout &&
		a.Name == b.Name &&
		a.Country == b.Country &&
		a.Status == b.Status &&
		a.RegisteredAt == b.RegisteredAt
}

func describeGrantDrift(got, want store.Grant) string {
	switch {
	case got.Status != want.Status:
		return fmt.Sprintf("grant %d: read model status %q, chain %q", want.GrantID, got.Status, want.Status)
	case got.NextTerm != want.NextTerm:
		return fmt.Sprintf("grant %d: read model next_term %d, chain %d", want.GrantID, got.NextTerm, want.NextTerm)
	default:
		return fmt.Sprintf("grant %d: field mismatch with contract state", want.GrantID)
	}
}
