package phase

import (
	"context"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/store"
)

type ContractExecutor struct {
	Phase     config.Phase
	Migration string
}

func (c *ContractExecutor) Type() PhaseType { return TypeContract }

func (c *ContractExecutor) Execute(ctx context.Context, adapter db.Adapter, s store.Store) error {
	return nil // TODO: implement
}

func (c *ContractExecutor) Rollback(ctx context.Context, adapter db.Adapter, s store.Store) error {
	return nil // TODO: implement
}
