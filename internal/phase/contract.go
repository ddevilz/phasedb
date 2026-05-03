package phase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/store"
)

type ContractExecutor struct {
	Phase         config.Phase
	Migration     string
	AttemptNumber int // set by runner before Execute is called
}

func (c *ContractExecutor) Type() PhaseType { return TypeContract }

func (c *ContractExecutor) Execute(ctx context.Context, adapter db.Adapter, s store.Store) error {
	stmts := splitSQLStatements(c.Phase.SQL)

	// Find resume point from last checkpoint
	lastCP, _ := s.LatestCheckpoint(ctx, c.Migration, c.Phase.Name, c.AttemptNumber)
	resumeFrom := 0
	if lastCP != nil {
		resumeFrom = lastCP.StatementIndex + 1
	}

	realIdx := 0
	for _, raw := range stmts {
		stmt := strings.TrimSpace(raw)
		if stmt == "" {
			continue // don't count empty statements
		}
		if realIdx < resumeFrom {
			realIdx++
			continue
		}

		if _, err := adapter.ExecDDL(ctx, stmt, defaultDDLLockTimeout); err != nil {
			return fmt.Errorf("contract statement %d: %w", realIdx, err)
		}

		// Checkpoint after each statement
		cp := map[string]int{"statement_index": realIdx}
		cpJSON, jsonErr := json.Marshal(cp)
		if jsonErr != nil {
			cpJSON = []byte(`{}`)
			slog.Warn("contract checkpoint marshal failed", "err", jsonErr)
		}
		if cpErr := s.InsertCheckpoint(ctx, store.CheckpointRow{
			MigrationName:  c.Migration,
			PhaseName:      c.Phase.Name,
			AttemptNumber:  c.AttemptNumber,
			StatementIndex: realIdx,
			CheckpointJSON: string(cpJSON),
		}); cpErr != nil {
			slog.Warn("contract checkpoint insert failed", "statement", realIdx, "err", cpErr)
		}
		realIdx++
	}
	return nil
}

func (c *ContractExecutor) Rollback(ctx context.Context, adapter db.Adapter, s store.Store) error {
	if c.Phase.RollbackSQL == "" {
		return nil
	}
	stmts := splitSQLStatements(c.Phase.RollbackSQL)
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := adapter.ExecDDL(ctx, stmt, defaultDDLLockTimeout); err != nil {
			return fmt.Errorf("contract rollback: %w", err)
		}
	}
	return nil
}

func splitSQLStatements(sql string) []string {
	return strings.Split(sql, ";")
}
