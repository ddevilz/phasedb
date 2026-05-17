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

// TestBackfillExecutor_CheckpointEveryZero verifies that a zero CheckpointEvery
// (YAML field absent) is normalized to 1 and does not panic on batchNum%0.
func TestBackfillExecutor_CheckpointEveryZero(t *testing.T) {
	adapter := &mockAdapterFull{
		batchResults:  []int64{5, 0},
		scalarResults: []int64{5, 0}, // initial done_when=5, post-empty done_when=0
	}
	s := &mockStore{}

	ex := &phase.BackfillExecutor{
		Phase: config.Phase{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:           "UPDATE T SET C = 1 WHERE C IS NULL LIMIT {batch_size}",
				Size:            5,
				DelayMs:         0,
				DoneWhen:        "SELECT COUNT(*) FROM T WHERE C IS NULL",
				DoneExpected:    0,
				CheckpointEvery: 0, // absent from YAML = Go zero value
			},
		},
		Migration:     "V1",
		AttemptNumber: 1,
	}

	if err := ex.Execute(context.Background(), adapter, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Normalized to 1 → checkpoint after every batch with affected>0 → 1 checkpoint
	if len(s.checkpoints) != 1 {
		t.Errorf("expected 1 checkpoint (checkpointEvery normalized to 1), got %d", len(s.checkpoints))
	}
}

// TestBackfillExecutor_CheckpointEvery verifies that InsertCheckpoint is called
// every N batches (not every batch).
func TestBackfillExecutor_CheckpointEvery(t *testing.T) {
	// 10 batches with affected>0, then one empty batch that satisfies done_when
	batchResults := make([]int64, 11)
	for i := 0; i < 10; i++ {
		batchResults[i] = 5
	}
	// batchResults[10] = 0 (default zero)

	adapter := &mockAdapterFull{
		batchResults:  batchResults,
		scalarResults: []int64{50, 0}, // initial done_when=50; post-empty done_when=0
	}
	s := &mockStore{}

	ex := &phase.BackfillExecutor{
		Phase: config.Phase{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:           "UPDATE T SET C = 1 WHERE C IS NULL LIMIT {batch_size}",
				Size:            5,
				DelayMs:         0,
				DoneWhen:        "SELECT COUNT(*) FROM T WHERE C IS NULL",
				DoneExpected:    0,
				CheckpointEvery: 5,
			},
		},
		Migration:     "V1",
		AttemptNumber: 1,
	}

	if err := ex.Execute(context.Background(), adapter, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Checkpoint at batch 5 and batch 10 only = 2 total
	if len(s.checkpoints) != 2 {
		t.Errorf("expected 2 checkpoints (at batch 5 and 10), got %d", len(s.checkpoints))
	}
}

// TestBackfillExecutor_LagNotCalledWhenThresholdZero verifies that GetReplicaLag
// is never called when lag_threshold_ms is 0 (the default).
func TestBackfillExecutor_LagNotCalledWhenThresholdZero(t *testing.T) {
	adapter := &mockAdapterFull{
		batchResults:  []int64{0},
		scalarResults: []int64{0, 0}, // initial done_when=0; post-empty done_when=0
	}
	s := &mockStore{}

	ex := &phase.BackfillExecutor{
		Phase: config.Phase{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:          "UPDATE T SET C = 1 WHERE C IS NULL LIMIT {batch_size}",
				Size:           5,
				DelayMs:        0,
				LagThresholdMs: 0,
				DoneWhen:       "SELECT COUNT(*) FROM T WHERE C IS NULL",
				DoneExpected:   0,
			},
		},
		Migration:     "V1",
		AttemptNumber: 1,
	}

	if err := ex.Execute(context.Background(), adapter, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adapter.lagCallCount != 0 {
		t.Errorf("expected 0 GetReplicaLag calls with threshold=0, got %d", adapter.lagCallCount)
	}
}
