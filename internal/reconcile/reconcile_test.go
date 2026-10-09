package reconcile

import (
	"context"
	"testing"

	"github.com/tetedu/tuitio-backend/internal/chain"
	"github.com/tetedu/tuitio-backend/internal/store"
)

// fakeChain is authoritative contract state for the test.
type fakeChain struct {
	next   uint64
	grants map[uint64]chain.GrantState
	terms  map[[2]uint64]chain.TermState
	insts  map[string]chain.InstitutionState
}

func (f *fakeChain) NextGrantID(context.Context) (uint64, error) { return f.next, nil }

func (f *fakeChain) Grant(_ context.Context, id uint64) (chain.GrantState, error) {
	g, ok := f.grants[id]
	if !ok {
		return g, chain.ErrNotFound
	}
	return g, nil
}

func (f *fakeChain) Term(_ context.Context, id uint64, i uint32) (chain.TermState, error) {
	t, ok := f.terms[[2]uint64{id, uint64(i)}]
	if !ok {
		// Unwritten terms read as pending, mirroring the contract.
		return chain.TermState{Status: chain.TermPending}, nil
	}
	return t, nil
}

func (f *fakeChain) Institution(_ context.Context, addr string) (chain.InstitutionState, error) {
	i, ok := f.insts[addr]
	if !ok {
		return i, chain.ErrNotFound
	}
	return i, nil
}

// memRepo is an in-memory read model recording every repair applied.
type memRepo struct {
	grants   map[int64]store.Grant
	terms    map[[2]int64]store.Term
	insts    map[string]store.Institution
	repaired []string
}

func newMemRepo() *memRepo {
	return &memRepo{
		grants: map[int64]store.Grant{},
		terms:  map[[2]int64]store.Term{},
		insts:  map[string]store.Institution{},
	}
}

func (m *memRepo) Grant(_ context.Context, id int64) (store.Grant, error) {
	g, ok := m.grants[id]
	if !ok {
		return g, errNotPresent
	}
	return g, nil
}

func (m *memRepo) Terms(_ context.Context, id int64) ([]store.Term, error) {
	g, ok := m.grants[id]
	if !ok {
		return nil, errNotPresent
	}
	out := make([]store.Term, 0, g.TermsTotal)
	for i := 0; i < g.TermsTotal; i++ {
		if t, ok := m.terms[[2]int64{id, int64(i)}]; ok {
			out = append(out, t)
		} else {
			out = append(out, store.Term{GrantID: id, TermIndex: i, Status: "pending"})
		}
	}
	return out, nil
}

func (m *memRepo) Institutions(context.Context) ([]store.Institution, error) {
	out := make([]store.Institution, 0, len(m.insts))
	for _, i := range m.insts {
		out = append(out, i)
	}
	return out, nil
}

func (m *memRepo) Institution(_ context.Context, addr string) (store.Institution, error) {
	i, ok := m.insts[addr]
	if !ok {
		return i, errNotPresent
	}
	return i, nil
}

func (m *memRepo) ReconcileGrant(_ context.Context, g store.Grant) error {
	m.grants[g.GrantID] = g
	m.repaired = append(m.repaired, "grant")
	return nil
}

func (m *memRepo) ReconcileTerm(_ context.Context, t store.Term) error {
	m.terms[[2]int64{t.GrantID, int64(t.TermIndex)}] = t
	m.repaired = append(m.repaired, "term")
	return nil
}

func (m *memRepo) ReconcileInstitution(_ context.Context, i store.Institution) error {
	m.insts[i.Address] = i
	m.repaired = append(m.repaired, "institution")
	return nil
}

type notPresent struct{}

func (notPresent) Error() string { return "not present" }

var errNotPresent = notPresent{}

const (
	sponsor = "GSPONSOR"
	student = "GSTUDENT"
	school  = "GSCHOOL"
	token   = "CTOKEN"
)

func chainFixture() *fakeChain {
	return &fakeChain{
		next: 1,
		grants: map[uint64]chain.GrantState{
			0: {
				Sponsor: sponsor, Beneficiary: student, Institution: school, Token: token,
				TermAmount: 500000000, TermsTotal: 3, NextTerm: 1,
				Status: chain.GrantActive, CreatedAt: 1000,
			},
		},
		terms: map[[2]uint64]chain.TermState{
			{0, 0}: {Status: chain.TermReleased},
		},
		insts: map[string]chain.InstitutionState{
			school: {
				Admin: school, Payout: school, Name: "Kigali Technical College",
				Country: "RW", Status: chain.InstitutionVerified, RegisteredAt: 900,
			},
		},
	}
}

// An empty read model — the state after the indexer had to skip history — must
// be rebuilt entirely from contract state.
func TestRebuildsEmptyReadModel(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepo()

	rep, err := Run(ctx, chainFixture(), chainFixture(), repo)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.GrantsRepaired != 1 {
		t.Errorf("grants repaired = %d, want 1", rep.GrantsRepaired)
	}
	if rep.InstitutionsRepaired != 1 {
		t.Errorf("institutions repaired = %d, want 1", rep.InstitutionsRepaired)
	}
	if rep.TermsRepaired != 1 {
		t.Errorf("terms repaired = %d, want 1 (only the released term differs)", rep.TermsRepaired)
	}
	if rep.Clean() {
		t.Error("report claimed clean despite repairs")
	}

	g := repo.grants[0]
	if g.NextTerm != 1 || g.Status != "active" || g.TermsTotal != 3 {
		t.Errorf("rebuilt grant wrong: %+v", g)
	}
	if got := repo.terms[[2]int64{0, 0}].Status; got != "released" {
		t.Errorf("term 0 status = %q, want released", got)
	}
	if repo.insts[school].Status != "verified" {
		t.Errorf("institution status = %q", repo.insts[school].Status)
	}
}

// A read model that already agrees with the chain must not be written to.
func TestCleanReadModelIsLeftAlone(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepo()

	// First pass rebuilds it.
	if _, err := Run(ctx, chainFixture(), chainFixture(), repo); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	repo.repaired = nil

	rep, err := Run(ctx, chainFixture(), chainFixture(), repo)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !rep.Clean() {
		t.Errorf("second pass reported repairs: %s / drift %v", rep, rep.Drift)
	}
	if len(repo.repaired) != 0 {
		t.Errorf("second pass wrote %v", repo.repaired)
	}
	if rep.GrantsChecked != 1 || rep.TermsChecked != 3 || rep.InstitutionsChecked != 1 {
		t.Errorf("checked counts wrong: %+v", rep)
	}
}

// The specific drift a missed event causes: the chain advanced a term but the
// read model still shows the old status.
func TestRepairsStaleTermStatus(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepo()
	if _, err := Run(ctx, chainFixture(), chainFixture(), repo); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Simulate a missed term_released event: the chain released term 1 and
	// advanced, the read model never saw it.
	cf := chainFixture()
	g := cf.grants[0]
	g.NextTerm = 2
	cf.grants[0] = g
	cf.terms[[2]uint64{0, 1}] = chain.TermState{Status: chain.TermReleased}

	repo.repaired = nil
	rep, err := Run(ctx, cf, cf, repo)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.GrantsRepaired != 1 {
		t.Errorf("grant not repaired: %+v", rep)
	}
	if rep.TermsRepaired != 1 {
		t.Errorf("term not repaired: %+v", rep)
	}
	if repo.grants[0].NextTerm != 2 {
		t.Errorf("next_term = %d, want 2", repo.grants[0].NextTerm)
	}
	if repo.terms[[2]int64{0, 1}].Status != "released" {
		t.Errorf("term 1 = %q, want released", repo.terms[[2]int64{0, 1}].Status)
	}
	if len(rep.Drift) == 0 {
		t.Error("drift not described")
	}
}

// A grant id the contract allocated but whose entry is gone must be reported,
// not crash the pass.
func TestMissingContractEntryIsReported(t *testing.T) {
	ctx := context.Background()
	cf := chainFixture()
	cf.next = 2 // id 1 was allocated but has no entry
	rep, err := Run(ctx, cf, cf, newMemRepo())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.GrantsChecked != 1 {
		t.Errorf("checked = %d, want 1", rep.GrantsChecked)
	}
	found := false
	for _, d := range rep.Drift {
		if d == "grant 1 missing from contract state" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing entry not reported: %v", rep.Drift)
	}
}
