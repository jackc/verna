package main

import (
	"fmt"
	"os"

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
	appCmd.AddCommand(newAppSetCmd())

	envCmd := newEnvCmd()
	envCmd.AddCommand(newEnvListCmd())
	envCmd.AddCommand(newEnvGetCmd())
	envCmd.AddCommand(newEnvSetCmd())
	envCmd.AddCommand(newEnvUnsetCmd())
	appCmd.AddCommand(envCmd)

	appCmd.AddCommand(newDeployCmd())

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
