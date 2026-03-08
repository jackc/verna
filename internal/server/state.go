package server

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/verna/internal/ssh"
)

const DefaultStartPort = 18001

type ServerState struct {
	NextPort int                  `json:"next_port"`
	Apps     map[string]*AppState `json:"apps"`
}

type AppState struct {
	Domains            []string            `json:"domains"`
	HealthCheckPath    string              `json:"health_check_path"`
	HealthCheckTimeout int                 `json:"health_check_timeout"`
	ReleaseRetention   int                 `json:"release_retention"`
	User               string              `json:"user"`
	Group              string              `json:"group"`
	ExecArgs           []string             `json:"exec_args,omitempty"`
	Env                map[string]string    `json:"env,omitempty"`
	ActiveSlot         string              `json:"active_slot"`
	Slots              map[string]SlotState `json:"slots"`
}

type SlotState struct {
	Port       int    `json:"port"`
	Release    string `json:"release,omitempty"`
	DeployedAt string `json:"deployed_at,omitempty"`
	Commit     string `json:"commit,omitempty"`
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

func WriteState(client *ssh.Client, rootDir string, state *ServerState) error {
	data, err := Marshal(state)
	if err != nil {
		return err
	}

	path := rootDir + "/verna.json"
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
