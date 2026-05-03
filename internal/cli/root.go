// internal/cli/root.go
package cli

import "github.com/spf13/cobra"

func NewRootCmd(version string) *cobra.Command {
	return &cobra.Command{Use: "phasedb", Version: version}
}
