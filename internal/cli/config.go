package cli

import (
	"fmt"
	"os"

	"github.com/example/omp-sync/internal/config"
	"github.com/example/omp-sync/internal/credentials"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage the omp-sync configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newConfigListCmd(),
		newConfigGetCmd(),
		newConfigSetCmd(),
		newConfigSchemaCmd(),
	)
	return cmd
}

func newConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Print the resolved configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigList(cmd)
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print a single configuration value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigGet(cmd, args[0])
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a credential (other keys must be edited in the config file)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigSet(cmd, args[0], args[1])
		},
	}
}

func newConfigSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON schema for config.toml",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigSchema(cmd)
		},
	}
}

func runConfigList(cmd *cobra.Command) error {
	cfg, err := loadConfig(globalFlags.ConfigPath)
	if err != nil {
		return err
	}
	if globalFlags.JSON {
		return PrintJSON(cmd.OutOrStdout(), cfg)
	}
	data, err := cfg.Marshal()
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return nil
}

func runConfigGet(cmd *cobra.Command, key string) error {
	cfg, err := loadConfig(globalFlags.ConfigPath)
	if err != nil {
		return err
	}
	switch key {
	case "backend":
		fmt.Fprintln(cmd.OutOrStdout(), cfg.Backend)
	case "omp_dir":
		fmt.Fprintln(cmd.OutOrStdout(), cfg.OmpDir)
	default:
		return exitErr(ExitUsage, fmt.Errorf("unknown key %q (try `omp-sync config list`)", key))
	}
	return nil
}

func runConfigSet(cmd *cobra.Command, key, value string) error {
	if key == "credential" {
		if err := credentials.Store(value, value); err != nil {
			return exitErr(ExitGeneric, fmt.Errorf("store credential: %w", err))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "stored credential %q in keyring\n", value)
		return nil
	}
	return exitErr(ExitUsage, fmt.Errorf("only `credential` is settable via this command; edit %s manually", globalFlags.ConfigPath))
}

func runConfigSchema(cmd *cobra.Command) error {
	_, err := os.Stdout.Write([]byte(schemaJSON))
	return err
}

const schemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "omp-sync config",
  "type": "object",
  "required": ["backend", "omp_dir"],
  "properties": {
    "backend": { "type": "string", "enum": ["local", "webdav", "github"] },
    "omp_dir": { "type": "string" },
    "include": { "type": "array", "items": { "type": "string" } },
    "exclude": { "type": "array", "items": { "type": "string" } },
    "webdav": { "type": "object" },
    "github": { "type": "object" },
    "local":  { "type": "object" }
  }
}`

var _ = config.DefaultPath
