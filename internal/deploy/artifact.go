package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type Manifest struct {
	App       string `json:"app"`
	Release   string `json:"release"`
	Commit    string `json:"commit,omitempty"`
	BuildTime string `json:"build_time"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	HasPublic bool   `json:"has_public"`
}

type ArtifactOptions struct {
	AppName    string
	BinaryPath string
	PublicDir  string
	Commit     string
	BuildTime  time.Time
	OS         string
	Arch       string
}

// GenerateReleaseID produces a release identifier from a timestamp and optional commit hash.
// Format: "20060102T150405Z-<commit7>" or "20060102T150405Z" if commit is empty.
func GenerateReleaseID(t time.Time, commit string) string {
	ts := t.UTC().Format("20060102T150405Z")
	c := TruncateCommit(commit, 7)
	if c == "" {
		return ts
	}
	return ts + "-" + c
}

// TruncateCommit safely truncates a commit hash to n characters.
func TruncateCommit(commit string, n int) string {
	if len(commit) <= n {
		return commit
	}
	return commit[:n]
}

// BuildArtifact creates a .tar.gz artifact in memory and returns the buffer and manifest.
func BuildArtifact(opts ArtifactOptions) (*bytes.Buffer, *Manifest, error) {
	// Validate binary.
	binInfo, err := os.Stat(opts.BinaryPath)
	if err != nil {
		return nil, nil, fmt.Errorf("binary %s: %w", opts.BinaryPath, err)
	}
	if binInfo.IsDir() {
		return nil, nil, fmt.Errorf("binary %s is a directory, not a file", opts.BinaryPath)
	}

	// Validate public dir if specified.
	hasPublic := opts.PublicDir != ""
	if hasPublic {
		pubInfo, err := os.Stat(opts.PublicDir)
		if err != nil {
			return nil, nil, fmt.Errorf("public directory %s: %w", opts.PublicDir, err)
		}
		if !pubInfo.IsDir() {
			return nil, nil, fmt.Errorf("public path %s is not a directory", opts.PublicDir)
		}
	}

	buildTime := opts.BuildTime
	if buildTime.IsZero() {
		buildTime = time.Now().UTC()
	}

	release := GenerateReleaseID(buildTime, opts.Commit)

	manifest := &Manifest{
		App:       opts.AppName,
		Release:   release,
		Commit:    TruncateCommit(opts.Commit, 7),
		BuildTime: buildTime.UTC().Format(time.RFC3339),
		OS:        opts.OS,
		Arch:      opts.Arch,
		HasPublic: hasPublic,
	}

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Add manifest.json.
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling manifest: %w", err)
	}
	manifestJSON = append(manifestJSON, '\n')
	if err := tw.WriteHeader(&tar.Header{
		Name: "manifest.json",
		Size: int64(len(manifestJSON)),
		Mode: 0644,
	}); err != nil {
		return nil, nil, fmt.Errorf("writing manifest header: %w", err)
	}
	if _, err := tw.Write(manifestJSON); err != nil {
		return nil, nil, fmt.Errorf("writing manifest: %w", err)
	}

	// Add binary.
	binData, err := os.ReadFile(opts.BinaryPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading binary: %w", err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: "bin/" + opts.AppName,
		Size: int64(len(binData)),
		Mode: 0755,
	}); err != nil {
		return nil, nil, fmt.Errorf("writing binary header: %w", err)
	}
	if _, err := tw.Write(binData); err != nil {
		return nil, nil, fmt.Errorf("writing binary: %w", err)
	}

	// Add public directory contents.
	if hasPublic {
		if err := addPublicDir(tw, opts.PublicDir); err != nil {
			return nil, nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, nil, fmt.Errorf("closing tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, nil, fmt.Errorf("closing gzip writer: %w", err)
	}

	return &buf, manifest, nil
}

func addPublicDir(tw *tar.Writer, publicDir string) error {
	return filepath.WalkDir(publicDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip symlinks.
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		rel, err := filepath.Rel(publicDir, path)
		if err != nil {
			return err
		}
		archivePath := "public/" + filepath.ToSlash(rel)

		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir,
				Name:     archivePath + "/",
				Mode:     int64(info.Mode().Perm()),
			})
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: archivePath,
			Size: int64(len(data)),
			Mode: int64(info.Mode().Perm()),
		}); err != nil {
			return fmt.Errorf("writing header for %s: %w", archivePath, err)
		}
		if _, err := tw.Write(data); err != nil {
			return fmt.Errorf("writing %s: %w", archivePath, err)
		}
		return nil
	})
}
