package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// GlobalConfig holds the top-level phasedb.yaml configuration.
type GlobalConfig struct {
	DatabaseURL string `yaml:"database_url"`
}

// ResolveDSN returns the database DSN using the following priority order:
// 1. flagVal (--db flag)
// 2. DATABASE_URL environment variable
// 3. database_url field in phasedb.yaml
// Returns an error if no DSN is found from any source.
func ResolveDSN(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if env := os.Getenv("DATABASE_URL"); env != "" {
		return env, nil
	}
	data, err := os.ReadFile("phasedb.yaml")
	if err == nil {
		var gc GlobalConfig
		if parseErr := yaml.Unmarshal(data, &gc); parseErr != nil {
			return "", fmt.Errorf("phasedb.yaml: %w", parseErr)
		}
		if gc.DatabaseURL != "" {
			return gc.DatabaseURL, nil
		}
	}
	return "", fmt.Errorf("no database URL: provide --db flag, DATABASE_URL env, or database_url in phasedb.yaml")
}
