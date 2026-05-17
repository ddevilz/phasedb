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

func TestContractPhase_RunsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t)
	adapter, err := db.NewAdapter(testDSN())
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer adapter.Close()

	_, _ = adapter.ExecDDL(ctx, "DROP TABLE IF EXISTS integration_test_contract", 5*time.Second)
	_, _ = adapter.ExecDDL(ctx, `CREATE TABLE integration_test_contract (
		id       INT PRIMARY KEY AUTO_INCREMENT,
		checksum VARCHAR(64) NULL
	)`, 5*time.Second)
	t.Cleanup(func() {
		_, _ = adapter.ExecDDL(context.Background(), "DROP INDEX IF EXISTS idx_contract_checksum ON integration_test_contract", 5*time.Second)
		_, _ = adapter.ExecDDL(context.Background(), "DROP TABLE IF EXISTS integration_test_contract", 5*time.Second)
	})

	// Seed a row with checksum set (backfill already done)
	_, _ = adapter.ExecBatch(ctx, "INSERT INTO integration_test_contract (checksum) VALUES ('abc123')", 1)

	m := &config.MigrationFile{
		Name:     "V_integration_contract",
		Database: "mysql",
		Phases: []config.Phase{{
			Name: "contract",
			SQL: `ALTER TABLE integration_test_contract MODIFY COLUMN checksum VARCHAR(64) NOT NULL;
ALTER TABLE integration_test_contract ADD INDEX idx_contract_checksum (checksum)`,
			RollbackSQL: `ALTER TABLE integration_test_contract DROP INDEX idx_contract_checksum;
ALTER TABLE integration_test_contract MODIFY COLUMN checksum VARCHAR(64) NULL`,
		}},
	}

	r := &runner.Runner{Migration: m, DB: adapter, Store: s, Version: "test"}

	// First run
	if err := r.Run(ctx); err != nil {
		t.Fatalf("contract run failed: %v", err)
	}

	// Index should exist
	exists, err := adapter.IndexExists(ctx, "integration_test_contract", "idx_contract_checksum")
	if err != nil || !exists {
		t.Fatalf("index not found after contract: %v", err)
	}

	ev, _ := s.LatestEvent(ctx, m.Name, "contract")
	if ev == nil || ev.EventType != store.EventCompleted {
		t.Fatalf("expected PHASE_COMPLETED, got %v", ev)
	}

	// Second run — idempotent
	if err := r.Run(ctx); err != nil {
		t.Fatalf("second contract run failed: %v", err)
	}
}
