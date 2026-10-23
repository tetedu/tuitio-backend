package store

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Pagination bounds shared by every list endpoint. The default keeps a single
// page cheap; the cap stops a hostile ?limit= value from turning one request
// into a full-table scan.
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// ParsePageLimit validates the ?limit= query parameter. An unset value falls
// back to DefaultPageLimit; a non-integer is rejected; out-of-range values are
// clamped into [1, MaxPageLimit] rather than erroring, so a typo'd limit never
// breaks the request.
func ParsePageLimit(raw string) (int, error) {
	if raw == "" {
		return DefaultPageLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid limit %q: must be an integer", raw)
	}
	if n < 1 {
		n = 1
	}
	if n > MaxPageLimit {
		n = MaxPageLimit
	}
	return n, nil
}

// ----- cursor (de)serialization ---------------------------------------------

// Cursors are base64url-encoded (no padding) so they are opaque to clients and
// safe to drop straight into a query string: no '+' '/' '=' characters that the
// URL parser would mangle. The payload inside is the page's sort-key tuple.

func encodeCursor(payload string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func decodeCursor(s string) (string, error) {
	payload, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("invalid cursor")
	}
	return string(payload), nil
}

func encodeCursorInt(v int64) string { return encodeCursor(strconv.FormatInt(v, 10)) }

func encodeInstitutionCursor(i Institution) string {
	return encodeCursor(fmt.Sprintf("%d.%s", i.RegisteredAt, i.Address))
}

// DecodeInstitutionCursor parses a cursor produced by encodeInstitutionCursor.
// The first return is registered_at, the second the institution address.
func DecodeInstitutionCursor(s string) (int64, string, error) {
	payload, err := decodeCursor(s)
	if err != nil {
		return 0, "", err
	}
	parts := strings.SplitN(payload, ".", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("invalid cursor")
	}
	reg, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("invalid cursor")
	}
	return reg, parts[1], nil
}

// DecodeGrantCursor parses a grant_id cursor.
func DecodeGrantCursor(s string) (int64, error) {
	payload, err := decodeCursor(s)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	gid, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	return gid, nil
}

func encodeActivityCursor(a Activity) string {
	return encodeCursor(fmt.Sprintf("%d|%s", a.Ledger, a.SeenAt.Format(time.RFC3339Nano)))
}

// DecodeActivityCursor parses an (ledger, seen_at) cursor.
func DecodeActivityCursor(s string) (int64, time.Time, error) {
	payload, err := decodeCursor(s)
	if err != nil {
		return 0, time.Time{}, err
	}
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return 0, time.Time{}, fmt.Errorf("invalid cursor")
	}
	ledger, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("invalid cursor")
	}
	seen, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("invalid cursor")
	}
	return ledger, seen, nil
}

// nextCursorFromOverflow returns a cursor for the last *returned* row when the
// page was fetched with one extra row to probe for a following page. The probe
// row (rows[limit]) is dropped — the cursor must point at the boundary the
// client actually saw, not the hidden lookahead, or a row gets skipped.
func nextCursorFromOverflow[T any](rows []T, limit int, encode func(T) string) (out []T, next string) {
	if len(rows) > limit {
		out = rows[:limit]
		next = encode(rows[limit-1])
	} else {
		out = rows
	}
	return out, next
}

// ----- InstitutionsPage ------------------------------------------------------

// InstitutionsPage returns up to limit institutions ordered by registration
// time (newest first), keyset-paginated on (registered_at, address). The cursor
// from a previous call pins the page start so the index stays fast as the
// institution set grows.
func (s *Store) InstitutionsPage(ctx context.Context, cursor string, limit int) ([]Institution, string, error) {
	args := []any{}
	query := `SELECT address, payout, name, country, status, registered_at FROM institutions`
	if cursor != "" {
		reg, addr, err := DecodeInstitutionCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		query += ` WHERE (registered_at, address) < ($1, $2)`
		args = append(args, reg, addr)
	}
	query += ` ORDER BY registered_at DESC, address DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Institution
	for rows.Next() {
		var i Institution
		if err := rows.Scan(&i.Address, &i.Payout, &i.Name, &i.Country, &i.Status, &i.RegisteredAt); err != nil {
			return nil, "", err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	items, next := nextCursorFromOverflow(out, limit, encodeInstitutionCursor)
	return items, next, nil
}

// ----- GrantsPage ------------------------------------------------------------

// GrantsPage returns up to limit grants, honoring the same sponsor / institution
// / status filters as Grants, keyset-paginated on grant_id (newest first).
func (s *Store) GrantsPage(ctx context.Context, sponsor, institution, status, cursor string, limit int) ([]Grant, string, error) {
	args := []any{sponsor, institution, status}
	query := `
		SELECT grant_id, sponsor, beneficiary, institution, token, term_amount, terms_total, next_term, status, created_at,
		       CASE WHEN status = 'active' THEN term_amount * (terms_total - next_term) ELSE 0 END AS locked_amount
		FROM grants
		WHERE ($1 = '' OR sponsor = $1)
		  AND ($2 = '' OR institution = $2)
		  AND ($3 = '' OR status = $3)`
	if cursor != "" {
		gid, err := DecodeGrantCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		query += ` AND grant_id < $4`
		args = append(args, gid)
	}
	query += ` ORDER BY grant_id DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.GrantID, &g.Sponsor, &g.Beneficiary, &g.Institution, &g.Token, &g.TermAmount, &g.TermsTotal, &g.NextTerm, &g.Status, &g.CreatedAt, &g.LockedAmount); err != nil {
			return nil, "", err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	items, next := nextCursorFromOverflow(out, limit, func(g Grant) string { return encodeCursorInt(g.GrantID) })
	return items, next, nil
}

// ----- ActivityPage ----------------------------------------------------------

// ActivityPage returns up to limit audit events keyset-paginated on
// (ledger, seen_at), newest first, replacing the older Activity helper so the
// feed no longer returns the whole table in one response.
func (s *Store) ActivityPage(ctx context.Context, cursor string, limit int) ([]Activity, string, error) {
	args := []any{}
	query := `SELECT ledger, tx_hash, contract, event, payload, seen_at FROM activity`
	if cursor != "" {
		ledger, seen, err := DecodeActivityCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		query += ` WHERE (ledger, seen_at) < ($1, $2)`
		args = append(args, ledger, seen)
	}
	query += ` ORDER BY ledger DESC, seen_at DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.Ledger, &a.TxHash, &a.Contract, &a.Event, &a.Payload, &a.SeenAt); err != nil {
			return nil, "", err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	items, next := nextCursorFromOverflow(out, limit, encodeActivityCursor)
	return items, next, nil
}
