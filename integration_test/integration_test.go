// Package integration_test provides end-to-end tests that connect to a real server over SSH.
//
// # Prerequisites
//
// Ubuntu server with systemd and Caddy installed (admin API on localhost:2019).
// The server does NOT need to be publicly routable — only SSH access is required.
//
// # Setup
//
//	# On the test server (as root):
//	touch /verna-integration-test-target
//
//	# On your local machine, generate a key if needed:
//	ssh-keygen -t ed25519 -f ~/.ssh/verna-test -N ""
//
//	# Copy to server (use IP address if no DNS):
//	ssh-copy-id -i ~/.ssh/verna-test root@<test-server-ip>
//
// # Running
//
// If your SSH agent has a key for the test server:
//
//	VERNA_TEST_SSH_HOST=<ip> go test -v -timeout 300s ./integration_test/
//
// Or with an explicit key file:
//
//	VERNA_TEST_SSH_HOST=<ip> VERNA_TEST_SSH_KEY_FILE=~/.ssh/verna-test go test -v -timeout 300s ./integration_test/
package integration_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/verna/internal/ssh"
)

const (
	markerFile   = "/verna-integration-test-target"
	testAppName  = "verna-itest"
	testDomain   = "verna-itest.example.com"
	vernaRootDir = "/var/lib/verna"
)

var (
	vernaBin          string
	testAppTarball    string
	caddyTemplatePath string
	sshHost           string
	sshPort           int
	sshKeyFile        string
	skipRemote        bool
	tmpDir            string
)

func TestMain(m *testing.M) {
	sshHost = os.Getenv("VERNA_TEST_SSH_HOST")
	sshPort = 22
	if portStr := os.Getenv("VERNA_TEST_SSH_PORT"); portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid VERNA_TEST_SSH_PORT: %v\n", err)
			os.Exit(1)
		}
		sshPort = p
	}
	sshKeyFile = os.Getenv("VERNA_TEST_SSH_KEY_FILE")

	if sshHost == "" {
		fmt.Println("VERNA_TEST_SSH_HOST not set; remote tests will be skipped")
		skipRemote = true
	}

	if !skipRemote {
		client, err := sshConnect()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to connect to test server: %v\n", err)
			os.Exit(1)
		}
		_, err = client.Run("test -f " + markerFile)
		client.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ABORT: marker file %s not found on %s — refusing to run against a non-test server\n", markerFile, sshHost)
			os.Exit(1)
		}
	}

	// Create temp dir for build artifacts.
	var err error
	tmpDir, err = os.MkdirTemp("", "verna-itest-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create temp dir: %v\n", err)
		os.Exit(1)
	}

	// Build verna binary.
	vernaBin = filepath.Join(tmpDir, "verna")
	if err := buildBinary("./cmd/verna", vernaBin, "", ""); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to build verna: %v\n", err)
		os.Exit(1)
	}

	if !skipRemote {
		// Build test app for the remote server.
		goarch := os.Getenv("VERNA_TEST_GOARCH")
		if goarch == "" {
			goarch = "amd64"
		}
		testAppBinDir := filepath.Join(tmpDir, "testapp-bin")
		os.MkdirAll(testAppBinDir, 0o755)
		testAppBinPath := filepath.Join(testAppBinDir, "testapp")
		if err := buildBinary("./integration_test/testapp", testAppBinPath, "linux", goarch); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to build test app: %v\n", err)
			os.Exit(1)
		}

		// Create tarball.
		testAppTarball = filepath.Join(tmpDir, "testapp.tar.gz")
		cmd := exec.Command("tar", "-czf", testAppTarball, "-C", testAppBinDir, "testapp")
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create tarball: %v\n%s\n", err, out)
			os.Exit(1)
		}

		// Write caddy handle template file.
		caddyTemplatePath = filepath.Join(tmpDir, "caddy-handle-template.json")
		if err := os.WriteFile(caddyTemplatePath, []byte(`[{"handler": "reverse_proxy", "upstreams": [{"dial": "{{.Dial}}"}]}]`), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write caddy template: %v\n", err)
			os.Exit(1)
		}

		// Clean server before tests (in case previous run left artifacts).
		cleanupServer()
	}

	code := m.Run()

	if !skipRemote {
		cleanupServer()
	}
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

func requireRemote(t *testing.T) {
	t.Helper()
	if skipRemote {
		t.Skip("VERNA_TEST_SSH_HOST required for remote tests")
	}
}

func sshConnect() (*ssh.Client, error) {
	return ssh.Connect(ssh.ConnectConfig{
		Host:    sshHost,
		User:    "root",
		Port:    sshPort,
		KeyFile: sshKeyFile,
	})
}

func buildBinary(pkg, outputPath, goos, goarch string) error {
	cmd := exec.Command("go", "build", "-o", outputPath, pkg)
	cmd.Dir = "/workspaces/verna-a"
	cmd.Env = os.Environ()
	if goos != "" {
		cmd.Env = append(cmd.Env, "GOOS="+goos)
	}
	if goarch != "" {
		cmd.Env = append(cmd.Env, "GOARCH="+goarch)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v\n%s", err, out)
	}
	return nil
}

func runVerna(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(vernaBin, args...)
	cmd.Env = append(os.Environ(),
		"VERNA_SSH_HOST="+sshHost,
		"VERNA_SSH_USER=root",
		"VERNA_SSH_PORT="+strconv.Itoa(sshPort),
	)
	if sshKeyFile != "" {
		cmd.Env = append(cmd.Env, "VERNA_SSH_KEY_FILE="+sshKeyFile)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	return outBuf.String(), errBuf.String(), runErr
}

func runVernaOK(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, err := runVerna(t, args...)
	if err != nil {
		t.Fatalf("verna %v failed: %v\nstdout: %s\nstderr: %s", args, err, stdout, stderr)
	}
	return stdout
}

func runVernaFail(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	stdout, stderr, err := runVerna(t, args...)
	if err == nil {
		t.Fatalf("verna %v succeeded unexpectedly\nstdout: %s", args, stdout)
	}
	return stdout, stderr
}

// runOnServer executes a command on the test server via SSH and returns stdout.
func runOnServer(t *testing.T, command string) string {
	t.Helper()
	client, err := sshConnect()
	if err != nil {
		t.Fatalf("SSH connect failed: %v", err)
	}
	defer client.Close()
	out, err := client.Run(command)
	if err != nil {
		t.Fatalf("remote command %q failed: %v\noutput: %s", command, err, out)
	}
	return out
}

// disableCaddyAutoHTTPS disables automatic HTTPS on all Caddy servers via the admin API.
func disableCaddyAutoHTTPS(t *testing.T) {
	t.Helper()
	client, err := sshConnect()
	if err != nil {
		t.Fatalf("SSH connect failed: %v", err)
	}
	defer client.Close()

	// List servers and disable auto-HTTPS on each.
	out, err := client.Run(`curl -sf http://localhost:2019/config/apps/http/servers`)
	if err != nil {
		// No servers yet, nothing to disable.
		return
	}

	// Parse server names from the JSON keys. Simple approach: use the Caddy API
	// to set automatic_https on each known server.
	// We know the server is likely named "verna" after server init.
	for _, name := range []string{"verna"} {
		if strings.Contains(out, `"`+name+`"`) || strings.Contains(out, `"`+name+`":`) {
			client.Run(fmt.Sprintf(
				`curl -sf -X PUT -H "Content-Type: application/json" -d '{"disable": true}' http://localhost:2019/config/apps/http/servers/%s/automatic_https`,
				name,
			))
		}
	}
}

func cleanupServer() {
	client, err := sshConnect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cleanup: failed to connect: %v\n", err)
		return
	}
	defer client.Close()

	// Stop and disable test app systemd units.
	client.Run(fmt.Sprintf("systemctl stop %s@blue.service %s@green.service 2>/dev/null", testAppName, testAppName))
	client.Run(fmt.Sprintf("systemctl disable %s@blue.service %s@green.service 2>/dev/null", testAppName, testAppName))

	// Remove systemd unit file.
	client.Run(fmt.Sprintf("rm -f /etc/systemd/system/%s@.service", testAppName))
	client.Run("systemctl daemon-reload")

	// Remove Caddy route for test app.
	client.Run(fmt.Sprintf(`curl -sf -X DELETE http://localhost:2019/id/verna_%s 2>/dev/null`, testAppName))

	// Remove the "verna" Caddy server if it exists (created by server init when no servers exist).
	client.Run(`curl -sf -X DELETE http://localhost:2019/config/apps/http/servers/verna 2>/dev/null`)

	// Remove app directory and OS user.
	client.Run(fmt.Sprintf("rm -rf %s/apps/%s", vernaRootDir, testAppName))
	client.Run(fmt.Sprintf("userdel %s 2>/dev/null", testAppName))

	// Remove entire verna directory to return to virgin state.
	client.Run(fmt.Sprintf("rm -rf %s", vernaRootDir))
}

func TestFullLifecycle(t *testing.T) {
	requireRemote(t)

	// ServerInit
	t.Run("ServerInit", func(t *testing.T) {
		out := runVernaOK(t, "server", "init")
		if !strings.Contains(out, "Server initialized") && !strings.Contains(out, "already initialized") {
			t.Fatalf("unexpected output: %s", out)
		}
	})
	if t.Failed() {
		return
	}

	// ServerInstallCaddy
	t.Run("ServerInstallCaddy", func(t *testing.T) {
		out := runVernaOK(t, "server", "install-caddy")
		if !strings.Contains(out, "installed and running") && !strings.Contains(out, "admin API") {
			t.Fatalf("unexpected output: %s", out)
		}
	})
	if t.Failed() {
		return
	}

	// Disable Caddy auto-HTTPS so it doesn't try to provision certs for the test domain.
	disableCaddyAutoHTTPS(t)

	// ServerDoctor
	t.Run("ServerDoctor", func(t *testing.T) {
		out := runVernaOK(t, "server", "doctor")
		if !strings.Contains(out, "verna.json") {
			t.Fatalf("unexpected output: %s", out)
		}
	})
	if t.Failed() {
		return
	}

	// AppInit
	t.Run("AppInit", func(t *testing.T) {
		out := runVernaOK(t, "app", "--app", testAppName, "init",
			"--domain", testDomain,
			"--exec-path", "testapp",
		)
		if !strings.Contains(out, "initialized") {
			t.Fatalf("unexpected output: %s", out)
		}
	})
	if t.Failed() {
		return
	}

	// AppConfigList
	t.Run("AppConfigList", func(t *testing.T) {
		out := runVernaOK(t, "app", "--app", testAppName, "config", "list")
		if !strings.Contains(out, testDomain) {
			t.Fatalf("domain not in config list: %s", out)
		}
		if !strings.Contains(out, "testapp") {
			t.Fatalf("exec-path not in config list: %s", out)
		}
	})
	if t.Failed() {
		return
	}

	// AppConfigGet
	t.Run("AppConfigGet", func(t *testing.T) {
		out := runVernaOK(t, "app", "--app", testAppName, "config", "get", "exec-path")
		if strings.TrimSpace(out) != "testapp" {
			t.Fatalf("unexpected exec-path: %q", out)
		}
	})
	if t.Failed() {
		return
	}

	// AppConfigSet
	t.Run("AppConfigSet", func(t *testing.T) {
		runVernaOK(t, "app", "--app", testAppName, "config", "set",
			"--health-check-timeout", "20",
		)
		out := runVernaOK(t, "app", "--app", testAppName, "config", "get", "health-check-timeout")
		if strings.TrimSpace(out) != "20" {
			t.Fatalf("health-check-timeout not updated: %q", out)
		}
	})
	if t.Failed() {
		return
	}

	// AppEnv
	t.Run("AppEnv", func(t *testing.T) {
		runVernaOK(t, "app", "--app", testAppName, "env", "set", "FOO=bar", "BAZ=qux")

		out := runVernaOK(t, "app", "--app", testAppName, "env", "get", "FOO")
		if strings.TrimSpace(out) != "bar" {
			t.Fatalf("FOO != bar: %q", out)
		}

		out = runVernaOK(t, "app", "--app", testAppName, "env", "list")
		if !strings.Contains(out, "BAZ=qux") {
			t.Fatalf("BAZ not in env list: %s", out)
		}

		runVernaOK(t, "app", "--app", testAppName, "env", "unset", "BAZ")
		_, _, err := runVerna(t, "app", "--app", testAppName, "env", "get", "BAZ")
		if err == nil {
			t.Fatal("expected error getting unset BAZ")
		}
	})
	if t.Failed() {
		return
	}

	// DeployFirst
	t.Run("DeployFirst", func(t *testing.T) {
		out := runVernaOK(t, "app", "--app", testAppName, "deploy",
			"--caddy-handle-template-path", caddyTemplatePath,
			testAppTarball,
		)
		if !strings.Contains(out, "Deploy complete") {
			t.Fatalf("deploy did not complete: %s", out)
		}
		if !strings.Contains(out, "blue") {
			t.Fatalf("expected blue slot: %s", out)
		}

		// Verify the app is reachable through Caddy.
		healthOut := runOnServer(t, fmt.Sprintf(`curl -sf -H "Host: %s" http://127.0.0.1:80/health`, testDomain))
		if !strings.Contains(healthOut, "ok") {
			t.Fatalf("app not reachable through Caddy: %s", healthOut)
		}
	})
	if t.Failed() {
		return
	}

	// StatusAfterDeploy
	t.Run("StatusAfterDeploy", func(t *testing.T) {
		out := runVernaOK(t, "app", "--app", testAppName, "status")
		if !strings.Contains(out, testDomain) {
			t.Fatalf("domain not in status: %s", out)
		}
		if !strings.Contains(out, "blue") {
			t.Fatalf("active slot not shown: %s", out)
		}
	})
	if t.Failed() {
		return
	}

	// DeploySecond — should go to green
	t.Run("DeploySecond", func(t *testing.T) {
		out := runVernaOK(t, "app", "--app", testAppName, "deploy",
			"--caddy-handle-template-path", caddyTemplatePath,
			testAppTarball,
		)
		if !strings.Contains(out, "Deploy complete") {
			t.Fatalf("deploy did not complete: %s", out)
		}
		if !strings.Contains(out, "green") {
			t.Fatalf("expected green slot for second deploy: %s", out)
		}
	})
	if t.Failed() {
		return
	}

	// Rollback — should go back to blue
	t.Run("Rollback", func(t *testing.T) {
		out := runVernaOK(t, "app", "--app", testAppName, "rollback")
		if !strings.Contains(out, "Rollback complete") {
			t.Fatalf("rollback did not complete: %s", out)
		}
		if !strings.Contains(out, "blue") {
			t.Fatalf("expected rollback to blue: %s", out)
		}

		// Verify traffic actually switched through Caddy.
		healthOut := runOnServer(t, fmt.Sprintf(`curl -sf -H "Host: %s" http://127.0.0.1:80/health`, testDomain))
		if !strings.Contains(healthOut, "ok") {
			t.Fatalf("app not reachable through Caddy after rollback: %s", healthOut)
		}
	})
	if t.Failed() {
		return
	}

	// Logs
	t.Run("Logs", func(t *testing.T) {
		runVernaOK(t, "app", "--app", testAppName, "logs", "--lines", "5")
	})
	if t.Failed() {
		return
	}

	// AppDelete
	t.Run("AppDelete", func(t *testing.T) {
		out := runVernaOK(t, "app", "--app", testAppName, "delete", "--yes")
		if !strings.Contains(out, "deleted") {
			t.Fatalf("app not deleted: %s", out)
		}
	})
}
