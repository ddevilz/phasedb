package phase

import (
	"context"
	"time"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/store"
)

const defaultDDLLockTimeout = 30 * time.Second

type PhaseType string

const (
	TypeExpand   PhaseType = "EXPAND"
	TypeBackfill PhaseType = "BACKFILL"
	TypeGate     PhaseType = "GATE"
	TypeContract PhaseType = "CONTRACT"
)

type PhaseExecutor interface {
	Type() PhaseType
	// Execute runs phase logic. Returns nil on success.
	// Must NOT insert PHASE_COMPLETED/FAILED/TIMED_OUT — runner does that.
	Execute(ctx context.Context, adapter db.Adapter, s store.Store) error
	// Rollback undoes phase changes. Called only when on_failure: rollback.
	Rollback(ctx context.Context, adapter db.Adapter, s store.Store) error
}

// BuildExecutors constructs the ordered list of executors for a migration.
func BuildExecutors(m *config.MigrationFile) []PhaseExecutor {
	var exs []PhaseExecutor
	for _, p := range m.Phases {
		ph := p // capture loop variable
		switch p.Name {
		case "expand":
			exs = append(exs, &ExpandExecutor{Phase: ph, Migration: m.Name})
		case "backfill":
			exs = append(exs, &BackfillExecutor{Phase: ph, Migration: m.Name})
		case "gate":
			exs = append(exs, &GateExecutor{Phase: ph, Migration: m.Name})
		case "contract":
			exs = append(exs, &ContractExecutor{Phase: ph, Migration: m.Name})
		}
	}
	return exs
}

// BuildSingleExecutor constructs a single executor for a phase, using the given attempt number.
// This allows attempt-aware executors (backfill, contract) to use the correct checkpoint key.
func BuildSingleExecutor(p config.Phase, migrationName string, attempt int) PhaseExecutor {
	switch p.Name {
	case "expand":
		return &ExpandExecutor{Phase: p, Migration: migrationName}
	case "backfill":
		return &BackfillExecutor{Phase: p, Migration: migrationName, AttemptNumber: attempt}
	case "gate":
		return &GateExecutor{Phase: p, Migration: migrationName}
	case "contract":
		return &ContractExecutor{Phase: p, Migration: migrationName, AttemptNumber: attempt}
	}
	return nil
}
