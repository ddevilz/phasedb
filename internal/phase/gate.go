package phase

import (
	"context"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/store"
)

type GateExecutor struct {
	Phase     config.Phase
	Migration string
}

func (g *GateExecutor) Type() PhaseType { return TypeGate }

func (g *GateExecutor) Execute(ctx context.Context, adapter db.Adapter, s store.Store) error {
	return nil // TODO: implement
}

func (g *GateExecutor) Rollback(ctx context.Context, adapter db.Adapter, s store.Store) error {
	return nil
}
