package reconcile

import (
	"context"
	"sync"
	"testing"
	"time"
)

// recordingRepo wraps memRepo and remembers the repaired count handed to
// RecordReconcile, so the test can confirm the supervisor persisted the outcome.
type recordingRepo struct {
	*memRepo
	mu       sync.Mutex
	recorded int
}

func (r *recordingRepo) RecordReconcile(_ context.Context, repaired int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recorded += repaired
	return nil
}

// A single gap must produce exactly one pass, and the outcome must be recorded.
func TestSupervisorTriggersPassOnGap(t *testing.T) {
	ctx := context.Background()
	repo := &recordingRepo{memRepo: newMemRepo()}
	cf := chainFixture()

	var passCount int
	var passMu sync.Mutex
	super := NewSupervisor(
		func(ctx context.Context) (Report, error) {
			passMu.Lock()
			passCount++
			passMu.Unlock()
			return Run(ctx, cf, cf, repo)
		},
		func(ctx context.Context, repaired int) error {
			return repo.RecordReconcile(ctx, repaired)
		},
	)

	super.Trigger(ctx)

	// Wait for the pass to finish (recording the outcome is its last step).
	deadline := time.Now().Add(5 * time.Second)
	for {
		repo.mu.Lock()
		done := repo.recorded
		repo.mu.Unlock()
		if done > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	passMu.Lock()
	pc := passCount
	passMu.Unlock()
	if pc != 1 {
		t.Fatalf("passes run = %d, want 1", pc)
	}
	if len(repo.repaired) == 0 {
		t.Fatal("no repairs applied by the triggered pass")
	}
	repo.mu.Lock()
	rec := repo.recorded
	repo.mu.Unlock()
	if rec == 0 {
		t.Error("RecordReconcile not called with the repaired count")
	}
}

// A burst of gaps while a pass is running must coalesce into a single pass:
// none are queued, none overlap.
func TestSupervisorCoalescesConcurrentTriggers(t *testing.T) {
	ctx := context.Background()
	repo := &recordingRepo{memRepo: newMemRepo()}
	cf := chainFixture()

	entered := make(chan struct{})
	hold := make(chan struct{})
	var passCount int
	var passMu sync.Mutex

	super := NewSupervisor(
		func(ctx context.Context) (Report, error) {
			passMu.Lock()
			passCount++
			isFirst := passCount == 1
			passMu.Unlock()
			if isFirst {
				close(entered) // tell the test the first pass is now in flight
				<-hold         // stay running so the other triggers coalesce
			}
			return Run(ctx, cf, cf, repo)
		},
		func(ctx context.Context, repaired int) error {
			return repo.RecordReconcile(ctx, repaired)
		},
	)

	const n = 25
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			super.Trigger(ctx)
		}()
	}

	<-entered // first pass has started and is holding
	wg.Wait() // all 25 triggers have returned (24 of them coalesced)

	close(hold) // release the in-flight pass

	// Wait for the pass to record its outcome.
	deadline := time.Now().Add(5 * time.Second)
	for {
		repo.mu.Lock()
		done := repo.recorded
		repo.mu.Unlock()
		if done > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	passMu.Lock()
	pc := passCount
	passMu.Unlock()
	if pc != 1 {
		t.Fatalf("passes run under %d concurrent triggers = %d, want 1 (coalesced)", n, pc)
	}
	if len(repo.repaired) == 0 {
		t.Fatal("the single coalesced pass applied no repairs")
	}
}

// An opted-in periodic safety net must actually run passes.
func TestSupervisorPeriodicRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &recordingRepo{memRepo: newMemRepo()}
	cf := chainFixture()

	var passCount int
	var passMu sync.Mutex
	super := NewSupervisor(
		func(ctx context.Context) (Report, error) {
			passMu.Lock()
			passCount++
			passMu.Unlock()
			return Run(ctx, cf, cf, repo)
		},
		func(ctx context.Context, repaired int) error {
			return repo.RecordReconcile(ctx, repaired)
		},
	)
	super.StartPeriodic(ctx, 10*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for {
		passMu.Lock()
		pc := passCount
		passMu.Unlock()
		if pc >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	passMu.Lock()
	pc := passCount
	passMu.Unlock()
	if pc < 1 {
		t.Fatalf("periodic passes run = %d, want >= 1", pc)
	}
}
