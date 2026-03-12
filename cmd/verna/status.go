package main

import (
	"fmt"
	"strings"

	"github.com/jackc/verna/internal/server"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show application deployment status",
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

			fmt.Printf("App:        %s\n", appName)
			fmt.Printf("Domains:    %s\n", strings.Join(app.Domains, ", "))

			if app.ActiveSlot == "" {
				fmt.Println("\nNo deployment yet.")
				return nil
			}

			// Active slot info.
			activeSlot := app.Slots[app.ActiveSlot]
			fmt.Printf("\nActive:     %s (port %d)\n", app.ActiveSlot, activeSlot.Port)
			fmt.Printf("Release:    %s\n", activeSlot.Release)
			fmt.Printf("Deployed:   %s\n", activeSlot.DeployedAt)

			// Service status.
			activeUnit := fmt.Sprintf("%s@%s.service", appName, app.ActiveSlot)
			serviceStatus, err := client.Run(fmt.Sprintf("systemctl is-active %s", activeUnit))
			if err != nil {
				serviceStatus = "unknown"
			}
			fmt.Printf("Service:    %s\n", strings.TrimSpace(serviceStatus))

			// Health check (single probe, not polling).
			healthStatus, err := client.Run(fmt.Sprintf(
				"curl -s -o /dev/null -w '%%{http_code}' http://127.0.0.1:%d%s",
				activeSlot.Port, app.HealthCheckPath,
			))
			if err != nil {
				healthStatus = "unreachable"
			}
			fmt.Printf("Health:     %s\n", strings.TrimSpace(healthStatus))

			// Inactive slot info.
			inactiveSlotName := "green"
			if app.ActiveSlot == "green" {
				inactiveSlotName = "blue"
			}
			inactiveSlot := app.Slots[inactiveSlotName]
			fmt.Printf("\nInactive:   %s (port %d)\n", inactiveSlotName, inactiveSlot.Port)
			if inactiveSlot.Release != "" {
				fmt.Printf("Release:    %s\n", inactiveSlot.Release)
				fmt.Printf("Deployed:   %s\n", inactiveSlot.DeployedAt)
			} else {
				fmt.Println("Release:    (none)")
			}

			inactiveUnit := fmt.Sprintf("%s@%s.service", appName, inactiveSlotName)
			inactiveStatus, err := client.Run(fmt.Sprintf("systemctl is-active %s", inactiveUnit))
			if err != nil {
				inactiveStatus = "inactive"
			}
			fmt.Printf("Service:    %s\n", strings.TrimSpace(inactiveStatus))

			return nil
		},
	}
}
