package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrationFile represents the top-level structure of a phasedb migration YAML file.
type MigrationFile struct {
	Name     string  `yaml:"migration"`
	Database string  `yaml:"database"`
	Phases   []Phase `yaml:"phases"`
}

// ParseMigration parses and validates a migration YAML file from raw bytes.
func ParseMigration(data []byte) (*MigrationFile, error) {
	var m MigrationFile
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("yaml parse: %w", err)
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *MigrationFile) validate() error {
	if m.Name == "" {
		return fmt.Errorf("migration name is required")
	}
	if m.Database == "" {
		return fmt.Errorf("database field is required")
	}
	for i, p := range m.Phases {
		if err := validatePhase(p, i); err != nil {
			return err
		}
	}
	return nil
}

func validatePhase(p Phase, idx int) error {
	switch p.Name {
	case "expand", "contract":
		if p.SQL == "" {
			return fmt.Errorf("phase[%d] %q: sql is required", idx, p.Name)
		}
	case "gate":
		if p.WaitUntil == nil {
			return fmt.Errorf("phase[%d] %q: gate phase requires wait_until block", idx, p.Name)
		}
	case "backfill":
		if p.Batch == nil {
			return fmt.Errorf("phase[%d] %q: backfill phase requires batch block", idx, p.Name)
		}
		if !strings.Contains(p.Batch.Query, "{batch_size}") {
			return fmt.Errorf("phase[%d] %q: batch query must contain {batch_size} placeholder", idx, p.Name)
		}
		if p.Batch.Size <= 0 {
			return fmt.Errorf("phase[%d] %q: batch.size must be > 0", idx, p.Name)
		}
	}
	switch p.OnFailure {
	case "", "rollback", "none":
		// valid
	default:
		return fmt.Errorf("phase[%d] %q: on_failure must be one of: rollback, none (got %q)", idx, p.Name, p.OnFailure)
	}
	if p.OnFailure == "rollback" && p.RollbackSQL == "" {
		return fmt.Errorf("phase[%d] %q: on_failure: rollback requires rollback_sql", idx, p.Name)
	}
	return nil
}

// ApplyCLIOverrides applies CLI flag values onto the parsed migration, overriding YAML values.
// An empty CLIOverrides (zero value) is a no-op.
func (m *MigrationFile) ApplyCLIOverrides(o CLIOverrides) {
	if o.OnFailure == "" {
		return
	}
	for i := range m.Phases {
		m.Phases[i].OnFailure = o.OnFailure
	}
}
