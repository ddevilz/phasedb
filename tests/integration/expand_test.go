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

func TestExpandPhase_RunsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t)
	adapter, err := db.NewAdapter(testDSN())
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer adapter.Close()

	// Clean state
	_, _ = adapter.ExecDDL(ctx, "DROP TABLE IF EXISTS integration_test_expand", 5*time.Second)
	_, _ = adapter.ExecDDL(ctx, "CREATE TABLE integration_test_expand (id INT PRIMARY KEY, name VARCHAR(64))", 5*time.Second)
	t.Cleanup(func() {
		_, _ = adapter.ExecDDL(context.Background(), "DROP TABLE IF EXISTS integration_test_expand", 5*time.Second)
	})

	m := &config.MigrationFile{
		Name:     "V_integration_expand",
		Database: "mysql",
		Phases: []config.Phase{{
			Name: "expand",
			SQL:  "ALTER TABLE integration_test_expand ADD COLUMN new_col VARCHAR(64) NULL",
		}},
	}

	r := &runner.Runner{Migration: m, DB: adapter, Store: s, Version: "test"}

	// First run
	if err := r.Run(ctx); err != nil {
		t.Fatalf("first run failed: %v", err)
	}

	// Verify column exists
	exists, err := adapter.ColumnExists(ctx, "integration_test_expand", "new_col")
	if err != nil || !exists {
		t.Fatalf("column new_col not found after expand: %v", err)
	}

	// Verify COMPLETED in history
	ev, _ := s.LatestEvent(ctx, m.Name, "expand")
	if ev == nil || ev.EventType != store.EventCompleted {
		t.Fatalf("expected PHASE_COMPLETED, got %v", ev)
	}

	// Second run — must be idempotent (completed phase is skipped)
	if err := r.Run(ctx); err != nil {
		t.Fatalf("second run (idempotent) failed: %v", err)
	}
}
