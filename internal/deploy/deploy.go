package deploy

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/jackc/verna/internal/caddy"
	"github.com/jackc/verna/internal/health"
	"github.com/jackc/verna/internal/server"
	"github.com/jackc/verna/internal/ssh"
)

type DeployConfig struct {
	Client        *ssh.Client
	RootDir       string
	AppName       string
	State         *server.ServerState
	TarballReader io.Reader
	ReleaseID     string
	PublicPath    string // relative path to public dir within artifact (for Caddy route)
}

type DeployResult struct {
	Release  string
	Slot     string
	PrevSlot string
	Port     int
}

func Deploy(cfg DeployConfig) (*DeployResult, error) {
	app := cfg.State.Apps[cfg.AppName]

	// Step 1: Determine target slot.
	activeSlot := app.ActiveSlot
	var targetSlot string
	if activeSlot == "" || activeSlot == "green" {
		targetSlot = "blue"
	} else {
		targetSlot = "green"
	}
	targetPort := app.Slots[targetSlot].Port

	appDir := fmt.Sprintf("%s/apps/%s", cfg.RootDir, cfg.AppName)
	releaseDir := fmt.Sprintf("%s/releases/%s", appDir, cfg.ReleaseID)
	slotLink := fmt.Sprintf("%s/slots/%s", appDir, targetSlot)
	unitName := fmt.Sprintf("%s@%s.service", cfg.AppName, targetSlot)

	// Step 2: Upload artifact.
	fmt.Println("  Uploading artifact...")
	tmpTarball := fmt.Sprintf("/tmp/verna-%s-%s.tar.gz", cfg.AppName, cfg.ReleaseID)
	if err := cfg.Client.Upload(cfg.TarballReader, tmpTarball); err != nil {
		return nil, fmt.Errorf("uploading artifact: %w", err)
	}

	// Step 3: Unpack to release directory.
	fmt.Printf("  Unpacking to releases/%s...\n", cfg.ReleaseID)
	if _, err := cfg.Client.Run(fmt.Sprintf("mkdir -p %s && tar -xzf %s -C %s && rm -f %s", releaseDir, tmpTarball, releaseDir, tmpTarball)); err != nil {
		return nil, fmt.Errorf("unpacking artifact: %w", err)
	}

	// Step 4: Validate executable exists and is executable.
	execPath := fmt.Sprintf("%s/%s", releaseDir, app.ExecPath)
	if _, err := cfg.Client.Run(fmt.Sprintf("test -f %s && test -x %s", execPath, execPath)); err != nil {
		cfg.Client.Run(fmt.Sprintf("rm -rf %s", releaseDir))
		return nil, fmt.Errorf("executable %s not found or not executable in release", app.ExecPath)
	}

	// Step 5: Set ownership.
	if _, err := cfg.Client.Run(fmt.Sprintf("chown -R %s:%s %s", app.User, app.Group, releaseDir)); err != nil {
		return nil, fmt.Errorf("setting release ownership: %w", err)
	}

	// Step 6: Update slot symlink.
	fmt.Printf("  Updating slot %s -> %s\n", targetSlot, cfg.ReleaseID)
	if _, err := cfg.Client.Run(fmt.Sprintf("ln -sfn %s %s", releaseDir, slotLink)); err != nil {
		return nil, fmt.Errorf("updating slot symlink: %w", err)
	}

	// Step 7: Write runtime.env.
	fmt.Println("  Writing runtime.env...")
	if err := server.WriteRuntimeEnv(cfg.Client, cfg.RootDir, cfg.AppName, targetSlot, targetPort, app.Env); err != nil {
		return nil, fmt.Errorf("writing runtime.env: %w", err)
	}

	// Step 8: Restart systemd unit.
	fmt.Printf("  Starting %s...\n", unitName)
	if _, err := cfg.Client.Run(fmt.Sprintf("systemctl restart %s", unitName)); err != nil {
		return nil, fmt.Errorf("restarting %s: %w", unitName, err)
	}

	// Step 9: Health check.
	healthTimeout := time.Duration(app.HealthCheckTimeout) * time.Second
	fmt.Printf("  Waiting for health check (http://127.0.0.1:%d%s)...\n", targetPort, app.HealthCheckPath)
	if err := health.WaitForHealthy(cfg.Client, targetPort, app.HealthCheckPath, healthTimeout); err != nil {
		// Stop the failed slot — old slot remains untouched.
		cfg.Client.Run(fmt.Sprintf("systemctl stop %s", unitName))
		return nil, fmt.Errorf("health check failed, deploy aborted: %w", err)
	}
	fmt.Println("  Health check passed")

	// Step 10: Update Caddy route.
	hasPublic := cfg.PublicPath != ""
	fmt.Printf("  Switching traffic to %s (port %d)...\n", targetSlot, targetPort)
	routeCfg := caddy.RouteConfig{
		AppName:        cfg.AppName,
		Domains:        app.Domains,
		Port:           targetPort,
		HasPublic:      hasPublic,
		SlotPublicRoot: fmt.Sprintf("%s/slots/%s/%s", appDir, targetSlot, cfg.PublicPath),
	}
	if err := caddy.UpdateAppRoute(cfg.Client, routeCfg); err != nil {
		return nil, fmt.Errorf("updating Caddy route: %w", err)
	}

	// Step 11: Stop old slot (skip on first deploy).
	if activeSlot != "" {
		oldUnit := fmt.Sprintf("%s@%s.service", cfg.AppName, activeSlot)
		fmt.Printf("  Stopping %s...\n", oldUnit)
		if _, err := cfg.Client.Run(fmt.Sprintf("systemctl stop %s", oldUnit)); err != nil {
			fmt.Printf("  Warning: failed to stop old slot %s: %v\n", oldUnit, err)
		}
	}

	// Step 12: Update verna.json.
	slot := app.Slots[targetSlot]
	slot.Release = cfg.ReleaseID
	slot.DeployedAt = time.Now().UTC().Format(time.RFC3339)
	app.Slots[targetSlot] = slot
	app.ActiveSlot = targetSlot
	if err := server.WriteState(cfg.Client, cfg.RootDir, cfg.State); err != nil {
		fmt.Printf("  Warning: failed to write state: %v\n", err)
	}

	// Step 13: Prune old releases.
	pruned, err := pruneReleases(cfg.Client, cfg.RootDir, cfg.AppName, app)
	if err != nil {
		fmt.Printf("  Warning: prune failed: %v\n", err)
	} else if pruned > 0 {
		fmt.Printf("  Pruned %d old release(s)\n", pruned)
	}

	return &DeployResult{
		Release:  cfg.ReleaseID,
		Slot:     targetSlot,
		PrevSlot: activeSlot,
		Port:     targetPort,
	}, nil
}

// pruneReleases removes old releases beyond the retention limit, preserving any in use by a slot.
func pruneReleases(client *ssh.Client, rootDir, appName string, app *server.AppState) (int, error) {
	releasesDir := fmt.Sprintf("%s/apps/%s/releases", rootDir, appName)
	output, err := client.Run(fmt.Sprintf("ls -1 %s", releasesDir))
	if err != nil {
		return 0, fmt.Errorf("listing releases: %w", err)
	}

	output = strings.TrimSpace(output)
	if output == "" {
		return 0, nil
	}
	releases := strings.Split(output, "\n")

	if len(releases) <= app.ReleaseRetention {
		return 0, nil
	}

	// Determine which releases are in use.
	inUse := make(map[string]bool)
	for _, slot := range app.Slots {
		if slot.Release != "" {
			inUse[slot.Release] = true
		}
	}

	toRemove := selectReleasesToPrune(releases, app.ReleaseRetention, inUse)
	for _, rel := range toRemove {
		if _, err := client.Run(fmt.Sprintf("rm -rf %s/%s", releasesDir, rel)); err != nil {
			return len(toRemove), fmt.Errorf("removing release %s: %w", rel, err)
		}
	}
	return len(toRemove), nil
}

// selectReleasesToPrune returns the releases that should be removed based on retention and in-use status.
func selectReleasesToPrune(releases []string, retention int, inUse map[string]bool) []string {
	if len(releases) <= retention {
		return nil
	}

	sort.Strings(releases)

	candidates := releases[:len(releases)-retention]
	var toRemove []string
	for _, rel := range candidates {
		if !inUse[rel] {
			toRemove = append(toRemove, rel)
		}
	}
	return toRemove
}
