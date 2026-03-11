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
			printSetting("caddy-handle-template", app.CaddyHandleTemplate)
			printSetting("health-check-path", app.HealthCheckPath)
			printSetting("health-check-timeout", fmt.Sprintf("%d", app.HealthCheckTimeout))
			printSetting("release-retention", fmt.Sprintf("%d", app.ReleaseRetention))
			printSetting("exec-args", strings.Join(app.ExecArgs, ", "))
			printSetting("caddy-server", app.CaddyServer)
			return nil
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get the value of a configuration setting",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName, err := requireApp()
			if err != nil {
				return err
			}

			key := args[0]

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

			switch key {
			case "domains":
				fmt.Println(strings.Join(app.Domains, ", "))
			case "exec-path":
				fmt.Println(app.ExecPath)
			case "caddy-handle-template":
				fmt.Println(app.CaddyHandleTemplate)
			case "health-check-path":
				fmt.Println(app.HealthCheckPath)
			case "health-check-timeout":
				fmt.Println(app.HealthCheckTimeout)
			case "release-retention":
				fmt.Println(app.ReleaseRetention)
			case "exec-args":
				fmt.Println(strings.Join(app.ExecArgs, ", "))
			case "caddy-server":
				fmt.Println(app.CaddyServer)
			default:
				return fmt.Errorf("unknown config key %q (valid keys: domains, exec-path, caddy-handle-template, health-check-path, health-check-timeout, release-retention, exec-args, caddy-server)", key)
			}

			return nil
		},
	}
}

func printSetting(label, value string) {
	fmt.Printf("%-22s %s\n", label+":", value)
}
