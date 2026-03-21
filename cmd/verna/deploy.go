package main

import (
	"fmt"
	"os"
	"time"

	"github.com/jackc/verna/internal/deploy"
	"github.com/jackc/verna/internal/server"
	"github.com/spf13/cobra"
)

func newDeployCmd() *cobra.Command {
	var caddyHandleTemplatePath string

	cmd := &cobra.Command{
		Use:   "deploy <tarball>",
		Short: "Deploy an application to the server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tarballPath := args[0]

			// Validate tarball exists and is a file.
			info, err := os.Stat(tarballPath)
			if err != nil {
				return fmt.Errorf("tarball %s: %w", tarballPath, err)
			}
			if info.IsDir() {
				return fmt.Errorf("tarball %s is a directory, not a file", tarballPath)
			}

			appName, err := requireApp()
			if err != nil {
				return err
			}

			// Read and validate caddy handle template locally.
			caddyHandleTemplate, err := ensureCaddyHandleTemplateFile(caddyHandleTemplatePath)
			if err != nil {
				return err
			}

			// Generate release ID from timestamp + tarball content hash.
			releaseID, err := deploy.GenerateReleaseID(time.Now(), tarballPath)
			if err != nil {
				return fmt.Errorf("generating release ID: %w", err)
			}

			// Connect to server and read app config.
			client, err := connectToServer()
			if err != nil {
				return err
			}
			defer client.Close()

			state, err := server.ReadState(client, defaultRootDir)
			if err != nil {
				return fmt.Errorf("reading server state: %w", err)
			}
			if _, exists := state.Apps[appName]; !exists {
				return fmt.Errorf("app %q not found (run `verna app init` first)", appName)
			}

			// Open tarball for streaming to server.
			f, err := os.Open(tarballPath)
			if err != nil {
				return fmt.Errorf("opening tarball: %w", err)
			}
			defer f.Close()

			fmt.Printf("Deploying %s (release %s)...\n", appName, releaseID)
			result, err := deploy.Deploy(deploy.DeployConfig{
				Client:              client,
				RootDir:             defaultRootDir,
				AppName:             appName,
				State:               state,
				TarballReader:       f,
				ReleaseID:           releaseID,
				CaddyHandleTemplate: caddyHandleTemplate,
				Meta:                newStateMetadata(),
			})
			if err != nil {
				return fmt.Errorf("deploy failed: %w", err)
			}

			fmt.Printf("\nDeploy complete:\n")
			fmt.Printf("  Release:  %s\n", result.Release)
			fmt.Printf("  Slot:     %s\n", result.Slot)
			fmt.Printf("  Port:     %d\n", result.Port)
			if result.PrevSlot != "" {
				fmt.Printf("  Previous: %s (stopped)\n", result.PrevSlot)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&caddyHandleTemplatePath, "caddy-handle-template-path", defaultCaddyHandleTemplatePath, "local path to the Caddy handle template file")

	return cmd
}
