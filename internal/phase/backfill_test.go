package phase_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/phase"
	"github.com/ddevilz/phasedb/internal/store"
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
	// Normalized to 1 → checkpoint after every batch with affected>0 → 1 checkpoint, plus final checkpoint on clean exit → 2 total
	if len(s.checkpoints) != 2 {
		t.Errorf("expected 2 checkpoints (1 after batch + 1 final), got %d", len(s.checkpoints))
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
	// Checkpoint at batch 5 and batch 10, plus final checkpoint on clean exit = 3 total
	if len(s.checkpoints) != 3 {
		t.Errorf("expected 3 checkpoints (at batch 5, 10, and final), got %d", len(s.checkpoints))
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

// TestBackfillExecutor_LastIDSubstituted verifies that {last_id} in the batch
// query is replaced with the current cursor value when PKCursorQuery is set.
func TestBackfillExecutor_LastIDSubstituted(t *testing.T) {
	adapter := &mockAdapterFull{
		batchResults:  []int64{5, 0},
		scalarResults: []int64{5, 500, 0},
	}
	s := &mockStore{}

	ex := &phase.BackfillExecutor{
		Phase: config.Phase{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:         "UPDATE T SET C = 1 WHERE ID > {last_id} AND C IS NULL LIMIT {batch_size}",
				Size:          5,
				DelayMs:       0,
				PKColumn:      "id",
				PKCursorQuery: "SELECT COALESCE(MAX(ID), {last_id}) FROM T WHERE ID > {last_id} ORDER BY ID LIMIT {batch_size}",
				DoneWhen:      "SELECT COUNT(*) FROM T WHERE C IS NULL",
				DoneExpected:  0,
			},
		},
		Migration:     "V1",
		AttemptNumber: 1,
	}

	if err := ex.Execute(context.Background(), adapter, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(adapter.capturedBatchQueries) < 2 {
		t.Fatalf("expected 2 batch calls, got %d", len(adapter.capturedBatchQueries))
	}
	// Batch 1: lastPK=0, so {last_id} → "0"
	if !strings.Contains(adapter.capturedBatchQueries[0], "ID > 0") {
		t.Errorf("batch 1: expected 'ID > 0', got: %s", adapter.capturedBatchQueries[0])
	}
	// Batch 2: cursor returned 500, so {last_id} → "500"
	if !strings.Contains(adapter.capturedBatchQueries[1], "ID > 500") {
		t.Errorf("batch 2: expected 'ID > 500', got: %s", adapter.capturedBatchQueries[1])
	}
}

// TestBackfillExecutor_LastIDNotSubstitutedWithoutCursorQuery verifies that
// {last_id} in the query is left as-is when PKCursorQuery is empty, preserving
// backward compatibility with existing migrations.
func TestBackfillExecutor_LastIDNotSubstitutedWithoutCursorQuery(t *testing.T) {
	adapter := &mockAdapterFull{
		batchResults:  []int64{0},
		scalarResults: []int64{0, 0},
	}
	s := &mockStore{}

	const query = "UPDATE T SET C = 1 WHERE ID > {last_id} AND C IS NULL LIMIT {batch_size}"
	ex := &phase.BackfillExecutor{
		Phase: config.Phase{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:        query,
				Size:         5,
				DelayMs:      0,
				DoneWhen:     "SELECT COUNT(*) FROM T WHERE C IS NULL",
				DoneExpected: 0,
				// PKCursorQuery intentionally empty
			},
		},
		Migration:     "V1",
		AttemptNumber: 1,
	}

	if err := ex.Execute(context.Background(), adapter, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(adapter.capturedBatchQueries) == 0 {
		t.Fatal("no batch queries captured")
	}
	// {last_id} must survive unchanged
	if !strings.Contains(adapter.capturedBatchQueries[0], "{last_id}") {
		t.Errorf("expected literal {last_id} in query (no substitution), got: %s", adapter.capturedBatchQueries[0])
	}
}

// TestBackfillExecutor_PKCursorQueryError verifies that an error from
// pk_cursor_query causes Execute to return immediately (fatal behavior).
func TestBackfillExecutor_PKCursorQueryError(t *testing.T) {
	adapter := &mockAdapterFull{
		batchResults:  []int64{5},
		scalarResults: []int64{5},
		scalarErrs:    []error{nil, fmt.Errorf("cursor query failed: table not found")},
	}
	s := &mockStore{}

	ex := &phase.BackfillExecutor{
		Phase: config.Phase{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:         "UPDATE T SET C = 1 WHERE ID > {last_id} AND C IS NULL LIMIT {batch_size}",
				Size:          5,
				DelayMs:       0,
				PKColumn:      "id",
				PKCursorQuery: "SELECT MAX(ID) FROM T WHERE ID > {last_id}",
				DoneWhen:      "SELECT COUNT(*) FROM T WHERE C IS NULL",
				DoneExpected:  0,
			},
		},
		Migration:     "V1",
		AttemptNumber: 1,
	}

	err := ex.Execute(context.Background(), adapter, s)
	if err == nil {
		t.Fatal("expected error from pk_cursor_query failure, got nil")
	}
	if !strings.Contains(err.Error(), "cursor query failed") {
		t.Errorf("expected cursor error in message, got: %v", err)
	}
}

// TestBackfillExecutor_ResumeLoadsLastPK verifies that when a checkpoint exists
// with a LastPK value, the backfill resumes from that position rather than 0.
func TestBackfillExecutor_ResumeLoadsLastPK(t *testing.T) {
	cpJSON := `{"rows_processed_total":50,"batch_number":10,"last_pk":5000}`
	adapter := &mockAdapterFull{
		// 1 batch with rows starting from lastPK=5000, then empty + done
		batchResults:  []int64{5, 0},
		scalarResults: []int64{5, 5500, 0}, // done_when_initial=5, cursor→5500, done_when_post=0
	}
	s := &mockStore{
		checkpointToReturn: &store.CheckpointRow{
			CheckpointJSON: cpJSON,
		},
	}

	ex := &phase.BackfillExecutor{
		Phase: config.Phase{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:         "UPDATE T SET C = 1 WHERE ID > {last_id} AND C IS NULL LIMIT {batch_size}",
				Size:          5,
				DelayMs:       0,
				PKColumn:      "id",
				PKCursorQuery: "SELECT COALESCE(MAX(ID), {last_id}) FROM T WHERE ID > {last_id} ORDER BY ID LIMIT {batch_size}",
				DoneWhen:      "SELECT COUNT(*) FROM T WHERE C IS NULL",
				DoneExpected:  0,
			},
		},
		Migration:     "V1",
		AttemptNumber: 1,
	}

	if err := ex.Execute(context.Background(), adapter, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(adapter.capturedBatchQueries) == 0 {
		t.Fatal("no batch queries captured")
	}
	// Resume should start from lastPK=5000, not 0
	if !strings.Contains(adapter.capturedBatchQueries[0], "ID > 5000") {
		t.Errorf("expected resume from ID > 5000, got: %s", adapter.capturedBatchQueries[0])
	}
}
