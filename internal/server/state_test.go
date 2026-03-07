package server

import (
	"testing"
)

func TestNewServerState(t *testing.T) {
	state := NewServerState()
	if state.NextPort != DefaultStartPort {
		t.Errorf("expected NextPort %d, got %d", DefaultStartPort, state.NextPort)
	}
	if state.Apps == nil {
		t.Fatal("expected Apps to be initialized")
	}
	if len(state.Apps) != 0 {
		t.Errorf("expected empty Apps, got %d", len(state.Apps))
	}
}

func TestMarshalParseRoundTrip(t *testing.T) {
	state := NewServerState()
	state.Apps["testapp"] = &AppState{
		Domains:            []string{"test.example.com"},
		HealthCheckPath:    "/health",
		HealthCheckTimeout: 15,
		ReleaseRetention:   5,
		User:               "testapp",
		Group:              "testapp",
		Env:                map[string]string{"KEY": "value"},
		ActiveSlot:         "blue",
		Slots: map[string]SlotState{
			"blue": {
				Port:       18001,
				Release:    "20260307T120102Z-1f2e3d4",
				DeployedAt: "2026-03-07T12:01:15Z",
				Commit:     "1f2e3d4",
			},
			"green": {
				Port: 18002,
			},
		},
	}

	data, err := Marshal(state)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	parsed, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if parsed.NextPort != state.NextPort {
		t.Errorf("NextPort: expected %d, got %d", state.NextPort, parsed.NextPort)
	}

	app, ok := parsed.Apps["testapp"]
	if !ok {
		t.Fatal("expected testapp in parsed state")
	}

	if app.ActiveSlot != "blue" {
		t.Errorf("ActiveSlot: expected blue, got %s", app.ActiveSlot)
	}
	if len(app.Domains) != 1 || app.Domains[0] != "test.example.com" {
		t.Errorf("Domains: expected [test.example.com], got %v", app.Domains)
	}
	if app.Env["KEY"] != "value" {
		t.Errorf("Env[KEY]: expected value, got %s", app.Env["KEY"])
	}

	blue := app.Slots["blue"]
	if blue.Port != 18001 {
		t.Errorf("blue port: expected 18001, got %d", blue.Port)
	}
	if blue.Release != "20260307T120102Z-1f2e3d4" {
		t.Errorf("blue release: expected 20260307T120102Z-1f2e3d4, got %s", blue.Release)
	}

	green := app.Slots["green"]
	if green.Port != 18002 {
		t.Errorf("green port: expected 18002, got %d", green.Port)
	}
	if green.Release != "" {
		t.Errorf("green release: expected empty, got %s", green.Release)
	}
}

func TestParseEmptyState(t *testing.T) {
	data := []byte(`{"next_port": 18001, "apps": {}}`)
	state, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(state.Apps) != 0 {
		t.Errorf("expected empty Apps, got %d", len(state.Apps))
	}
	if state.NextPort != 18001 {
		t.Errorf("expected NextPort 18001, got %d", state.NextPort)
	}
}

func TestParseNullApps(t *testing.T) {
	data := []byte(`{"next_port": 18001}`)
	state, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if state.Apps == nil {
		t.Fatal("expected Apps to be initialized even when null in JSON")
	}
}

func TestMultipleAppsPreserved(t *testing.T) {
	state := NewServerState()
	state.Apps["app1"] = &AppState{
		Domains: []string{"app1.example.com"},
		User:    "app1",
		Group:   "app1",
		Slots:   map[string]SlotState{},
	}
	state.Apps["app2"] = &AppState{
		Domains: []string{"app2.example.com"},
		User:    "app2",
		Group:   "app2",
		Slots:   map[string]SlotState{},
	}

	data, err := Marshal(state)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	parsed, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(parsed.Apps) != 2 {
		t.Errorf("expected 2 apps, got %d", len(parsed.Apps))
	}
	if _, ok := parsed.Apps["app1"]; !ok {
		t.Error("app1 missing after round-trip")
	}
	if _, ok := parsed.Apps["app2"]; !ok {
		t.Error("app2 missing after round-trip")
	}
}
