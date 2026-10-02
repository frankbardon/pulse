package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Predict-arm coverage for the EXTENSION half of the distinct-key slab
// partition gate. E1-S3 gated built-in fan-out groupers only, because
// types.GroupType.FansOut() knows built-in constants; an embedder's
// multi-key grouper on a summed-across dim passed and inflated n
// silently. The fact reaches this arm over
// descriptor.ExtensionsSnapshot — descriptor/ may not import
// internal/processing/ and so cannot assert MultiKeyStreamingGrouper itself.

const pwExtGrouper = "GROUP_ACME_PANEL_X"

// pwExtSnapshot builds a snapshot carrying one registered grouper with
// the given fan-out declaration. present=false yields a snapshot that
// does not carry the grouper at all.
func pwExtSnapshot(present, fansOut bool) *ExtensionsSnapshot {
	snap := &ExtensionsSnapshot{}
	if present {
		snap.Groupers = append(snap.Groupers, descriptor.OperatorMeta{
			Name:      pwExtGrouper,
			Namespace: "ACME",
			FansOut:   fansOut,
		})
	}
	return snap
}

// TestExtensionsSnapshot_GrouperFanOut pins the bridge accessor itself,
// including the nil receiver every no-extensions host hits.
func TestExtensionsSnapshot_GrouperFanOut(t *testing.T) {
	cases := []struct {
		name      string
		snap      *ExtensionsSnapshot
		lookup    string
		wantFan   bool
		wantFound bool
	}{
		{"declared fan-out", pwExtSnapshot(true, true), pwExtGrouper, true, true},
		{"declared single-key", pwExtSnapshot(true, false), pwExtGrouper, false, true},
		{"not in the snapshot", pwExtSnapshot(false, false), pwExtGrouper, false, false},
		{"other name", pwExtSnapshot(true, true), "GROUP_ACME_OTHER_X", false, false},
		{"nil snapshot", nil, pwExtGrouper, false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fan, found := tc.snap.GrouperFanOut(tc.lookup)
			if fan != tc.wantFan || found != tc.wantFound {
				t.Fatalf("GrouperFanOut(%q) = (%v, %v), want (%v, %v)",
					tc.lookup, fan, found, tc.wantFan, tc.wantFound)
			}
		})
	}
}

// pwExtRequest is the offending shape with an EXTENSION grouper as the
// inner (summed-across) pair-axis level: flat `segment` outer, the
// custom grouper inner, n_within_depth=0.
func pwExtRequest() *types.Request {
	return pwPartRequest(
		[]*types.Group{
			pwPartGroup(types.GROUP_CATEGORY, "segment"),
			pwPartGroup(types.GroupType(pwExtGrouper), "panel"),
		},
		[]*types.Group{pwPartGroup(types.GROUP_CATEGORY, "wave")},
		types.OverlayScopeRow,
		`{"n_source":"n_within_distinct","n_within_depth":0}`,
	)
}

// TestValidateOverlays_ExtensionFanOutRefused is the story's core
// predict assertion: a registered multi-key grouper on a summed-across
// dim refuses with the same code and the same details a built-in
// fan-out grouper would.
func TestValidateOverlays_ExtensionFanOutRefused(t *testing.T) {
	env := descriptor.NewEnvelope(nil)
	ValidateOverlays(env, pwExtRequest(), nil, &PredictOptions{Extensions: pwExtSnapshot(true, true)})

	got := pwPartFindError(env, string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED))
	if got == nil {
		t.Fatalf("predict accepted an extension fan-out grouper on a summed-across dim; codes %v",
			pwPartErrorCodes(env))
	}
	for key, want := range map[string]any{
		"dim_index":  1,
		"group_type": pwExtGrouper,
		"field":      "panel",
		"axis":       "row",
	} {
		if have := got.Details[key]; have != want {
			t.Errorf("Details[%q] = %v, want %v", key, have, want)
		}
	}
}

// TestValidateOverlays_ExtensionFanOutResolution covers the rest of the
// decision table on the identical request, so the ONLY variable is
// what the snapshot says about the grouper.
func TestValidateOverlays_ExtensionFanOutResolution(t *testing.T) {
	cases := []struct {
		name       string
		opts       *PredictOptions
		wantRefuse bool
	}{
		{"declared fan-out", &PredictOptions{Extensions: pwExtSnapshot(true, true)}, true},
		// A declared single-key grouper partitions, so the summed
		// distinct counts are exact. Refusing it would be a false
		// refusal of a correct request.
		{"declared single-key", &PredictOptions{Extensions: pwExtSnapshot(true, false)}, false},
		// Registered nowhere: the request cannot execute (the runtime
		// refuses to build an unknown group type), so there is no wrong
		// number to prevent and the gate stays silent rather than
		// burying the accurate unknown-operator diagnostic.
		{"absent from the snapshot", &PredictOptions{Extensions: pwExtSnapshot(false, false)}, false},
		{"nil snapshot", &PredictOptions{}, false},
		{"nil opts", nil, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			env := descriptor.NewEnvelope(nil)
			ValidateOverlays(env, pwExtRequest(), nil, tc.opts)
			got := pwPartFindError(env, string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED))
			if tc.wantRefuse && got == nil {
				t.Fatalf("expected a refusal; codes %v", pwPartErrorCodes(env))
			}
			if !tc.wantRefuse && got != nil {
				t.Fatalf("unexpected refusal: %s", got.Message)
			}
		})
	}
}

// TestValidateOverlays_ExtensionFanOutInPrefixRuns keeps the accepted
// shape accepted: the custom fan-out grouper OUTER, inside the fixed
// prefix, multiplies slabs rather than cells. Gating it would refuse
// the very shape n_within_distinct exists for.
func TestValidateOverlays_ExtensionFanOutInPrefixRuns(t *testing.T) {
	req := pwPartRequest(
		[]*types.Group{
			pwPartGroup(types.GroupType(pwExtGrouper), "panel"),
			pwPartGroup(types.GROUP_CATEGORY, "segment"),
		},
		[]*types.Group{pwPartGroup(types.GROUP_CATEGORY, "wave")},
		types.OverlayScopeRow,
		`{"n_source":"n_within_distinct","n_within_depth":0}`,
	)
	env := descriptor.NewEnvelope(nil)
	ValidateOverlays(env, req, nil, &PredictOptions{Extensions: pwExtSnapshot(true, true)})
	if got := pwPartFindError(env, string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)); got != nil {
		t.Fatalf("refused an in-prefix extension fan-out: %s", got.Message)
	}
}
