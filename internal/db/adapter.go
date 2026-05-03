package db

import (
	"context"
	"time"

	"github.com/ddevilz/phasedb/internal/db/dbtypes"
)

// Re-export types from dbtypes so callers can use db.DDLResult etc.
type DDLResult = dbtypes.DDLResult
type ColumnDef = dbtypes.ColumnDef
type EXPLAINResult = dbtypes.EXPLAINResult
type DDLError = dbtypes.DDLError

type Adapter interface {
	ExecDDL(ctx context.Context, sql string, lockTimeout time.Duration) (*DDLResult, error)
	ExecBatch(ctx context.Context, query string, batchSize int) (int64, error)
	QueryScalar(ctx context.Context, query string, args ...any) (int64, error)
	ColumnExists(ctx context.Context, table, column string) (bool, error)
	GetColumnDefinition(ctx context.Context, table, column string) (*ColumnDef, error)
	IndexExists(ctx context.Context, table, index string) (bool, error)
	GetTableRowCount(ctx context.Context, table string) (int64, error)
	GetServerVersion(ctx context.Context) (string, error)
	GetReplicaLag(ctx context.Context) (int64, error)
	RunEXPLAIN(ctx context.Context, query string) (*EXPLAINResult, error)
	Ping(ctx context.Context) error
	Close() error
}
