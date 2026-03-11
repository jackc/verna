package main

import (
	"fmt"
	"strings"

	"github.com/jackc/verna/internal/server"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Manage application configuration",
	}
}

func newConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all configuration settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appName, err := requireApp()
			if err != nil {
				return err
			}

			client, err := connectToServer()
			if err != nil {
				return err
			}
			defer client.Close()

			state, err := server.ReadState(client, defaultRootDir)
			if err != nil {
				return fmt.Errorf("reading server state: %w", err)
			}

			app, err := lookupApp(state, appName)
			if err != nil {
				return err
			}

			printSetting("domains", strings.Join(app.Domains, ", "))
			printSetting("exec-path", app.ExecPath)
			printSetting("public-path", app.PublicPath)
			printSetting("health-check-path", app.HealthCheckPath)
			printSetting("health-check-timeout", fmt.Sprintf("%d", app.HealthCheckTimeout))
			printSetting("release-retention", fmt.Sprintf("%d", app.ReleaseRetention))
			printSetting("exec-args", strings.Join(app.ExecArgs, ", "))
			printSetting("caddy-server", app.CaddyServer)
			return nil
		},
	}
}

func printSetting(label, value string) {
	fmt.Printf("%-22s %s\n", label+":", value)
}
