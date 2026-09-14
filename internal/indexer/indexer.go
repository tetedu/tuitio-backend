package indexer

import (
	"context"
	"log"
	"time"

	"github.com/tetedu/tuitio-backend/internal/rpc"
	"github.com/tetedu/tuitio-backend/internal/store"
)

// Indexer polls the Soroban RPC for contract events and folds them into the
// read model. It is crash-safe: the cursor and the events it covers commit in
// one transaction, and every write is idempotent, so a replay after a crash
// converges to the same state.
type Indexer struct {
	rpc         *rpc.Client
	store       *store.Store
	contracts   []string
	poll        time.Duration
	startLedger uint32
}

func New(client *rpc.Client, st *store.Store, contracts []string, poll time.Duration, startLedger uint32) *Indexer {
	return &Indexer{rpc: client, store: st, contracts: contracts, poll: poll, startLedger: startLedger}
}

// Run blocks until the context is cancelled.
func (ix *Indexer) Run(ctx context.Context) {
	ix.initCursor(ctx)
	ticker := time.NewTicker(ix.poll)
	defer ticker.Stop()
	for {
		ix.pollOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// initCursor sets the starting ledger on first boot. With START_LEDGER=0 the
// indexer begins at the chain tip and skips history; set it below the first
// tracked event to backfill.
func (ix *Indexer) initCursor(ctx context.Context) {
	cursor, err := ix.store.Cursor(ctx)
	if err != nil {
		log.Fatalf("read cursor: %v", err)
	}
	if cursor > 0 {
		return
	}
	if ix.startLedger > 0 {
		if err := ix.store.SetCursor(ctx, int64(ix.startLedger)); err != nil {
			log.Fatalf("set cursor: %v", err)
		}
		log.Printf("indexer: starting from configured ledger %d", ix.startLedger)
		return
	}
	latest, err := ix.rpc.LatestLedger(ctx)
	if err != nil {
		log.Fatalf("latest ledger: %v", err)
	}
	// Start one ledger back so the first poll has a range to read.
	if err := ix.store.SetCursor(ctx, int64(latest)-1); err != nil {
		log.Fatalf("set cursor: %v", err)
	}
	log.Printf("indexer: no history requested, starting at chain tip %d", latest)
}

// The RPC limits how far back getEvents can read. Older ranges return an
// error, which we treat as unrecoverable for that window and skip forward.
const maxSpan = 10_000

func (ix *Indexer) pollOnce(ctx context.Context) {
	for {
		cursor, err := ix.store.Cursor(ctx)
		if err != nil {
			log.Printf("indexer: read cursor: %v", err)
			return
		}

		events, latest, err := ix.rpc.Events(ctx, uint32(cursor+1), ix.contracts)
		if err != nil {
			// If we are beyond the retention window the RPC rejects the
			// range. Jump to the oldest readable ledger and continue.
			oldest, ok := ledgerFromRpcError(err, latest)
			if ok {
				log.Printf("indexer: range rejected, jumping to ledger %d", oldest)
				if err := ix.store.SetCursor(ctx, int64(oldest-1)); err != nil {
					log.Printf("indexer: set cursor: %v", err)
				}
				continue
			}
			log.Printf("indexer: get events: %v", err)
			return
		}

		if len(events) == 0 {
			if latest > uint32(cursor) {
				if err := ix.store.SetCursor(ctx, int64(latest)); err != nil {
					log.Printf("indexer: set cursor: %v", err)
				}
			}
			return
		}

		var applied []store.Applied
		maxLedger := int64(0)
		for _, ev := range events {
			if int64(ev.Ledger) > maxLedger {
				maxLedger = int64(ev.Ledger)
			}
			decoded, err := decodeEvent(ev)
			if err != nil {
				log.Printf("indexer: skip undecodable event ledger %d: %v", ev.Ledger, err)
				continue
			}
			if decoded == nil {
				continue
			}
			applied = append(applied, store.Applied{
				Event: eventName(ev), Ledger: int64(ev.Ledger),
				TxHash: ev.TxHash, Contract: ev.ContractID, Decoded: decoded,
			})
		}

		if err := ix.store.Apply(ctx, applied, maxLedger); err != nil {
			log.Printf("indexer: apply: %v", err)
			return
		}

		ix.recordActivity(ctx, events)

		if len(events) < 100 {
			return // caught up
		}
		// Full page: keep paging from past the highest ledger seen.
	}
}

// decodeEvent turns a raw RPC event into a store write (or nil for the
// informational events whose state change is carried by another event).
func decodeEvent(ev rpc.Event) (any, error) {
	if len(ev.Topic) == 0 {
		return nil, nil
	}
	nameVal, err := decodeScVal(ev.Topic[0])
	if err != nil {
		return nil, err
	}
	name, err := symbolString(nameVal)
	if err != nil {
		return nil, err
	}

	decoded, err := Decode(name, ev.Topic, ev.Value)
	if err != nil {
		return nil, err
	}

	switch d := decoded.(type) {
	case InstitutionRegistered:
		return store.UpsertInstitution{
			Address: d.Institution, Payout: d.Payout, Name: d.Name,
			Country: d.Country, Status: "pending", RegisteredAt: int64(d.RegisteredAt),
		}, nil
	case InstitutionVerified:
		return store.SetInstitutionStatus{Address: d.Institution, Status: "verified"}, nil
	case InstitutionSuspended:
		return store.SetInstitutionStatus{Address: d.Institution, Status: "suspended"}, nil
	case PayoutUpdated:
		return store.SetPayout{Address: d.Institution, Payout: d.NewPayout}, nil
	case GrantCreated:
		return store.UpsertGrant{
			GrantID: int64(d.GrantID), Sponsor: d.Sponsor, Beneficiary: d.Beneficiary,
			Institution: d.Institution, Token: d.Token, TermAmount: d.TermAmount,
			TermsTotal: int(d.TermsTotal), CreatedAt: createdAtUnix(ev.LedgerClosedAt),
		}, nil
	case TermAttested:
		return store.UpsertTerm{
			GrantID: int64(d.GrantID), TermIndex: int(d.TermIndex), Status: "attested",
			AttestedAt: int64(d.AttestedAt), ReleaseAfter: int64(d.ReleaseAfter),
		}, nil
	case TermReleased:
		return store.Composite{
			Writes: []any{
				store.UpsertTerm{GrantID: int64(d.GrantID), TermIndex: int(d.TermIndex), Status: "released"},
				store.AdvanceTerm{GrantID: int64(d.GrantID), NextTerm: int(d.TermIndex) + 1, StatusIfDone: "completed"},
			},
		}, nil
	case TermDisputed:
		return store.UpsertTerm{
			GrantID: int64(d.GrantID), TermIndex: int(d.TermIndex), Status: "disputed",
		}, nil
	case DisputeResolved:
		status := "refunded"
		if d.Released {
			status = "released"
		}
		return store.Composite{
			Writes: []any{
				store.UpsertTerm{GrantID: int64(d.GrantID), TermIndex: int(d.TermIndex), Status: status},
				store.AdvanceTerm{GrantID: int64(d.GrantID), NextTerm: int(d.TermIndex) + 1, StatusIfDone: "completed"},
			},
		}, nil
	case TermRefunded:
		// Informational: the refund's authoritative state change arrives in
		// the DisputeResolved event of the same transaction. Record the term
		// status only; the advance comes with DisputeResolved.
		return store.UpsertTerm{
			GrantID: int64(d.GrantID), TermIndex: int(d.TermIndex), Status: "refunded",
		}, nil
	case GrantCancelled:
		return store.SetGrantStatus{GrantID: int64(d.GrantID), Status: "cancelled"}, nil
	case GrantCompleted:
		// Informational: AdvanceTerm already completes the grant when the
		// final term settles. Recorded for the activity feed only.
		return nil, nil
	}
	return nil, nil
}

// createdAtUnix converts the RPC's ledger close time to unix seconds.
func createdAtUnix(closedAt string) int64 {
	if closedAt == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, closedAt)
	if err != nil {
		return 0
	}
	return t.Unix()
}

func eventName(ev rpc.Event) string {
	if len(ev.Topic) == 0 {
		return ""
	}
	nameVal, err := decodeScVal(ev.Topic[0])
	if err != nil {
		return ""
	}
	name, err := symbolString(nameVal)
	if err != nil {
		return ""
	}
	return name
}

func (ix *Indexer) recordActivity(ctx context.Context, events []rpc.Event) {
	for _, ev := range events {
		name := eventName(ev)
		if name == "" {
			continue
		}
		decoded, err := Decode(name, ev.Topic, ev.Value)
		if err != nil || decoded == nil {
			continue
		}
		// GrantCreated arrives without a timestamp; use the attested-free
		// fields as-is. The activity row keeps the raw payload.
		if err := ix.store.RecordActivity(ctx, int64(ev.Ledger), ev.TxHash, ev.ContractID, name, decoded); err != nil {
			log.Printf("indexer: record activity: %v", err)
			return
		}
	}
}

// ledgerFromRpcError extracts the oldest readable ledger from an RPC range
// error. The Soroban RPC reports the retention boundary in some errors; when
// it does not, we fall back to latest minus the maximum span.
func ledgerFromRpcError(err error, latest uint32) (uint32, bool) {
	if latest == 0 {
		return 0, false
	}
	if span := latest; span > maxSpan {
		return latest - maxSpan, true
	}
	return 1, true
}
