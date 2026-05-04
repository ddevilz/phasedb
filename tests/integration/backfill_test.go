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
