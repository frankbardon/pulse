package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Predict-arm coverage for the distinct-key slab partition gate.
// descriptor.ValidateOverlays is reached from exactly one place
// (predict.go), so this exercises the validator directly; the runtime
// twin lives in internal/processing/ and the two-arm parity is pinned end to end
// at the repo root.

func pwPartGroup(kind types.GroupType, field string) *types.Group {
	return &types.Group{Type: kind, Field: field}
}

func pwPartRequest(rows, cols []*types.Group, scope types.OverlayScope, params string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: "c.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    rows,
			Columns: cols,
			Cell: &types.Aggregation{
				Type:   types.AGG_DISTINCT_SUM,
				Field:  "value",
				Params: json.RawMessage(`{"distinct_by":"respondent"}`),
			},
			Shape: types.CrosstabShapeMatrix,
		},
		Overlays: []types.OverlaySpec{{
			Name:   "pw",
			Kind:   types.OverlayKindPairwisePropZ,
			Scope:  scope,
			Params: json.RawMessage(params),
		}},
	}
}

func pwPartErrorCodes(env *descriptor.Envelope) []string {
	out := make([]string, 0, len(env.Errors))
	for _, e := range env.Errors {
		out = append(out, e.Code)
	}
	return out
}

func pwPartFindError(env *descriptor.Envelope, code string) *descriptor.EnvelopeEntry {
	for _, e := range env.Errors {
		if e.Code == code {
			return e
		}
	}
	return nil
}

// TestValidateOverlays_DistinctSlabNotPartitioned_Row is the ROW-scope
// refusal: a GROUP_SET_PER_ELEMENT level that the slab sums ACROSS.
func TestValidateOverlays_DistinctSlabNotPartitioned_Row(t *testing.T) {
	req := pwPartRequest(
		[]*types.Group{
			pwPartGroup(types.GROUP_CATEGORY, "segment"),
			pwPartGroup(types.GROUP_SET_PER_ELEMENT, "brand"),
		},
		[]*types.Group{pwPartGroup(types.GROUP_CATEGORY, "wave")},
		types.OverlayScopeRow,
		`{"n_source":"n_within_distinct","n_within_depth":0}`,
	)
	env := descriptor.NewEnvelope(nil)
	ValidateOverlays(env, req, nil, nil)

	got := pwPartFindError(env, string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED))
	if got == nil {
		t.Fatalf("expected %s; got codes %v",
			errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED, pwPartErrorCodes(env))
	}
	for key, want := range map[string]any{
		"dim_index":  1,
		"group_type": string(types.GROUP_SET_PER_ELEMENT),
		"axis":       "row",
		"field":      "brand",
		"n_source":   types.PairwiseNSourceNWithinDistinct,
	} {
		if have := got.Details[key]; have != want {
			t.Errorf("Details[%q] = %v, want %v", key, have, want)
		}
	}
	if !strings.Contains(got.Message, "GROUP_SET_PER_ELEMENT") {
		t.Errorf("message does not name the offending grouper: %q", got.Message)
	}
}

// TestValidateOverlays_DistinctSlabNotPartitioned_Column is the same
// refusal on the COLUMN axis — the gate is symmetric, not row-only.
func TestValidateOverlays_DistinctSlabNotPartitioned_Column(t *testing.T) {
	req := pwPartRequest(
		[]*types.Group{pwPartGroup(types.GROUP_CATEGORY, "wave")},
		[]*types.Group{
			pwPartGroup(types.GROUP_CATEGORY, "segment"),
			pwPartGroup(types.GROUP_SET_PER_ELEMENT, "brand"),
		},
		types.OverlayScopeColumn,
		`{"n_source":"n_within_distinct","n_within_depth":0}`,
	)
	env := descriptor.NewEnvelope(nil)
	ValidateOverlays(env, req, nil, nil)

	got := pwPartFindError(env, string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED))
	if got == nil {
		t.Fatalf("expected %s on COLUMN scope; got codes %v",
			errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED, pwPartErrorCodes(env))
	}
	if got.Details["axis"] != "column" {
		t.Errorf("Details[axis] = %v, want column", got.Details["axis"])
	}
	if got.Details["dim_index"] != 1 {
		t.Errorf("Details[dim_index] = %v, want 1", got.Details["dim_index"])
	}
}

// TestValidateOverlays_DistinctSlabAccepts covers every shape the gate
// must NOT refuse. Each one is a request that is correct today; a false
// refusal here breaks a working caller.
func TestValidateOverlays_DistinctSlabAccepts(t *testing.T) {
	flat := func(f string) *types.Group { return pwPartGroup(types.GROUP_CATEGORY, f) }
	fan := func(f string) *types.Group { return pwPartGroup(types.GROUP_SET_PER_ELEMENT, f) }

	tests := []struct {
		name   string
		rows   []*types.Group
		cols   []*types.Group
		scope  types.OverlayScope
		params string
	}{
		{
			// The filed shape: the fan-out sits inside the FIXED prefix,
			// so it multiplies slabs rather than cells.
			name:   "fan-out inside the fixed prefix",
			rows:   []*types.Group{fan("brand"), flat("segment")},
			cols:   []*types.Group{flat("wave")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"n_within_distinct","n_within_depth":0}`,
		},
		{
			name:   "fan-out on the OPPOSITE axis, row scope",
			rows:   []*types.Group{flat("segment")},
			cols:   []*types.Group{flat("wave"), fan("brand")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"n_within_distinct","n_within_depth":0}`,
		},
		{
			name:   "fan-out on the OPPOSITE axis, column scope",
			rows:   []*types.Group{flat("segment"), fan("brand")},
			cols:   []*types.Group{flat("wave")},
			scope:  types.OverlayScopeColumn,
			params: `{"n_source":"n_within_distinct","n_within_depth":0}`,
		},
		{
			// Record counts are additive; gating n_within would refuse
			// a request that is correct today.
			name:   "n_within over the same fan-out inner level",
			rows:   []*types.Group{flat("segment"), fan("brand")},
			cols:   []*types.Group{flat("wave")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"n_within","n_within_depth":0}`,
		},
		{
			// The MARGIN modes are not gated by this story; E1-S4 owns
			// their distinct siblings.
			name:   "row_margin_n over the same fan-out inner level",
			rows:   []*types.Group{flat("segment"), fan("brand")},
			cols:   []*types.Group{flat("wave")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"row_margin_n"}`,
		},
		{
			name:   "column_margin_n over the same fan-out inner level",
			rows:   []*types.Group{flat("segment"), fan("brand")},
			cols:   []*types.Group{flat("wave")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"column_margin_n"}`,
		},
		{
			name:   "raising n_within_depth covers the fan-out dim",
			rows:   []*types.Group{flat("segment"), fan("brand"), flat("wave2")},
			cols:   []*types.Group{flat("wave")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"n_within_distinct","n_within_depth":1}`,
		},
		{
			name:   "GROUP_SET_VALUE inner level partitions",
			rows:   []*types.Group{flat("segment"), pwPartGroup(types.GROUP_SET_VALUE, "brand")},
			cols:   []*types.Group{flat("wave")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"n_within_distinct","n_within_depth":0}`,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			env := descriptor.NewEnvelope(nil)
			ValidateOverlays(env, pwPartRequest(tc.rows, tc.cols, tc.scope, tc.params), nil, nil)
			if e := pwPartFindError(env, string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)); e != nil {
				t.Fatalf("unexpected refusal: %s", e.Message)
			}
		})
	}
}

// TestValidateOverlays_DistinctSlabGateRunsAfterParamGuards pins the
// ordering: a malformed / out-of-range Params blob must still surface
// its own PULSE_OVERLAY_PARAM_MISSING rather than being masked by the
// partition gate, which reads the very fields those guards validate.
func TestValidateOverlays_DistinctSlabGateRunsAfterParamGuards(t *testing.T) {
	req := pwPartRequest(
		[]*types.Group{
			pwPartGroup(types.GROUP_CATEGORY, "segment"),
			pwPartGroup(types.GROUP_SET_PER_ELEMENT, "brand"),
		},
		[]*types.Group{pwPartGroup(types.GROUP_CATEGORY, "wave")},
		types.OverlayScopeRow,
		`{"n_source":"n_within_distinct","n_within_depth":-1}`,
	)
	env := descriptor.NewEnvelope(nil)
	ValidateOverlays(env, req, nil, nil)

	if pwPartFindError(env, string(errors.PULSE_OVERLAY_PARAM_MISSING)) == nil {
		t.Fatalf("expected PULSE_OVERLAY_PARAM_MISSING for a negative n_within_depth; got %v",
			pwPartErrorCodes(env))
	}
	if e := pwPartFindError(env, string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)); e != nil {
		t.Errorf("partition gate fired on top of the range guard: %s", e.Message)
	}
}
