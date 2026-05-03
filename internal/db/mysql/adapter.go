package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

type mysqlAdapter struct {
	db *sql.DB
}

func New(dsn string) (*mysqlAdapter, error) {
	normalized, err := normalizeDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("mysql: invalid DSN: %w", err)
	}
	db, err := sql.Open("mysql", normalized)
	if err != nil {
		return nil, fmt.Errorf("mysql: open: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	return &mysqlAdapter{db: db}, nil
}

// normalizeDSN converts mysql://user:pass@host:3306/dbname to
// user:pass@tcp(host:3306)/dbname?parseTime=true&loc=UTC
// IMPORTANT: Do NOT include multiStatements=true (security risk — injection)
func normalizeDSN(rawDSN string) (string, error) {
	s := strings.TrimPrefix(rawDSN, "mysql://")
	atIdx := strings.LastIndex(s, "@")
	if atIdx < 0 {
		return "", fmt.Errorf("missing @ in DSN")
	}
	userPass := s[:atIdx]
	hostDB := s[atIdx+1:]
	slashIdx := strings.Index(hostDB, "/")
	if slashIdx < 0 {
		return "", fmt.Errorf("missing database name in DSN")
	}
	host := hostDB[:slashIdx]
	dbName := strings.Split(hostDB[slashIdx+1:], "?")[0]
	return fmt.Sprintf("%s@tcp(%s)/%s?parseTime=true&loc=UTC", userPass, host, dbName), nil
}

func (a *mysqlAdapter) Ping(ctx context.Context) error {
	return a.db.PingContext(ctx)
}

func (a *mysqlAdapter) Close() error {
	return a.db.Close()
}

func (a *mysqlAdapter) ExecBatch(ctx context.Context, query string, batchSize int) (int64, error) {
	q := strings.ReplaceAll(query, "{batch_size}", fmt.Sprintf("%d", batchSize))
	res, err := a.db.ExecContext(ctx, q)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (a *mysqlAdapter) QueryScalar(ctx context.Context, query string, args ...any) (int64, error) {
	var val int64
	if err := a.db.QueryRowContext(ctx, query, args...).Scan(&val); err != nil {
		return 0, err
	}
	return val, nil
}

func (a *mysqlAdapter) GetTableRowCount(ctx context.Context, table string) (int64, error) {
	return a.QueryScalar(ctx, fmt.Sprintf("SELECT COUNT(*) FROM `%s`", table))
}

func (a *mysqlAdapter) GetServerVersion(ctx context.Context) (string, error) {
	var v string
	if err := a.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&v); err != nil {
		return "", err
	}
	return v, nil
}
