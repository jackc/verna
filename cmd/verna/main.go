package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	flagHost    string
	flagUser    string
	flagPort    int
	flagKeyFile string
)

func main() {
	rootCmd := &cobra.Command{
		Use:           "verna",
		Short:         "Systemd-native blue/green deployment tool",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.PersistentFlags().StringVar(&flagHost, "host", "", "SSH host (required)")
	rootCmd.PersistentFlags().StringVar(&flagUser, "user", "root", "SSH user")
	rootCmd.PersistentFlags().IntVar(&flagPort, "port", 22, "SSH port")
	rootCmd.PersistentFlags().StringVar(&flagKeyFile, "key-file", "", "path to SSH private key")

	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Server management commands",
	}
	serverCmd.AddCommand(newServerInitCmd())
	serverCmd.AddCommand(newServerInstallCaddyCmd())
	serverCmd.AddCommand(newServerDoctorCmd())

	appCmd := newAppCmd()
	appCmd.AddCommand(newAppInitCmd())

	envCmd := newEnvCmd()
	envCmd.AddCommand(newEnvListCmd())
	envCmd.AddCommand(newEnvGetCmd())
	envCmd.AddCommand(newEnvSetCmd())
	envCmd.AddCommand(newEnvUnsetCmd())
	appCmd.AddCommand(envCmd)

	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(appCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
