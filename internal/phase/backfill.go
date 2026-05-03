package phase

import (
	"context"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/store"
)

type BackfillExecutor struct {
	Phase     config.Phase
	Migration string
}

func (b *BackfillExecutor) Type() PhaseType { return TypeBackfill }

func (b *BackfillExecutor) Execute(ctx context.Context, adapter db.Adapter, s store.Store) error {
	return nil // TODO: implement
}

func (b *BackfillExecutor) Rollback(ctx context.Context, adapter db.Adapter, s store.Store) error {
	return nil // TODO: implement
}
