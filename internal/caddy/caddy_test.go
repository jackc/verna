package caddy

import (
	"encoding/json"
	"testing"
)

func TestBuildRouteJSON(t *testing.T) {
	cfg := RouteConfig{
		AppName: "myapp",
		Domains: []string{"myapp.example.com"},
		Port:    18001,
	}

	data, err := buildRouteJSON(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var route map[string]any
	if err := json.Unmarshal(data, &route); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Check @id.
	if route["@id"] != "verna_myapp" {
		t.Errorf("expected @id %q, got %q", "verna_myapp", route["@id"])
	}

	// Check match hosts.
	matches := route["match"].([]any)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	matchObj := matches[0].(map[string]any)
	hosts := matchObj["host"].([]any)
	if len(hosts) != 1 || hosts[0] != "myapp.example.com" {
		t.Errorf("expected host [myapp.example.com], got %v", hosts)
	}

	// Check handle upstream.
	handles := route["handle"].([]any)
	if len(handles) != 1 {
		t.Fatalf("expected 1 handler, got %d", len(handles))
	}
	handler := handles[0].(map[string]any)
	if handler["handler"] != "reverse_proxy" {
		t.Errorf("expected handler reverse_proxy, got %v", handler["handler"])
	}
	upstreams := handler["upstreams"].([]any)
	if len(upstreams) != 1 {
		t.Fatalf("expected 1 upstream, got %d", len(upstreams))
	}
	upstream := upstreams[0].(map[string]any)
	if upstream["dial"] != "127.0.0.1:18001" {
		t.Errorf("expected dial 127.0.0.1:18001, got %v", upstream["dial"])
	}
}

func TestBuildRouteJSONMultipleDomains(t *testing.T) {
	cfg := RouteConfig{
		AppName: "myapp",
		Domains: []string{"myapp.example.com", "www.myapp.example.com"},
		Port:    18001,
	}

	data, err := buildRouteJSON(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var route map[string]any
	if err := json.Unmarshal(data, &route); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	matches := route["match"].([]any)
	matchObj := matches[0].(map[string]any)
	hosts := matchObj["host"].([]any)
	if len(hosts) != 2 {
		t.Errorf("expected 2 hosts, got %d", len(hosts))
	}
}
