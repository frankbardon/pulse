package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_SYMMETRY refuses identically in predict and at runtime (code,
// message and details — index included) through the shared overlayhost
// refusals, runs end to end on weighted hosts, and leaves the host
// crosstab byte-identical to the overlay-free run.

func symmetryOverlay(params string) types.OverlaySpec {
	s := types.OverlaySpec{Name: "sym", Kind: types.OverlayKindSymmetry, Scope: types.OverlayScopeMatrix}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

func TestSymmetry_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	square := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
	twoCols := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}, {Type: types.GROUP_CATEGORY, Field: "subj"}}
	cellScope := symmetryOverlay("")
	cellScope.Scope = types.OverlayScopeCell
	withRef := symmetryOverlay("")
	withRef.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}
	for _, c := range []struct {
		name string
		req  *types.Request
		want errors.Code
	}{
		{"average cell", markovCrosstab(cohort, square, types.AGG_AVERAGE, nil, symmetryOverlay("")), errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"columns carry two groupers", markovCrosstab(cohort, twoCols, types.AGG_COUNT, nil, symmetryOverlay("")), errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE},
		{"non-boolean correct", markovCrosstab(cohort, square, types.AGG_COUNT, nil, symmetryOverlay(`{"correct":1}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown param", markovCrosstab(cohort, square, types.AGG_COUNT, nil, symmetryOverlay(`{"exact":true}`)), errors.PULSE_REQUEST_UNKNOWN_FIELD},
		{"cell scope", markovCrosstab(cohort, square, types.AGG_COUNT, nil, cellScope), errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{"ref set", markovCrosstab(cohort, square, types.AGG_COUNT, nil, withRef), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"level set", markovCrosstab(cohort, square, types.AGG_COUNT, nil, func() types.OverlaySpec { s := symmetryOverlay(""); s.Level = 1; return s }()), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := p.Process(ctx, c.req)
			ce := requireCode(t, err, c.want)
			if !overlaySpecZeroLocated(ce) {
				t.Fatalf("runtime details %v lack the spec index", ce.Details)
			}
			sameEntry(t, predictEnvelope(t, p, fs, cohort, c.req), err)
		})
	}
}

// TestSymmetry_EndToEnd: a weighted and an unweighted g x g host (COUNT
// and SUM cells) run, predict accepts, the layer is a scalar χ² with its
// summary, and the host crosstab is byte-identical to the overlay-free
// run. g x subj (one grouper per axis, different keys) runs too: a
// one-axis state is a zero row / column, never a refusal.
func TestSymmetry_EndToEnd(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	for _, cols := range [][]*types.Group{
		{{Type: types.GROUP_CATEGORY, Field: "g"}},
		{{Type: types.GROUP_CATEGORY, Field: "subj"}},
	} {
		for name, w := range map[string]*types.WeightSpec{
			"unweighted":  nil,
			"probability": {Field: "y", Kind: types.WeightKindProbability},
		} {
			t.Run(cols[0].Field+"/"+name, func(t *testing.T) {
				for _, cell := range []types.AggregationType{types.AGG_COUNT, types.AGG_SUM} {
					req := markovCrosstab(cohort, cols, cell, w, symmetryOverlay(""))
					if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
						t.Fatalf("%s: predict %+v", cell, env.Errors)
					}
					resp, err := p.Process(ctx, req)
					if err != nil {
						t.Fatalf("%s: %v", cell, err)
					}
					if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Shape != types.OverlayShapeScalar || resp.Overlays[0].Summary == nil ||
						resp.Overlays[0].Summary.PValue == nil {
						t.Fatalf("%s: overlays %+v", cell, resp.Overlays)
					}
					if df := resp.Overlays[0].Summary.Parameters["df"]; math.IsNaN(df) {
						t.Fatalf("%s: df %v", cell, df)
					}
					if _, err := json.Marshal(resp); err != nil {
						t.Fatalf("%s: marshal: %v", cell, err)
					}
					bare, err := p.Process(ctx, markovCrosstab(cohort, cols, cell, w))
					if err != nil {
						t.Fatal(err)
					}
					got, _ := json.Marshal(resp.Crosstab)
					want, _ := json.Marshal(bare.Crosstab)
					if string(got) != string(want) || bare.Overlays != nil {
						t.Fatalf("%s: the overlay moved the host crosstab", cell)
					}
				}
			})
		}
	}
}
