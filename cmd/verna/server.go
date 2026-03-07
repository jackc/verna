package main

import (
	"fmt"
	"strings"

	"github.com/jackc/verna/internal/server"
	"github.com/jackc/verna/internal/ssh"
	"github.com/spf13/cobra"
)

const defaultRootDir = "/var/verna"

type checkResult struct {
	name    string
	ok      bool
	message string
}

// checkPrerequisites runs prerequisite checks shared by server init and server doctor.
func checkPrerequisites(client *ssh.Client) []checkResult {
	var results []checkResult

	// systemd
	if output, err := client.Run("systemctl --version"); err != nil {
		results = append(results, checkResult{"systemd", false, "systemctl not found"})
	} else {
		version := strings.SplitN(strings.TrimSpace(output), "\n", 2)[0]
		results = append(results, checkResult{"systemd", true, version})
	}

	// curl
	if _, err := client.Run("which curl"); err != nil {
		results = append(results, checkResult{"curl", false, "curl not found in PATH"})
	} else {
		results = append(results, checkResult{"curl", true, "found"})
	}

	// caddy
	if _, err := client.Run("curl -sf localhost:2019/config/"); err != nil {
		if _, pathErr := client.Run("which caddy"); pathErr != nil {
			results = append(results, checkResult{"caddy", false, "admin API not responding and caddy not found in PATH"})
		} else {
			results = append(results, checkResult{"caddy", false, "caddy found but admin API not responding on localhost:2019"})
		}
	} else {
		results = append(results, checkResult{"caddy", true, "admin API responding"})
	}

	return results
}

func printCheckResults(results []checkResult) {
	for _, r := range results {
		status := "ok"
		if !r.ok {
			status = "FAIL"
		}
		fmt.Printf("  %-10s %s  %s\n", r.name, status, r.message)
	}
}

func connectToServer() (*ssh.Client, error) {
	if flagHost == "" {
		return nil, fmt.Errorf("--host is required")
	}
	return ssh.Connect(ssh.ConnectConfig{
		Host:    flagHost,
		User:    flagUser,
		Port:    flagPort,
		KeyFile: flagKeyFile,
	})
}

func newServerInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize verna on the server",
		Long:  "Creates the verna directory structure and an empty verna.json on the server.",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := connectToServer()
			if err != nil {
				return err
			}
			defer client.Close()

			// Create directory structure.
			if _, err := client.Run(fmt.Sprintf("mkdir -p %s/apps", defaultRootDir)); err != nil {
				return fmt.Errorf("creating directory structure: %w", err)
			}

			// Check if verna.json already exists.
			if _, err := client.Run(fmt.Sprintf("test -f %s/verna.json", defaultRootDir)); err == nil {
				fmt.Println("Server already initialized (verna.json exists).")
				return nil
			}

			// Write empty state file.
			state := server.NewServerState()
			if err := server.WriteState(client, defaultRootDir, state); err != nil {
				return fmt.Errorf("writing initial state: %w", err)
			}

			// Check prerequisites.
			results := checkPrerequisites(client)
			printCheckResults(results)

			fmt.Printf("Server initialized at %s\n", defaultRootDir)
			return nil
		},
	}
}

func newServerDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check server prerequisites and setup",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := connectToServer()
			if err != nil {
				return err
			}
			defer client.Close()

			fmt.Println("Checking server prerequisites...")

			results := checkPrerequisites(client)

			// Check verna.json last.
			if _, err := client.Run(fmt.Sprintf("test -f %s/verna.json", defaultRootDir)); err != nil {
				results = append(results, checkResult{"verna.json", false, fmt.Sprintf("%s/verna.json not found (run server init)", defaultRootDir)})
			} else {
				results = append(results, checkResult{"verna.json", true, "found"})
			}

			printCheckResults(results)

			allOk := true
			for _, r := range results {
				if !r.ok {
					allOk = false
					break
				}
			}
			if !allOk {
				return fmt.Errorf("some checks failed")
			}
			return nil
		},
	}
}
