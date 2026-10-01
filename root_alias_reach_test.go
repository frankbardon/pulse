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
