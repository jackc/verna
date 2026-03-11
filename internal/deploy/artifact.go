package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"
)

// GenerateReleaseID produces a release identifier from a timestamp and the SHA-256 of a tarball.
// Format: "20060102T150405Z-<first 12 hex chars of sha256>".
func GenerateReleaseID(t time.Time, tarballPath string) (string, error) {
	f, err := os.Open(tarballPath)
	if err != nil {
		return "", fmt.Errorf("opening tarball for hashing: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hashing tarball: %w", err)
	}

	ts := t.UTC().Format("20060102T150405Z")
	hash := hex.EncodeToString(h.Sum(nil))[:12]
	return ts + "-" + hash, nil
}
