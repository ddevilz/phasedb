package store

import "time"

type EventType string

const (
	EventStarted    EventType = "PHASE_STARTED"
	EventCompleted  EventType = "PHASE_COMPLETED"
	EventFailed     EventType = "PHASE_FAILED"
	EventTimedOut   EventType = "PHASE_TIMED_OUT"
	EventSkipped    EventType = "PHASE_SKIPPED"
	EventRolledBack EventType = "PHASE_ROLLED_BACK"
)

func (e EventType) IsTerminal() bool {
	switch e {
	case EventCompleted, EventFailed, EventTimedOut, EventRolledBack:
		return true
	}
	return false
}

type PhaseEvent struct {
	ID              int64
	MigrationName   string
	PhaseName       string
	AttemptNumber   int
	EventType       EventType
	PhaseType       string
	PhaseConfigJSON string
	RowsAffected    *int64
	ErrorMessage    *string
	InstalledBy     string
	PhasedbVersion  string
	CreatedAt       time.Time
}

type CheckpointRow struct {
	ID             int64
	MigrationName  string
	PhaseName      string
	AttemptNumber  int
	StatementIndex int
	CheckpointJSON string
	CreatedAt      time.Time
}

type LockRow struct {
	MigrationName string
	ProcessID     string
	AcquiredAt    time.Time
	ExpiresAt     time.Time
}
