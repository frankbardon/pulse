package pulse_test

import (
	"testing"

	"github.com/frankbardon/pulse"
)

// TestRootAliases_NameFacadeResultFields pins that every element type a
// shard / index facade result carries is nameable through a root alias:
// an embedder must be able to declare a variable of each one without
// importing an internal package.
func TestRootAliases_NameFacadeResultFields(t *testing.T) {
	var (
		add    pulse.AddShardResult
		create pulse.CreateShardArchiveResult
		verify pulse.VerifyResult
		built  pulse.BuildIndexResult
	)

	var warnings []pulse.CohesionWarning = add.Warnings
	warnings = append(warnings, create.Warnings...)
	warnings = append(warnings, verify.Warnings...)
	warnings = append(warnings, pulse.CohesionWarning{Code: "PULSE_SHARD_SET_WIDENED"})
	if warnings[len(warnings)-1].Code != "PULSE_SHARD_SET_WIDENED" {
		t.Fatal("CohesionWarning alias lost its Code field")
	}

	var headroom []pulse.GroupIndexHeadroom = verify.GroupIndexHeadroom
	headroom = append(headroom, pulse.GroupIndexHeadroom{Kind: "constant", Capacity: 1})
	if headroom[0].Capacity != 1 {
		t.Fatal("GroupIndexHeadroom alias lost its Capacity field")
	}

	var idx *pulse.SidecarIndex = built.Index
	if idx != nil {
		t.Fatal("zero BuildIndexResult carries a non-nil index")
	}
	idx = &pulse.SidecarIndex{SourceSize: 9}
	built.Index = idx
	if built.Index.SourceSize != 9 {
		t.Fatal("SidecarIndex alias is not BuildIndexResult.Index's type")
	}
}

// TestRootAliases_WalkSidecarIndex pins that the whole type closure of
// SidecarIndex — fingerprint, key spec, buckets and entries — is
// nameable through root aliases, so an embedder can construct and walk
// a BuildIndexResult.Index without importing an internal package.
func TestRootAliases_WalkSidecarIndex(t *testing.T) {
	var fp pulse.CohortFingerprint
	fp[0] = 0xAB
	idx := pulse.SidecarIndex{
		Fingerprint: fp,
		Keys:        []pulse.SidecarIndexKeySpec{{Name: "id"}},
		Buckets: []pulse.SidecarIndexBucket{
			{Entries: []pulse.SidecarIndexEntry{{Key: []byte{1}, RowIDs: []uint64{7, 9}}}},
		},
	}

	var gotFP pulse.CohortFingerprint = idx.Fingerprint
	if gotFP[0] != 0xAB || len(gotFP) != 32 {
		t.Fatalf("CohortFingerprint alias mismatch: %x", gotFP)
	}
	var key pulse.SidecarIndexKeySpec = idx.Keys[0]
	if key.Name != "id" {
		t.Fatalf("SidecarIndexKeySpec.Name = %q", key.Name)
	}
	var rows []uint64
	for _, b := range idx.Buckets {
		var bucket pulse.SidecarIndexBucket = b
		for _, e := range bucket.Entries {
			var entry pulse.SidecarIndexEntry = e
			rows = append(rows, entry.RowIDs...)
		}
	}
	if len(rows) != 2 || rows[0] != 7 || rows[1] != 9 {
		t.Fatalf("walked row IDs = %v, want [7 9]", rows)
	}
}
