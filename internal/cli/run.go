package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/runner"
	"github.com/ddevilz/phasedb/internal/store"
	"github.com/spf13/cobra"
)

func newRunCmd(version string, gf *GlobalFlags) *cobra.Command {
	var onFailure string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a migration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigration(cmd.Context(), gf, version, false, onFailure, dryRun)
		},
	}
	cmd.Flags().StringVar(&onFailure, "on-failure", "", "Action on failure: rollback|\"\"")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate and estimate without executing")
	return cmd
}

func runMigration(ctx context.Context, gf *GlobalFlags, version string, resumeMode bool, onFailure string, dryRun bool) error {
	if gf.Migration == "" {
		return fmt.Errorf("--migration flag required")
	}
	data, err := os.ReadFile(gf.Migration)
	if err != nil {
		return fmt.Errorf("read migration file: %w", err)
	}
	m, err := config.ParseMigration(data)
	if err != nil {
		return err
	}
	if onFailure != "" {
		if onFailure != "rollback" && onFailure != "none" {
			return fmt.Errorf("--on-failure must be rollback or none, got %q", onFailure)
		}
		m.ApplyCLIOverrides(config.CLIOverrides{OnFailure: onFailure})
	}
	if dryRun {
		fmt.Printf("dry-run: migration %q validated, %d phases\n", m.Name, len(m.Phases))
		return nil
	}
	dsn, err := config.ResolveDSN(gf.DB)
	if err != nil {
		return err
	}
	adapter, err := db.NewAdapter(dsn)
	if err != nil {
		return err
	}
	defer adapter.Close()

	s, err := store.NewMySQLFromDSN(dsn)
	if err != nil {
		return err
	}
	defer s.Close()

	r := &runner.Runner{
		Migration:  m,
		DB:         adapter,
		Store:      s,
		ResumeMode: resumeMode,
		Version:    version,
	}
	return r.Run(ctx)
}
