package runner

import (
	"errors"
	"fmt"
	"time"

	"github.com/ddevilz/phasedb/internal/store"
)

var (
	ErrAlreadyRunning    = errors.New("migration already running on another process")
	ErrRequiresResume    = errors.New("migration in incomplete state; use phasedb resume")
	ErrAlreadyTerminated = errors.New("phase already has terminal event")
)

type PhaseState int

const (
	StatePending PhaseState = iota
	StateRunning
	StateCompleted
	StateFailed
	StateTimedOut
	StateRolledBack
	StateOrphaned // STARTED but no live lock
)

func DetermineState(latest *store.PhaseEvent, lock *store.LockRow) PhaseState {
	if latest == nil {
		return StatePending
	}
	switch latest.EventType {
	case store.EventCompleted:
		return StateCompleted
	case store.EventFailed:
		return StateFailed
	case store.EventTimedOut:
		return StateTimedOut
	case store.EventRolledBack:
		return StateRolledBack
	case store.EventStarted:
		if lock == nil {
			return StateOrphaned
		}
		if time.Now().UTC().After(lock.ExpiresAt) {
			return StateOrphaned // lock expired = dead process
		}
		return StateRunning
	}
	return StatePending
}

func CheckStateGate(state PhaseState, resumeMode bool, lock *store.LockRow) error {
	switch state {
	case StateCompleted:
		return nil // will be skipped
	case StateRunning:
		// lock is non-nil when DetermineState returns StateRunning
		return fmt.Errorf("%w: held by %s", ErrAlreadyRunning, lock.ProcessID)
	case StateOrphaned, StateFailed, StateTimedOut:
		if !resumeMode {
			return ErrRequiresResume
		}
		return nil
	case StateRolledBack:
		return nil // rolled-back phase can be re-run without resumeMode (state is clean)
	case StatePending:
		return nil
	}
	return nil
}
