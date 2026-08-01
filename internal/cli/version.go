package cli

import (
	"fmt"

	"github.com/donrami/omp-sync/internal/version"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the omp-sync version",
		Long:  "Print the binary's version string. The same value is available as `--version` on the root command.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "omp-sync", version.Version)
		},
	}
}
