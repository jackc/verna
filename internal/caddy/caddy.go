package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/jackc/verna/internal/ssh"
)

const caddyAdminAddr = "127.0.0.1:2019"

type RouteConfig struct {
	AppName        string
	Domains        []string
	Port           int
	HasPublic      bool
	SlotPublicRoot string // e.g. "/var/lib/verna/apps/myapp/slots/blue/public"
}

func newHTTPClient(sshClient *ssh.Client) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return sshClient.Dial("tcp", caddyAdminAddr)
			},
		},
		Timeout: 10 * time.Second,
	}
}

func checkResponse(resp *http.Response, action string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("%s: HTTP %d: %s", action, resp.StatusCode, string(body))
}

// PingAdminAPI checks if the Caddy admin API is responding.
func PingAdminAPI(client *ssh.Client) error {
	httpClient := newHTTPClient(client)
	resp, err := httpClient.Get("http://localhost:2019/config/")
	if err != nil {
		return fmt.Errorf("caddy admin API not responding: %w", err)
	}
	resp.Body.Close()
	return checkResponse(resp, "caddy admin API ping")
}

// EnsureVernaCaddyServer checks if the "verna" HTTP server exists in Caddy config
// and creates it if absent.
func EnsureVernaCaddyServer(client *ssh.Client) error {
	httpClient := newHTTPClient(client)

	// Check if the verna server already exists.
	resp, err := httpClient.Get("http://localhost:2019/config/apps/http/servers/verna")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
	}

	// Bootstrap the server with listen addresses and empty routes.
	serverJSON := []byte(`{"listen":[":80",":443"],"routes":[]}`)
	req, err := http.NewRequest(http.MethodPut, "http://localhost:2019/config/apps/http/servers/verna", bytes.NewReader(serverJSON))
	if err != nil {
		return fmt.Errorf("creating Caddy verna server: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("creating Caddy verna server: %w", err)
	}
	defer resp.Body.Close()

	return checkResponse(resp, "creating Caddy verna server")
}

// AddAppRoute adds a reverse proxy route for the app to the Caddy verna server.
func AddAppRoute(client *ssh.Client, cfg RouteConfig) error {
	routeJSON, err := buildRouteJSON(cfg)
	if err != nil {
		return fmt.Errorf("building route JSON: %w", err)
	}

	httpClient := newHTTPClient(client)
	resp, err := httpClient.Post("http://localhost:2019/config/apps/http/servers/verna/routes", "application/json", bytes.NewReader(routeJSON))
	if err != nil {
		return fmt.Errorf("adding Caddy route for %s: %w", cfg.AppName, err)
	}
	defer resp.Body.Close()

	return checkResponse(resp, fmt.Sprintf("adding Caddy route for %s", cfg.AppName))
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
	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("http://localhost:2019/id/%s", id), bytes.NewReader(routeJSON))
	if err != nil {
		return fmt.Errorf("updating Caddy route for %s: %w", cfg.AppName, err)
	}
	req.Header.Set("Content-Type", "application/json")

	httpClient := newHTTPClient(client)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("updating Caddy route for %s: %w", cfg.AppName, err)
	}
	defer resp.Body.Close()

	return checkResponse(resp, fmt.Sprintf("updating Caddy route for %s", cfg.AppName))
}

// DeleteAppRoute removes the Caddy route for the app via the admin API.
func DeleteAppRoute(client *ssh.Client, appName string) error {
	id := "verna_" + appName
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("http://localhost:2019/id/%s", id), nil)
	if err != nil {
		return fmt.Errorf("deleting Caddy route for %s: %w", appName, err)
	}

	httpClient := newHTTPClient(client)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("deleting Caddy route for %s: %w", appName, err)
	}
	defer resp.Body.Close()

	return checkResponse(resp, fmt.Sprintf("deleting Caddy route for %s", appName))
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
