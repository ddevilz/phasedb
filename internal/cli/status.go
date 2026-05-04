package cli

import (
	"fmt"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/output"
	"github.com/ddevilz/phasedb/internal/store"
	"github.com/spf13/cobra"
)

func newStatusCmd(version string, gf *GlobalFlags) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show migration status",
		RunE: func(cmd *cobra.Command, args []string) error {
			if gf.Migration == "" {
				return fmt.Errorf("--migration flag required")
			}
			dsn, err := config.ResolveDSN(gf.DB)
			if err != nil {
				return err
			}
			s, err := store.NewMySQLFromDSN(dsn)
			if err != nil {
				return err
			}
			defer s.Close()
			st, err := output.BuildStatus(cmd.Context(), gf.Migration, s, version)
			if err != nil {
				return err
			}
			if format == "json" {
				return output.PrintStatus(st)
			}
			fmt.Printf("migration: %s\nphase: %s\nstatus: %s\n",
				st.Migration, st.CurrentPhase, st.PhaseStatus)
			if st.Warning != "" {
				fmt.Printf("warning: %s\n", st.Warning)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text|json")
	return cmd
}
