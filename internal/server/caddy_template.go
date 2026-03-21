package server

import (
	"fmt"

	"github.com/jackc/verna/internal/ssh"
)

// WriteCaddyHandleTemplate writes the caddy handle template file for a given app and slot.
// It creates the .verna/ directory inside the slot path if needed.
func WriteCaddyHandleTemplate(client *ssh.Client, rootDir, appName, slot, template string) error {
	vernaDir := fmt.Sprintf("%s/apps/%s/slots/%s/.verna", rootDir, appName, slot)

	if _, err := client.Run(fmt.Sprintf("mkdir -p %s", vernaDir)); err != nil {
		return fmt.Errorf("creating .verna directory: %w", err)
	}

	filePath := vernaDir + "/caddy-handle-template.json"
	tmpPath := filePath + ".tmp"
	writeCmd := fmt.Sprintf("cat > %q << 'VERNA_CADDY_EOF'\n%s\nVERNA_CADDY_EOF", tmpPath, template)
	if _, err := client.Run(writeCmd); err != nil {
		return fmt.Errorf("writing caddy-handle-template.json: %w", err)
	}
	if _, err := client.Run(fmt.Sprintf("mv %q %q", tmpPath, filePath)); err != nil {
		return fmt.Errorf("renaming caddy-handle-template.json: %w", err)
	}

	return nil
}
