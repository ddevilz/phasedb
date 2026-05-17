package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	lockTTL = 90 * time.Second
)

type mysqlStore struct {
	db    *sql.DB
	ownDB bool // true when this store opened the *sql.DB and owns its Close
}

// NewMySQL creates a Store from an existing *sql.DB (used by the runner which already has a connection).
// Close is a no-op because the caller owns the *sql.DB lifecycle.
func NewMySQL(db *sql.DB) Store {
	return &mysqlStore{db: db, ownDB: false}
}

// NewMySQLFromDSN opens a new *sql.DB from a DSN string and returns a Store.
// It normalizes mysql:// URL format to go-sql-driver format with UTC enforced.
func NewMySQLFromDSN(dsn string) (Store, error) {
	normalized := normalizeMySQLDSN(dsn)
	db, err := sql.Open("mysql", normalized)
	if err != nil {
		return nil, fmt.Errorf("store: open DB: %w", err)
	}
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(1)
	return &mysqlStore{db: db, ownDB: true}, nil
}

// Close closes the underlying *sql.DB if this store opened it.
func (s *mysqlStore) Close() error {
	if s.ownDB {
		return s.db.Close()
	}
	return nil
}

// normalizeMySQLDSN converts mysql:// URL to go-sql-driver format with UTC enforced.
// Existing query parameters are preserved; parseTime and loc are always overridden to true/UTC.
func normalizeMySQLDSN(rawDSN string) string {
	s := strings.TrimPrefix(rawDSN, "mysql://")
	atIdx := strings.LastIndex(s, "@")
	if atIdx < 0 {
		return rawDSN // not parseable, pass through
	}
	userPass := s[:atIdx]
	hostDB := s[atIdx+1:]
	slashIdx := strings.Index(hostDB, "/")
	if slashIdx < 0 {
		return rawDSN
	}
	host := hostDB[:slashIdx]
	rest := hostDB[slashIdx+1:]

	// Parse existing query params
	parts := strings.SplitN(rest, "?", 2)
	dbName := parts[0]
	existing := url.Values{}
	if len(parts) == 2 {
		existing, _ = url.ParseQuery(parts[1])
	}

	// Enforce required params (override caller values)
	existing.Set("parseTime", "true")
	existing.Set("loc", "UTC")

	return fmt.Sprintf("%s@tcp(%s)/%s?%s", userPass, host, dbName, existing.Encode())
}

// EnsureSchema creates the phasedb tables if they do not exist.
// Statements are split on ';' and executed individually (no multiStatements).
func (s *mysqlStore) EnsureSchema(ctx context.Context) error {
	stmts := strings.Split(mysqlSchema, ";")
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: EnsureSchema: %w", err)
		}
	}
	return nil
}

// InsertEvent inserts a PhaseEvent into phasedb_history.
func (s *mysqlStore) InsertEvent(ctx context.Context, e PhaseEvent) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO PHASEDB_HISTORY
			(MIGRATION_NAME, PHASE_NAME, ATTEMPT_NUMBER, EVENT_TYPE, PHASE_TYPE,
			 PHASE_CONFIG_JSON, ROWS_AFFECTED, ERROR_MESSAGE, INSTALLED_BY, PHASEDB_VERSION)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.MigrationName, e.PhaseName, e.AttemptNumber, e.EventType, e.PhaseType,
		e.PhaseConfigJSON, e.RowsAffected, e.ErrorMessage, e.InstalledBy, e.PhasedbVersion,
	)
	if err != nil {
		return fmt.Errorf("store: InsertEvent: %w", err)
	}
	return nil
}

// LatestEvent returns the most recent event for a given migration and phase.
func (s *mysqlStore) LatestEvent(ctx context.Context, migration, phase string) (*PhaseEvent, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT ID, MIGRATION_NAME, PHASE_NAME, ATTEMPT_NUMBER, EVENT_TYPE, PHASE_TYPE,
		       PHASE_CONFIG_JSON, ROWS_AFFECTED, ERROR_MESSAGE, INSTALLED_BY, PHASEDB_VERSION, CREATED_AT
		FROM PHASEDB_HISTORY
		WHERE MIGRATION_NAME = ? AND PHASE_NAME = ?
		ORDER BY ID DESC
		LIMIT 1`,
		migration, phase,
	)
	e, err := scanEvent(row)
	if err != nil {
		return nil, fmt.Errorf("store: LatestEvent: %w", err)
	}
	return e, nil
}

// LatestEventForAttempt returns the most recent event for a given migration, phase, and attempt number.
func (s *mysqlStore) LatestEventForAttempt(ctx context.Context, migration, phase string, attempt int) (*PhaseEvent, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT ID, MIGRATION_NAME, PHASE_NAME, ATTEMPT_NUMBER, EVENT_TYPE, PHASE_TYPE,
		       PHASE_CONFIG_JSON, ROWS_AFFECTED, ERROR_MESSAGE, INSTALLED_BY, PHASEDB_VERSION, CREATED_AT
		FROM PHASEDB_HISTORY
		WHERE MIGRATION_NAME = ? AND PHASE_NAME = ? AND ATTEMPT_NUMBER = ?
		ORDER BY ID DESC
		LIMIT 1`,
		migration, phase, attempt,
	)
	e, err := scanEvent(row)
	if err != nil {
		return nil, fmt.Errorf("store: LatestEventForAttempt: %w", err)
	}
	return e, nil
}

// MaxAttemptNumber returns the highest attempt_number recorded for a migration/phase pair.
// Returns 0 if no attempts exist.
func (s *mysqlStore) MaxAttemptNumber(ctx context.Context, migration, phase string) (int, error) {
	var max sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT MAX(ATTEMPT_NUMBER)
		FROM PHASEDB_HISTORY
		WHERE MIGRATION_NAME = ? AND PHASE_NAME = ?`,
		migration, phase,
	).Scan(&max)
	if err != nil {
		return 0, fmt.Errorf("store: MaxAttemptNumber: %w", err)
	}
	if !max.Valid {
		return 0, nil
	}
	return int(max.Int64), nil
}

// InsertCheckpoint inserts a checkpoint row into phasedb_checkpoints.
func (s *mysqlStore) InsertCheckpoint(ctx context.Context, c CheckpointRow) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO PHASEDB_CHECKPOINTS
			(MIGRATION_NAME, PHASE_NAME, ATTEMPT_NUMBER, STATEMENT_INDEX, CHECKPOINT_JSON)
		VALUES (?, ?, ?, ?, ?)`,
		c.MigrationName, c.PhaseName, c.AttemptNumber, c.StatementIndex, c.CheckpointJSON,
	)
	if err != nil {
		return fmt.Errorf("store: InsertCheckpoint: %w", err)
	}
	return nil
}

// LatestCheckpoint returns the most recent checkpoint for a given migration, phase, and attempt.
func (s *mysqlStore) LatestCheckpoint(ctx context.Context, migration, phase string, attempt int) (*CheckpointRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT ID, MIGRATION_NAME, PHASE_NAME, ATTEMPT_NUMBER, STATEMENT_INDEX, CHECKPOINT_JSON, CREATED_AT
		FROM PHASEDB_CHECKPOINTS
		WHERE MIGRATION_NAME = ? AND PHASE_NAME = ? AND ATTEMPT_NUMBER = ?
		ORDER BY ID DESC
		LIMIT 1`,
		migration, phase, attempt,
	)
	var c CheckpointRow
	err := row.Scan(
		&c.ID, &c.MigrationName, &c.PhaseName, &c.AttemptNumber,
		&c.StatementIndex, &c.CheckpointJSON, &c.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: LatestCheckpoint: %w", err)
	}
	return &c, nil
}

// InsertHeartbeat records a heartbeat for a running phase.
func (s *mysqlStore) InsertHeartbeat(ctx context.Context, migration, phase string, attempt int, processID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO PHASEDB_HEARTBEATS (MIGRATION_NAME, PHASE_NAME, ATTEMPT_NUMBER, PROCESS_ID)
		VALUES (?, ?, ?, ?)`,
		migration, phase, attempt, processID,
	)
	if err != nil {
		return fmt.Errorf("store: InsertHeartbeat: %w", err)
	}
	return nil
}

// DeleteHeartbeatsForCompletedMigrations removes heartbeat rows for phases that are no longer running.
func (s *mysqlStore) DeleteHeartbeatsForCompletedMigrations(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE h FROM PHASEDB_HEARTBEATS h
		WHERE NOT EXISTS (
			SELECT 1 FROM PHASEDB_HISTORY ph
			WHERE ph.MIGRATION_NAME = h.MIGRATION_NAME
			  AND ph.PHASE_NAME = h.PHASE_NAME
			  AND ph.EVENT_TYPE = 'PHASE_STARTED'
			  AND NOT EXISTS (
				SELECT 1 FROM PHASEDB_HISTORY ph2
				WHERE ph2.MIGRATION_NAME = ph.MIGRATION_NAME
				  AND ph2.PHASE_NAME = ph.PHASE_NAME
				  AND ph2.ATTEMPT_NUMBER = ph.ATTEMPT_NUMBER
				  AND ph2.EVENT_TYPE IN ('PHASE_COMPLETED','PHASE_FAILED','PHASE_TIMED_OUT','PHASE_ROLLED_BACK')
				  AND ph2.ID > ph.ID
			  )
		)`)
	if err != nil {
		return 0, fmt.Errorf("store: DeleteHeartbeatsForCompletedMigrations: %w", err)
	}
	return res.RowsAffected()
}

// AcquireLock attempts to acquire a distributed lock for a migration.
// If the lock is held and not expired, returns ErrLockHeld.
// Uses an atomic INSERT + conditional UPDATE on duplicate key (error 1062).
func (s *mysqlStore) AcquireLock(ctx context.Context, migration, processID string) error {
	now := time.Now().UTC()
	expiresAt := now.Add(lockTTL)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO PHASEDB_LOCKS (MIGRATION_NAME, PROCESS_ID, ACQUIRED_AT, EXPIRES_AT)
		VALUES (?, ?, ?, ?)`,
		migration, processID, now, expiresAt,
	)
	if err == nil {
		return nil
	}

	if !isDuplicateKeyError(err) {
		return fmt.Errorf("store: AcquireLock: %w", err)
	}

	// Lock row exists — attempt a steal if the existing lock has expired.
	res, err := s.db.ExecContext(ctx, `
		UPDATE PHASEDB_LOCKS
		SET PROCESS_ID = ?, ACQUIRED_AT = ?, EXPIRES_AT = ?
		WHERE MIGRATION_NAME = ? AND EXPIRES_AT < NOW(3)`,
		processID, now, expiresAt, migration,
	)
	if err != nil {
		return fmt.Errorf("store: AcquireLock (steal): %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: AcquireLock (rows affected): %w", err)
	}
	if rows == 0 {
		return ErrLockHeld
	}
	return nil
}

// RefreshLock extends the TTL of an existing lock held by the given processID.
func (s *mysqlStore) RefreshLock(ctx context.Context, migration, processID string) error {
	newExpiry := time.Now().UTC().Add(lockTTL)
	res, err := s.db.ExecContext(ctx, `
		UPDATE PHASEDB_LOCKS
		SET EXPIRES_AT = ?
		WHERE MIGRATION_NAME = ? AND PROCESS_ID = ?`,
		newExpiry, migration, processID,
	)
	if err != nil {
		return fmt.Errorf("store: RefreshLock: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: RefreshLock (rows affected): %w", err)
	}
	if rows == 0 {
		return ErrLockLost
	}
	return nil
}

// ReleaseLock deletes the lock row for a migration.
func (s *mysqlStore) ReleaseLock(ctx context.Context, migration string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM PHASEDB_LOCKS WHERE MIGRATION_NAME = ?`,
		migration,
	)
	if err != nil {
		return fmt.Errorf("store: ReleaseLock: %w", err)
	}
	return nil
}

// GetLock returns the current lock row for a migration, or nil if no lock exists.
func (s *mysqlStore) GetLock(ctx context.Context, migration string) (*LockRow, error) {
	var l LockRow
	err := s.db.QueryRowContext(ctx, `
		SELECT MIGRATION_NAME, PROCESS_ID, ACQUIRED_AT, EXPIRES_AT
		FROM PHASEDB_LOCKS
		WHERE MIGRATION_NAME = ?`,
		migration,
	).Scan(&l.MigrationName, &l.ProcessID, &l.AcquiredAt, &l.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: GetLock: %w", err)
	}
	return &l, nil
}

// scanEvent scans a *sql.Row into a PhaseEvent, handling nullable columns safely.
func scanEvent(row *sql.Row) (*PhaseEvent, error) {
	var e PhaseEvent
	var rowsAffected sql.NullInt64
	var errorMessage sql.NullString
	err := row.Scan(
		&e.ID, &e.MigrationName, &e.PhaseName, &e.AttemptNumber,
		&e.EventType, &e.PhaseType, &e.PhaseConfigJSON,
		&rowsAffected, &errorMessage,
		&e.InstalledBy, &e.PhasedbVersion, &e.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if rowsAffected.Valid {
		e.RowsAffected = &rowsAffected.Int64
	}
	if errorMessage.Valid {
		e.ErrorMessage = &errorMessage.String
	}
	return &e, nil
}

// isDuplicateKeyError returns true if the error is a MySQL duplicate key error (1062).
// Uses errors.As so wrapped errors are handled correctly.
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
