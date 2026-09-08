package store

import (
	"context"
	"io"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// newTestStore boots a throwaway Postgres, giving the store layer a real
// database to run against in tests.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	cfg := embeddedpostgres.DefaultConfig().
		RuntimePath(dir).
		DataPath(dir + "/data").
		Username("tuitio").Password("tuitio").Database("tuitio").
		Port(9876).
		Logger(io.Discard)
	pg := embeddedpostgres.NewDatabase(cfg)
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	st, err := Connect(context.Background(), "postgres://tuitio:tuitio@localhost:9876/tuitio?sslmode=disable")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return st
}

func applyFixture(t *testing.T, st *Store) {
	t.Helper()
	events := []Applied{
		{Event: "institution_registered", Ledger: 100, TxHash: "tx1", Contract: "registry", Decoded: UpsertInstitution{
			Address: "GSCHOOL", Payout: "GSCHOOL", Name: "Kigali Technical College", Country: "RW", Status: "pending", RegisteredAt: 1000,
		}},
		{Event: "institution_verified", Ledger: 101, TxHash: "tx2", Contract: "registry", Decoded: SetInstitutionStatus{Address: "GSCHOOL", Status: "verified"}},
		{Event: "grant_created", Ledger: 102, TxHash: "tx3", Contract: "escrow", Decoded: UpsertGrant{
			GrantID: 0, Sponsor: "GSPONSOR", Beneficiary: "GSTUDENT", Institution: "GSCHOOL",
			Token: "CTOKEN", TermAmount: 500000000, TermsTotal: 3, CreatedAt: 2000,
		}},
		{Event: "grant_created", Ledger: 103, TxHash: "tx4", Contract: "escrow", Decoded: UpsertGrant{
			GrantID: 1, Sponsor: "GSPONSOR", Beneficiary: "GSTUDENT", Institution: "GSCHOOL",
			Token: "CTOKEN", TermAmount: 500000000, TermsTotal: 2, CreatedAt: 2100,
		}},
		{Event: "term_attested", Ledger: 104, TxHash: "tx5", Contract: "escrow", Decoded: UpsertTerm{
			GrantID: 0, TermIndex: 0, Status: "attested", AttestedAt: 3000, ReleaseAfter: 3600,
		}},
		{Event: "term_attested", Ledger: 105, TxHash: "tx6", Contract: "escrow", Decoded: UpsertTerm{
			GrantID: 1, TermIndex: 0, Status: "attested", AttestedAt: 3100, ReleaseAfter: 3700,
		}},
		{Event: "term_disputed", Ledger: 106, TxHash: "tx7", Contract: "escrow", Decoded: UpsertTerm{
			GrantID: 1, TermIndex: 0, Status: "disputed",
		}},
		{Event: "dispute_resolved", Ledger: 107, TxHash: "tx8", Contract: "escrow", Decoded: Composite{Writes: []any{
			UpsertTerm{GrantID: 1, TermIndex: 0, Status: "released"},
			AdvanceTerm{GrantID: 1, NextTerm: 1, StatusIfDone: "completed"},
		}}},
	}
	if err := st.Apply(context.Background(), events, 107); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

func findGrant(t *testing.T, st *Store, id int64) Grant {
	t.Helper()
	g, err := st.Grant(context.Background(), id)
	if err != nil {
		t.Fatalf("grant %d: %v", id, err)
	}
	return g
}

func TestApplyBuildsReadModel(t *testing.T) {
	st := newTestStore(t)
	applyFixture(t, st)
	ctx := context.Background()

	insts, err := st.Institutions(ctx)
	if err != nil || len(insts) != 1 {
		t.Fatalf("institutions: %v %v", insts, err)
	}
	if insts[0].Status != "verified" {
		t.Errorf("institution status = %q", insts[0].Status)
	}

	grants, err := st.Grants(ctx, "", "", "")
	if err != nil || len(grants) != 2 {
		t.Fatalf("grants: %v %v", grants, err)
	}
	g1 := findGrant(t, st, 1)
	if g1.NextTerm != 1 {
		t.Errorf("grant 1 next_term = %d, want 1", g1.NextTerm)
	}
	if g1.LockedAmount != 500000000 {
		t.Errorf("grant 1 locked = %d, want 500000000", g1.LockedAmount)
	}

	terms, err := st.Terms(ctx, 1)
	if err != nil {
		t.Fatalf("terms: %v", err)
	}
	if len(terms) != 2 {
		t.Fatalf("grant 1 terms = %d, want 2", len(terms))
	}
	if terms[0].Status != "released" || terms[1].Status != "pending" {
		t.Errorf("term statuses = %q, %q", terms[0].Status, terms[1].Status)
	}

	stats, err := st.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.TermsReleased != 1 || stats.InstitutionsLive != 1 {
		t.Errorf("stats = %+v", stats)
	}

	cursor, err := st.Cursor(ctx)
	if err != nil || cursor != 107 {
		t.Errorf("cursor = %d err=%v, want 107", cursor, err)
	}
}

func TestApplyIsIdempotentOnReplay(t *testing.T) {
	st := newTestStore(t)
	applyFixture(t, st)
	// Replay the whole batch, as would happen after a crash mid-apply.
	applyFixture(t, st)
	ctx := context.Background()

	grants, err := st.Grants(ctx, "", "", "")
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	if len(grants) != 2 {
		t.Fatalf("replay duplicated grants: %d", len(grants))
	}
	if g := findGrant(t, st, 1); g.NextTerm != 1 {
		t.Errorf("replay advanced next_term twice: %d", g.NextTerm)
	}
	terms, err := st.Terms(ctx, 1)
	if err != nil || len(terms) != 2 {
		t.Fatalf("replay duplicated terms: %v", err)
	}
}

func TestGrantFilters(t *testing.T) {
	st := newTestStore(t)
	applyFixture(t, st)
	ctx := context.Background()

	bySponsor, err := st.Grants(ctx, "GSPONSOR", "", "")
	if err != nil || len(bySponsor) != 2 {
		t.Fatalf("sponsor filter: %v %v", bySponsor, err)
	}
	byWrong, err := st.Grants(ctx, "GNOBODY", "", "")
	if err != nil || len(byWrong) != 0 {
		t.Fatalf("unknown sponsor should be empty: %v %v", byWrong, err)
	}
	_, err = st.Grant(ctx, 99)
	if err == nil {
		t.Fatal("missing grant should error")
	}
}
