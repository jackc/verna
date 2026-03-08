package caddy

import (
	"encoding/json"
	"fmt"

	"github.com/jackc/verna/internal/ssh"
)

type RouteConfig struct {
	AppName string
	Domains []string
	Port    int
}

// EnsureVernaCaddyServer checks if the "verna" HTTP server exists in Caddy config
// and creates it if absent.
func EnsureVernaCaddyServer(client *ssh.Client) error {
	// Check if the verna server already exists.
	if _, err := client.Run("curl -sf localhost:2019/config/apps/http/servers/verna"); err == nil {
		return nil
	}

	// Bootstrap the server with listen addresses and empty routes.
	serverJSON := `{"listen":[":80",":443"],"routes":[]}`
	cmd := fmt.Sprintf("curl -sf -X PUT -H 'Content-Type: application/json' -d %q localhost:2019/config/apps/http/servers/verna", serverJSON)
	if _, err := client.Run(cmd); err != nil {
		return fmt.Errorf("creating Caddy verna server: %w", err)
	}

	return nil
}

// AddAppRoute adds a reverse proxy route for the app to the Caddy verna server.
func AddAppRoute(client *ssh.Client, cfg RouteConfig) error {
	routeJSON, err := buildRouteJSON(cfg)
	if err != nil {
		return fmt.Errorf("building route JSON: %w", err)
	}

	cmd := fmt.Sprintf("curl -sf -X POST -H 'Content-Type: application/json' -d %q localhost:2019/config/apps/http/servers/verna/routes", string(routeJSON))
	if _, err := client.Run(cmd); err != nil {
		return fmt.Errorf("adding Caddy route for %s: %w", cfg.AppName, err)
	}

	return nil
}

func buildRouteJSON(cfg RouteConfig) ([]byte, error) {
	route := map[string]any{
		"@id": "verna_" + cfg.AppName,
		"match": []map[string]any{
			{"host": cfg.Domains},
		},
		"handle": []map[string]any{
			{
				"handler": "reverse_proxy",
				"upstreams": []map[string]string{
					{"dial": fmt.Sprintf("127.0.0.1:%d", cfg.Port)},
				},
			},
		},
	}

	return json.Marshal(route)
}
