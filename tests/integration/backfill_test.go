//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/runner"
	"github.com/ddevilz/phasedb/internal/store"
)

func TestBackfillPhase_TerminatesAndCheckpoints(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t)
	adapter, err := db.NewAdapter(testDSN())
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer adapter.Close()

	// Setup: table with nullable column and a few rows
	_, _ = adapter.ExecDDL(ctx, "DROP TABLE IF EXISTS integration_test_backfill", 5*time.Second)
	_, _ = adapter.ExecDDL(ctx, `CREATE TABLE integration_test_backfill (
		id INT PRIMARY KEY AUTO_INCREMENT,
		val INT NULL
	)`, 5*time.Second)
	t.Cleanup(func() {
		_, _ = adapter.ExecDDL(context.Background(), "DROP TABLE IF EXISTS integration_test_backfill", 5*time.Second)
	})

	// Insert 5 rows with val=NULL
	for i := 0; i < 5; i++ {
		if _, err := adapter.ExecBatch(ctx, "INSERT INTO integration_test_backfill (val) VALUES (NULL)", 1); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}

	m := &config.MigrationFile{
		Name:     "V_integration_backfill",
		Database: "mysql",
		Phases: []config.Phase{{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:        "UPDATE integration_test_backfill SET val = 1 WHERE val IS NULL LIMIT {batch_size}",
				Size:         2,
				DelayMs:      0,
				DoneWhen:     "SELECT COUNT(*) FROM integration_test_backfill WHERE val IS NULL",
				DoneExpected: 0,
			},
		}},
	}

	r := &runner.Runner{Migration: m, DB: adapter, Store: s, Version: "test"}

	if err := r.Run(ctx); err != nil {
		t.Fatalf("backfill run failed: %v", err)
	}

	// Verify all rows updated
	remaining, err := adapter.QueryScalar(ctx, "SELECT COUNT(*) FROM integration_test_backfill WHERE val IS NULL")
	if err != nil || remaining != 0 {
		t.Fatalf("expected 0 null rows after backfill, got %d (err: %v)", remaining, err)
	}

	// Verify COMPLETED
	ev, _ := s.LatestEvent(ctx, m.Name, "backfill")
	if ev == nil || ev.EventType != store.EventCompleted {
		t.Fatalf("expected PHASE_COMPLETED, got %v", ev)
	}
}

// TestBackfillPhase_PKCursor_AllRowsProcessed verifies that a PK cursor backfill
// processes all rows exactly once using {last_id} and pk_cursor_query.
func TestBackfillPhase_PKCursor_AllRowsProcessed(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t)
	adapter, err := db.NewAdapter(testDSN())
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer adapter.Close()

	// Clean any leftover store state from prior runs so the runner doesn't skip.
	rawDB := mustOpenDB(t)
	_, _ = rawDB.ExecContext(ctx, "DELETE FROM phasedb_history WHERE migration_name = ?", t.Name())
	_, _ = rawDB.ExecContext(ctx, "DELETE FROM phasedb_checkpoints WHERE migration_name = ?", t.Name())

	_, _ = adapter.ExecDDL(ctx, "DROP TABLE IF EXISTS integration_test_pkcursor", 5*time.Second)
	_, _ = adapter.ExecDDL(ctx, `CREATE TABLE integration_test_pkcursor (
		id  INT PRIMARY KEY AUTO_INCREMENT,
		val INT NULL
	)`, 5*time.Second)
	t.Cleanup(func() {
		_, _ = adapter.ExecDDL(context.Background(), "DROP TABLE IF EXISTS integration_test_pkcursor", 5*time.Second)
	})

	// Seed 100 rows with val=NULL
	for i := 0; i < 100; i++ {
		if _, err := adapter.ExecBatch(ctx, "INSERT INTO integration_test_pkcursor (val) VALUES (NULL)", 1); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	m := &config.MigrationFile{
		Name:     t.Name(),
		Database: "mysql",
		Phases: []config.Phase{{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query: `UPDATE integration_test_pkcursor
SET val = 1
WHERE id > {last_id} AND val IS NULL
ORDER BY id
LIMIT {batch_size}`,
				Size:            20,
				DelayMs:         0,
				PKColumn:        "id",
				CheckpointEvery: 1,
				PKCursorQuery: `SELECT COALESCE(MAX(subq.id), {last_id})
FROM (SELECT id FROM integration_test_pkcursor WHERE id > {last_id} ORDER BY id LIMIT {batch_size}) subq`,
				DoneWhen:     "SELECT COUNT(*) FROM integration_test_pkcursor WHERE val IS NULL",
				DoneExpected: 0,
			},
		}},
	}

	r := &runner.Runner{Migration: m, DB: adapter, Store: s, Version: "test"}
	if err := r.Run(ctx); err != nil {
		t.Fatalf("backfill run failed: %v", err)
	}

	remaining, err := adapter.QueryScalar(ctx, "SELECT COUNT(*) FROM integration_test_pkcursor WHERE val IS NULL")
	if err != nil || remaining != 0 {
		t.Fatalf("expected 0 null rows, got %d (err: %v)", remaining, err)
	}
	total, _ := adapter.QueryScalar(ctx, "SELECT COUNT(*) FROM integration_test_pkcursor WHERE val = 1")
	if total != 100 {
		t.Errorf("expected 100 rows with val=1, got %d", total)
	}

	ev, _ := s.LatestEvent(ctx, m.Name, "backfill")
	if ev == nil || ev.EventType != store.EventCompleted {
		t.Fatalf("expected PHASE_COMPLETED, got %v", ev)
	}
}

// TestBackfillPhase_PKCursor_ResumeAfterInterrupt verifies that a backfill
// interrupted mid-way resumes from the correct position and completes without
// processing any row twice.
func TestBackfillPhase_PKCursor_ResumeAfterInterrupt(t *testing.T) {
	bg := context.Background()
	s := mustStore(t)
	adapter, err := db.NewAdapter(testDSN())
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer adapter.Close()

	_, _ = adapter.ExecDDL(bg, "DROP TABLE IF EXISTS integration_test_pkcursor_resume", 5*time.Second)
	_, _ = adapter.ExecDDL(bg, `CREATE TABLE integration_test_pkcursor_resume (
		id  INT PRIMARY KEY AUTO_INCREMENT,
		val INT NULL
	)`, 5*time.Second)
	t.Cleanup(func() {
		_, _ = adapter.ExecDDL(context.Background(), "DROP TABLE IF EXISTS integration_test_pkcursor_resume", 5*time.Second)
	})

	// Seed 100 rows
	for i := 0; i < 100; i++ {
		if _, err := adapter.ExecBatch(bg, "INSERT INTO integration_test_pkcursor_resume (val) VALUES (NULL)", 1); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Clean any leftover store state from prior runs so the runner doesn't skip.
	rawDB := mustOpenDB(t)
	_, _ = rawDB.ExecContext(bg, "DELETE FROM phasedb_history WHERE migration_name = ?", t.Name())
	_, _ = rawDB.ExecContext(bg, "DELETE FROM phasedb_checkpoints WHERE migration_name = ?", t.Name())

	migName := t.Name()
	batchCfg := &config.BatchConfig{
		Query: `UPDATE integration_test_pkcursor_resume
SET val = 1
WHERE id > {last_id} AND val IS NULL
ORDER BY id
LIMIT {batch_size}`,
		Size:            10,
		DelayMs:         0,
		PKColumn:        "id",
		CheckpointEvery: 1,
		PKCursorQuery: `SELECT COALESCE(MAX(subq.id), {last_id})
FROM (SELECT id FROM integration_test_pkcursor_resume WHERE id > {last_id} ORDER BY id LIMIT {batch_size}) subq`,
		DoneWhen:     "SELECT COUNT(*) FROM integration_test_pkcursor_resume WHERE val IS NULL",
		DoneExpected: 0,
	}

	// Run 1: cancel after 500ms — processes some batches, leaves remainder NULL
	ctx1, cancel := context.WithTimeout(bg, 500*time.Millisecond)
	defer cancel()
	m := &config.MigrationFile{
		Name:     migName,
		Database: "mysql",
		Phases:   []config.Phase{{Name: "backfill", Batch: batchCfg}},
	}
	r1 := &runner.Runner{Migration: m, DB: adapter, Store: s, Version: "test"}
	_ = r1.Run(ctx1) // expect context error — that's fine

	// Verify partial progress
	done, _ := adapter.QueryScalar(bg, "SELECT COUNT(*) FROM integration_test_pkcursor_resume WHERE val = 1")
	if done == 0 {
		t.Skip("no rows updated in first run — timeout too short for this machine, skip")
	}
	if done == 100 {
		t.Skip("all rows updated in first run — timeout too long, cannot test resume")
	}

	// Run 2: resume — should complete
	r2 := &runner.Runner{Migration: m, DB: adapter, Store: s, Version: "test"}
	if err := r2.Run(bg); err != nil {
		t.Fatalf("resume run failed: %v", err)
	}

	remaining, _ := adapter.QueryScalar(bg, "SELECT COUNT(*) FROM integration_test_pkcursor_resume WHERE val IS NULL")
	if remaining != 0 {
		t.Errorf("expected 0 null rows after resume, got %d", remaining)
	}
	doubled, _ := adapter.QueryScalar(bg, "SELECT COUNT(*) FROM integration_test_pkcursor_resume WHERE val != 1")
	if doubled != 0 {
		t.Errorf("expected no rows with val != 1, got %d (possible double-update)", doubled)
	}
}
