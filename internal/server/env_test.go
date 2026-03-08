package server

import (
	"strings"
	"testing"
)

func TestFormatRuntimeEnvPortOnly(t *testing.T) {
	result := FormatRuntimeEnv(18001, nil)
	if result != "PORT=18001\n" {
		t.Errorf("expected PORT=18001\\n, got %q", result)
	}
}

func TestFormatRuntimeEnvSimpleValues(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL": "postgres://localhost/db",
		"APP_MODE":     "production",
	}
	result := FormatRuntimeEnv(18001, env)
	lines := strings.Split(strings.TrimSpace(result), "\n")

	if lines[0] != "PORT=18001" {
		t.Errorf("expected PORT=18001 as first line, got %q", lines[0])
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	// Keys should be sorted: APP_MODE before DATABASE_URL
	if lines[1] != "APP_MODE=production" {
		t.Errorf("expected APP_MODE=production, got %q", lines[1])
	}
	if lines[2] != "DATABASE_URL=postgres://localhost/db" {
		t.Errorf("expected DATABASE_URL=postgres://localhost/db, got %q", lines[2])
	}
}

func TestFormatRuntimeEnvQuotedValues(t *testing.T) {
	env := map[string]string{
		"WITH_SPACE":  "hello world",
		"WITH_DOLLAR": "price$100",
		"WITH_HASH":   "color#red",
		"EMPTY":       "",
	}
	result := FormatRuntimeEnv(18001, env)

	if !strings.Contains(result, `EMPTY=""`) {
		t.Errorf("expected EMPTY to be quoted, got %q", result)
	}
	if !strings.Contains(result, `WITH_SPACE="hello world"`) {
		t.Errorf("expected WITH_SPACE to be quoted, got %q", result)
	}
	if !strings.Contains(result, `WITH_DOLLAR="price$100"`) {
		t.Errorf("expected WITH_DOLLAR to be quoted, got %q", result)
	}
	if !strings.Contains(result, `WITH_HASH="color#red"`) {
		t.Errorf("expected WITH_HASH to be quoted, got %q", result)
	}
}

func TestFormatRuntimeEnvSortedKeys(t *testing.T) {
	env := map[string]string{
		"ZEBRA": "z",
		"ALPHA": "a",
		"MANGO": "m",
	}
	result := FormatRuntimeEnv(18001, env)
	lines := strings.Split(strings.TrimSpace(result), "\n")

	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}
	if lines[1] != "ALPHA=a" {
		t.Errorf("expected ALPHA first, got %q", lines[1])
	}
	if lines[2] != "MANGO=m" {
		t.Errorf("expected MANGO second, got %q", lines[2])
	}
	if lines[3] != "ZEBRA=z" {
		t.Errorf("expected ZEBRA third, got %q", lines[3])
	}
}

func TestFormatRuntimeEnvEmptyMap(t *testing.T) {
	result := FormatRuntimeEnv(18002, map[string]string{})
	if result != "PORT=18002\n" {
		t.Errorf("expected PORT=18002\\n, got %q", result)
	}
}
