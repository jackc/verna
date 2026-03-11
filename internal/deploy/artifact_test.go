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

func TestBuildArtifact_ExecOnly(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "myapp"), []byte("executable-content"), 0755); err != nil {
		t.Fatal(err)
	}

	ts := time.Date(2026, 3, 7, 12, 1, 2, 0, time.UTC)
	buf, manifest, err := BuildArtifact(ArtifactOptions{
		AppName:       "myapp",
		ArtifactDir:   dir,
		ExecRelPath: "bin/myapp",
		Commit:        "abc1234",
		BuildTime:     ts,
		OS:            "linux",
		Arch:          "amd64",
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

	// Verify executable.
	execEntry, ok := entries["bin/myapp"]
	if !ok {
		t.Fatal("bin/myapp not found in tarball")
	}
	if string(execEntry.data) != "executable-content" {
		t.Errorf("expected executable-content, got %s", string(execEntry.data))
	}
	if execEntry.mode != 0755 {
		t.Errorf("expected mode 0755, got %o", execEntry.mode)
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

	// Create bin/myapp
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "myapp"), []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create public/ with files
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
		AppName:       "myapp",
		ArtifactDir:   dir,
		ExecRelPath: "bin/myapp",
		PublicRelPath:  "public",
		Commit:        "abc1234",
		BuildTime:     ts,
		OS:            "linux",
		Arch:          "amd64",
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

func TestBuildArtifact_ExtraFiles(t *testing.T) {
	dir := t.TempDir()

	// Create bin/myapp
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "myapp"), []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create extra files (templates, config)
	tmplDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "email.html"), []byte("<email>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("key = \"val\""), 0644); err != nil {
		t.Fatal(err)
	}

	ts := time.Date(2026, 3, 7, 12, 1, 2, 0, time.UTC)
	buf, _, err := BuildArtifact(ArtifactOptions{
		AppName:       "myapp",
		ArtifactDir:   dir,
		ExecRelPath: "bin/myapp",
		Commit:        "abc1234",
		BuildTime:     ts,
		OS:            "linux",
		Arch:          "amd64",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries := extractTarGz(t, buf)

	// Verify extra files are included.
	tmplEntry, ok := entries["templates/email.html"]
	if !ok {
		t.Fatal("templates/email.html not found in tarball")
	}
	if string(tmplEntry.data) != "<email>" {
		t.Errorf("expected <email>, got %s", string(tmplEntry.data))
	}

	cfgEntry, ok := entries["config.toml"]
	if !ok {
		t.Fatal("config.toml not found in tarball")
	}
	if string(cfgEntry.data) != "key = \"val\"" {
		t.Errorf("expected key = \"val\", got %s", string(cfgEntry.data))
	}

	// Verify executable and manifest still present.
	if _, ok := entries["manifest.json"]; !ok {
		t.Error("manifest.json not found")
	}
	if _, ok := entries["bin/myapp"]; !ok {
		t.Error("bin/myapp not found")
	}
}

func TestBuildArtifact_MissingExec(t *testing.T) {
	dir := t.TempDir()
	_, _, err := BuildArtifact(ArtifactOptions{
		AppName:       "myapp",
		ArtifactDir:   dir,
		ExecRelPath: "bin/myapp",
		OS:            "linux",
		Arch:          "amd64",
	})
	if err == nil {
		t.Fatal("expected error for missing executable")
	}
}

func TestBuildArtifact_ExecIsDirectory(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin", "myapp")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	_, _, err := BuildArtifact(ArtifactOptions{
		AppName:       "myapp",
		ArtifactDir:   dir,
		ExecRelPath: "bin/myapp",
		OS:            "linux",
		Arch:          "amd64",
	})
	if err == nil {
		t.Fatal("expected error when executable is a directory")
	}
}

func TestBuildArtifact_PublicDirNotExist(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "myapp"), []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}

	_, _, err := BuildArtifact(ArtifactOptions{
		AppName:       "myapp",
		ArtifactDir:   dir,
		ExecRelPath: "bin/myapp",
		PublicRelPath:  "public",
		OS:            "linux",
		Arch:          "amd64",
	})
	if err == nil {
		t.Fatal("expected error for missing public dir")
	}
}

func TestBuildArtifact_MissingArtifactDir(t *testing.T) {
	_, _, err := BuildArtifact(ArtifactOptions{
		AppName:       "myapp",
		ArtifactDir:   "/nonexistent/dir",
		ExecRelPath: "bin/myapp",
		OS:            "linux",
		Arch:          "amd64",
	})
	if err == nil {
		t.Fatal("expected error for missing artifact directory")
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
