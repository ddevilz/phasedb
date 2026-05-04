package cli

import (
	"fmt"
	"os"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/lint"
	"github.com/spf13/cobra"
)

func newLintCmd(version string, gf *GlobalFlags) *cobra.Command {
	var estimate bool

	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Validate a migration YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			var adapter db.Adapter
			if estimate && gf.DB != "" {
				dsn, dsnErr := config.ResolveDSN(gf.DB)
				if dsnErr != nil {
					return dsnErr
				}
				adapter, err = db.NewAdapter(dsn)
				if err != nil {
					return fmt.Errorf("open adapter for estimate: %w", err)
				}
				defer adapter.Close()
			}

			l := &lint.Linter{Migration: m, Adapter: adapter}
			result := l.Run(cmd.Context())
			result.Print()

			if estimate {
				if adapter == nil {
					fmt.Println("note: --estimate requires --db flag for row count queries")
				} else {
					estimates, estErr := lint.Estimate(cmd.Context(), m, adapter)
					if estErr != nil {
						fmt.Fprintf(os.Stderr, "estimate error: %v\n", estErr)
					} else {
						for _, e := range estimates {
							fmt.Printf("estimate: table=%s rows=%d backfill_secs=%.0f\n",
								e.TableName, e.RowCount, e.EstimatedBackfillSecs)
							if !e.GateTimeoutAdequate {
								fmt.Printf("warning: %s\n", e.GateTimeoutWarning)
							}
						}
					}
				}
			}

			if result.HasError {
				return fmt.Errorf("lint found errors")
			}
			fmt.Println("lint: OK")
			return nil
		},
	}
	cmd.Flags().BoolVar(&estimate, "estimate", false, "Print dry-run row count and duration estimates (requires --db)")
	return cmd
}
