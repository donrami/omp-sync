package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/donrami/omp-sync/internal/config"
	"github.com/donrami/omp-sync/internal/credentials"
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
	var valueFlag string
	cmd := &cobra.Command{
		Use:   "set credential <name>",
		Short: "Store a credential in the OS keyring",
		Long: "Stores a secret under <name>. The value is read from --value if " +
			"provided, otherwise from stdin (one line).",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigSet(cmd, args[0], args[1], valueFlag)
		},
	}
	cmd.Flags().StringVar(&valueFlag, "value", "", "credential value (else read from stdin)")
	return cmd
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

// runConfigSet stores a credential. args[0] is the literal subcommand name
// ("credential"); args[1] is the credential name; the secret is read from
// --value (preferred) or from stdin.
func runConfigSet(cmd *cobra.Command, key, name, valueFlag string) error {
	if key != "credential" {
		return exitErr(ExitUsage, fmt.Errorf("only `set credential` is supported (got `set %s`); edit %s manually", key, globalFlags.ConfigPath))
	}
	if name == "" {
		return exitErr(ExitUsage, fmt.Errorf("credential name is required"))
	}

	value, err := resolveCredentialValue(cmd, valueFlag)
	if err != nil {
		return exitErr(ExitGeneric, err)
	}
	if value == "" {
		return exitErr(ExitUsage, fmt.Errorf("credential value is empty; provide via --value or stdin"))
	}

	if err := credentials.Store(name, value); err != nil {
		return exitErr(ExitGeneric, fmt.Errorf("store credential: %w", err))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "stored credential %q in keyring\n", name)
	return nil
}

// resolveCredentialValue returns the secret from --value (preferred)
// or by reading one line from stdin.
func resolveCredentialValue(cmd *cobra.Command, valueFlag string) (string, error) {
	if valueFlag != "" {
		return valueFlag, nil
	}
	stat, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("stat stdin: %w", err)
	}
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "Enter credential value (stdin):")
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return "", errors.New("no credential value provided")
	}
	return strings.TrimRight(scanner.Text(), "\r\n"), nil
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
