package config

// Phase represents a single migration phase in a migration file.
type Phase struct {
	Name        string       `yaml:"name"`
	SQL         string       `yaml:"sql"`
	RollbackSQL string       `yaml:"rollback_sql"`
	OnFailure   string       `yaml:"on_failure"` // "rollback" | ""
	Batch       *BatchConfig `yaml:"batch"`
	WaitUntil   *GateConfig  `yaml:"wait_until"`
}

// BatchConfig holds configuration for batch-based data backfill phases.
type BatchConfig struct {
	Query          string `yaml:"query"`
	Size           int    `yaml:"size"`
	DelayMs        int    `yaml:"delay_ms"`
	LagThresholdMs int    `yaml:"lag_threshold_ms"`
	PKColumn       string `yaml:"pk_column"`
	PKCursorQuery  string `yaml:"pk_cursor_query"`
	CheckpointEvery int   `yaml:"checkpoint_every"`
	DoneWhen       string `yaml:"done_when"`
	DoneExpected   int64  `yaml:"done_expected"`
}

// GateConfig holds configuration for gate phases that wait on a condition.
type GateConfig struct {
	Query          string `yaml:"query"`
	Expected       int64  `yaml:"expected"`
	PollIntervalMs int    `yaml:"poll_interval_ms"`
	TimeoutMinutes int    `yaml:"timeout_minutes"`
}

// CLIOverrides holds values passed via CLI flags that take precedence over YAML config.
type CLIOverrides struct {
	OnFailure string // "" means no override
}
