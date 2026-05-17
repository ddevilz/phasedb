package phase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
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
	LastPK             int64  `json:"last_pk"`
	CheckpointedAt     string `json:"checkpointed_at"`
}

func (b *BackfillExecutor) Execute(ctx context.Context, adapter db.Adapter, s store.Store) error {
	cfg := b.Phase.Batch
	delay := time.Duration(cfg.DelayMs) * time.Millisecond

	// Normalize checkpointEvery: 0 (YAML field absent) → 1 (checkpoint every batch)
	checkpointEvery := cfg.CheckpointEvery
	if checkpointEvery <= 0 {
		checkpointEvery = 1
	}

	// Initial done_when check
	nullAtStart, err := adapter.QueryScalar(ctx, cfg.DoneWhen)
	if err != nil {
		return fmt.Errorf("backfill: done_when initial check: %w", err)
	}

	// Warn when pk_column is set without pk_cursor_query
	if cfg.PKColumn != "" && cfg.PKCursorQuery == "" {
		slog.Warn("backfill: pk_column set but pk_cursor_query is empty — pk_column ignored; set pk_cursor_query to enable PK cursor mode",
			"pk_column", cfg.PKColumn)
	}

	// Resume: load last_pk from the most recent checkpoint for this attempt
	var lastPK int64
	if cfg.PKCursorQuery != "" {
		cp, cpErr := s.LatestCheckpoint(ctx, b.Migration, b.Phase.Name, b.AttemptNumber)
		if cpErr != nil {
			return fmt.Errorf("backfill: load checkpoint: %w", cpErr)
		}
		if cp != nil {
			var saved backfillCheckpoint
			if jsonErr := json.Unmarshal([]byte(cp.CheckpointJSON), &saved); jsonErr == nil {
				lastPK = saved.LastPK
			}
		}
	}

	var batchNum int64
	var totalProcessed int64
	consecutiveLagEvents := 0
	consecutiveGoodReadings := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Replica lag throttle — skipped entirely when lag_threshold_ms == 0
		if cfg.LagThresholdMs > 0 {
			lag, lagErr := adapter.GetReplicaLag(ctx)
			if lagErr != nil {
				slog.Warn("replica lag check failed, proceeding as lag=0", "err", lagErr)
				lag = 0
			}
			if lag > int64(cfg.LagThresholdMs) {
				consecutiveLagEvents++
				consecutiveGoodReadings = 0
				backoffMs := float64(cfg.DelayMs) * math.Pow(2, float64(consecutiveLagEvents))
				if backoffMs > 60000 {
					backoffMs = 60000
				}
				slog.Warn("replica lag above threshold, throttling", "lag_ms", lag, "backoff_ms", backoffMs)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(backoffMs) * time.Millisecond):
				}
				continue
			}
			consecutiveGoodReadings++
			if consecutiveGoodReadings >= 3 {
				consecutiveLagEvents = 0
				consecutiveGoodReadings = 0
			}
		}

		// Build batch query — substitute {last_id} only when pk_cursor_query is configured
		query := cfg.Query
		if cfg.PKCursorQuery != "" {
			query = strings.ReplaceAll(query, "{last_id}", strconv.FormatInt(lastPK, 10))
		}

		// Execute batch
		affected, batchErr := adapter.ExecBatch(ctx, query, cfg.Size)
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

		if affected > 0 {
			// Advance PK cursor
			if cfg.PKCursorQuery != "" {
				cursorQ := strings.ReplaceAll(cfg.PKCursorQuery, "{last_id}", strconv.FormatInt(lastPK, 10))
				cursorQ = strings.ReplaceAll(cursorQ, "{batch_size}", strconv.Itoa(cfg.Size))
				newPK, cursorErr := adapter.QueryScalar(ctx, cursorQ)
				if cursorErr != nil {
					return fmt.Errorf("backfill: pk_cursor_query: %w", cursorErr)
				}
				lastPK = newPK
			}

			// Checkpoint — every N batches (only when rows were processed)
			if batchNum%int64(checkpointEvery) == 0 {
				cp := backfillCheckpoint{
					RowsProcessedTotal: totalProcessed,
					BatchNumber:        batchNum,
					NullRowsAtStart:    nullAtStart,
					LastBatchAffected:  affected,
					LastPK:             lastPK,
					CheckpointedAt:     time.Now().UTC().Format(time.RFC3339),
				}
				cpJSON, jsonErr := json.Marshal(cp)
				if jsonErr != nil {
					cpJSON = []byte(`{}`)
					slog.Warn("checkpoint marshal failed", "err", jsonErr)
				}
				if cpErr := s.InsertCheckpoint(ctx, store.CheckpointRow{
					MigrationName:  b.Migration,
					PhaseName:      b.Phase.Name,
					AttemptNumber:  b.AttemptNumber,
					StatementIndex: 0,
					CheckpointJSON: string(cpJSON),
				}); cpErr != nil {
					slog.Warn("backfill checkpoint insert failed", "err", cpErr)
				}
			}

			slog.Info("backfill batch", "batch", batchNum, "affected", affected, "total", totalProcessed, "last_pk", lastPK)
		}

		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
}

func (b *BackfillExecutor) Rollback(ctx context.Context, adapter db.Adapter, s store.Store) error {
	return nil // no-op; rollback_sql handled by runner if configured
}
