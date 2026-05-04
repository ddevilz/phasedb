package cli

import (
	"github.com/ddevilz/phasedb/internal/output"
	"github.com/spf13/cobra"
)

// GlobalFlags holds flags shared across all subcommands.
type GlobalFlags struct {
	DB        string
	Migration string
	Config    string
	LogFormat string
}

func NewRootCmd(version string) *cobra.Command {
	var gf GlobalFlags

	root := &cobra.Command{
		Use:     "phasedb",
		Short:   "Expand-contract migration coordinator",
		Version: version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			output.SetupLogger(gf.LogFormat)
			return nil
		},
	}
	root.PersistentFlags().StringVar(&gf.DB, "db", "", "Database URL (overrides DATABASE_URL env)")
	root.PersistentFlags().StringVar(&gf.Migration, "migration", "", "Path to migration YAML file")
	root.PersistentFlags().StringVar(&gf.Config, "config", "phasedb.yaml", "Path to phasedb config file")
	root.PersistentFlags().StringVar(&gf.LogFormat, "log-format", "text", "Log format: text|json")

	root.AddCommand(newRunCmd(version, &gf))
	root.AddCommand(newStatusCmd(version, &gf))
	root.AddCommand(newResumeCmd(version, &gf))
	root.AddCommand(newRollbackCmd(version, &gf))
	root.AddCommand(newGCCmd(version, &gf))
	root.AddCommand(newLintCmd(version, &gf))

	return root
}
