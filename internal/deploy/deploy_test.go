package deploy

import (
	"testing"

	"github.com/jackc/verna/internal/server"
)

func TestPruneReleasesSelection(t *testing.T) {
	// Test the pruning logic by verifying which releases would be removed.
	// We can't call pruneReleases directly (needs SSH), so we test the selection logic.

	tests := []struct {
		name       string
		releases   []string
		retention  int
		inUse      map[string]bool
		wantRemove []string
	}{
		{
			name:       "fewer than retention",
			releases:   []string{"20260301T000000Z-aaa", "20260302T000000Z-bbb"},
			retention:  5,
			inUse:      map[string]bool{},
			wantRemove: nil,
		},
		{
			name:      "more than retention, oldest removed",
			releases:  []string{"20260301T000000Z-aaa", "20260302T000000Z-bbb", "20260303T000000Z-ccc"},
			retention: 2,
			inUse:     map[string]bool{},
			wantRemove: []string{"20260301T000000Z-aaa"},
		},
		{
			name:       "in-use release preserved even if oldest",
			releases:   []string{"20260301T000000Z-aaa", "20260302T000000Z-bbb", "20260303T000000Z-ccc"},
			retention:  2,
			inUse:      map[string]bool{"20260301T000000Z-aaa": true},
			wantRemove: nil,
		},
		{
			name:      "mixed: some removable, some in-use",
			releases:  []string{"20260301T000000Z-aaa", "20260302T000000Z-bbb", "20260303T000000Z-ccc", "20260304T000000Z-ddd", "20260305T000000Z-eee"},
			retention: 3,
			inUse:     map[string]bool{"20260302T000000Z-bbb": true},
			wantRemove: []string{"20260301T000000Z-aaa"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectReleasesToPrune(tt.releases, tt.retention, tt.inUse)
			if len(got) != len(tt.wantRemove) {
				t.Errorf("expected %d removals, got %d: %v", len(tt.wantRemove), len(got), got)
				return
			}
			for i, r := range got {
				if r != tt.wantRemove[i] {
					t.Errorf("removal[%d]: expected %s, got %s", i, tt.wantRemove[i], r)
				}
			}
		})
	}
}

// Helper to build an AppState with specific slot releases for testing.
func appWithSlots(blueRelease, greenRelease string) *server.AppState {
	inUse := make(map[string]bool)
	if blueRelease != "" {
		inUse[blueRelease] = true
	}
	if greenRelease != "" {
		inUse[greenRelease] = true
	}
	return &server.AppState{
		ReleaseRetention: 3,
		Slots: map[string]server.SlotState{
			"blue":  {Release: blueRelease},
			"green": {Release: greenRelease},
		},
	}
}
