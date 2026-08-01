// Command omp-sync synchronizes a user's omp configuration across devices.
package main

import (
	"fmt"
	"os"

	"github.com/example/omp-sync/internal/cli"
	"github.com/example/omp-sync/internal/version"
)

func main() {
	// Print version on -v/--version flag is handled by cobra.
	if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Fprintln(os.Stdout, "omp-sync", version.Version)
		return
	}

	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(cli.ExitCode(err))
	}
}
