package cli

import (
	"fmt"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/store"
	"github.com/spf13/cobra"
)

func newGCCmd(version string, gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "gc",
		Short: "Prune heartbeats for completed migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			dsn, err := config.ResolveDSN(gf.DB)
			if err != nil {
				return err
			}
			s, err := store.NewMySQLFromDSN(dsn)
			if err != nil {
				return err
			}
			defer s.Close()
			deleted, err := s.DeleteHeartbeatsForCompletedMigrations(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Printf("gc: deleted %d heartbeat rows\n", deleted)
			return nil
		},
	}
}
