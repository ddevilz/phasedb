package phase

import (
	"context"
	"fmt"
	"time"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/store"
)

type ExpandExecutor struct {
	Phase     config.Phase
	Migration string
}

func (e *ExpandExecutor) Type() PhaseType { return TypeExpand }

func (e *ExpandExecutor) Execute(ctx context.Context, adapter db.Adapter, s store.Store) error {
	if e.Phase.SQL == "" {
		return fmt.Errorf("expand phase has no sql")
	}
	result, err := adapter.ExecDDL(ctx, e.Phase.SQL, 30*time.Second)
	if err != nil {
		return fmt.Errorf("expand DDL: %w", err)
	}
	_ = result // IsAlreadyApplied checked by runner if needed
	return nil
}

func (e *ExpandExecutor) Rollback(ctx context.Context, adapter db.Adapter, s store.Store) error {
	if e.Phase.RollbackSQL == "" {
		return nil
	}
	_, err := adapter.ExecDDL(ctx, e.Phase.RollbackSQL, 30*time.Second)
	return err
}
