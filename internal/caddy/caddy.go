package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/verna/internal/ssh"
)

const caddyAdminAddr = "127.0.0.1:2019"
const caddyBaseURL = "http://localhost:2019"

type RouteConfig struct {
	AppName        string
	CaddyServer    string
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

// caddyPathExists checks if a Caddy admin API path returns a 2xx status.
func caddyPathExists(httpClient *http.Client, path string) bool {
	resp, err := httpClient.Get(caddyBaseURL + path)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// caddyPut sends a PUT request with JSON body to the Caddy admin API.
func caddyPut(httpClient *http.Client, path string, body []byte) error {
	req, err := http.NewRequest(http.MethodPut, caddyBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkResponse(resp, "PUT "+path)
}

// PingAdminAPI checks if the Caddy admin API is responding.
func PingAdminAPI(client *ssh.Client) error {
	httpClient := newHTTPClient(client)
	resp, err := httpClient.Get(caddyBaseURL + "/config/")
	if err != nil {
		return fmt.Errorf("caddy admin API not responding: %w", err)
	}
	resp.Body.Close()
	return checkResponse(resp, "caddy admin API ping")
}

// ListServers returns the names of all HTTP servers configured in Caddy.
func ListServers(client *ssh.Client) ([]string, error) {
	httpClient := newHTTPClient(client)
	resp, err := httpClient.Get(caddyBaseURL + "/config/apps/http/servers")
	if err != nil {
		return nil, nil // Caddy not reachable or no HTTP app configured
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil // No servers configured
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading servers response: %w", err)
	}

	var servers map[string]json.RawMessage
	if err := json.Unmarshal(body, &servers); err != nil {
		return nil, nil // Unexpected format, treat as no servers
	}

	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// CreateServer creates a new Caddy HTTP server with the given name, listening
// on :80 and :443 with an empty routes array. It builds intermediate config
// paths if they don't exist (e.g., on a fresh Caddy instance).
func CreateServer(client *ssh.Client, name string) error {
	httpClient := newHTTPClient(client)
	serverJSON := []byte(`{"listen":[":80",":443"],"routes":[]}`)

	// Fast path: try creating the server directly (works if /config/apps/http/servers exists).
	if err := caddyPut(httpClient, "/config/apps/http/servers/"+name, serverJSON); err == nil {
		return nil
	}

	// Build intermediate paths. Each level is only created if missing.
	if !caddyPathExists(httpClient, "/config/apps") {
		if err := caddyPut(httpClient, "/config/apps", []byte(`{}`)); err != nil {
			return fmt.Errorf("creating Caddy apps config: %w", err)
		}
	}
	if !caddyPathExists(httpClient, "/config/apps/http") {
		if err := caddyPut(httpClient, "/config/apps/http", []byte(`{"servers":{}}`)); err != nil {
			return fmt.Errorf("creating Caddy HTTP app: %w", err)
		}
	}
	if !caddyPathExists(httpClient, "/config/apps/http/servers") {
		if err := caddyPut(httpClient, "/config/apps/http/servers", []byte(`{}`)); err != nil {
			return fmt.Errorf("creating Caddy servers config: %w", err)
		}
	}

	if err := caddyPut(httpClient, "/config/apps/http/servers/"+name, serverJSON); err != nil {
		return fmt.Errorf("creating Caddy server %q: %w", name, err)
	}
	return nil
}

// EnsureServerRoutes ensures the routes array exists for the given Caddy server.
// If the server exists but routes is null/missing (e.g., after Caddy restart drops
// empty arrays), this recreates it.
func EnsureServerRoutes(client *ssh.Client, serverName string) error {
	httpClient := newHTTPClient(client)

	if caddyPathExists(httpClient, "/config/apps/http/servers/"+serverName+"/routes") {
		return nil
	}

	if err := caddyPut(httpClient, "/config/apps/http/servers/"+serverName+"/routes", []byte(`[]`)); err != nil {
		return fmt.Errorf("ensuring routes for Caddy server %q: %w", serverName, err)
	}
	return nil
}

// ResolveCaddyServer determines which Caddy server to use for an app.
// If caddyServerFlag is set, it verifies the server exists.
// Otherwise: 0 servers → creates "verna", 1 server → uses it, multiple → error.
func ResolveCaddyServer(client *ssh.Client, caddyServerFlag string) (string, error) {
	servers, err := ListServers(client)
	if err != nil {
		return "", fmt.Errorf("listing Caddy servers: %w", err)
	}

	if caddyServerFlag != "" {
		if slices.Contains(servers, caddyServerFlag) {
			return caddyServerFlag, nil
		}
		return "", fmt.Errorf("Caddy server %q not found (available: %s)", caddyServerFlag, strings.Join(servers, ", "))
	}

	switch len(servers) {
	case 0:
		fmt.Println("No Caddy HTTP servers found, creating \"verna\" server...")
		if err := CreateServer(client, "verna"); err != nil {
			return "", err
		}
		return "verna", nil
	case 1:
		fmt.Printf("Using Caddy server %q\n", servers[0])
		return servers[0], nil
	default:
		return "", fmt.Errorf("multiple Caddy servers found (%s), specify --caddy-server", strings.Join(servers, ", "))
	}
}

// AddAppRoute adds a reverse proxy route for the app to the specified Caddy server.
// It removes any existing route with the same @id first to prevent duplicates
// (e.g., from a previous failed app init that added the route but didn't complete).
func AddAppRoute(client *ssh.Client, cfg RouteConfig) error {
	routeJSON, err := buildRouteJSON(cfg)
	if err != nil {
		return fmt.Errorf("building route JSON: %w", err)
	}

	// Remove any stale route with this ID (ignore errors — may not exist).
	_ = DeleteAppRoute(client, cfg.AppName)

	httpClient := newHTTPClient(client)
	url := fmt.Sprintf("%s/config/apps/http/servers/%s/routes", caddyBaseURL, cfg.CaddyServer)
	resp, err := httpClient.Post(url, "application/json", bytes.NewReader(routeJSON))
	if err != nil {
		return fmt.Errorf("adding Caddy route for %s: %w", cfg.AppName, err)
	}
	defer resp.Body.Close()

	return checkResponse(resp, fmt.Sprintf("adding Caddy route for %s", cfg.AppName))
}

// UpdateAppRoute atomically replaces the existing Caddy route for the app
// using PATCH on its @id. PATCH strictly replaces an existing value in place.
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
	req, err := http.NewRequest(http.MethodPatch, fmt.Sprintf("%s/id/%s", caddyBaseURL, id), bytes.NewReader(routeJSON))
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
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/id/%s", caddyBaseURL, id), nil)
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
