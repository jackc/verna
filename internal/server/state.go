package server

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/verna/internal/ssh"
)

const DefaultStartPort = 18001

type StateMetadata struct {
	Verna    string `json:"verna"`
	Username string `json:"username"`
}

type ServerState struct {
	LastModifiedBy *StateMetadata       `json:"last_modified_by,omitempty"`
	NextPort       int                  `json:"next_port"`
	Apps           map[string]*AppState `json:"apps"`
}

type AppState struct {
	Domains                []string            `json:"domains"`
	ExecPath               string              `json:"exec_path"`
	HealthCheckPath        string              `json:"health_check_path"`
	HealthCheckTimeout     int                 `json:"health_check_timeout"`
	ReleaseRetention       int                 `json:"release_retention"`
	User                   string              `json:"user"`
	Group                  string              `json:"group"`
	ExecArgs               []string             `json:"exec_args,omitempty"`
	CaddyServer            string              `json:"caddy_server"`
	Env                    map[string]string    `json:"env,omitempty"`
	ActiveSlot             string              `json:"active_slot"`
	Slots                  map[string]SlotState `json:"slots"`
}

type SlotState struct {
	Port                int    `json:"port"`
	Release             string `json:"release,omitempty"`
	DeployedAt          string `json:"deployed_at,omitempty"`
	CaddyHandleTemplate string `json:"caddy_handle_template,omitempty"`
}

// EffectiveCaddyHandleTemplate returns the Caddy handle template for the given slot.
// Returns the slot's template if set, otherwise empty string.
func (a *AppState) EffectiveCaddyHandleTemplate(slotName string) string {
	if slot, ok := a.Slots[slotName]; ok && slot.CaddyHandleTemplate != "" {
		return slot.CaddyHandleTemplate
	}
	return ""
}

func NewServerState() *ServerState {
	return &ServerState{
		NextPort: DefaultStartPort,
		Apps:     make(map[string]*AppState),
	}
}

func Parse(data []byte) (*ServerState, error) {
	var state ServerState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing server state: %w", err)
	}
	if state.Apps == nil {
		state.Apps = make(map[string]*AppState)
	}
	return &state, nil
}

func Marshal(state *ServerState) ([]byte, error) {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling server state: %w", err)
	}
	data = append(data, '\n')
	return data, nil
}

func ReadState(client *ssh.Client, rootDir string) (*ServerState, error) {
	path := rootDir + "/verna.json"
	output, err := client.Run(fmt.Sprintf("cat %q", path))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse([]byte(output))
}

func WriteState(client *ssh.Client, rootDir string, state *ServerState, meta StateMetadata) error {
	state.LastModifiedBy = &meta
	data, err := Marshal(state)
	if err != nil {
		return err
	}

	path := rootDir + "/verna.json"

	// Backup current state file (ignore errors if file doesn't exist yet).
	backupPath := path + ".bak." + fmt.Sprintf("%d", time.Now().UnixNano())
	client.Run(fmt.Sprintf("cp %q %q 2>/dev/null", path, backupPath))

	// Prune old backups, keeping only the most recent 10.
	client.Run(fmt.Sprintf("ls -1t %q.bak.* 2>/dev/null | tail -n +11 | xargs -r rm --", path))

	tmpPath := path + ".tmp." + fmt.Sprintf("%d", time.Now().UnixNano())

	// Write to temp file, then atomically rename.
	if _, err := client.Run(fmt.Sprintf("cat > %q << 'VERNA_EOF'\n%sVERNA_EOF", tmpPath, string(data))); err != nil {
		return fmt.Errorf("writing temp state file: %w", err)
	}
	if _, err := client.Run(fmt.Sprintf("mv %q %q", tmpPath, path)); err != nil {
		return fmt.Errorf("renaming temp state file: %w", err)
	}

	return nil
}
