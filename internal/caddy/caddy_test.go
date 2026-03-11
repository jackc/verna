package caddy

import (
	"encoding/json"
	"testing"
)

func TestBuildRouteJSON_DefaultTemplate(t *testing.T) {
	cfg := RouteConfig{
		AppName: "myapp",
		Domains: []string{"myapp.example.com"},
		Port:    18001,
		SlotDir: "/var/lib/verna/apps/myapp/slots/blue",
	}

	data, err := buildRouteJSON(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var route map[string]any
	if err := json.Unmarshal(data, &route); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if route["@id"] != "verna_myapp" {
		t.Errorf("expected @id %q, got %q", "verna_myapp", route["@id"])
	}

	matches := route["match"].([]any)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	matchObj := matches[0].(map[string]any)
	hosts := matchObj["host"].([]any)
	if len(hosts) != 1 || hosts[0] != "myapp.example.com" {
		t.Errorf("expected host [myapp.example.com], got %v", hosts)
	}

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

func TestBuildRouteJSON_MultipleDomains(t *testing.T) {
	cfg := RouteConfig{
		AppName: "myapp",
		Domains: []string{"myapp.example.com", "www.myapp.example.com"},
		Port:    18001,
		SlotDir: "/var/lib/verna/apps/myapp/slots/blue",
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

func TestBuildRouteJSON_CustomTemplate(t *testing.T) {
	cfg := RouteConfig{
		AppName: "myapp",
		Domains: []string{"myapp.example.com"},
		Port:    18001,
		SlotDir: "/var/lib/verna/apps/myapp/slots/blue",
		CaddyHandleTemplate: `[{"handler":"subroute","routes":[{"handle":[{"handler":"file_server","root":"{{.SlotDir}}/public","pass_thru":true}]},{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"{{.Dial}}"}]}]}]}]`,
	}

	data, err := buildRouteJSON(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var route map[string]any
	if err := json.Unmarshal(data, &route); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if route["@id"] != "verna_myapp" {
		t.Errorf("expected @id verna_myapp, got %v", route["@id"])
	}

	handles := route["handle"].([]any)
	if len(handles) != 1 {
		t.Fatalf("expected 1 top-level handler, got %d", len(handles))
	}
	subroute := handles[0].(map[string]any)
	if subroute["handler"] != "subroute" {
		t.Fatalf("expected subroute handler, got %v", subroute["handler"])
	}

	routes := subroute["routes"].([]any)
	if len(routes) != 2 {
		t.Fatalf("expected 2 subroutes, got %d", len(routes))
	}

	// Route 1: file_server with pass_thru.
	r1 := routes[0].(map[string]any)
	r1Handles := r1["handle"].([]any)
	r1FileServer := r1Handles[0].(map[string]any)
	if r1FileServer["handler"] != "file_server" {
		t.Errorf("expected file_server handler, got %v", r1FileServer["handler"])
	}
	if r1FileServer["root"] != "/var/lib/verna/apps/myapp/slots/blue/public" {
		t.Errorf("expected correct root, got %v", r1FileServer["root"])
	}
	if r1FileServer["pass_thru"] != true {
		t.Errorf("expected pass_thru true, got %v", r1FileServer["pass_thru"])
	}

	// Route 2: reverse_proxy fallback.
	r2 := routes[1].(map[string]any)
	r2Handles := r2["handle"].([]any)
	r2Proxy := r2Handles[0].(map[string]any)
	if r2Proxy["handler"] != "reverse_proxy" {
		t.Errorf("expected reverse_proxy handler, got %v", r2Proxy["handler"])
	}
	upstreams := r2Proxy["upstreams"].([]any)
	upstream := upstreams[0].(map[string]any)
	if upstream["dial"] != "127.0.0.1:18001" {
		t.Errorf("expected dial 127.0.0.1:18001, got %v", upstream["dial"])
	}
}

func TestValidateHandleTemplate_Valid(t *testing.T) {
	err := ValidateHandleTemplate(`[{"handler": "reverse_proxy", "upstreams": [{"dial": "{{.Dial}}"}]}]`)
	if err != nil {
		t.Errorf("expected valid template, got error: %v", err)
	}
}

func TestValidateHandleTemplate_InvalidSyntax(t *testing.T) {
	err := ValidateHandleTemplate(`[{"handler": "reverse_proxy", "upstreams": [{"dial": "{{.Dial}"}]}]`)
	if err == nil {
		t.Error("expected error for invalid template syntax")
	}
}

func TestValidateHandleTemplate_InvalidJSON(t *testing.T) {
	err := ValidateHandleTemplate(`not json {{.Dial}}`)
	if err == nil {
		t.Error("expected error for template producing invalid JSON")
	}
}

func TestValidateHandleTemplate_NotArray(t *testing.T) {
	err := ValidateHandleTemplate(`{"handler": "reverse_proxy"}`)
	if err == nil {
		t.Error("expected error for template producing JSON object instead of array")
	}
}
