package systemd

import (
	"strings"
	"testing"
)

func TestGenerateTemplateUnit(t *testing.T) {
	cfg := UnitConfig{
		AppName: "myapp",
		User:    "verna-myapp",
		Group:   "verna-myapp",
		RootDir: "/var/verna",
	}

	unit, err := GenerateTemplateUnit(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []struct {
		name     string
		expected string
	}{
		{"description", "Description=myapp (%i)"},
		{"user", "User=verna-myapp"},
		{"group", "Group=verna-myapp"},
		{"working dir", "WorkingDirectory=/var/verna/apps/myapp/slots/%i"},
		{"env file", "EnvironmentFile=-/var/verna/apps/myapp/slots/%i/env/runtime.env"},
		{"exec start", "ExecStart=/var/verna/apps/myapp/slots/%i/bin/myapp"},
		{"verna app env", "Environment=VERNA_APP=myapp"},
		{"verna slot env", "Environment=VERNA_SLOT=%i"},
		{"read write paths", "ReadWritePaths=/var/verna/apps/myapp/shared"},
	}

	for _, c := range checks {
		if !strings.Contains(unit, c.expected) {
			t.Errorf("%s: expected unit to contain %q", c.name, c.expected)
		}
	}
}

func TestGenerateTemplateUnitWithExecArgs(t *testing.T) {
	cfg := UnitConfig{
		AppName:  "myapp",
		User:     "verna-myapp",
		Group:    "verna-myapp",
		RootDir:  "/var/verna",
		ExecArgs: []string{"--config", "/etc/myapp.toml"},
	}

	unit, err := GenerateTemplateUnit(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "ExecStart=/var/verna/apps/myapp/slots/%i/bin/myapp --config /etc/myapp.toml"
	if !strings.Contains(unit, expected) {
		t.Errorf("expected unit to contain %q, got:\n%s", expected, unit)
	}
}

func TestGenerateTemplateUnitWithHyphenatedName(t *testing.T) {
	cfg := UnitConfig{
		AppName: "my-app",
		User:    "verna-my-app",
		Group:   "verna-my-app",
		RootDir: "/var/verna",
	}

	unit, err := GenerateTemplateUnit(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(unit, "Description=my-app (%i)") {
		t.Error("expected description to contain hyphenated app name")
	}
	if !strings.Contains(unit, "ExecStart=/var/verna/apps/my-app/slots/%i/bin/my-app") {
		t.Error("expected exec start to contain hyphenated app name")
	}
}
