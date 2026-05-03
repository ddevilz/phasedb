package phase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/store"
)

type BackfillExecutor struct {
	Phase         config.Phase
	Migration     string
	AttemptNumber int // set by runner before Execute is called
}

func (b *BackfillExecutor) Type() PhaseType { return TypeBackfill }

type backfillCheckpoint struct {
	RowsProcessedTotal int64  `json:"rows_processed_total"`
	BatchNumber        int64  `json:"batch_number"`
	NullRowsAtStart    int64  `json:"null_rows_at_start"`
	LastBatchAffected  int64  `json:"last_batch_affected"`
	CheckpointedAt     string `json:"checkpointed_at"`
}

func (b *BackfillExecutor) Execute(ctx context.Context, adapter db.Adapter, s store.Store) error {
	cfg := b.Phase.Batch
	delay := time.Duration(cfg.DelayMs) * time.Millisecond

	// Initial done_when check
	nullAtStart, err := adapter.QueryScalar(ctx, cfg.DoneWhen)
	if err != nil {
		return fmt.Errorf("backfill: done_when initial check: %w", err)
	}

	// max_pk_at_start — only supported when a clean table name is available
	// PKColumn is set but we cannot reliably parse the table name from the query without a SQL parser
	// This is deferred to the lint/estimate phase which has access to the migration's target table
	var maxPKAtStart *int64
	_ = maxPKAtStart // used in checkpoint below

	var batchNum int64
	var totalProcessed int64
	consecutiveLagEvents := 0
	consecutiveGoodReadings := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Replica lag throttle
		lag, lagErr := adapter.GetReplicaLag(ctx)
		if lagErr != nil {
			slog.Warn("replica lag check failed, proceeding as lag=0", "err", lagErr)
			lag = 0
		}
		lagThreshold := int64(cfg.LagThresholdMs)
		if lagThreshold > 0 && lag > lagThreshold {
			consecutiveLagEvents++
			consecutiveGoodReadings = 0
			backoffMs := float64(cfg.DelayMs) * math.Pow(2, float64(consecutiveLagEvents))
			if backoffMs > 60000 {
				backoffMs = 60000
			}
			slog.Warn("replica lag above threshold, throttling", "lag_ms", lag, "backoff_ms", backoffMs)
			time.Sleep(time.Duration(backoffMs) * time.Millisecond)
			continue
		}
		consecutiveGoodReadings++
		if consecutiveGoodReadings >= 3 {
			consecutiveLagEvents = 0
			consecutiveGoodReadings = 0
		}

		// Execute batch
		affected, batchErr := adapter.ExecBatch(ctx, cfg.Query, cfg.Size)
		if batchErr != nil {
			return fmt.Errorf("backfill batch: %w", batchErr)
		}
		batchNum++
		totalProcessed += affected

		if affected == 0 {
			// Check explicit done_when predicate
			remaining, doneErr := adapter.QueryScalar(ctx, cfg.DoneWhen)
			if doneErr != nil {
				return fmt.Errorf("backfill: done_when check: %w", doneErr)
			}
			if remaining == cfg.DoneExpected {
				return nil // done — runner inserts PHASE_COMPLETED
			}
			slog.Info("backfill: batch empty but done_when not satisfied, continuing",
				"remaining", remaining, "expected", cfg.DoneExpected)
		}

		// Checkpoint
		cp := backfillCheckpoint{
			RowsProcessedTotal: totalProcessed,
			BatchNumber:        batchNum,
			NullRowsAtStart:    nullAtStart,
			LastBatchAffected:  affected,
			CheckpointedAt:     time.Now().UTC().Format(time.RFC3339),
		}
		cpJSON, _ := json.Marshal(cp)
		if cpErr := s.InsertCheckpoint(ctx, store.CheckpointRow{
			MigrationName:  b.Migration,
			PhaseName:      b.Phase.Name,
			AttemptNumber:  b.AttemptNumber,
			StatementIndex: 0,
			CheckpointJSON: string(cpJSON),
		}); cpErr != nil {
			slog.Warn("backfill checkpoint insert failed", "err", cpErr)
		}

		if affected > 0 {
			slog.Info("backfill batch", "batch", batchNum, "affected", affected, "total", totalProcessed)
		}

		if delay > 0 {
			time.Sleep(delay)
		}
	}
}

func (b *BackfillExecutor) Rollback(ctx context.Context, adapter db.Adapter, s store.Store) error {
	return nil // no-op; rollback_sql handled by runner if configured
}
