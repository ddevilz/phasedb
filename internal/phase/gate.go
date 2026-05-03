package phase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/store"
)

var ErrGateTimeout = errors.New("gate timed out")

type GateExecutor struct {
	Phase     config.Phase
	Migration string
}

func (g *GateExecutor) Type() PhaseType { return TypeGate }

func (g *GateExecutor) Execute(ctx context.Context, adapter db.Adapter, s store.Store) error {
	cfg := g.Phase.WaitUntil
	timeout := time.Duration(cfg.TimeoutMinutes) * time.Minute
	if timeout == 0 {
		timeout = 120 * time.Minute
	}
	pollInterval := time.Duration(cfg.PollIntervalMs) * time.Millisecond
	if pollInterval == 0 {
		pollInterval = 5 * time.Second
	}

	deadline := time.Now().Add(timeout)
	var nullRowsAtStart int64
	var gotStart bool
	consecutiveErrors := 0

	for {
		if time.Now().After(deadline) {
			return ErrGateTimeout
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		result, err := adapter.QueryScalar(ctx, cfg.Query)
		if err != nil {
			if isTransientMySQLError(err) {
				consecutiveErrors++
				if consecutiveErrors >= 5 {
					return fmt.Errorf("gate: 5 consecutive transient errors: %w", err)
				}
				backoff := pollInterval * time.Duration(1<<consecutiveErrors)
				if backoff > 60*time.Second {
					backoff = 60 * time.Second
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(backoff):
				}
				continue
			}
			return fmt.Errorf("gate: fatal query error: %w", err)
		}
		consecutiveErrors = 0

		if !gotStart {
			nullRowsAtStart = result
			gotStart = true
		}

		if result == cfg.Expected {
			return nil // runner inserts PHASE_COMPLETED
		}

		progress := float64(0)
		if nullRowsAtStart > 0 {
			progress = float64(nullRowsAtStart-result) / float64(nullRowsAtStart)
		}
		slog.Info("gate polling",
			"current", result,
			"expected", cfg.Expected,
			"progress", fmt.Sprintf("%.1f%%", progress*100),
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func (g *GateExecutor) Rollback(ctx context.Context, adapter db.Adapter, s store.Store) error {
	// Gate phases are read-only; rollback is intentionally a no-op.
	return nil
}

func isTransientMySQLError(err error) bool {
	type mysqlNum interface{ Number() uint16 }
	var me mysqlNum
	if errors.As(err, &me) {
		switch me.Number() {
		case 1040, // too many connections
			1205, // lock wait timeout
			2003, // can't connect
			2006: // server gone away
			return true
		}
	}
	return false
}
