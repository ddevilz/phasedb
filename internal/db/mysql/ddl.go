package mysql

import (
	"context"
	"fmt"
	"time"

	"github.com/ddevilz/phasedb/internal/db/dbtypes"
)

const defaultDDLLockRetries = 3

func (a *mysqlAdapter) ExecDDL(ctx context.Context, sqlStr string, lockTimeout time.Duration) (*dbtypes.DDLResult, error) {
	secs := int(lockTimeout.Seconds())
	if secs < 1 {
		secs = 30
	}
	if _, err := a.db.ExecContext(ctx, fmt.Sprintf("SET SESSION lock_wait_timeout = %d", secs)); err != nil {
		return nil, fmt.Errorf("set lock_wait_timeout: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < defaultDDLLockRetries; attempt++ {
		if _, execErr := a.db.ExecContext(ctx, sqlStr); execErr == nil {
			return &dbtypes.DDLResult{}, nil
		} else if isMySQLLockTimeout(execErr) {
			lastErr = execErr
			continue
		} else {
			return nil, execErr
		}
	}
	return nil, &dbtypes.DDLError{Err: lastErr, IsRetryable: true}
}

func isMySQLLockTimeout(err error) bool {
	if err == nil {
		return false
	}
	return containsCode(err, 1205)
}

func containsCode(err error, code uint16) bool {
	type mysqlErr interface{ Number() uint16 }
	if me, ok := err.(mysqlErr); ok {
		return me.Number() == code
	}
	return false
}
