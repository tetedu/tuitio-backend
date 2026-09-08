// Package store owns the Postgres read model: schema, event application, and
// the queries the REST API serves.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func Connect(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS indexer_state (
    id            INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    cursor_ledger BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS institutions (
    address       TEXT PRIMARY KEY,
    payout        TEXT NOT NULL,
    name          TEXT NOT NULL,
    country       TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    registered_at BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS grants (
    grant_id     BIGINT PRIMARY KEY,
    sponsor      TEXT NOT NULL,
    beneficiary  TEXT NOT NULL,
    institution  TEXT NOT NULL,
    token        TEXT NOT NULL,
    term_amount  BIGINT NOT NULL,
    terms_total  INT NOT NULL,
    next_term    INT NOT NULL DEFAULT 0,
    status       TEXT NOT NULL DEFAULT 'active',
    created_at   BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS terms (
    grant_id      BIGINT NOT NULL,
    term_index    INT NOT NULL,
    status        TEXT NOT NULL,
    attested_at   BIGINT NOT NULL DEFAULT 0,
    release_after BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (grant_id, term_index)
);

CREATE TABLE IF NOT EXISTS activity (
    event_key TEXT PRIMARY KEY,
    ledger    BIGINT NOT NULL,
    tx_hash   TEXT NOT NULL,
    contract  TEXT NOT NULL,
    event     TEXT NOT NULL,
    payload   JSONB NOT NULL,
    seen_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS activity_ledger_idx ON activity (ledger DESC);

INSERT INTO indexer_state (id, cursor_ledger) VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;
`

// ----- indexer state --------------------------------------------------------

func (s *Store) Cursor(ctx context.Context) (int64, error) {
	var cursor int64
	err := s.pool.QueryRow(ctx, `SELECT cursor_ledger FROM indexer_state WHERE id = 1`).Scan(&cursor)
	return cursor, err
}

func (s *Store) SetCursor(ctx context.Context, cursor int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE indexer_state SET cursor_ledger = $1 WHERE id = 1`, cursor)
	return err
}

// Apply runs one event batch in a transaction so the cursor and the state it
// reflects always commit together.
func (s *Store) Apply(ctx context.Context, events []Applied, cursor int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, e := range events {
		if err := applyOne(ctx, tx, e); err != nil {
			return fmt.Errorf("apply %s ledger %d: %w", e.Event, e.Ledger, err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE indexer_state SET cursor_ledger = $1 WHERE id = 1`, cursor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Applied is a decoded event plus its chain position.
type Applied struct {
	Event    string
	Ledger   int64
	TxHash   string
	Contract string
	Decoded  any
}

func applyOne(ctx context.Context, tx pgx.Tx, e Applied) error {
	if c, ok := e.Decoded.(Composite); ok {
		for _, w := range c.Writes {
			if err := applyOne(ctx, tx, Applied{Event: e.Event, Ledger: e.Ledger, TxHash: e.TxHash, Contract: e.Contract, Decoded: w}); err != nil {
				return err
			}
		}
		return nil
	}
	switch d := e.Decoded.(type) {
	case UpsertInstitution:
		_, err := tx.Exec(ctx, `
			INSERT INTO institutions (address, payout, name, country, status, registered_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (address) DO NOTHING`,
			d.Address, d.Payout, d.Name, d.Country, d.Status, d.RegisteredAt)
		return err
	case SetInstitutionStatus:
		_, err := tx.Exec(ctx, `UPDATE institutions SET status = $1 WHERE address = $2`, d.Status, d.Address)
		return err
	case SetPayout:
		_, err := tx.Exec(ctx, `UPDATE institutions SET payout = $1 WHERE address = $2`, d.Payout, d.Address)
		return err
	case UpsertGrant:
		_, err := tx.Exec(ctx, `
			INSERT INTO grants (grant_id, sponsor, beneficiary, institution, token, term_amount, terms_total, next_term, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (grant_id) DO NOTHING`,
			d.GrantID, d.Sponsor, d.Beneficiary, d.Institution, d.Token, d.TermAmount, d.TermsTotal, 0, "active", d.CreatedAt)
		return err
	case UpsertTerm:
		_, err := tx.Exec(ctx, `
			INSERT INTO terms (grant_id, term_index, status, attested_at, release_after)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (grant_id, term_index) DO UPDATE
			  SET status = EXCLUDED.status,
			      attested_at = GREATEST(terms.attested_at, EXCLUDED.attested_at),
			      release_after = GREATEST(terms.release_after, EXCLUDED.release_after)`,
			d.GrantID, d.TermIndex, d.Status, d.AttestedAt, d.ReleaseAfter)
		return err
	case AdvanceTerm:
		// next_term only ever moves forward, so replays cannot rewind state.
		_, err := tx.Exec(ctx, `
			UPDATE grants
			SET next_term = GREATEST(next_term, $1),
			    status = CASE WHEN GREATEST(next_term, $1) >= terms_total THEN $2 ELSE status END
			WHERE grant_id = $3`,
			d.NextTerm, d.StatusIfDone, d.GrantID)
		return err
	case SetGrantStatus:
		_, err := tx.Exec(ctx, `UPDATE grants SET status = $1 WHERE grant_id = $2`, d.Status, d.GrantID)
		return err
	}
	return nil
}

// Composite bundles several store writes that originate from one contract
// event and must commit together.
type Composite struct {
	Writes []any
}

// Typed write operations the indexer produces.
type UpsertInstitution struct {
	Address, Payout, Name, Country, Status string
	RegisteredAt                           int64
}

type SetInstitutionStatus struct {
	Address, Status string
}

type SetPayout struct {
	Address, Payout string
}

type UpsertGrant struct {
	GrantID                                  int64
	Sponsor, Beneficiary, Institution, Token string
	TermAmount                               int64
	TermsTotal                               int
	CreatedAt                                int64
}

type UpsertTerm struct {
	GrantID                  int64
	TermIndex                int
	Status                   string
	AttestedAt, ReleaseAfter int64
}

type AdvanceTerm struct {
	GrantID      int64
	NextTerm     int
	StatusIfDone string
}

type SetGrantStatus struct {
	GrantID int64
	Status  string
}

// RecordActivity writes an event to the audit feed, keyed by a hash of its
// identity so replays are no-ops.
func (s *Store) RecordActivity(ctx context.Context, ledger int64, txHash, contract, event string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%s|%s", ledger, txHash, contract, event, raw)))
	_, err = s.pool.Exec(ctx, `
		INSERT INTO activity (event_key, ledger, tx_hash, contract, event, payload)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (event_key) DO NOTHING`,
		hex.EncodeToString(key[:]), ledger, txHash, contract, event, raw)
	return err
}

// ----- read model types ------------------------------------------------------

type Institution struct {
	Address      string `json:"address"`
	Payout       string `json:"payout"`
	Name         string `json:"name"`
	Country      string `json:"country"`
	Status       string `json:"status"`
	RegisteredAt int64  `json:"registered_at"`
}

type Grant struct {
	GrantID      int64  `json:"grant_id"`
	Sponsor      string `json:"sponsor"`
	Beneficiary  string `json:"beneficiary"`
	Institution  string `json:"institution"`
	Token        string `json:"token"`
	TermAmount   int64  `json:"term_amount"`
	TermsTotal   int    `json:"terms_total"`
	NextTerm     int    `json:"next_term"`
	Status       string `json:"status"`
	CreatedAt    int64  `json:"created_at"`
	LockedAmount int64  `json:"locked_amount"`
}

type Term struct {
	GrantID      int64  `json:"grant_id"`
	TermIndex    int    `json:"term_index"`
	Status       string `json:"status"`
	AttestedAt   int64  `json:"attested_at"`
	ReleaseAfter int64  `json:"release_after"`
}

type Activity struct {
	Ledger   int64           `json:"ledger"`
	TxHash   string          `json:"tx_hash"`
	Contract string          `json:"contract"`
	Event    string          `json:"event"`
	Payload  json.RawMessage `json:"payload"`
	SeenAt   time.Time       `json:"seen_at"`
}

type Stats struct {
	InstitutionsTotal int `json:"institutions_total"`
	InstitutionsLive  int `json:"institutions_live"`
	GrantsTotal       int `json:"grants_total"`
	GrantsActive      int `json:"grants_active"`
	TermsReleased     int `json:"terms_released"`
	TermsDisputed     int `json:"terms_disputed"`
	TermsRefunded     int `json:"terms_refunded"`
}

// ----- queries ---------------------------------------------------------------

func (s *Store) Institutions(ctx context.Context) ([]Institution, error) {
	rows, err := s.pool.Query(ctx, `SELECT address, payout, name, country, status, registered_at FROM institutions ORDER BY registered_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Institution
	for rows.Next() {
		var i Institution
		if err := rows.Scan(&i.Address, &i.Payout, &i.Name, &i.Country, &i.Status, &i.RegisteredAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) Institution(ctx context.Context, address string) (Institution, error) {
	var i Institution
	err := s.pool.QueryRow(ctx,
		`SELECT address, payout, name, country, status, registered_at FROM institutions WHERE address = $1`, address,
	).Scan(&i.Address, &i.Payout, &i.Name, &i.Country, &i.Status, &i.RegisteredAt)
	return i, err
}

// Grants lists grants with optional sponsor / institution / status filters.
func (s *Store) Grants(ctx context.Context, sponsor, institution, status string) ([]Grant, error) {
	sql := `
		SELECT grant_id, sponsor, beneficiary, institution, token, term_amount, terms_total, next_term, status, created_at,
		       CASE WHEN status = 'active' THEN term_amount * (terms_total - next_term) ELSE 0 END AS locked_amount
		FROM grants
		WHERE ($1 = '' OR sponsor = $1)
		  AND ($2 = '' OR institution = $2)
		  AND ($3 = '' OR status = $3)
		ORDER BY grant_id DESC`
	rows, err := s.pool.Query(ctx, sql, sponsor, institution, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.GrantID, &g.Sponsor, &g.Beneficiary, &g.Institution, &g.Token, &g.TermAmount, &g.TermsTotal, &g.NextTerm, &g.Status, &g.CreatedAt, &g.LockedAmount); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) Grant(ctx context.Context, grantID int64) (Grant, error) {
	var g Grant
	err := s.pool.QueryRow(ctx, `
		SELECT grant_id, sponsor, beneficiary, institution, token, term_amount, terms_total, next_term, status, created_at,
		       CASE WHEN status = 'active' THEN term_amount * (terms_total - next_term) ELSE 0 END
		FROM grants WHERE grant_id = $1`, grantID,
	).Scan(&g.GrantID, &g.Sponsor, &g.Beneficiary, &g.Institution, &g.Token, &g.TermAmount, &g.TermsTotal, &g.NextTerm, &g.Status, &g.CreatedAt, &g.LockedAmount)
	return g, err
}

// Terms returns every term of a grant. Terms the contract never wrote default
// to pending, mirroring the contract's own storage semantics.
func (s *Store) Terms(ctx context.Context, grantID int64) ([]Term, error) {
	var total int
	var nextTerm int
	var status string
	if err := s.pool.QueryRow(ctx,
		`SELECT terms_total, next_term, status FROM grants WHERE grant_id = $1`, grantID,
	).Scan(&total, &nextTerm, &status); err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT grant_id, term_index, status, attested_at, release_after
		FROM terms WHERE grant_id = $1 ORDER BY term_index`, grantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byIndex := map[int]Term{}
	for rows.Next() {
		var t Term
		if err := rows.Scan(&t.GrantID, &t.TermIndex, &t.Status, &t.AttestedAt, &t.ReleaseAfter); err != nil {
			return nil, err
		}
		byIndex[t.TermIndex] = t
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]Term, 0, total)
	for i := 0; i < total; i++ {
		if t, ok := byIndex[i]; ok {
			out = append(out, t)
		} else {
			out = append(out, Term{GrantID: grantID, TermIndex: i, Status: "pending"})
		}
	}
	return out, nil
}

func (s *Store) Activity(ctx context.Context, limit int) ([]Activity, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT ledger, tx_hash, contract, event, payload, seen_at
		FROM activity ORDER BY ledger DESC, seen_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.Ledger, &a.TxHash, &a.Contract, &a.Event, &a.Payload, &a.SeenAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT COUNT(*) FROM institutions),
		  (SELECT COUNT(*) FROM institutions WHERE status = 'verified'),
		  (SELECT COUNT(*) FROM grants),
		  (SELECT COUNT(*) FROM grants WHERE status = 'active'),
		  (SELECT COUNT(*) FROM terms WHERE status = 'released'),
		  (SELECT COUNT(*) FROM terms WHERE status = 'disputed'),
		  (SELECT COUNT(*) FROM terms WHERE status = 'refunded')`,
	).Scan(&st.InstitutionsTotal, &st.InstitutionsLive, &st.GrantsTotal, &st.GrantsActive,
		&st.TermsReleased, &st.TermsDisputed, &st.TermsRefunded)
	return st, err
}
