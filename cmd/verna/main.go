package main

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"strings"

	"github.com/jackc/verna/internal/caddy"
	"github.com/jackc/verna/internal/server"
	"github.com/spf13/cobra"
)

// Set by goreleaser at build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var (
	flagSSHHost    string
	flagSSHUser    string
	flagSSHPort    int
	flagSSHKeyFile string
)

func main() {
	rootCmd := &cobra.Command{
		Use:           "verna",
		Short:         "Systemd-native blue/green deployment tool",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.PersistentFlags().StringVar(&flagSSHHost, "ssh-host", "", "SSH host (env: VERNA_SSH_HOST)")
	rootCmd.PersistentFlags().StringVar(&flagSSHUser, "ssh-user", "root", "SSH user (env: VERNA_SSH_USER)")
	rootCmd.PersistentFlags().IntVar(&flagSSHPort, "ssh-port", 22, "SSH port (env: VERNA_SSH_PORT)")
	rootCmd.PersistentFlags().StringVar(&flagSSHKeyFile, "ssh-key-file", "", "path to SSH private key (env: VERNA_SSH_KEY_FILE)")

	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		applyEnvDefaults(cmd)
	}

	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Server management commands",
	}
	serverCmd.AddCommand(newServerInitCmd())
	serverCmd.AddCommand(newServerInstallCaddyCmd())
	serverCmd.AddCommand(newServerDoctorCmd())

	appCmd := newAppCmd()
	appCmd.AddCommand(newAppInitCmd())
	configCmd := newConfigCmd()
	configCmd.AddCommand(newConfigGetCmd())
	configCmd.AddCommand(newConfigSetCmd())
	configCmd.AddCommand(newConfigListCmd())
	appCmd.AddCommand(configCmd)

	envCmd := newEnvCmd()
	envCmd.AddCommand(newEnvListCmd())
	envCmd.AddCommand(newEnvGetCmd())
	envCmd.AddCommand(newEnvSetCmd())
	envCmd.AddCommand(newEnvUnsetCmd())
	appCmd.AddCommand(envCmd)

	appCmd.AddCommand(newDeployCmd())
	appCmd.AddCommand(newStatusCmd())
	appCmd.AddCommand(newRollbackCmd())
	appCmd.AddCommand(newLogsCmd())
	appCmd.AddCommand(newAppDeleteCmd())

	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(appCmd)
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("verna version %s\n", version)
			fmt.Printf("  commit: %s\n", commit)
			fmt.Printf("  built:  %s\n", date)
		},
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ensureCaddyHandleTemplateFile checks if the caddy handle template file exists
// at the given path. If it doesn't, it interactively offers to create it from a
// preset. Returns the validated template content.
func ensureCaddyHandleTemplateFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		// File exists — validate and return.
		tmpl := strings.TrimRight(string(data), "\n")
		if err := caddy.ValidateHandleTemplate(tmpl); err != nil {
			return "", fmt.Errorf("invalid caddy handle template in %s: %w", path, err)
		}
		fmt.Printf("Using caddy handle template from %s\n", path)
		return tmpl, nil
	}

	if !os.IsNotExist(err) {
		return "", fmt.Errorf("reading caddy handle template %s: %w", path, err)
	}

	// File doesn't exist — offer to create from a preset.
	fmt.Printf("Caddy handle template not found at %s\n", path)
	fmt.Println("Available presets:")
	presetNames := caddy.PresetNames()
	for i, name := range presetNames {
		fmt.Printf("  %d) %s\n", i+1, name)
	}
	fmt.Print("\nSelect a preset [1]: ")

	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(answer)

	choice := 0
	if answer == "" {
		choice = 1
	} else {
		if _, err := fmt.Sscanf(answer, "%d", &choice); err != nil || choice < 1 || choice > len(presetNames) {
			return "", fmt.Errorf("invalid selection %q", answer)
		}
	}

	selectedPreset := presetNames[choice-1]
	tmpl, _ := caddy.ResolvePreset(selectedPreset)

	// Create parent directories and write the file.
	dir := path
	if lastSlash := strings.LastIndex(dir, "/"); lastSlash >= 0 {
		dir = dir[:lastSlash]
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, []byte(tmpl+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("writing caddy handle template %s: %w", path, err)
	}

	fmt.Printf("Wrote %s preset to %s\n", selectedPreset, path)
	return tmpl, nil
}

func newStateMetadata() server.StateMetadata {
	username := "unknown"
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	return server.StateMetadata{
		Verna:    version,
		Username: username,
	}
}

func applyEnvDefaults(cmd *cobra.Command) {
	envFlags := []struct {
		flag   string
		envVar string
	}{
		{"ssh-host", "VERNA_SSH_HOST"},
		{"ssh-user", "VERNA_SSH_USER"},
		{"ssh-port", "VERNA_SSH_PORT"},
		{"ssh-key-file", "VERNA_SSH_KEY_FILE"},
		{"app", "VERNA_APP"},
		{"caddy-handle-template-path", "VERNA_CADDY_HANDLE_TEMPLATE_PATH"},
	}

	for _, ef := range envFlags {
		f := cmd.Flags().Lookup(ef.flag)
		if f == nil || f.Changed {
			continue
		}
		if v, ok := os.LookupEnv(ef.envVar); ok {
			f.Value.Set(v)
		}
	}
}
