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
	AppName       string
	ArtifactDir   string // local directory to tar
	ExecRelPath   string // relative path to executable within ArtifactDir (for validation + chmod 0755)
	PublicRelPath string // relative path to public dir within ArtifactDir (optional, for manifest HasPublic)
	Commit        string
	BuildTime     time.Time
	OS            string
	Arch          string
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

// BuildArtifact creates a .tar.gz artifact in memory by tarring the entire ArtifactDir.
// It prepends a manifest.json and ensures the executable has 0755 permissions.
func BuildArtifact(opts ArtifactOptions) (*bytes.Buffer, *Manifest, error) {
	// Validate artifact directory.
	dirInfo, err := os.Stat(opts.ArtifactDir)
	if err != nil {
		return nil, nil, fmt.Errorf("artifact directory %s: %w", opts.ArtifactDir, err)
	}
	if !dirInfo.IsDir() {
		return nil, nil, fmt.Errorf("artifact path %s is not a directory", opts.ArtifactDir)
	}

	// Validate executable exists within artifact dir.
	execAbs := filepath.Join(opts.ArtifactDir, opts.ExecRelPath)
	execInfo, err := os.Stat(execAbs)
	if err != nil {
		return nil, nil, fmt.Errorf("executable %s: %w", execAbs, err)
	}
	if execInfo.IsDir() {
		return nil, nil, fmt.Errorf("executable %s is a directory, not a file", execAbs)
	}

	// Validate public dir if specified.
	hasPublic := opts.PublicRelPath != ""
	if hasPublic {
		publicAbs := filepath.Join(opts.ArtifactDir, opts.PublicRelPath)
		pubInfo, err := os.Stat(publicAbs)
		if err != nil {
			return nil, nil, fmt.Errorf("public directory %s: %w", publicAbs, err)
		}
		if !pubInfo.IsDir() {
			return nil, nil, fmt.Errorf("public path %s is not a directory", publicAbs)
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

	// Add manifest.json first.
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

	// Normalize executable relative path for comparison.
	execRel := filepath.ToSlash(opts.ExecRelPath)

	// Walk entire artifact directory.
	if err := filepath.WalkDir(opts.ArtifactDir, func(path string, d fs.DirEntry, err error) error {
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

		rel, err := filepath.Rel(opts.ArtifactDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		archivePath := filepath.ToSlash(rel)

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

		mode := int64(info.Mode().Perm())
		if archivePath == execRel {
			mode = 0755
		}

		if err := tw.WriteHeader(&tar.Header{
			Name: archivePath,
			Size: int64(len(data)),
			Mode: mode,
		}); err != nil {
			return fmt.Errorf("writing header for %s: %w", archivePath, err)
		}
		if _, err := tw.Write(data); err != nil {
			return fmt.Errorf("writing %s: %w", archivePath, err)
		}
		return nil
	}); err != nil {
		return nil, nil, fmt.Errorf("walking artifact directory: %w", err)
	}

	if err := tw.Close(); err != nil {
		return nil, nil, fmt.Errorf("closing tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, nil, fmt.Errorf("closing gzip writer: %w", err)
	}

	return &buf, manifest, nil
}
