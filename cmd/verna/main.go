package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// Set by goreleaser at build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var (
	flagSSHHost    string
	flagSSHUser    string
	flagSSHPort    int
	flagSSHKeyFile string
)

func main() {
	rootCmd := &cobra.Command{
		Use:           "verna",
		Short:         "Systemd-native blue/green deployment tool",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.PersistentFlags().StringVar(&flagSSHHost, "ssh-host", "", "SSH host (env: VERNA_SSH_HOST)")
	rootCmd.PersistentFlags().StringVar(&flagSSHUser, "ssh-user", "root", "SSH user (env: VERNA_SSH_USER)")
	rootCmd.PersistentFlags().IntVar(&flagSSHPort, "ssh-port", 22, "SSH port (env: VERNA_SSH_PORT)")
	rootCmd.PersistentFlags().StringVar(&flagSSHKeyFile, "ssh-key-file", "", "path to SSH private key (env: VERNA_SSH_KEY_FILE)")

	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		applyEnvDefaults(cmd)
	}

	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Server management commands",
	}
	serverCmd.AddCommand(newServerInitCmd())
	serverCmd.AddCommand(newServerInstallCaddyCmd())
	serverCmd.AddCommand(newServerDoctorCmd())

	appCmd := newAppCmd()
	appCmd.AddCommand(newAppInitCmd())
	configCmd := newConfigCmd()
	configCmd.AddCommand(newConfigSetCmd())
	configCmd.AddCommand(newConfigListCmd())
	appCmd.AddCommand(configCmd)

	envCmd := newEnvCmd()
	envCmd.AddCommand(newEnvListCmd())
	envCmd.AddCommand(newEnvGetCmd())
	envCmd.AddCommand(newEnvSetCmd())
	envCmd.AddCommand(newEnvUnsetCmd())
	appCmd.AddCommand(envCmd)

	appCmd.AddCommand(newDeployCmd())
	appCmd.AddCommand(newAppDeleteCmd())

	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(appCmd)
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("verna version %s\n", version)
			fmt.Printf("  commit: %s\n", commit)
			fmt.Printf("  built:  %s\n", date)
		},
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// resolveFileArg checks if val starts with "@" and, if so, reads the file at
// the remaining path and returns its contents. Otherwise returns val as-is.
func resolveFileArg(val string) (string, error) {
	if !strings.HasPrefix(val, "@") {
		return val, nil
	}
	data, err := os.ReadFile(val[1:])
	if err != nil {
		return "", fmt.Errorf("reading file %s: %w", val[1:], err)
	}
	return strings.TrimRight(string(data), "\n"), nil
}

func applyEnvDefaults(cmd *cobra.Command) {
	envFlags := []struct {
		flag   string
		envVar string
	}{
		{"ssh-host", "VERNA_SSH_HOST"},
		{"ssh-user", "VERNA_SSH_USER"},
		{"ssh-port", "VERNA_SSH_PORT"},
		{"ssh-key-file", "VERNA_SSH_KEY_FILE"},
		{"app", "VERNA_APP"},
	}

	for _, ef := range envFlags {
		f := cmd.Flags().Lookup(ef.flag)
		if f == nil || f.Changed {
			continue
		}
		if v, ok := os.LookupEnv(ef.envVar); ok {
			f.Value.Set(v)
		}
	}
}
