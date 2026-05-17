//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/runner"
	"github.com/ddevilz/phasedb/internal/store"
)

// TestFullMigration_ExpandBackfillGateContract runs the complete 4-phase
// expand-contract lifecycle on a real MySQL instance with real data.
func TestFullMigration_ExpandBackfillGateContract(t *testing.T) {
	const table = "e2e_orders"
	const rows = 500
	ctx := context.Background()
	s := mustStore(t)
	adapter, err := db.NewAdapter(testDSN())
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer adapter.Close()

	// Setup: base table with no checksum column
	_, _ = adapter.ExecDDL(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table), 5*time.Second)
	_, _ = adapter.ExecDDL(ctx, fmt.Sprintf(`CREATE TABLE %s (
		id      INT PRIMARY KEY AUTO_INCREMENT,
		user_id VARCHAR(64) NOT NULL,
		payload TEXT NOT NULL
	)`, table), 5*time.Second)
	t.Cleanup(func() {
		_, _ = adapter.ExecDDL(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table), 5*time.Second)
	})

	// Seed rows
	for i := 0; i < rows; i++ {
		q := fmt.Sprintf("INSERT INTO %s (user_id, payload) VALUES ('user%d', 'data%d')", table, i, i)
		if _, err := adapter.ExecBatch(ctx, q, 1); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}

	m := &config.MigrationFile{
		Name:     "e2e_add_checksum",
		Database: "mysql",
		Phases: []config.Phase{
			{
				Name: "expand",
				SQL:  fmt.Sprintf("ALTER TABLE %s ADD COLUMN checksum VARCHAR(64) NULL", table),
				RollbackSQL: fmt.Sprintf("ALTER TABLE %s DROP COLUMN checksum", table),
			},
			{
				Name:      "backfill",
				OnFailure: "rollback",
				Batch: &config.BatchConfig{
					Query:        fmt.Sprintf("UPDATE %s SET checksum = SHA2(CONCAT(user_id, payload), 256) WHERE checksum IS NULL LIMIT {batch_size}", table),
					Size:         50,
					DelayMs:      0,
					DoneWhen:     fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE checksum IS NULL", table),
					DoneExpected: 0,
				},
				RollbackSQL: fmt.Sprintf("UPDATE %s SET checksum = NULL", table),
			},
			{
				Name: "gate",
				WaitUntil: &config.GateConfig{
					Query:          fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE checksum IS NULL", table),
					Expected:       0,
					PollIntervalMs: 200,
					TimeoutMinutes: 2,
				},
			},
			{
				Name: "contract",
				SQL: fmt.Sprintf(`ALTER TABLE %s MODIFY COLUMN checksum VARCHAR(64) NOT NULL;
ALTER TABLE %s ADD INDEX idx_e2e_checksum (checksum)`, table, table),
				RollbackSQL: fmt.Sprintf(`ALTER TABLE %s DROP INDEX idx_e2e_checksum;
ALTER TABLE %s MODIFY COLUMN checksum VARCHAR(64) NULL`, table, table),
			},
		},
	}

	r := &runner.Runner{Migration: m, DB: adapter, Store: s, Version: "test"}

	t.Log("running full 4-phase migration...")
	if err := r.Run(ctx); err != nil {
		t.Fatalf("full migration failed: %v", err)
	}

	// Assert: no null checksums remain
	nullCount, err := adapter.QueryScalar(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE checksum IS NULL", table))
	if err != nil {
		t.Fatalf("query null count: %v", err)
	}
	if nullCount != 0 {
		t.Fatalf("expected 0 null checksums, got %d", nullCount)
	}

	// Assert: index exists
	exists, err := adapter.IndexExists(ctx, table, "idx_e2e_checksum")
	if err != nil || !exists {
		t.Fatalf("index idx_e2e_checksum not found after contract: %v", err)
	}

	// Assert: all 4 phases completed in history
	for _, phaseName := range []string{"expand", "backfill", "gate", "contract"} {
		ev, err := s.LatestEvent(ctx, m.Name, phaseName)
		if err != nil || ev == nil {
			t.Fatalf("no history for phase %q: %v", phaseName, err)
		}
		if ev.EventType != store.EventCompleted {
			t.Fatalf("phase %q: expected COMPLETED, got %v", phaseName, ev.EventType)
		}
	}

	t.Logf("all %d rows backfilled, index created, all phases COMPLETED", rows)
}

// TestFullMigration_ResumeAfterBackfillInterrupt simulates a mid-backfill
// crash by running only expand, then starting a fresh runner in resume mode.
func TestFullMigration_ResumeAfterBackfillInterrupt(t *testing.T) {
	const table = "e2e_resume_orders"
	ctx := context.Background()
	s := mustStore(t)
	adapter, err := db.NewAdapter(testDSN())
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer adapter.Close()

	_, _ = adapter.ExecDDL(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table), 5*time.Second)
	_, _ = adapter.ExecDDL(ctx, fmt.Sprintf(`CREATE TABLE %s (
		id      INT PRIMARY KEY AUTO_INCREMENT,
		user_id VARCHAR(64) NOT NULL,
		payload TEXT NOT NULL
	)`, table), 5*time.Second)
	t.Cleanup(func() {
		_, _ = adapter.ExecDDL(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table), 5*time.Second)
	})

	for i := 0; i < 100; i++ {
		q := fmt.Sprintf("INSERT INTO %s (user_id, payload) VALUES ('user%d', 'data%d')", table, i, i)
		if _, err := adapter.ExecBatch(ctx, q, 1); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	migration := "e2e_resume_test"
	expandPhase := config.Phase{
		Name: "expand",
		SQL:  fmt.Sprintf("ALTER TABLE %s ADD COLUMN checksum VARCHAR(64) NULL", table),
	}
	backfillPhase := config.Phase{
		Name: "backfill",
		Batch: &config.BatchConfig{
			Query:        fmt.Sprintf("UPDATE %s SET checksum = SHA2(CONCAT(user_id, payload), 256) WHERE checksum IS NULL LIMIT {batch_size}", table),
			Size:         10,
			DelayMs:      0,
			DoneWhen:     fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE checksum IS NULL", table),
			DoneExpected: 0,
		},
	}

	// Run only expand phase
	r1 := &runner.Runner{
		Migration: &config.MigrationFile{
			Name: migration, Database: "mysql",
			Phases: []config.Phase{expandPhase},
		},
		DB: adapter, Store: s, Version: "test",
	}
	if err := r1.Run(ctx); err != nil {
		t.Fatalf("expand run: %v", err)
	}

	// Manually insert PHASE_STARTED for backfill (simulate interrupted run)
	if err := s.InsertEvent(ctx, store.PhaseEvent{
		MigrationName:  migration,
		PhaseName:      "backfill",
		AttemptNumber:  1,
		EventType:      store.EventStarted,
		PhaseType:      "BACKFILL",
		InstalledBy:    "test-interrupt",
		PhasedbVersion: "test",
	}); err != nil {
		t.Fatalf("simulate interrupt: %v", err)
	}
	// Partially update a few rows to simulate partial backfill
	_, _ = adapter.ExecBatch(ctx, fmt.Sprintf(
		"UPDATE %s SET checksum = SHA2(CONCAT(user_id, payload), 256) WHERE checksum IS NULL LIMIT 20", table), 1)

	// Resume with full migration
	r2 := &runner.Runner{
		Migration: &config.MigrationFile{
			Name: migration, Database: "mysql",
			Phases: []config.Phase{expandPhase, backfillPhase},
		},
		DB: adapter, Store: s, Version: "test",
		ResumeMode: true,
	}
	if err := r2.Run(ctx); err != nil {
		t.Fatalf("resume run: %v", err)
	}

	// All rows should be filled
	nullCount, err := adapter.QueryScalar(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE checksum IS NULL", table))
	if err != nil || nullCount != 0 {
		t.Fatalf("expected 0 null rows after resume, got %d (err: %v)", nullCount, err)
	}

	ev, _ := s.LatestEvent(ctx, migration, "backfill")
	if ev == nil || ev.EventType != store.EventCompleted {
		t.Fatalf("expected backfill COMPLETED after resume, got %v", ev)
	}
}

// TestFullMigration_RollbackOnFailure verifies on_failure: rollback works —
// a backfill that fails triggers rollback_sql and records PHASE_ROLLED_BACK.
func TestFullMigration_RollbackOnFailure(t *testing.T) {
	const table = "e2e_rollback_orders"
	ctx := context.Background()
	s := mustStore(t)
	adapter, err := db.NewAdapter(testDSN())
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer adapter.Close()

	_, _ = adapter.ExecDDL(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table), 5*time.Second)
	_, _ = adapter.ExecDDL(ctx, fmt.Sprintf(`CREATE TABLE %s (
		id      INT PRIMARY KEY AUTO_INCREMENT,
		payload TEXT NOT NULL
	)`, table), 5*time.Second)
	t.Cleanup(func() {
		_, _ = adapter.ExecDDL(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table), 5*time.Second)
	})

	for i := 0; i < 10; i++ {
		_, _ = adapter.ExecBatch(ctx, fmt.Sprintf("INSERT INTO %s (payload) VALUES ('data%d')", table, i), 1)
	}

	m := &config.MigrationFile{
		Name:     "e2e_rollback_test",
		Database: "mysql",
		Phases: []config.Phase{
			{
				Name:      "backfill",
				OnFailure: "rollback",
				Batch: &config.BatchConfig{
					// References non-existent column → will fail
					Query:        fmt.Sprintf("UPDATE %s SET nonexistent_col = 1 WHERE nonexistent_col IS NULL LIMIT {batch_size}", table),
					Size:         10,
					DelayMs:      0,
					DoneWhen:     fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE nonexistent_col IS NULL", table),
					DoneExpected: 0,
				},
				RollbackSQL: fmt.Sprintf("SELECT 1 FROM %s LIMIT 1", table), // no-op rollback for test
			},
		},
	}

	r := &runner.Runner{Migration: m, DB: adapter, Store: s, Version: "test"}
	err = r.Run(ctx)
	if err == nil {
		t.Fatal("expected error from bad backfill query, got nil")
	}

	ev, _ := s.LatestEvent(ctx, m.Name, "backfill")
	if ev == nil {
		t.Fatal("no event recorded for backfill")
	}
	if ev.EventType != store.EventRolledBack {
		t.Fatalf("expected PHASE_ROLLED_BACK, got %v", ev.EventType)
	}
}

// TestConcurrentLock_SecondRunnerBlocked verifies that a second runner
// attempting the same migration while first holds the lock gets ErrAlreadyRunning.
func TestConcurrentLock_SecondRunnerBlocked(t *testing.T) {
	const table = "e2e_lock_test"
	ctx := context.Background()
	s := mustStore(t)
	adapter, err := db.NewAdapter(testDSN())
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer adapter.Close()

	_, _ = adapter.ExecDDL(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table), 5*time.Second)
	_, _ = adapter.ExecDDL(ctx, fmt.Sprintf("CREATE TABLE %s (id INT PRIMARY KEY)", table), 5*time.Second)
	t.Cleanup(func() {
		_, _ = adapter.ExecDDL(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table), 5*time.Second)
	})

	for i := 0; i < 200; i++ {
		_, _ = adapter.ExecBatch(ctx, fmt.Sprintf("INSERT INTO %s VALUES (%d)", table, i), 1)
	}

	migration := "e2e_concurrent_lock"
	m := &config.MigrationFile{
		Name:     migration,
		Database: "mysql",
		Phases: []config.Phase{{
			Name: "expand",
			SQL:  fmt.Sprintf("ALTER TABLE %s ADD COLUMN val INT NULL", table),
		}},
	}

	// Manually acquire lock to simulate first runner holding it
	if err := s.AcquireLock(ctx, migration, "fake-process:99999"); err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	t.Cleanup(func() { _ = s.ReleaseLock(context.Background(), migration) })

	// Inject PHASE_STARTED so runner sees RUNNING state
	_ = s.InsertEvent(ctx, store.PhaseEvent{
		MigrationName:  migration,
		PhaseName:      "expand",
		AttemptNumber:  1,
		EventType:      store.EventStarted,
		PhaseType:      "EXPAND",
		InstalledBy:    "fake-process:99999",
		PhasedbVersion: "test",
	})

	r := &runner.Runner{Migration: m, DB: adapter, Store: s, Version: "test"}
	err = r.Run(ctx)
	if err == nil {
		t.Fatal("expected ErrAlreadyRunning, got nil")
	}
	if !isAlreadyRunning(err) {
		t.Fatalf("expected ErrAlreadyRunning, got: %v", err)
	}
}

func isAlreadyRunning(err error) bool {
	return err != nil && (err.Error() == "migration already running" ||
		contains(err.Error(), "already running") ||
		contains(err.Error(), "ErrAlreadyRunning"))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
