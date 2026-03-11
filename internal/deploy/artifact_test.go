package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateReleaseID(t *testing.T) {
	// Create a temp tarball file for hashing.
	dir := t.TempDir()
	tarball := filepath.Join(dir, "app.tar.gz")
	if err := os.WriteFile(tarball, []byte("tarball-content"), 0644); err != nil {
		t.Fatal(err)
	}

	ts := time.Date(2026, 3, 7, 12, 1, 2, 0, time.UTC)

	id, err := GenerateReleaseID(ts, tarball)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should start with timestamp.
	if !strings.HasPrefix(id, "20260307T120102Z-") {
		t.Errorf("expected timestamp prefix 20260307T120102Z-, got %s", id)
	}

	// Hash suffix should be 12 hex chars.
	parts := strings.SplitN(id, "-", 2)
	if len(parts) != 2 {
		t.Fatalf("expected timestamp-hash format, got %s", id)
	}
	if len(parts[1]) != 12 {
		t.Errorf("expected 12-char hash suffix, got %d chars: %s", len(parts[1]), parts[1])
	}
}

func TestGenerateReleaseID_SameFileSameHash(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "app.tar.gz")
	if err := os.WriteFile(tarball, []byte("same-content"), 0644); err != nil {
		t.Fatal(err)
	}

	ts := time.Date(2026, 3, 7, 12, 1, 2, 0, time.UTC)

	id1, err := GenerateReleaseID(ts, tarball)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := GenerateReleaseID(ts, tarball)
	if err != nil {
		t.Fatal(err)
	}

	if id1 != id2 {
		t.Errorf("same file should produce same ID: %s vs %s", id1, id2)
	}
}

func TestGenerateReleaseID_DifferentFilesDifferentHash(t *testing.T) {
	dir := t.TempDir()
	tarball1 := filepath.Join(dir, "a.tar.gz")
	tarball2 := filepath.Join(dir, "b.tar.gz")
	if err := os.WriteFile(tarball1, []byte("content-a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tarball2, []byte("content-b"), 0644); err != nil {
		t.Fatal(err)
	}

	ts := time.Date(2026, 3, 7, 12, 1, 2, 0, time.UTC)

	id1, err := GenerateReleaseID(ts, tarball1)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := GenerateReleaseID(ts, tarball2)
	if err != nil {
		t.Fatal(err)
	}

	if id1 == id2 {
		t.Errorf("different files should produce different IDs: both %s", id1)
	}
}

func TestGenerateReleaseID_NonexistentFile(t *testing.T) {
	ts := time.Date(2026, 3, 7, 12, 1, 2, 0, time.UTC)
	_, err := GenerateReleaseID(ts, "/nonexistent/file.tar.gz")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}
