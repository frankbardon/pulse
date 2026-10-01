package pulse

import (
	"context"
	"testing"

	"github.com/spf13/afero"
)

// TestProfile_ShardArchiveRunContinuation: a shard archive profiles as
// the concatenation of its shards, and run-continuation restarts at each
// shard. Each shard holds one constant region, different per shard, so
// the region field repeats on every within-shard pair and would break on
// a pair spanning the boundary.
func TestProfile_ShardArchiveRunContinuation(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "shard1.pulse", []string{"region", "id"},
		[][]string{{"east", "1"}, {"east", "2"}, {"east", "3"}})
	createTestPulseFile(t, memFs, "shard2.pulse", []string{"region", "id"},
		[][]string{{"west", "4"}, {"west", "5"}, {"west", "6"}, {"west", "7"}})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if _, err := p.CreateShardArchive(ctx, "archive.pulse", []string{"shard1.pulse", "shard2.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	prof, err := p.Profile(ctx, "archive.pulse", ProfileOptions{RunContinuation: true})
	if err != nil {
		t.Fatalf("Profile(archive): %v", err)
	}
	if prof.RowCount != 7 {
		t.Fatalf("row count = %d, want 7 across both shards", prof.RowCount)
	}
	// Shard 2's dictionary was remapped into the canonical one at insert
	// time, so decoding every shard against the canonical schema resolves
	// both regions.
	got := map[string]float64{}
	for _, f := range prof.Fields {
		if f.Name == "region" && f.Categorical != nil {
			for _, h := range f.Categorical.Top {
				got[h.Value] = h.Weight
			}
		}
	}
	if len(got) != 2 || got["east"] == 0 || got["west"] == 0 {
		t.Fatalf("region top values = %v, want east and west", got)
	}
	rc := prof.RunContinuation
	if rc == nil {
		t.Fatal("RunContinuation missing")
	}
	if rc.Shards != 2 || rc.Pairs != 5 {
		t.Fatalf("shards/pairs = %d/%d, want 2/5 (no pair across the boundary)", rc.Shards, rc.Pairs)
	}
	var region float64 = -1
	for _, f := range rc.Fields {
		if f.Name == "region" {
			region = f.Rate
		}
	}
	if region != 1 {
		t.Fatalf("rate(region) = %v, want 1 — a pair spanned the shard boundary", region)
	}

	// The same cohort as one file: the region column breaks once.
	createTestPulseFile(t, memFs, "single.pulse", []string{"region", "id"},
		[][]string{{"east", "1"}, {"east", "2"}, {"east", "3"}, {"west", "4"}, {"west", "5"}, {"west", "6"}, {"west", "7"}})
	single, err := p.Profile(ctx, "single.pulse", ProfileOptions{RunContinuation: true})
	if err != nil {
		t.Fatalf("Profile(single): %v", err)
	}
	if single.RunContinuation.Shards != 1 || single.RunContinuation.Pairs != 6 {
		t.Fatalf("single file shards/pairs = %d/%d, want 1/6", single.RunContinuation.Shards, single.RunContinuation.Pairs)
	}
}
