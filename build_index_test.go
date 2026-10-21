package pulse

import (
	"context"
	"testing"

	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestBuildIndex_WritesSidecarAtDerivedPath is the facade-level
// smoke test for Pulse.BuildIndex — the same contract
// service.Service.BuildIndex covers exhaustively (see
// internal/service/index_build_test.go), exercised through the public facade
// so the thin delegation (including the touchManaged call) is TDD'd
// at this layer too.
func TestBuildIndex_WritesSidecarAtDerivedPath(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "cohort.pulse", []string{"id", "region"}, [][]string{
		{"1", "north"},
		{"2", "south"},
		{"3", "north"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := p.BuildIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	wantPath := encx.SidecarIndexPath("cohort.pulse", []string{"id"})
	if res.IndexPath != wantPath {
		t.Errorf("IndexPath = %q, want %q", res.IndexPath, wantPath)
	}
	exists, err := afero.Exists(memFs, wantPath)
	if err != nil {
		t.Fatalf("afero.Exists: %v", err)
	}
	if !exists {
		t.Fatalf("sidecar not written at derived path %q", wantPath)
	}
}

// TestBuildIndex_CompositeKey confirms a multi-column --key builds a
// single composite-key index with the columns carried in the order
// supplied, matching the CLI's comma-split contract.
func TestBuildIndex_CompositeKey(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "cohort.pulse", []string{"id", "region"}, [][]string{
		{"1", "north"},
		{"2", "south"},
		{"3", "north"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := p.BuildIndex(context.Background(), "cohort.pulse", []string{"id", "region"})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if len(res.Index.Keys) != 2 || res.Index.Keys[0].Name != "id" || res.Index.Keys[1].Name != "region" {
		t.Errorf("Index.Keys = %+v, want [{id} {region}] in that order", res.Index.Keys)
	}
}

// TestBuildIndex_ShardArchiveWritesPerShardSidecars: the facade indexes
// a shard archive one sidecar per shard (no single Index), with the
// counts and the per-shard paths on the result.
func TestBuildIndex_ShardArchiveWritesPerShardSidecars(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "shard1.pulse", []string{"id"}, [][]string{{"1"}, {"2"}})
	createTestPulseFile(t, memFs, "shard2.pulse", []string{"id"}, [][]string{{"3"}, {"4"}})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := p.CreateShardArchive(context.Background(), "archive.pulse", []string{"shard1.pulse", "shard2.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	res, err := p.BuildIndex(context.Background(), "archive.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("BuildIndex(archive): %v", err)
	}
	if res.Index != nil || len(res.ShardIndexPaths) != 2 || res.DistinctKeys != 4 || res.IndexedRecords != 4 {
		t.Errorf("BuildIndex(archive) = %+v, want no single Index, 2 shard sidecars, 4/4", res)
	}
	got, err := p.Lookup(context.Background(), &LookupRequest{Cohort: &types.Cohort{Filename: "archive.pulse"}, Field: "id", Value: "3"})
	if err != nil || len(got.Rows) != 1 {
		t.Errorf("Lookup(archive, id=3) = %+v, %v; want one row from shard2", got, err)
	}
}
