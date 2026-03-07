package main

import (
	"fmt"
	"os"
	"os/user"

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

	currentUser := ""
	if u, err := user.Current(); err == nil {
		currentUser = u.Username
	}

	rootCmd.PersistentFlags().StringVar(&flagHost, "host", "", "SSH host (required)")
	rootCmd.PersistentFlags().StringVar(&flagUser, "user", currentUser, "SSH user")
	rootCmd.PersistentFlags().IntVar(&flagPort, "port", 22, "SSH port")
	rootCmd.PersistentFlags().StringVar(&flagKeyFile, "key-file", "", "path to SSH private key")

	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Server management commands",
	}
	serverCmd.AddCommand(newServerInitCmd())
	serverCmd.AddCommand(newServerDoctorCmd())

	rootCmd.AddCommand(serverCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
