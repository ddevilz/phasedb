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

func TestGatePhase_PassesWhenConditionMet(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t)
	adapter, err := db.NewAdapter(testDSN())
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer adapter.Close()

	_, _ = adapter.ExecDDL(ctx, "DROP TABLE IF EXISTS integration_test_gate", 5*time.Second)
	_, _ = adapter.ExecDDL(ctx, `CREATE TABLE integration_test_gate (
		id  INT PRIMARY KEY AUTO_INCREMENT,
		val INT NULL
	)`, 5*time.Second)
	t.Cleanup(func() {
		_, _ = adapter.ExecDDL(context.Background(), "DROP TABLE IF EXISTS integration_test_gate", 5*time.Second)
	})

	// All rows already have val set — gate should pass immediately
	for i := 0; i < 3; i++ {
		_, _ = adapter.ExecBatch(ctx, "INSERT INTO integration_test_gate (val) VALUES (1)", 1)
	}

	m := &config.MigrationFile{
		Name:     "V_integration_gate",
		Database: "mysql",
		Phases: []config.Phase{{
			Name: "gate",
			WaitUntil: &config.GateConfig{
				Query:          "SELECT COUNT(*) FROM integration_test_gate WHERE val IS NULL",
				Expected:       0,
				PollIntervalMs: 100,
				TimeoutMinutes: 1,
			},
		}},
	}

	r := &runner.Runner{Migration: m, DB: adapter, Store: s, Version: "test"}
	if err := r.Run(ctx); err != nil {
		t.Fatalf("gate run failed: %v", err)
	}

	ev, _ := s.LatestEvent(ctx, m.Name, "gate")
	if ev == nil || ev.EventType != store.EventCompleted {
		t.Fatalf("expected PHASE_COMPLETED, got %v", ev)
	}
}

func TestGatePhase_TimesOut(t *testing.T) {
	bg := context.Background()
	s := mustStore(t)
	adapter, err := db.NewAdapter(testDSN())
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer adapter.Close()

	_, _ = adapter.ExecDDL(bg, "DROP TABLE IF EXISTS integration_test_gate_timeout", 5*time.Second)
	_, _ = adapter.ExecDDL(bg, `CREATE TABLE integration_test_gate_timeout (
		id  INT PRIMARY KEY AUTO_INCREMENT,
		val INT NULL
	)`, 5*time.Second)
	t.Cleanup(func() {
		_, _ = adapter.ExecDDL(context.Background(), "DROP TABLE IF EXISTS integration_test_gate_timeout", 5*time.Second)
	})

	// Row with val=NULL — condition never met
	_, _ = adapter.ExecBatch(bg, "INSERT INTO integration_test_gate_timeout (val) VALUES (NULL)", 1)

	m := &config.MigrationFile{
		Name:     "V_integration_gate_timeout",
		Database: "mysql",
		Phases: []config.Phase{{
			Name: "gate",
			WaitUntil: &config.GateConfig{
				Query:          "SELECT COUNT(*) FROM integration_test_gate_timeout WHERE val IS NULL",
				Expected:       0,
				PollIntervalMs: 200,
				TimeoutMinutes: 1, // minimum non-zero value
			},
		}},
	}

	// Use a short-lived context — gate executor returns ctx.Err() when cancelled,
	// runner maps context cancellation from a running gate to PHASE_TIMED_OUT
	ctx, cancel := context.WithTimeout(bg, 2*time.Second)
	defer cancel()

	r := &runner.Runner{Migration: m, DB: adapter, Store: s, Version: "test"}
	err = r.Run(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Runner records PHASE_FAILED (ctx cancelled) or PHASE_TIMED_OUT depending on impl.
	// Either way the phase must not be COMPLETED.
	ev, _ := s.LatestEvent(bg, m.Name, "gate")
	if ev == nil {
		t.Fatal("no event recorded for gate")
	}
	if ev.EventType == store.EventCompleted {
		t.Fatalf("gate should not have completed, got %v", ev.EventType)
	}
}
