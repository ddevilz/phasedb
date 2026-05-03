package runner

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ddevilz/phasedb/internal/store"
)

const heartbeatInterval = 30 * time.Second

func RunHeartbeat(ctx context.Context, s store.Store, migration, phase string, attempt int, processID string) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.InsertHeartbeat(ctx, migration, phase, attempt, processID); err != nil {
				slog.Warn("heartbeat insert failed", "err", err)
			}
			if err := s.RefreshLock(ctx, migration, processID); err != nil {
				if errors.Is(err, store.ErrLockLost) {
					slog.Error("lock lost during heartbeat — another process may have stolen it", "migration", migration)
					// context cancellation will propagate through the running phase
					return
				}
				slog.Warn("lock refresh failed", "err", err)
			}
		}
	}
}
