package reconcile

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/tetedu/tuitio-backend/internal/chain"
	"github.com/tetedu/tuitio-backend/internal/store"
)

// fakeChain is authoritative contract state for the test.
type fakeChain struct {
	next      uint64
	grants    map[uint64]chain.GrantState
	terms     map[[2]uint64]chain.TermState
	insts     map[string]chain.InstitutionState
	order     []string
	noList    bool
	listErr   error
	listCalls [][2]uint32
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

func (f *fakeChain) List(_ context.Context, start, limit uint32) ([]string, error) {
	f.listCalls = append(f.listCalls, [2]uint32{start, limit})
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.noList {
		return nil, &chain.ContractError{Detail: "missing function list"}
	}
	addrs := f.order
	if len(addrs) == 0 {
		addrs = make([]string, 0, len(f.insts))
		for addr := range f.insts {
			addrs = append(addrs, addr)
		}
		sort.Strings(addrs)
	}
	if start >= uint32(len(addrs)) || limit == 0 {
		return []string{}, nil
	}
	if limit > chain.MaxRegistryPage {
		limit = chain.MaxRegistryPage
	}
	end := start + limit
	if end > uint32(len(addrs)) {
		end = uint32(len(addrs))
	}
	return addrs[start:end], nil
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

// An institution registered in the contract that is absent from both the read
// model and every grant must still be discovered via Registry.List and inserted.
func TestDiscoversUnreferencedInstitutionViaList(t *testing.T) {
	ctx := context.Background()
	cf := chainFixture()
	const unreferenced = "GUNREFERENCEDSCHOOL"
	cf.insts[unreferenced] = chain.InstitutionState{
		Admin:        unreferenced,
		Payout:       unreferenced,
		Name:         "Makerere Institute",
		Country:      "UG",
		Status:       chain.InstitutionPending,
		RegisteredAt: 1200,
	}

	repo := newMemRepo()
	rep, err := Run(ctx, cf, cf, repo)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.InstitutionsChecked != 2 {
		t.Errorf("institutions checked = %d, want 2", rep.InstitutionsChecked)
	}
	if rep.InstitutionsRepaired != 2 {
		t.Errorf("institutions repaired = %d, want 2", rep.InstitutionsRepaired)
	}
	got, ok := repo.insts[unreferenced]
	if !ok {
		t.Fatalf("unreferenced institution %s was not inserted into read model", unreferenced)
	}
	if got.Name != "Makerere Institute" || got.Status != "pending" {
		t.Errorf("unexpected institution record: %+v", got)
	}
}

// Paging through the registry must respect MaxRegistryPage and stop as soon as
// a short page is returned.
func TestListPaginationStopsOnShortPage(t *testing.T) {
	ctx := context.Background()
	cf := &fakeChain{
		next:   0,
		grants: map[uint64]chain.GrantState{},
		terms:  map[[2]uint64]chain.TermState{},
		insts:  map[string]chain.InstitutionState{},
	}

	totalInsts := int(chain.MaxRegistryPage)*2 + 15
	for i := 0; i < totalInsts; i++ {
		addr := fmt.Sprintf("GSCHOOL_%03d", i)
		cf.order = append(cf.order, addr)
		cf.insts[addr] = chain.InstitutionState{
			Admin:        addr,
			Payout:       addr,
			Name:         fmt.Sprintf("School %d", i),
			Country:      "RW",
			Status:       chain.InstitutionVerified,
			RegisteredAt: uint64(1000 + i),
		}
	}

	repo := newMemRepo()
	rep, err := Run(ctx, cf, cf, repo)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.InstitutionsChecked != totalInsts {
		t.Errorf("checked = %d, want %d", rep.InstitutionsChecked, totalInsts)
	}
	if len(cf.listCalls) != 3 {
		t.Fatalf("listCalls = %v, want 3 pages", cf.listCalls)
	}
	wantCalls := [][2]uint32{
		{0, chain.MaxRegistryPage},
		{chain.MaxRegistryPage, chain.MaxRegistryPage},
		{chain.MaxRegistryPage * 2, chain.MaxRegistryPage},
	}
	for i, want := range wantCalls {
		if cf.listCalls[i] != want {
			t.Errorf("listCalls[%d] = %v, want %v", i, cf.listCalls[i], want)
		}
	}
}

// Registries deployed before list() existed trap on list(); the reconciler must
// fall back to the inferred institution address set from grants and read model.
func TestFallbackToInferredAddressesWhenListUnavailable(t *testing.T) {
	ctx := context.Background()
	cf := chainFixture()
	cf.noList = true

	repo := newMemRepo()
	rep, err := Run(ctx, cf, cf, repo)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.InstitutionsChecked != 1 || rep.InstitutionsRepaired != 1 {
		t.Errorf("fallback institution counts wrong: %+v", rep)
	}
	if _, ok := repo.insts[school]; !ok {
		t.Errorf("grant-referenced institution %s was not repaired via fallback", school)
	}
}

// A transient RPC failure on list() must abort the pass rather than silently
// falling back to a potentially incomplete inferred institution set.
func TestPropagatesTransientListRPCError(t *testing.T) {
	ctx := context.Background()
	cf := chainFixture()
	cf.listErr = fmt.Errorf("simulate list: connection reset by peer")

	if _, err := Run(ctx, cf, cf, newMemRepo()); err == nil {
		t.Fatal("expected transient RPC error on list() to abort Run")
	}
}
