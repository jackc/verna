package caddy

import (
	"encoding/json"
	"fmt"

	"github.com/jackc/verna/internal/ssh"
)

type RouteConfig struct {
	AppName        string
	Domains        []string
	Port           int
	HasPublic      bool
	SlotPublicRoot string // e.g. "/var/lib/verna/apps/myapp/slots/blue/public"
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

// UpdateAppRoute replaces the existing Caddy route for the app atomically via PUT.
func UpdateAppRoute(client *ssh.Client, cfg RouteConfig) error {
	var routeJSON []byte
	var err error
	if cfg.HasPublic {
		routeJSON, err = buildRouteWithPublicJSON(cfg)
	} else {
		routeJSON, err = buildRouteJSON(cfg)
	}
	if err != nil {
		return fmt.Errorf("building route JSON: %w", err)
	}

	id := "verna_" + cfg.AppName
	cmd := fmt.Sprintf("curl -sf -X PUT -H 'Content-Type: application/json' -d %q localhost:2019/id/%s", string(routeJSON), id)
	if _, err := client.Run(cmd); err != nil {
		return fmt.Errorf("updating Caddy route for %s: %w", cfg.AppName, err)
	}
	return nil
}

// DeleteAppRoute removes the Caddy route for the app via the admin API.
func DeleteAppRoute(client *ssh.Client, appName string) error {
	id := "verna_" + appName
	cmd := fmt.Sprintf("curl -sf -X DELETE http://localhost:2019/id/%s", id)
	if _, err := client.Run(cmd); err != nil {
		return fmt.Errorf("deleting Caddy route for %s: %w", appName, err)
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

func buildRouteWithPublicJSON(cfg RouteConfig) ([]byte, error) {
	route := map[string]any{
		"@id": "verna_" + cfg.AppName,
		"match": []map[string]any{
			{"host": cfg.Domains},
		},
		"handle": []map[string]any{
			{
				"handler": "subroute",
				"routes": []map[string]any{
					// Route 1: /assets/* with immutable cache headers
					{
						"match": []map[string]any{
							{"path": []string{"/assets/*"}},
						},
						"handle": []map[string]any{
							{
								"handler": "headers",
								"response": map[string]any{
									"set": map[string][]string{
										"Cache-Control": {"public, max-age=31536000, immutable"},
									},
								},
							},
							{
								"handler": "file_server",
								"root":    cfg.SlotPublicRoot,
							},
						},
					},
					// Route 2: All paths, file_server with pass_thru + no-cache
					{
						"handle": []map[string]any{
							{
								"handler": "headers",
								"response": map[string]any{
									"set": map[string][]string{
										"Cache-Control": {"no-cache"},
									},
								},
							},
							{
								"handler":   "file_server",
								"root":      cfg.SlotPublicRoot,
								"pass_thru": true,
							},
						},
					},
					// Route 3: Fallthrough to reverse_proxy
					{
						"handle": []map[string]any{
							{
								"handler": "reverse_proxy",
								"upstreams": []map[string]string{
									{"dial": fmt.Sprintf("127.0.0.1:%d", cfg.Port)},
								},
							},
						},
					},
				},
			},
		},
	}

	return json.Marshal(route)
}
