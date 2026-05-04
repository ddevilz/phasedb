package output

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ddevilz/phasedb/internal/store"
)

type StatusJSON struct {
	Migration          string   `json:"migration"`
	CurrentPhase       string   `json:"current_phase,omitempty"`
	PhaseStatus        string   `json:"phase_status"`
	BackfillProgress   *float64 `json:"backfill_progress,omitempty"`
	ProgressConfidence string   `json:"progress_confidence,omitempty"`
	NullRowsRemaining  *int64   `json:"null_rows_remaining,omitempty"`
	RowsProcessedTotal *int64   `json:"rows_processed_total,omitempty"`
	EstimatedCompletion string  `json:"estimated_completion,omitempty"`
	GateStatus         string   `json:"gate_status,omitempty"`
	AttemptNumber      int      `json:"attempt_number,omitempty"`
	RunningOn          string   `json:"running_on,omitempty"`
	Warning            string   `json:"warning,omitempty"`
	PhasedbVersion     string   `json:"phasedb_version"`
}

// PrintStatus writes the StatusJSON as indented JSON to stdout.
func PrintStatus(st *StatusJSON) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(st)
}

// BuildStatus constructs a StatusJSON by querying the store for the latest events across all phases.
// It iterates phases in canonical order and returns status for the most recently active phase.
func BuildStatus(ctx context.Context, migration string, s store.Store, version string) (*StatusJSON, error) {
	phases := []string{"expand", "backfill", "gate", "contract"}
	status := &StatusJSON{
		Migration:      migration,
		PhaseStatus:    "not_started",
		PhasedbVersion: version,
	}

	var currentPhase string
	var currentEvent *store.PhaseEvent

	for _, p := range phases {
		ev, err := s.LatestEvent(ctx, migration, p)
		if err != nil {
			return nil, fmt.Errorf("status query: %w", err)
		}
		if ev == nil {
			continue
		}
		currentPhase = p
		currentEvent = ev
	}

	if currentEvent == nil {
		return status, nil
	}

	status.CurrentPhase = currentPhase
	status.PhaseStatus = string(currentEvent.EventType)
	status.AttemptNumber = currentEvent.AttemptNumber

	// Live lock check
	lock, _ := s.GetLock(ctx, migration)
	if lock != nil {
		status.RunningOn = lock.ProcessID
		if time.Now().UTC().After(lock.ExpiresAt) {
			status.Warning = "lock is stale — process may have crashed"
		}
	} else if currentEvent.EventType == store.EventStarted {
		status.Warning = "process may be dead — no active lock found. Run phasedb resume or check liveness"
	}

	// Backfill progress from latest checkpoint
	if currentPhase == "backfill" {
		cp, _ := s.LatestCheckpoint(ctx, migration, currentPhase, currentEvent.AttemptNumber)
		if cp != nil {
			var bcp struct {
				RowsProcessedTotal int64                  `json:"rows_processed_total"`
				NullRowsAtStart    int64                  `json:"null_rows_at_start"`
			}
			var meta map[string]interface{}
			if err := json.Unmarshal([]byte(cp.CheckpointJSON), &bcp); err == nil && bcp.NullRowsAtStart > 0 {
				p := float64(bcp.RowsProcessedTotal) / float64(bcp.NullRowsAtStart)
				if p > 1.0 {
					p = 1.0 // cap at 100%
				}
				status.BackfillProgress = &p
				confidence := "estimated"
				if err2 := json.Unmarshal([]byte(cp.CheckpointJSON), &meta); err2 == nil {
					if _, hasDoneWhen := meta["done_when"]; hasDoneWhen {
						confidence = "exact"
					}
				}
				status.ProgressConfidence = confidence
				remaining := bcp.NullRowsAtStart - bcp.RowsProcessedTotal
				if remaining < 0 {
					remaining = 0
				}
				status.NullRowsRemaining = &remaining
				status.RowsProcessedTotal = &bcp.RowsProcessedTotal
			}
		}
	}

	// gate status from latest checkpoint
	if currentPhase == "gate" {
		cp, err := s.LatestCheckpoint(ctx, migration, currentPhase, currentEvent.AttemptNumber)
		if err == nil && cp != nil {
			var meta map[string]interface{}
			if jsonErr := json.Unmarshal([]byte(cp.CheckpointJSON), &meta); jsonErr == nil {
				if gs, ok := meta["gate_status"].(string); ok {
					status.GateStatus = gs
				}
			}
		}
	}

	return status, nil
}
