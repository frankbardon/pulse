package processing

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Runtime-arm coverage for the distinct-key slab partition gate.
//
// descriptor.ValidateOverlays is reached from exactly one place
// (descriptor/predict.go), so a predict-time refusal does NOT stop
// pulse.Process — this arm is why the gate is not predict-only. It
// hangs off applyOverlaysToResponse, the single hook both crosstab
// exits funnel through, and the tests below drive BOTH of them:
// RunCrosstab (buffered) and RunCrosstabFused (fused).

// partitionGateSchema is the filed shape in miniature: a multi-select
// `brand` for the FAN-OUT row level, a single-response `segment` for
// the flat one, a `wave` column axis, and the respondent / value pair
// an AGG_DISTINCT_SUM cell needs.
func partitionGateSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatalf("dict.Add(%q): %v", v, err)
			}
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "respondent", Type: encoding.FieldTypeU32},
		{Name: "value", Type: encoding.FieldTypeF64},
		{Name: "brand", Type: encoding.FieldTypeSetU8, Dictionary: dict("acme", "zenith")},
		{Name: "segment", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("urban", "rural")},
		{Name: "wave", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("w1", "w2")},
	}}
}

// partitionGateRecords: two records per respondent so a record count
// and a respondent count genuinely differ, and respondent 101 selects
// BOTH brands so the fan-out is real.
func partitionGateRecords(schema *encoding.Schema) []*Record {
	type row struct {
		respondent, value float64
		segment, wave     float64
		brandMask         uint64
	}
	rows := []row{
		{101, 30, 0, 0, 0b11},
		{102, 50, 0, 0, 0b01},
		{103, 40, 1, 0, 0b01},
		{104, 40, 0, 0, 0b10},
		{105, 90, 1, 1, 0b10},
	}
	out := make([]*Record, 0, len(rows)*2)
	for _, r := range rows {
		for i := 0; i < 2; i++ {
			out = append(out, NewRecordWithWide(schema,
				map[string]float64{
					"respondent": r.respondent,
					"value":      r.value,
					"segment":    r.segment,
					"wave":       r.wave,
				},
				nil,
				map[string]any{"brand": r.brandMask}))
		}
	}
	return out
}

// partitionGateRequest builds the crosstab + pairwise overlay. rowsFan
// places the fan-out grouper on the row axis (outer or inner); when
// false the column axis carries it instead.
func partitionGateRequest(rows, cols []*types.Group, scope types.OverlayScope, params string) *types.Request {
	return &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    rows,
			Columns: cols,
			Cell: &types.Aggregation{
				Type:   types.AGG_DISTINCT_SUM,
				Field:  "value",
				Label:  "distinct_value",
				Params: json.RawMessage(`{"distinct_by":"respondent"}`),
			},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
		Overlays: []types.OverlaySpec{{
			Name:   "pw",
			Kind:   types.OverlayKindPairwisePropZ,
			Scope:  scope,
			Params: json.RawMessage(params),
		}},
	}
}

func pgFlat(field string) *types.Group {
	return &types.Group{Type: types.GROUP_CATEGORY, Field: field}
}

func pgFan(field string) *types.Group {
	return &types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: field}
}

// partitionGateArm is one crosstab exit plus the error it returned.
type partitionGateArm struct {
	name string
	err  error
}

// partitionGateArms runs one request through BOTH crosstab exits. The
// gate lives at the shared overlay hook; running only one arm would
// pass even if the hook were bypassed on the other, so the result is an
// ORDERED slice the callers drive as subtests — a map range would let
// one arm's t.Fatalf hide the other entirely.
func partitionGateArms(t *testing.T, req *types.Request) []partitionGateArm {
	t.Helper()
	schema := partitionGateSchema(t)
	recs := partitionGateRecords(schema)

	buffered := NewProcessor(schema)
	_, bufErr := buffered.RunCrosstab(context.Background(), req, recs)

	fused := NewProcessor(schema)
	_, fusedErr := fused.RunCrosstabFused(context.Background(), req, NewSliceIterator(recs))

	return []partitionGateArm{{"buffered", bufErr}, {"fused", fusedErr}}
}

// TestCrosstabOverlays_DistinctSlabNotPartitioned_BothArms is the core
// refusal: a GROUP_SET_PER_ELEMENT level the slab sums ACROSS, refused
// identically by the buffered and the fused crosstab.
func TestCrosstabOverlays_DistinctSlabNotPartitioned_BothArms(t *testing.T) {
	req := partitionGateRequest(
		[]*types.Group{pgFlat("segment"), pgFan("brand")},
		[]*types.Group{pgFlat("wave")},
		types.OverlayScopeRow,
		`{"n_source":"n_within_distinct","n_within_depth":0}`,
	)
	for _, arm := range partitionGateArms(t, req) {
		arm := arm
		t.Run(arm.name, func(t *testing.T) {
			if arm.err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			coded := pairwiseCodedError(t, arm.err)
			if coded.Code != errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED {
				t.Fatalf("code = %q, want %q", coded.Code,
					errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)
			}
			for key, want := range map[string]any{
				"dim_index":  1,
				"group_type": string(types.GROUP_SET_PER_ELEMENT),
				"axis":       "row",
				"field":      "brand",
			} {
				if got := coded.Details[key]; got != want {
					t.Errorf("Details[%q] = %v, want %v", key, got, want)
				}
			}
			if !strings.Contains(coded.Message, "GROUP_SET_PER_ELEMENT") {
				t.Errorf("message does not name the grouper: %q", coded.Message)
			}
		})
	}
}

// TestCrosstabOverlays_DistinctSlabNotPartitioned_ColumnScope covers
// the column axis symmetrically — the gate is not row-only.
func TestCrosstabOverlays_DistinctSlabNotPartitioned_ColumnScope(t *testing.T) {
	req := partitionGateRequest(
		[]*types.Group{pgFlat("wave")},
		[]*types.Group{pgFlat("segment"), pgFan("brand")},
		types.OverlayScopeColumn,
		`{"n_source":"n_within_distinct","n_within_depth":0}`,
	)
	for _, arm := range partitionGateArms(t, req) {
		arm := arm
		t.Run(arm.name, func(t *testing.T) {
			if arm.err == nil {
				t.Fatal("expected a COLUMN-scope refusal, got nil")
			}
			coded := pairwiseCodedError(t, arm.err)
			if coded.Code != errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED {
				t.Fatalf("code = %q, want %q", coded.Code,
					errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)
			}
			if coded.Details["axis"] != "column" {
				t.Errorf("Details[axis] = %v, want column", coded.Details["axis"])
			}
		})
	}
}

// TestCrosstabOverlays_DistinctSlabAccepts is the false-refusal guard.
// Every case here is a request that runs correctly today; the gate must
// leave all of them alone on BOTH arms.
func TestCrosstabOverlays_DistinctSlabAccepts(t *testing.T) {
	tests := []struct {
		name   string
		rows   []*types.Group
		cols   []*types.Group
		scope  types.OverlayScope
		params string
	}{
		{
			// The filed shape — fan-out INSIDE the fixed prefix. It
			// multiplies slabs, not cells; refusing it would refuse the
			// request that motivated the mode.
			name:   "fan-out inside the fixed prefix",
			rows:   []*types.Group{pgFan("brand"), pgFlat("segment")},
			cols:   []*types.Group{pgFlat("wave")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"n_within_distinct","n_within_depth":0}`,
		},
		{
			// Each opposite coordinate is its own denominator; the slab
			// never sums across the opposite axis.
			name:   "fan-out on the OPPOSITE axis at row scope",
			rows:   []*types.Group{pgFlat("segment")},
			cols:   []*types.Group{pgFlat("wave"), pgFan("brand")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"n_within_distinct","n_within_depth":0}`,
		},
		{
			name:   "fan-out on the OPPOSITE axis at column scope",
			rows:   []*types.Group{pgFlat("segment"), pgFan("brand")},
			cols:   []*types.Group{pgFlat("wave")},
			scope:  types.OverlayScopeColumn,
			params: `{"n_source":"n_within_distinct","n_within_depth":0}`,
		},
		{
			// Record counts ARE additive under a fan-out grouper.
			name:   "n_within over the same fan-out inner level",
			rows:   []*types.Group{pgFlat("segment"), pgFan("brand")},
			cols:   []*types.Group{pgFlat("wave")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"n_within","n_within_depth":0}`,
		},
		{
			// The MARGIN modes are out of scope for this gate — a
			// margin accumulates over raw records rather than folding
			// cells, so no key can land in two summed buckets.
			name:   "row_margin_n over the same fan-out inner level",
			rows:   []*types.Group{pgFlat("segment"), pgFan("brand")},
			cols:   []*types.Group{pgFlat("wave")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"row_margin_n"}`,
		},
		{
			name:   "column_margin_n over the same fan-out inner level",
			rows:   []*types.Group{pgFlat("segment"), pgFan("brand")},
			cols:   []*types.Group{pgFlat("wave")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"column_margin_n"}`,
		},
		{
			// The DISTINCT margin modes, over the SAME axis shape the
			// gate refuses n_within_distinct on. They read distinct
			// keys but are exact by construction, so refusing them
			// would refuse a correct request.
			name:   "row_margin_distinct over the same fan-out inner level",
			rows:   []*types.Group{pgFlat("segment"), pgFan("brand")},
			cols:   []*types.Group{pgFlat("wave")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"row_margin_distinct"}`,
		},
		{
			name:   "column_margin_distinct over the same fan-out inner level",
			rows:   []*types.Group{pgFlat("segment"), pgFan("brand")},
			cols:   []*types.Group{pgFlat("wave")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"column_margin_distinct"}`,
		},
		{
			name:   "column_margin_distinct at column scope over a fan-out inner level",
			rows:   []*types.Group{pgFlat("wave")},
			cols:   []*types.Group{pgFlat("segment"), pgFan("brand")},
			scope:  types.OverlayScopeColumn,
			params: `{"n_source":"column_margin_distinct"}`,
		},
		{
			// A stray n_within_depth left over from an earlier edit
			// must not drag a margin mode into the slab gate.
			name:   "row_margin_distinct with a stray n_within_depth",
			rows:   []*types.Group{pgFlat("segment"), pgFan("brand")},
			cols:   []*types.Group{pgFlat("wave")},
			scope:  types.OverlayScopeRow,
			params: `{"n_source":"row_margin_distinct","n_within_depth":0}`,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := partitionGateRequest(tc.rows, tc.cols, tc.scope, tc.params)
			for _, arm := range partitionGateArms(t, req) {
				if arm.err == nil {
					continue
				}
				if coded, ok := arm.err.(*errors.CodedError); ok &&
					coded.Code == errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED {
					t.Errorf("%s arm: unexpected partition refusal: %s", arm.name, coded.Message)
					continue
				}
				t.Errorf("%s arm: unexpected error: %v", arm.name, arm.err)
			}
		})
	}
}

// TestCrosstabOverlays_DistinctSlabGateInertWithoutOverlays pins the
// additive-identity contract: the gate reads only req.Overlays, so a
// crosstab with the same offending axis shape and NO overlay is
// untouched.
func TestCrosstabOverlays_DistinctSlabGateInertWithoutOverlays(t *testing.T) {
	req := partitionGateRequest(
		[]*types.Group{pgFlat("segment"), pgFan("brand")},
		[]*types.Group{pgFlat("wave")},
		types.OverlayScopeRow,
		`{"n_source":"n_within_distinct","n_within_depth":0}`,
	)
	req.Overlays = nil
	for _, arm := range partitionGateArms(t, req) {
		if arm.err != nil {
			t.Errorf("%s arm: overlay-free request must not be gated: %v", arm.name, arm.err)
		}
	}
}
