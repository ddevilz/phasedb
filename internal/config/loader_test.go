package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ddevilz/phasedb/internal/config"
)

func TestResolveDSN_FlagTakesPriority(t *testing.T) {
	t.Setenv("DATABASE_URL", "env-dsn")
	got, err := config.ResolveDSN("flag-dsn")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got != "flag-dsn" {
		t.Errorf("expected %q, got %q", "flag-dsn", got)
	}
}

func TestResolveDSN_EnvFallback(t *testing.T) {
	t.Setenv("DATABASE_URL", "env-dsn")
	got, err := config.ResolveDSN("")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got != "env-dsn" {
		t.Errorf("expected %q, got %q", "env-dsn", got)
	}
}

func TestResolveDSN_NoSource(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	// Change to a temp dir so there is no phasedb.yaml present.
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	_, err = config.ResolveDSN("")
	if err == nil {
		t.Fatal("expected error when no DSN source is available, got nil")
	}
	if !strings.Contains(err.Error(), "no database URL") {
		t.Errorf("expected 'no database URL' error, got: %v", err)
	}
}

func TestResolveDSN_MalformedYAML(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "phasedb.yaml"), []byte("not: [valid: yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err = config.ResolveDSN("")
	if err == nil {
		t.Fatal("expected parse error for malformed phasedb.yaml, got nil")
	}
	if !strings.Contains(err.Error(), "phasedb.yaml") {
		t.Errorf("expected error to mention 'phasedb.yaml', got: %v", err)
	}
}
