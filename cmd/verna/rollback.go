package main

import (
	"fmt"
	"time"

	"github.com/jackc/verna/internal/caddy"
	"github.com/jackc/verna/internal/health"
	"github.com/jackc/verna/internal/server"
	"github.com/spf13/cobra"
)

func newRollbackCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rollback",
		Short: "Roll back to the previous deployment slot",
		Long:  "Restarts the inactive slot, health checks it, switches Caddy traffic, and stops the old slot.",
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

			if app.ActiveSlot == "" {
				return fmt.Errorf("no active deployment to roll back from")
			}

			// Determine inactive (rollback target) slot.
			targetSlot := "green"
			if app.ActiveSlot == "green" {
				targetSlot = "blue"
			}
			target := app.Slots[targetSlot]

			if target.Release == "" {
				return fmt.Errorf("no previous release in %s slot to roll back to", targetSlot)
			}

			appDir := fmt.Sprintf("%s/apps/%s", defaultRootDir, appName)
			targetUnit := fmt.Sprintf("%s@%s.service", appName, targetSlot)

			fmt.Printf("Rolling back %s: %s -> %s (release %s)...\n", appName, app.ActiveSlot, targetSlot, target.Release)

			// Write runtime.env for the target slot.
			fmt.Println("  Writing runtime.env...")
			if err := server.WriteRuntimeEnv(client, defaultRootDir, appName, targetSlot, target.Port, app.Env); err != nil {
				return fmt.Errorf("writing runtime.env: %w", err)
			}

			// Restart the target slot's systemd unit.
			fmt.Printf("  Starting %s...\n", targetUnit)
			if _, err := client.Run(fmt.Sprintf("systemctl restart %s", targetUnit)); err != nil {
				return fmt.Errorf("restarting %s: %w", targetUnit, err)
			}

			// Health check.
			healthTimeout := time.Duration(app.HealthCheckTimeout) * time.Second
			fmt.Printf("  Waiting for health check (http://127.0.0.1:%d%s)...\n", target.Port, app.HealthCheckPath)
			if err := health.WaitForHealthy(client, target.Port, app.HealthCheckPath, healthTimeout); err != nil {
				// Stop the failed slot — active slot remains untouched.
				client.Run(fmt.Sprintf("systemctl stop %s", targetUnit))
				return fmt.Errorf("health check failed, rollback aborted: %w", err)
			}
			fmt.Println("  Health check passed")

			// Switch Caddy traffic to the target slot.
			fmt.Printf("  Switching traffic to %s (port %d)...\n", targetSlot, target.Port)
			routeCfg := caddy.RouteConfig{
				AppName:             appName,
				CaddyServer:         app.CaddyServer,
				Domains:             app.Domains,
				Port:                target.Port,
				CaddyHandleTemplate: app.EffectiveCaddyHandleTemplate(targetSlot),
				SlotDir:             fmt.Sprintf("%s/slots/%s", appDir, targetSlot),
			}
			if err := caddy.UpdateAppRoute(client, routeCfg); err != nil {
				return fmt.Errorf("updating Caddy route: %w", err)
			}

			// Stop the old (previously active) slot.
			oldUnit := fmt.Sprintf("%s@%s.service", appName, app.ActiveSlot)
			fmt.Printf("  Stopping %s...\n", oldUnit)
			if _, err := client.Run(fmt.Sprintf("systemctl stop %s", oldUnit)); err != nil {
				fmt.Printf("  Warning: failed to stop old slot %s: %v\n", oldUnit, err)
			}

			// Update state.
			app.ActiveSlot = targetSlot
			if err := server.WriteState(client, defaultRootDir, state); err != nil {
				fmt.Printf("  Warning: failed to write state: %v\n", err)
			}

			fmt.Printf("\nRollback complete:\n")
			fmt.Printf("  Active:   %s\n", targetSlot)
			fmt.Printf("  Release:  %s\n", target.Release)
			fmt.Printf("  Port:     %d\n", target.Port)
			return nil
		},
	}
}
