package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/phase"
	"github.com/ddevilz/phasedb/internal/store"
)

type Runner struct {
	Migration  *config.MigrationFile
	DB         db.Adapter
	Store      store.Store
	ResumeMode bool
	Version    string
}

func (r *Runner) Run(ctx context.Context) error {
	if err := r.Store.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("EnsureSchema: %w", err)
	}

	executors := phase.BuildExecutors(r.Migration)
	processID := fmt.Sprintf("%s:%d", mustHostname(), os.Getpid())

	for i, ex := range executors {
		phaseName := r.Migration.Phases[i].Name // use YAML phase name (lowercase)
		if err := r.runPhase(ctx, ex, phaseName, processID); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) runPhase(ctx context.Context, ex phase.PhaseExecutor, phaseName, processID string) error {
	latest, err := r.Store.LatestEvent(ctx, r.Migration.Name, phaseName)
	if err != nil {
		return fmt.Errorf("LatestEvent: %w", err)
	}
	lock, err := r.Store.GetLock(ctx, r.Migration.Name)
	if err != nil {
		return fmt.Errorf("GetLock: %w", err)
	}

	state := DetermineState(latest, lock)

	if state == StateCompleted {
		slog.Info("phase already completed, skipping", "phase", phaseName)
		return nil
	}

	if err := CheckStateGate(state, r.ResumeMode, lock); err != nil {
		return err
	}

	// Acquire distributed lock
	if err := r.Store.AcquireLock(ctx, r.Migration.Name, processID); err != nil {
		return fmt.Errorf("AcquireLock: %w", err)
	}

	// Compute attempt number (optimistic — will be confirmed by UNIQUE KEY)
	maxAttempt, err := r.Store.MaxAttemptNumber(ctx, r.Migration.Name, phaseName)
	if err != nil {
		_ = r.Store.ReleaseLock(ctx, r.Migration.Name)
		return err
	}
	attempt := maxAttempt + 1

	// Per-phase config JSON for audit
	phaseConfigJSON := phaseConfigToJSON(r.Migration, phaseName)

	// Insert PHASE_STARTED — dup-key means this attempt was already started (resume scenario)
	startEvent := store.PhaseEvent{
		MigrationName:   r.Migration.Name,
		PhaseName:       phaseName,
		AttemptNumber:   attempt,
		EventType:       store.EventStarted,
		PhaseType:       string(ex.Type()),
		PhaseConfigJSON: phaseConfigJSON,
		InstalledBy:     currentUser(),
		PhasedbVersion:  r.Version,
	}
	if err := r.Store.InsertEvent(ctx, startEvent); err != nil {
		_ = r.Store.ReleaseLock(ctx, r.Migration.Name)
		return fmt.Errorf("insert PHASE_STARTED failed (attempt may already exist): %w", err)
	}

	// Start heartbeat goroutine with real attempt number — stopped by cancelHeartbeat on any exit
	hbCtx, cancelHeartbeat := context.WithCancel(ctx)
	defer cancelHeartbeat()
	go RunHeartbeat(hbCtx, r.Store, r.Migration.Name, phaseName, attempt, processID)

	// Execute phase
	execErr := ex.Execute(ctx, r.DB, r.Store)

	// Terminal guard — check if a terminal event already exists (e.g., context cancel)
	existing, checkErr := r.Store.LatestEventForAttempt(ctx, r.Migration.Name, phaseName, attempt)
	if checkErr != nil {
		slog.Warn("terminal guard check failed, proceeding with insert", "err", checkErr)
	}
	if existing != nil && existing.EventType.IsTerminal() {
		// Another path already inserted a terminal event — don't double-insert
		if execErr == nil {
			return nil
		}
		return execErr
	}

	// Build terminal event
	termEvent := store.PhaseEvent{
		MigrationName:   r.Migration.Name,
		PhaseName:       phaseName,
		AttemptNumber:   attempt,
		PhaseType:       string(ex.Type()),
		PhaseConfigJSON: phaseConfigJSON,
		InstalledBy:     currentUser(),
		PhasedbVersion:  r.Version,
	}

	if execErr == nil {
		termEvent.EventType = store.EventCompleted
	} else {
		msg := truncate(execErr.Error(), 4096)
		termEvent.EventType = store.EventFailed
		termEvent.ErrorMessage = &msg
	}

	if err := r.Store.InsertEvent(ctx, termEvent); err != nil {
		return fmt.Errorf("insert terminal event: %w", err)
	}

	// Auto-rollback on failure if on_failure: rollback
	if execErr != nil {
		phaseConfig := findPhase(r.Migration, phaseName)
		if phaseConfig != nil && phaseConfig.OnFailure == "rollback" {
			if rbErr := ex.Rollback(ctx, r.DB, r.Store); rbErr != nil {
				slog.Error("rollback failed", "phase", phaseName, "err", rbErr)
			}
			rbEvent := termEvent
			rbEvent.EventType = store.EventRolledBack
			if rbInsertErr := r.Store.InsertEvent(ctx, rbEvent); rbInsertErr != nil {
				slog.Error("failed to insert PHASE_ROLLED_BACK event", "phase", phaseName, "err", rbInsertErr)
			}
		}
	}

	// Release lock on success; on failure leave lock so resume can steal it
	if execErr == nil {
		_ = r.Store.ReleaseLock(ctx, r.Migration.Name)
	}

	return execErr
}

func mustHostname() string {
	h, _ := os.Hostname()
	return h
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func findPhase(m *config.MigrationFile, name string) *config.Phase {
	for i := range m.Phases {
		if m.Phases[i].Name == name {
			return &m.Phases[i]
		}
	}
	return nil
}

func phaseConfigToJSON(m *config.MigrationFile, phaseName string) string {
	for _, p := range m.Phases {
		if p.Name == phaseName {
			b, _ := json.Marshal(p)
			return string(b)
		}
	}
	return "{}"
}
