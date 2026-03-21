package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/verna/internal/ssh"
)

// FormatRuntimeEnv produces the content of a runtime.env file in systemd EnvironmentFile format.
// PORT is always the first entry. User env vars follow in sorted order.
func FormatRuntimeEnv(port int, envVars map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PORT=%d\n", port)

	keys := make([]string, 0, len(envVars))
	for k := range envVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := envVars[k]
		if needsQuoting(v) {
			fmt.Fprintf(&b, "%s=%q\n", k, v)
		} else {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
		}
	}
	return b.String()
}

// needsQuoting returns true if a value contains characters that need quoting in a systemd EnvironmentFile.
func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	for _, c := range s {
		if c == ' ' || c == '\t' || c == '"' || c == '\'' || c == '\\' || c == '#' || c == ';' || c == '$' {
			return true
		}
	}
	return false
}

// WriteRuntimeEnv writes the runtime.env file for a given app and slot.
// It creates the env/ directory inside the slot path if needed.
func WriteRuntimeEnv(client *ssh.Client, rootDir, appName, slot string, port int, envVars map[string]string) error {
	envDir := fmt.Sprintf("%s/apps/%s/slots/%s/.verna/env", rootDir, appName, slot)

	if _, err := client.Run(fmt.Sprintf("mkdir -p %s", envDir)); err != nil {
		return fmt.Errorf("creating env directory: %w", err)
	}

	content := FormatRuntimeEnv(port, envVars)

	envPath := envDir + "/runtime.env"
	tmpPath := envPath + ".tmp"
	writeCmd := fmt.Sprintf("cat > %q << 'VERNA_ENV_EOF'\n%sVERNA_ENV_EOF", tmpPath, content)
	if _, err := client.Run(writeCmd); err != nil {
		return fmt.Errorf("writing runtime.env: %w", err)
	}
	if _, err := client.Run(fmt.Sprintf("mv %q %q", tmpPath, envPath)); err != nil {
		return fmt.Errorf("renaming runtime.env: %w", err)
	}

	return nil
}
