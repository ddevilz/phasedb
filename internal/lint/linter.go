package lint

import (
	"context"
	"fmt"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
)

// Linter runs all registered lint rules against a migration.
type Linter struct {
	Migration *config.MigrationFile
	Adapter   db.Adapter // may be nil for offline lint
}

// Result holds all findings from a lint run.
type Result struct {
	Findings []Finding
	HasError bool
}

// Run executes all registered lint rules and returns the aggregated result.
func (l *Linter) Run(ctx context.Context) Result {
	var result Result
	for _, rule := range Rules() {
		findings := rule.Check(ctx, l.Migration, l.Adapter)
		result.Findings = append(result.Findings, findings...)
		for _, f := range findings {
			if f.Severity == SeverityError {
				result.HasError = true
			}
		}
	}
	return result
}

// Print writes all findings to stdout in human-readable format.
func (r Result) Print() {
	for _, f := range r.Findings {
		fmt.Printf("[%s] phase=%s: %s\n", f.Severity, f.Phase, f.Message)
	}
}
