package descriptor

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestPredict_PairwiseWeightedTwoMeansZ_WeightedAverageCell: full
// Predict over an AGG_AVERAGE crosstab cell (FR-18). Weighted by its
// slot, the request or the instance default, the cell is a valid host
// for OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z — neither the overlay shape
// gate nor the weighted-inference refusal fires (the kind is exempt).
// Unweighted, or opted out with `weight: null`, it is still
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestPredict_PairwiseWeightedTwoMeansZ_WeightedAverageCell(t *testing.T) {
	file := buildJoinPulseBytes(t, []encoding.Field{
		{Name: "cat", Type: encoding.FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 9, CsvColumnIdx: 2},
	})
	w := types.WeightSpec{Field: "w"}
	build := func(slot types.SlotWeight, reqW *types.WeightSpec) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "c.pulse"},
			Weight: reqW,
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Cell:    &types.Aggregation{Type: types.AGG_AVERAGE, Field: "x", Label: "avg", Weight: slot},
				Shape:   types.CrosstabShapeMatrix,
			},
			Overlays: []types.OverlaySpec{{
				Name: "wtz", Kind: types.OverlayKindPairwiseWeightedTwoMeansZ, Scope: types.OverlayScopeRow,
				Params: json.RawMessage(`{"n_basis":"kish"}`),
			}},
		}
	}
	cases := []struct {
		name  string
		req   *types.Request
		def   *types.WeightSpec
		admit bool
	}{
		{"slot", build(types.SlotWeightOf(w), nil), nil, true},
		{"request", build(types.SlotWeight{}, &w), nil, true},
		{"default", build(types.SlotWeight{}, nil), &w, true},
		{"unweighted", build(types.SlotWeight{}, nil), nil, false},
		{"opted_out", build(types.NullSlotWeight(), &w), nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := Predict(bytes.NewReader(file), tc.req, &PredictOptions{DefaultWeight: tc.def})
			if tc.admit {
				if len(env.Errors) != 0 {
					t.Fatalf("weighted AGG_AVERAGE host refused: %+v", env.Errors)
				}
				return
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
				t.Fatalf("errors %+v, want one PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", env.Errors)
			}
		})
	}
}
