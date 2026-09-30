package processing

import (
	"context"
	"testing"

	stderrors "errors"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Runtime-arm coverage for the EXTENSION half of the distinct-key slab
// partition gate. E1-S3 consulted types.GroupType.FansOut(), which
// knows built-in constants only, so an embedder's multi-key grouper on
// a summed-across dim ran and inflated n silently. This arm resolves
// the declaration out of the live ExtensionRegistry; the predict arm
// reaches the same fact through descriptor.ExtensionsSnapshot, and
// both delegate the ORDER to types.CheckPairwiseSlabPartitionWith.

const (
	// pgExtFan is registered against the real set-per-element factory,
	// so it genuinely fans one record into several buckets — the
	// crosstab has to materialise before the overlay hook runs, so a
	// stub that returns no groups would never reach the gate.
	pgExtFan = types.GroupType("GROUP_ACME_PANEL_X")
	// pgExtFlat is registered against the category factory: one bucket
	// per record.
	pgExtFlat = types.GroupType("GROUP_ACME_TIER_X")
)

// pgExtRegistry mirrors what pulse.New's buildRuntimeExtensions
// produces: a factory map plus the per-grouper FansOut declaration,
// always written together so a registered grouper always carries an
// entry.
func pgExtRegistry() *ExtensionRegistry {
	return &ExtensionRegistry{
		Groupers: map[types.GroupType]GrouperFactory{
			pgExtFan:  newSetPerElementGrouper,
			pgExtFlat: newCategoryGrouper,
		},
		FansOut: map[types.GroupType]bool{
			pgExtFan:  true,
			pgExtFlat: false,
		},
	}
}

// pgExtArms runs one request through BOTH crosstab exits with the
// extension registry wired in, exactly as partitionGateArms does for
// the built-in case. Ordered, so one arm's failure cannot hide the
// other's.
func pgExtArms(t *testing.T, req *types.Request, exts *ExtensionRegistry) []partitionGateArm {
	t.Helper()
	schema := partitionGateSchema(t)
	recs := partitionGateRecords(schema)

	buffered := NewProcessorWithExtensions(schema, exts)
	_, bufErr := buffered.RunCrosstab(context.Background(), req, recs)

	fused := NewProcessorWithExtensions(schema, exts)
	_, fusedErr := fused.RunCrosstabFused(context.Background(), req, NewSliceIterator(recs))

	return []partitionGateArm{{"buffered", bufErr}, {"fused", fusedErr}}
}

func pgExtGroup(kind types.GroupType, field string) *types.Group {
	return &types.Group{Type: kind, Field: field}
}

// TestExtensionRegistry_GrouperFanOut pins the runtime bridge
// accessor, including the nil receiver and the built-in names it
// deliberately does NOT answer for.
func TestExtensionRegistry_GrouperFanOut(t *testing.T) {
	reg := pgExtRegistry()
	cases := []struct {
		name      string
		reg       *ExtensionRegistry
		lookup    types.GroupType
		wantFan   bool
		wantFound bool
	}{
		{"declared fan-out", reg, pgExtFan, true, true},
		{"declared single-key", reg, pgExtFlat, false, true},
		{"unregistered name", reg, types.GroupType("GROUP_ACME_NOPE_X"), false, false},
		// Built-ins are not in the map on purpose: GroupType.FansOut()
		// is their authority and the shared predicate tries it first.
		{"built-in fan-out", reg, types.GROUP_SET_PER_ELEMENT, false, false},
		{"nil registry", nil, pgExtFan, false, false},
		{"registry with no FansOut map", &ExtensionRegistry{}, pgExtFan, false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fan, found := tc.reg.GrouperFanOut(tc.lookup)
			if fan != tc.wantFan || found != tc.wantFound {
				t.Fatalf("GrouperFanOut(%q) = (%v, %v), want (%v, %v)",
					tc.lookup, fan, found, tc.wantFan, tc.wantFound)
			}
		})
	}
	if reg.ExtensionGroupFanOut() == nil {
		t.Error("ExtensionGroupFanOut() = nil for a populated registry")
	}
	var nilReg *ExtensionRegistry
	if nilReg.ExtensionGroupFanOut() != nil {
		t.Error("ExtensionGroupFanOut() must be nil for a nil registry")
	}
}

// TestCrosstabOverlays_ExtensionFanOutRefused_BothArms is the story's
// core runtime assertion: a registered multi-key grouper as the INNER
// (summed-across) pair-axis level refuses, on both crosstab exits,
// with the same code and details the built-in case produces.
func TestCrosstabOverlays_ExtensionFanOutRefused_BothArms(t *testing.T) {
	req := partitionGateRequest(
		[]*types.Group{pgFlat("segment"), pgExtGroup(pgExtFan, "brand")},
		[]*types.Group{pgFlat("wave")},
		types.OverlayScopeRow,
		`{"n_source":"n_within_distinct","n_within_depth":0}`,
	)
	for _, arm := range pgExtArms(t, req, pgExtRegistry()) {
		arm := arm
		t.Run(arm.name, func(t *testing.T) {
			if arm.err == nil {
				t.Fatal("runtime accepted an extension fan-out grouper on a summed-across dim")
			}
			coded := pairwiseCodedError(t, arm.err)
			if coded.Code != errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED {
				t.Fatalf("code = %q, want %q", coded.Code,
					errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)
			}
			for key, want := range map[string]any{
				"dim_index":  1,
				"group_type": string(pgExtFan),
				"field":      "brand",
				"axis":       "row",
			} {
				if got := coded.Details[key]; got != want {
					t.Errorf("Details[%q] = %v, want %v", key, got, want)
				}
			}
		})
	}
}

// TestCrosstabOverlays_ExtensionSingleKeyRuns_BothArms is the
// false-refusal guard: a registered grouper that maps each record to
// one bucket partitions the key set, so the summed distinct counts are
// exact and the request must run.
func TestCrosstabOverlays_ExtensionSingleKeyRuns_BothArms(t *testing.T) {
	req := partitionGateRequest(
		[]*types.Group{pgFlat("wave"), pgExtGroup(pgExtFlat, "segment")},
		[]*types.Group{pgFlat("wave")},
		types.OverlayScopeRow,
		`{"n_source":"n_within_distinct","n_within_depth":0}`,
	)
	for _, arm := range pgExtArms(t, req, pgExtRegistry()) {
		arm := arm
		t.Run(arm.name, func(t *testing.T) {
			if arm.err != nil {
				t.Fatalf("refused a declared single-key extension grouper: %v", arm.err)
			}
		})
	}
}

// TestCrosstabOverlays_ExtensionFanOutInPrefixRuns_BothArms keeps the
// accepted shape accepted for extension groupers: OUTER, inside the
// fixed prefix, it multiplies slabs rather than cells.
func TestCrosstabOverlays_ExtensionFanOutInPrefixRuns_BothArms(t *testing.T) {
	req := partitionGateRequest(
		[]*types.Group{pgExtGroup(pgExtFan, "brand"), pgFlat("segment")},
		[]*types.Group{pgFlat("wave")},
		types.OverlayScopeRow,
		`{"n_source":"n_within_distinct","n_within_depth":0}`,
	)
	for _, arm := range pgExtArms(t, req, pgExtRegistry()) {
		arm := arm
		t.Run(arm.name, func(t *testing.T) {
			if arm.err != nil {
				t.Fatalf("refused an in-prefix extension fan-out: %v", arm.err)
			}
		})
	}
}

// TestCrosstabOverlays_UnregisteredGrouperIsNotAPartitionError pins the
// third resolution case — a name in neither the built-in catalog nor
// any registration — and it is the reason predict PASSES that case.
//
// Here the refusal comes from grouper construction, upstream of the
// overlay hook: an unregistered group type cannot be built, so the
// request dies with PROCESSING_CONFIG before the gate is consulted at
// all. That ordering is what makes "no wrong n can come of it" true,
// and it is why the predict arm must not invent a partition
// diagnostic for the same request: it would assert a fan-out property
// of a grouper that does not exist and bury the accurate
// unknown-operator error the operator actually needs.
func TestCrosstabOverlays_UnregisteredGrouperIsNotAPartitionError(t *testing.T) {
	req := partitionGateRequest(
		[]*types.Group{pgFlat("segment"), pgExtGroup(types.GroupType("GROUP_ACME_NOPE_X"), "brand")},
		[]*types.Group{pgFlat("wave")},
		types.OverlayScopeRow,
		`{"n_source":"n_within_distinct","n_within_depth":0}`,
	)
	for _, arm := range pgExtArms(t, req, pgExtRegistry()) {
		arm := arm
		t.Run(arm.name, func(t *testing.T) {
			if arm.err == nil {
				t.Fatal("an unknown grouper must still fail — just not as a partition violation")
			}
			if errors.HasCode(arm.err, errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED) {
				t.Fatalf("unknown grouper reported as a partition violation, burying the real error: %v", arm.err)
			}
			var coded *errors.CodedError
			if !stderrors.As(arm.err, &coded) {
				t.Fatalf("expected a coded error, got %T: %v", arm.err, arm.err)
			}
			if coded.Code != errors.PROCESSING_CONFIG {
				t.Errorf("code = %q, want %q (unknown group type)", coded.Code, errors.PROCESSING_CONFIG)
			}
		})
	}
}
