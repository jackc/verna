package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateReleaseID(t *testing.T) {
	ts := time.Date(2026, 3, 7, 12, 1, 2, 0, time.UTC)

	t.Run("with commit", func(t *testing.T) {
		id := GenerateReleaseID(ts, "1f2e3d4")
		if id != "20260307T120102Z-1f2e3d4" {
			t.Errorf("expected 20260307T120102Z-1f2e3d4, got %s", id)
		}
	})

	t.Run("without commit", func(t *testing.T) {
		id := GenerateReleaseID(ts, "")
		if id != "20260307T120102Z" {
			t.Errorf("expected 20260307T120102Z, got %s", id)
		}
	})

	t.Run("long commit truncated", func(t *testing.T) {
		id := GenerateReleaseID(ts, "1f2e3d4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e")
		if id != "20260307T120102Z-1f2e3d4" {
			t.Errorf("expected 20260307T120102Z-1f2e3d4, got %s", id)
		}
	})

	t.Run("short commit used as-is", func(t *testing.T) {
		id := GenerateReleaseID(ts, "abcd")
		if id != "20260307T120102Z-abcd" {
			t.Errorf("expected 20260307T120102Z-abcd, got %s", id)
		}
	})
}

func TestTruncateCommit(t *testing.T) {
	if got := TruncateCommit("", 7); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := TruncateCommit("abc", 7); got != "abc" {
		t.Errorf("expected abc, got %q", got)
	}
	if got := TruncateCommit("abcdefghij", 7); got != "abcdefg" {
		t.Errorf("expected abcdefg, got %q", got)
	}
}

func TestBuildArtifact_BinaryOnly(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "myapp")
	if err := os.WriteFile(binPath, []byte("binary-content"), 0755); err != nil {
		t.Fatal(err)
	}

	ts := time.Date(2026, 3, 7, 12, 1, 2, 0, time.UTC)
	buf, manifest, err := BuildArtifact(ArtifactOptions{
		AppName:    "myapp",
		BinaryPath: binPath,
		Commit:     "abc1234",
		BuildTime:  ts,
		OS:         "linux",
		Arch:       "amd64",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if manifest.App != "myapp" {
		t.Errorf("expected app myapp, got %s", manifest.App)
	}
	if manifest.Release != "20260307T120102Z-abc1234" {
		t.Errorf("expected release 20260307T120102Z-abc1234, got %s", manifest.Release)
	}
	if manifest.HasPublic {
		t.Error("expected has_public false")
	}
	if manifest.OS != "linux" {
		t.Errorf("expected os linux, got %s", manifest.OS)
	}
	if manifest.Arch != "amd64" {
		t.Errorf("expected arch amd64, got %s", manifest.Arch)
	}

	// Extract and verify tarball contents.
	entries := extractTarGz(t, buf)

	// Verify manifest.json.
	manifestEntry, ok := entries["manifest.json"]
	if !ok {
		t.Fatal("manifest.json not found in tarball")
	}
	var m Manifest
	if err := json.Unmarshal(manifestEntry.data, &m); err != nil {
		t.Fatalf("parsing manifest: %v", err)
	}
	if m.HasPublic {
		t.Error("manifest has_public should be false")
	}

	// Verify binary.
	binEntry, ok := entries["bin/myapp"]
	if !ok {
		t.Fatal("bin/myapp not found in tarball")
	}
	if string(binEntry.data) != "binary-content" {
		t.Errorf("expected binary-content, got %s", string(binEntry.data))
	}
	if binEntry.mode != 0755 {
		t.Errorf("expected mode 0755, got %o", binEntry.mode)
	}

	// Verify no public entries.
	for name := range entries {
		if len(name) > 7 && name[:7] == "public/" {
			t.Errorf("unexpected public entry: %s", name)
		}
	}
}

func TestBuildArtifact_WithPublicDir(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "myapp")
	if err := os.WriteFile(binPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}

	pubDir := filepath.Join(dir, "public")
	assetsDir := filepath.Join(pubDir, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pubDir, "index.html"), []byte("<html>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "app-abc123.js"), []byte("js"), 0644); err != nil {
		t.Fatal(err)
	}

	ts := time.Date(2026, 3, 7, 12, 1, 2, 0, time.UTC)
	buf, manifest, err := BuildArtifact(ArtifactOptions{
		AppName:    "myapp",
		BinaryPath: binPath,
		PublicDir:  pubDir,
		Commit:     "abc1234",
		BuildTime:  ts,
		OS:         "linux",
		Arch:       "amd64",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !manifest.HasPublic {
		t.Error("expected has_public true")
	}

	entries := extractTarGz(t, buf)

	if _, ok := entries["manifest.json"]; !ok {
		t.Error("manifest.json not found")
	}
	if _, ok := entries["bin/myapp"]; !ok {
		t.Error("bin/myapp not found")
	}

	htmlEntry, ok := entries["public/index.html"]
	if !ok {
		t.Fatal("public/index.html not found")
	}
	if string(htmlEntry.data) != "<html>" {
		t.Errorf("expected <html>, got %s", string(htmlEntry.data))
	}

	jsEntry, ok := entries["public/assets/app-abc123.js"]
	if !ok {
		t.Fatal("public/assets/app-abc123.js not found")
	}
	if string(jsEntry.data) != "js" {
		t.Errorf("expected js, got %s", string(jsEntry.data))
	}
}

func TestBuildArtifact_MissingBinary(t *testing.T) {
	_, _, err := BuildArtifact(ArtifactOptions{
		AppName:    "myapp",
		BinaryPath: "/nonexistent/binary",
		OS:         "linux",
		Arch:       "amd64",
	})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestBuildArtifact_BinaryIsDirectory(t *testing.T) {
	dir := t.TempDir()
	_, _, err := BuildArtifact(ArtifactOptions{
		AppName:    "myapp",
		BinaryPath: dir,
		OS:         "linux",
		Arch:       "amd64",
	})
	if err == nil {
		t.Fatal("expected error when binary is a directory")
	}
}

func TestBuildArtifact_PublicDirNotExist(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "myapp")
	if err := os.WriteFile(binPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}

	_, _, err := BuildArtifact(ArtifactOptions{
		AppName:    "myapp",
		BinaryPath: binPath,
		PublicDir:  "/nonexistent/public",
		OS:         "linux",
		Arch:       "amd64",
	})
	if err == nil {
		t.Fatal("expected error for missing public dir")
	}
}

type tarEntry struct {
	data []byte
	mode int64
	dir  bool
}

func extractTarGz(t *testing.T, buf *bytes.Buffer) map[string]tarEntry {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	entries := make(map[string]tarEntry)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		var data []byte
		if hdr.Typeflag != tar.TypeDir {
			data, err = io.ReadAll(tr)
			if err != nil {
				t.Fatalf("reading %s: %v", hdr.Name, err)
			}
		}
		entries[hdr.Name] = tarEntry{
			data: data,
			mode: hdr.Mode,
			dir:  hdr.Typeflag == tar.TypeDir,
		}
	}
	return entries
}
