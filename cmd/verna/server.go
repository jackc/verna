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
	if flagSSHHost == "" {
		return nil, fmt.Errorf("--ssh-host is required")
	}
	return ssh.Connect(ssh.ConnectConfig{
		Host:    flagSSHHost,
		User:    flagSSHUser,
		Port:    flagSSHPort,
		KeyFile: flagSSHKeyFile,
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

func newServerInstallCaddyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install-caddy",
		Short: "Install Caddy on the server",
		Long:  "Downloads the latest Caddy release from GitHub, installs it, creates a systemd unit, and verifies the admin API is responding.",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := connectToServer()
			if err != nil {
				return err
			}
			defer client.Close()

			// Detect server architecture.
			fmt.Print("Detecting server architecture... ")
			archOutput, err := client.Run("uname -m")
			if err != nil {
				return fmt.Errorf("detecting architecture: %w", err)
			}
			archRaw := strings.TrimSpace(archOutput)
			var arch string
			switch archRaw {
			case "x86_64":
				arch = "amd64"
			case "aarch64":
				arch = "arm64"
			default:
				return fmt.Errorf("unsupported architecture: %s", archRaw)
			}
			fmt.Println(arch)

			// Get latest Caddy version from GitHub.
			fmt.Print("Fetching latest Caddy version... ")
			versionOutput, err := client.Run(`curl -sI https://github.com/caddyserver/caddy/releases/latest | grep -i ^location: | sed 's/.*tag\///' | tr -d '\r\n'`)
			if err != nil {
				return fmt.Errorf("fetching latest caddy version: %w", err)
			}
			version := strings.TrimSpace(versionOutput)
			if version == "" || !strings.HasPrefix(version, "v") {
				return fmt.Errorf("could not determine latest Caddy version (got %q)", version)
			}
			fmt.Println(version)

			// Download Caddy.
			versionNum := strings.TrimPrefix(version, "v")
			url := fmt.Sprintf("https://github.com/caddyserver/caddy/releases/download/%s/caddy_%s_linux_%s.tar.gz", version, versionNum, arch)
			fmt.Printf("Downloading caddy %s for linux/%s...\n", version, arch)
			if _, err := client.Run(fmt.Sprintf("curl -sfL -o /tmp/caddy.tar.gz %q", url)); err != nil {
				return fmt.Errorf("downloading caddy: %w", err)
			}

			// Extract and install.
			fmt.Println("Installing caddy to /usr/local/bin/caddy...")
			if _, err := client.Run("tar -xzf /tmp/caddy.tar.gz -C /tmp caddy"); err != nil {
				return fmt.Errorf("extracting caddy: %w", err)
			}
			if _, err := client.Run("mv /tmp/caddy /usr/local/bin/caddy"); err != nil {
				return fmt.Errorf("installing caddy binary: %w", err)
			}
			if _, err := client.Run("chmod +x /usr/local/bin/caddy"); err != nil {
				return fmt.Errorf("setting caddy permissions: %w", err)
			}
			if _, err := client.Run("rm -f /tmp/caddy.tar.gz"); err != nil {
				return fmt.Errorf("cleaning up tarball: %w", err)
			}

			// Create caddy system user.
			fmt.Println("Creating caddy system user...")
			// useradd returns exit code 9 if user already exists, which is fine.
			if _, err := client.Run("id caddy >/dev/null 2>&1 || useradd --system --home /var/lib/caddy --shell /usr/sbin/nologin caddy"); err != nil {
				return fmt.Errorf("creating caddy user: %w", err)
			}
			if _, err := client.Run("mkdir -p /var/lib/caddy && chown caddy:caddy /var/lib/caddy"); err != nil {
				return fmt.Errorf("setting up caddy home directory: %w", err)
			}

			// Create systemd unit.
			fmt.Println("Creating systemd unit...")
			unit := `[Unit]
Description=Caddy
After=network.target network-online.target
Requires=network-online.target

[Service]
Type=notify
User=caddy
Group=caddy
ExecStart=/usr/local/bin/caddy run --resume
ExecReload=/usr/local/bin/caddy reload
TimeoutStopSec=5s
LimitNOFILE=1048576
LimitNPROC=512
PrivateTmp=true
ProtectSystem=full
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
`
			writeUnitCmd := `cat <<'UNIT_EOF' | tee /etc/systemd/system/caddy.service > /dev/null
` + unit + `UNIT_EOF`
			if _, err := client.Run(writeUnitCmd); err != nil {
				return fmt.Errorf("writing systemd unit: %w", err)
			}

			// Enable and start.
			fmt.Println("Enabling and starting caddy...")
			if _, err := client.Run("systemctl daemon-reload"); err != nil {
				return fmt.Errorf("reloading systemd: %w", err)
			}
			if _, err := client.Run("systemctl enable caddy"); err != nil {
				return fmt.Errorf("enabling caddy: %w", err)
			}
			if _, err := client.Run("systemctl restart caddy"); err != nil {
				return fmt.Errorf("starting caddy: %w", err)
			}

			// Verify admin API is responding.
			fmt.Print("Waiting for Caddy admin API... ")
			if _, err := client.Run("for i in 1 2 3 4 5; do curl -sf localhost:2019/config/ && exit 0; sleep 1; done; exit 1"); err != nil {
				return fmt.Errorf("caddy admin API not responding on localhost:2019 after 5 seconds")
			}
			fmt.Println("ok")

			fmt.Printf("\nCaddy %s installed and running (admin API responding on localhost:2019)\n", version)
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
