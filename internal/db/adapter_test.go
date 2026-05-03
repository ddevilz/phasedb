package db_test

import (
	"testing"

	dbpkg "github.com/ddevilz/phasedb/internal/db"
	_ "github.com/ddevilz/phasedb/internal/db/mysql"
)

// Compile-time check only — no actual DB connection needed
func TestAdapterInterface(t *testing.T) {
	t.Log("mysql.mysqlAdapter satisfies db.Adapter (checked at compile time in export_test.go)")
}

func TestNewAdapter_UnsupportedScheme(t *testing.T) {
	_, err := dbpkg.NewAdapter("postgres://localhost/mydb")
	if err == nil {
		t.Fatal("expected error for postgres:// scheme")
	}
}

func TestNewAdapter_MySQLScheme(t *testing.T) {
	// NewAdapter does sql.Open which doesn't connect — just validate no error
	a, err := dbpkg.NewAdapter("mysql://user:pass@localhost:3306/testdb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = a
}
