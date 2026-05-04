package lint

import (
	"context"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
)

// Severity classifies a lint finding.
type Severity string

const (
	SeverityError   Severity = "ERROR"
	SeverityWarning Severity = "WARNING"
)

// Finding is a single lint result for a phase.
type Finding struct {
	Severity Severity
	Phase    string
	Message  string
}

// LintRule is implemented by each lint check.
type LintRule interface {
	Check(ctx context.Context, m *config.MigrationFile, adapter db.Adapter) []Finding
}

var registry []LintRule

// Register adds a rule to the global registry. Called from init() in rules_*.go files.
func Register(r LintRule) { registry = append(registry, r) }

// Rules returns all registered lint rules.
func Rules() []LintRule { return registry }
