package reconcile

import (
	"context"
	"log"
	"sync"
	"time"
)

// Supervisor runs reconciliation passes triggered by external events — a gap the
// indexer just detected, or an optional periodic timer — while guaranteeing that
// at most one pass is ever in flight. A trigger that arrives while a pass is
// running is coalesced (ignored), never queued, so a burst of gaps cannot pile
// up overlapping passes that fight over the read model.
type Supervisor struct {
	// run performs a single reconciliation pass and returns its report.
	run func(ctx context.Context) (Report, error)
	// record persists the outcome (typically Store.RecordReconcile) so the
	// indexer status endpoint can show the last pass.
	record func(ctx context.Context, repaired int) error

	mu      sync.Mutex
	running bool
}

// NewSupervisor builds a Supervisor from the pass runner and outcome recorder.
// Both are plain functions so callers can adapt a *store.Store (which already
// implements reconcile.Repo and RecordReconcile) without an extra interface.
func NewSupervisor(run func(ctx context.Context) (Report, error), record func(ctx context.Context, repaired int) error) *Supervisor {
	return &Supervisor{run: run, record: record}
}

// Trigger starts a pass if none is running. Calls that arrive while a pass is in
// flight return immediately — the in-progress pass is assumed to cover the need.
func (s *Supervisor) Trigger(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()
		report, err := s.run(ctx)
		if err != nil {
			log.Printf("reconcile supervisor: pass failed: %v", err)
			return
		}
		repaired := report.GrantsRepaired + report.TermsRepaired + report.InstitutionsRepaired
		if err := s.record(ctx, repaired); err != nil {
			log.Printf("reconcile supervisor: record outcome: %v", err)
		}
	}()
}

// StartPeriodic runs a pass every interval until ctx is cancelled. An interval
// of zero is a no-op: the periodic safety net stays off unless explicitly
// opted into via RECONCILE_INTERVAL.
func (s *Supervisor) StartPeriodic(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.Trigger(ctx)
			}
		}
	}()
}
