package cli

import "github.com/spf13/cobra"

func newResumeCmd(version string, gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume a migration from its last checkpoint",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigration(cmd.Context(), gf, version, true, "", false)
		},
	}
}
