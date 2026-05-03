package phase

import (
	"context"
	"fmt"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/store"
)

// RollbackToPhase rolls back all phases after targetPhase in reverse order.
func RollbackToPhase(ctx context.Context, m *config.MigrationFile, targetPhase string, adapter db.Adapter, s store.Store) error {
	exs := BuildExecutors(m)
	targetIdx := -1
	for i, ex := range exs {
		if string(ex.Type()) == targetPhase || m.Phases[i].Name == targetPhase {
			targetIdx = i
		}
	}
	if targetIdx < 0 {
		return fmt.Errorf("phase %q not found in migration", targetPhase)
	}
	for i := len(exs) - 1; i > targetIdx; i-- {
		if err := exs[i].Rollback(ctx, adapter, s); err != nil {
			return fmt.Errorf("rollback phase %d: %w", i, err)
		}
	}
	return nil
}
