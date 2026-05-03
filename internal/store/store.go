package store

import (
	"context"
	"errors"
)

var (
	ErrLockHeld = errors.New("migration lock held by another process")
	ErrLockLost = errors.New("migration lock was lost (expired or stolen)")
)

type Store interface {
	InsertEvent(ctx context.Context, e PhaseEvent) error
	LatestEvent(ctx context.Context, migration, phase string) (*PhaseEvent, error)
	LatestEventForAttempt(ctx context.Context, migration, phase string, attempt int) (*PhaseEvent, error)
	MaxAttemptNumber(ctx context.Context, migration, phase string) (int, error)

	InsertCheckpoint(ctx context.Context, c CheckpointRow) error
	LatestCheckpoint(ctx context.Context, migration, phase string, attempt int) (*CheckpointRow, error)

	InsertHeartbeat(ctx context.Context, migration, phase string, attempt int, processID string) error
	DeleteHeartbeatsForCompletedMigrations(ctx context.Context) (int64, error)

	AcquireLock(ctx context.Context, migration, processID string) error
	RefreshLock(ctx context.Context, migration, processID string) error
	ReleaseLock(ctx context.Context, migration string) error
	GetLock(ctx context.Context, migration string) (*LockRow, error)

	EnsureSchema(ctx context.Context) error
}
