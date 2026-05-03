package phase_test

import (
	"context"
	"testing"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/phase"
)

// TestBackfillExecutor_DoneWhenTerminates verifies that Execute returns nil when
// the first batch returns 0 affected rows and the done_when query returns the
// expected value.
func TestBackfillExecutor_DoneWhenTerminates(t *testing.T) {
	adapter := &mockAdapterFull{
		// ExecBatch returns 0 affected immediately
		batchResults: []int64{0},
		// QueryScalar: first call is the initial done_when check (returns 5),
		// second call is the post-batch done_when check (returns 0 == DoneExpected).
		scalarResults: []int64{5, 0},
	}
	s := &mockStore{}

	ex := &phase.BackfillExecutor{
		Phase: config.Phase{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:        "UPDATE t SET c = 1 WHERE c IS NULL LIMIT 100",
				Size:         100,
				DelayMs:      0,
				DoneWhen:     "SELECT COUNT(*) FROM t WHERE c IS NULL",
				DoneExpected: 0,
			},
		},
		Migration:     "V1",
		AttemptNumber: 1,
	}

	if err := ex.Execute(context.Background(), adapter, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBackfillExecutor_ContextCancellation verifies that Execute returns a
// context error when the context is cancelled before any work starts.
func TestBackfillExecutor_ContextCancellation(t *testing.T) {
	adapter := &mockAdapterFull{
		// Initial QueryScalar for done_when check returns 10
		scalarResults: []int64{10},
		// ExecBatch will never be reached
	}
	s := &mockStore{}

	ex := &phase.BackfillExecutor{
		Phase: config.Phase{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:        "UPDATE t SET c = 1 WHERE c IS NULL LIMIT 100",
				Size:         100,
				DelayMs:      0,
				DoneWhen:     "SELECT COUNT(*) FROM t WHERE c IS NULL",
				DoneExpected: 0,
			},
		},
		Migration:     "V1",
		AttemptNumber: 1,
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after the initial done_when check but before any loop iteration
	// by setting batchErr to trigger early; instead just cancel upfront.
	cancel()

	err := ex.Execute(ctx, adapter, s)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
}
