package pulse

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// TestBuildIndex_WritesSidecarAtDerivedPath is the facade-level
// smoke test for Pulse.BuildIndex — the same contract
// service.Service.BuildIndex covers exhaustively (see
// service/index_build_test.go), exercised through the public facade
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

	wantPath := encoding.SidecarIndexPath("cohort.pulse", []string{"id"})
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

// TestBuildIndex_ShardArchiveRejected confirms the facade propagates
// service.Service.BuildIndex's PULSE_INDEX_UNSUPPORTED_SHARDED
// rejection verbatim for a shard-archive cohort.
func TestBuildIndex_ShardArchiveRejected(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "shard1.pulse", []string{"id"}, [][]string{{"1"}, {"2"}})
	createTestPulseFile(t, memFs, "shard2.pulse", []string{"id"}, [][]string{{"3"}, {"4"}})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := p.CreateShardArchive(context.Background(), "archive.pulse", []string{"shard1.pulse", "shard2.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	_, err = p.BuildIndex(context.Background(), "archive.pulse", []string{"id"})
	if err == nil {
		t.Fatal("expected error building an index against a shard archive")
	}
	if !errors.HasCode(err, errors.PULSE_INDEX_UNSUPPORTED_SHARDED) {
		t.Errorf("expected PULSE_INDEX_UNSUPPORTED_SHARDED, got: %v", err)
	}
}
