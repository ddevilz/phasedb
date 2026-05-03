package phase

import (
	"context"

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
	return nil // TODO: implement
}

func (e *ExpandExecutor) Rollback(ctx context.Context, adapter db.Adapter, s store.Store) error {
	return nil // TODO: implement
}
