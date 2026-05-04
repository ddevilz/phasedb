//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/ddevilz/phasedb/internal/store"
)

func testDSN() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "mysql://phasedb:phasedb@127.0.0.1:3306/phasedb_test"
}

func mustOpenDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", normalizeDSN(testDSN()))
	if err != nil {
		t.Fatalf("open DB: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := 0; i < 30; i++ {
		if err := db.PingContext(ctx); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustStore(t *testing.T) store.Store {
	t.Helper()
	db := mustOpenDB(t)
	s := store.NewMySQL(db)
	if err := s.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	return s
}

// normalizeDSN converts mysql:// URL to go-sql-driver format.
// IMPORTANT: multiStatements=true is never set — security invariant.
func normalizeDSN(dsn string) string {
	s := strings.TrimPrefix(dsn, "mysql://")
	atIdx := strings.LastIndex(s, "@")
	if atIdx < 0 {
		return dsn
	}
	userPass := s[:atIdx]
	hostDB := s[atIdx+1:]
	slashIdx := strings.Index(hostDB, "/")
	if slashIdx < 0 {
		return dsn
	}
	host := hostDB[:slashIdx]
	dbName := strings.Split(hostDB[slashIdx+1:], "?")[0]
	return fmt.Sprintf("%s@tcp(%s)/%s?parseTime=true&loc=UTC", userPass, host, dbName)
}
