package main

import (
	"fmt"

	"github.com/jackc/verna/internal/caddy"
	"github.com/jackc/verna/internal/server"
	"github.com/jackc/verna/internal/systemd"
	"github.com/spf13/cobra"
)

func newConfigSetCmd() *cobra.Command {
	var (
		domains            []string
		execPath           string
		caddyHandleTemplate string
		healthCheckPath    string
		healthCheckTimeout int
		releaseRetention   int
		execArgs           []string
	)

	cmd := &cobra.Command{
		Use:   "set",
		Short: "Update application settings",
		Long:  "Updates app configuration in verna.json. Changes to --domain or --caddy-handle-template update the Caddy route. Changes to --exec-path or --exec-arg regenerate the systemd unit and restart the active slot.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appName, err := requireApp()
			if err != nil {
				return err
			}

			// Check that at least one flag was provided.
			flags := cmd.Flags()
			if !flags.Changed("domain") && !flags.Changed("exec-path") && !flags.Changed("caddy-handle-template") &&
				!flags.Changed("health-check-path") &&
				!flags.Changed("health-check-timeout") && !flags.Changed("release-retention") &&
				!flags.Changed("exec-arg") {
				return fmt.Errorf("no settings to update (use --domain, --exec-path, --caddy-handle-template, --health-check-path, --health-check-timeout, --release-retention, or --exec-arg)")
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

			// Apply changes.
			needCaddyUpdate := false
			needSystemdUpdate := false

			if flags.Changed("domain") {
				if len(domains) == 0 {
					return fmt.Errorf("at least one --domain is required")
				}
				app.Domains = domains
				needCaddyUpdate = true
				fmt.Printf("  Domains: %v\n", domains)
			}

			if flags.Changed("exec-path") {
				app.ExecPath = execPath
				needSystemdUpdate = true
				fmt.Printf("  Exec path: %s\n", execPath)
			}

			if flags.Changed("caddy-handle-template") {
				if caddyHandleTemplate != "" {
					var err error
					caddyHandleTemplate, err = resolveHandleTemplate(caddyHandleTemplate)
					if err != nil {
						return err
					}
					if err := caddy.ValidateHandleTemplate(caddyHandleTemplate); err != nil {
						return fmt.Errorf("invalid caddy handle template: %w", err)
					}
				}
				app.CaddyHandleTemplate = caddyHandleTemplate
				needCaddyUpdate = true
				fmt.Printf("  Caddy handle template: %s\n", caddyHandleTemplate)
			}

			if flags.Changed("health-check-path") {
				app.HealthCheckPath = healthCheckPath
				fmt.Printf("  Health check path: %s\n", healthCheckPath)
			}

			if flags.Changed("health-check-timeout") {
				app.HealthCheckTimeout = healthCheckTimeout
				fmt.Printf("  Health check timeout: %d\n", healthCheckTimeout)
			}

			if flags.Changed("release-retention") {
				app.ReleaseRetention = releaseRetention
				fmt.Printf("  Release retention: %d\n", releaseRetention)
			}

			if flags.Changed("exec-arg") {
				app.ExecArgs = execArgs
				needSystemdUpdate = true
				fmt.Printf("  Exec args: %v\n", execArgs)
			}

			// Update Caddy route if domains or caddy-handle-template changed.
			if needCaddyUpdate {
				fmt.Println("Updating Caddy route...")
				activeSlot := app.ActiveSlot
				if activeSlot == "" {
					activeSlot = "blue"
				}
				activePort := app.Slots[activeSlot].Port
				if err := caddy.UpdateAppRoute(client, caddy.RouteConfig{
					AppName:             appName,
					CaddyServer:         app.CaddyServer,
					Domains:             app.Domains,
					Port:                activePort,
					CaddyHandleTemplate: app.CaddyHandleTemplate,
					SlotDir:             fmt.Sprintf("%s/apps/%s/slots/%s", defaultRootDir, appName, activeSlot),
				}); err != nil {
					return fmt.Errorf("updating Caddy route: %w", err)
				}
			}

			// Regenerate systemd unit if exec path or exec args changed.
			if needSystemdUpdate {
				fmt.Println("Regenerating systemd unit...")
				unitContent, err := systemd.GenerateTemplateUnit(systemd.UnitConfig{
					AppName:  appName,
					User:     app.User,
					Group:    app.Group,
					RootDir:  defaultRootDir,
					ExecPath: app.ExecPath,
					ExecArgs: app.ExecArgs,
				})
				if err != nil {
					return fmt.Errorf("generating systemd unit: %w", err)
				}

				unitPath := fmt.Sprintf("/etc/systemd/system/%s@.service", appName)
				writeUnitCmd := fmt.Sprintf("cat <<'UNIT_EOF' | tee %s > /dev/null\n%sUNIT_EOF", unitPath, unitContent)
				if _, err := client.Run(writeUnitCmd); err != nil {
					return fmt.Errorf("writing systemd unit: %w", err)
				}

				if _, err := client.Run("systemctl daemon-reload"); err != nil {
					return fmt.Errorf("reloading systemd: %w", err)
				}

				// Restart active slot if deployed.
				if app.ActiveSlot != "" {
					unitName := fmt.Sprintf("%s@%s.service", appName, app.ActiveSlot)
					fmt.Printf("Restarting %s...\n", unitName)
					if _, err := client.Run(fmt.Sprintf("systemctl restart %s", unitName)); err != nil {
						return fmt.Errorf("restarting %s: %w", unitName, err)
					}
				}
			}

			// Write updated state.
			if err := server.WriteState(client, defaultRootDir, state); err != nil {
				return fmt.Errorf("writing server state: %w", err)
			}

			fmt.Println("Settings updated.")
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&domains, "domain", nil, "domain name for the app (repeatable, replaces all existing domains)")
	cmd.Flags().StringVar(&execPath, "exec-path", "", "relative path to executable in artifact directory")
	cmd.Flags().StringVar(&caddyHandleTemplate, "caddy-handle-template", "", "Caddy route handle: preset name (proxy, static-proxy, static-proxy-cached), @file path, or Go text/template JSON (uses {{.Dial}} and {{.SlotDir}})")
	cmd.Flags().StringVar(&healthCheckPath, "health-check-path", "", "health check endpoint path")
	cmd.Flags().IntVar(&healthCheckTimeout, "health-check-timeout", 0, "health check timeout in seconds")
	cmd.Flags().IntVar(&releaseRetention, "release-retention", 0, "number of releases to retain")
	cmd.Flags().StringArrayVar(&execArgs, "exec-arg", nil, "argument to append to the executable in ExecStart (repeatable, replaces all existing args)")

	return cmd
}
