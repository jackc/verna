package main

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/jackc/verna/internal/deploy"
	"github.com/jackc/verna/internal/server"
	"github.com/spf13/cobra"
)

func newDeployCmd() *cobra.Command {
	var (
		binaryPath string
		publicDir  string
		commit     string
		targetOS   string
		targetArch string
	)

	cmd := &cobra.Command{
		Use:   "deploy <app>",
		Short: "Deploy an application to the server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]

			// Auto-detect commit if not provided.
			if commit == "" {
				commit = detectGitCommit()
			}

			// Build artifact.
			fmt.Println("Building artifact...")
			buf, manifest, err := deploy.BuildArtifact(deploy.ArtifactOptions{
				AppName:    appName,
				BinaryPath: binaryPath,
				PublicDir:  publicDir,
				Commit:     commit,
				OS:         targetOS,
				Arch:       targetArch,
			})
			if err != nil {
				return fmt.Errorf("building artifact: %w", err)
			}

			fmt.Printf("Artifact built: %s (%d bytes)\n", manifest.Release, buf.Len())

			// Connect to server.
			client, err := connectToServer()
			if err != nil {
				return err
			}
			defer client.Close()

			// Read state and validate app exists.
			state, err := server.ReadState(client, defaultRootDir)
			if err != nil {
				return fmt.Errorf("reading server state: %w", err)
			}
			if _, exists := state.Apps[appName]; !exists {
				return fmt.Errorf("app %q not found (run `verna app init %s` first)", appName, appName)
			}

			fmt.Printf("Deploying %s (release %s)...\n", appName, manifest.Release)
			result, err := deploy.Deploy(deploy.DeployConfig{
				Client:   client,
				RootDir:  defaultRootDir,
				AppName:  appName,
				State:    state,
				Artifact: buf,
				Manifest: manifest,
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

	cmd.Flags().StringVar(&binaryPath, "binary", "", "path to compiled binary (required)")
	cmd.MarkFlagRequired("binary")
	cmd.Flags().StringVar(&publicDir, "public", "", "path to public assets directory")
	cmd.Flags().StringVar(&commit, "commit", "", "git commit hash (auto-detected if omitted)")
	cmd.Flags().StringVar(&targetOS, "os", "linux", "target operating system")
	cmd.Flags().StringVar(&targetArch, "arch", "amd64", "target architecture")

	return cmd
}

// detectGitCommit attempts to get the current git commit hash.
// Returns empty string on any failure.
func detectGitCommit() string {
	out, err := exec.Command("git", "rev-parse", "--short=7", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
