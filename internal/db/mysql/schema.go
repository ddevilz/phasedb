package mysql

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ddevilz/phasedb/internal/db/dbtypes"
)

func (a *mysqlAdapter) ColumnExists(ctx context.Context, table, column string) (bool, error) {
	q := `SELECT COUNT(*) FROM information_schema.COLUMNS
          WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`
	n, err := a.QueryScalar(ctx, q, table, column)
	return n > 0, err
}

func (a *mysqlAdapter) GetColumnDefinition(ctx context.Context, table, column string) (*dbtypes.ColumnDef, error) {
	q := `SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT
          FROM information_schema.COLUMNS
          WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`
	row := a.db.QueryRowContext(ctx, q, table, column)
	var cd dbtypes.ColumnDef
	var nullable string
	var def sql.NullString
	if err := row.Scan(&cd.Name, &cd.DataType, &nullable, &def); err != nil {
		return nil, fmt.Errorf("GetColumnDefinition %s.%s: %w", table, column, err)
	}
	cd.IsNullable = nullable == "YES"
	if def.Valid {
		cd.Default = &def.String
	}
	return &cd, nil
}

func (a *mysqlAdapter) IndexExists(ctx context.Context, table, index string) (bool, error) {
	q := `SELECT COUNT(*) FROM information_schema.STATISTICS
          WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = ?`
	n, err := a.QueryScalar(ctx, q, table, index)
	return n > 0, err
}

func (a *mysqlAdapter) RunEXPLAIN(ctx context.Context, query string) (*dbtypes.EXPLAINResult, error) {
	rows, err := a.db.QueryContext(ctx, "EXPLAIN "+query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	vals := make([]sql.RawBytes, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	colIdx := -1
	for i, c := range cols {
		if c == "type" {
			colIdx = i
		}
	}
	var accessType string
	for rows.Next() {
		if err := rows.Scan(ptrs...); err == nil && colIdx >= 0 {
			accessType = string(vals[colIdx])
		}
	}
	return &dbtypes.EXPLAINResult{AccessType: accessType}, nil
}
