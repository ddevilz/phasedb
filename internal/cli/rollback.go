package cli

import (
	"fmt"
	"os"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/phase"
	"github.com/ddevilz/phasedb/internal/store"
	"github.com/spf13/cobra"
)

func newRollbackCmd(version string, gf *GlobalFlags) *cobra.Command {
	var toPhase string
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Rollback a migration to a target phase",
		RunE: func(cmd *cobra.Command, args []string) error {
			if toPhase == "" {
				return fmt.Errorf("--to flag required")
			}
			if gf.Migration == "" {
				return fmt.Errorf("--migration flag required")
			}
			data, err := os.ReadFile(gf.Migration)
			if err != nil {
				return err
			}
			m, err := config.ParseMigration(data)
			if err != nil {
				return err
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
			return phase.RollbackToPhase(cmd.Context(), m, toPhase, adapter, s)
		},
	}
	cmd.Flags().StringVar(&toPhase, "to", "", "Roll back to this phase (e.g. expand)")
	return cmd
}
