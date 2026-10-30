package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_RETENTION refuses identically in predict and at runtime (code,
// message and details — index included) through the shared overlayhost
// refusals, runs end to end on weighted hosts, and leaves the host
// crosstab byte-identical to the overlay-free run. An absorbing name the
// host lacks is the one runtime-only refusal (predict reads no record).

func retentionOverlay(params string) types.OverlaySpec {
	s := types.OverlaySpec{Name: "keep", Kind: types.OverlayKindRetention, Scope: types.OverlayScopeRow}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

func TestRetention_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	square := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
	twoCols := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}, {Type: types.GROUP_CATEGORY, Field: "subj"}}
	matrixScope := retentionOverlay("")
	matrixScope.Scope = types.OverlayScopeMatrix
	withRef := retentionOverlay("")
	withRef.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}
	for _, c := range []struct {
		name string
		req  *types.Request
		want errors.Code
	}{
		{"average cell", markovCrosstab(cohort, square, types.AGG_AVERAGE, nil, retentionOverlay("")), errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"columns carry two groupers", markovCrosstab(cohort, twoCols, types.AGG_COUNT, nil, retentionOverlay("")), errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE},
		{"empty absorbing", markovCrosstab(cohort, square, types.AGG_COUNT, nil, retentionOverlay(`{"absorbing":[]}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown param", markovCrosstab(cohort, square, types.AGG_COUNT, nil, retentionOverlay(`{"steps":[1]}`)), errors.PULSE_REQUEST_UNKNOWN_FIELD},
		{"matrix scope", markovCrosstab(cohort, square, types.AGG_COUNT, nil, matrixScope), errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{"ref set", markovCrosstab(cohort, square, types.AGG_COUNT, nil, withRef), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"level set", markovCrosstab(cohort, square, types.AGG_COUNT, nil, func() types.OverlaySpec { s := retentionOverlay(""); s.Level = 1; return s }()), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
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

	// The runtime-only refusal: predict cannot see which keys the host
	// holds, so it accepts an absorbing name the runtime then refuses.
	req := markovCrosstab(cohort, square, types.AGG_COUNT, nil, retentionOverlay(`{"absorbing":["lapsed"]}`))
	if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
		t.Fatalf("predict refused an absorbing name it cannot judge: %+v", env.Errors)
	}
	_, err := p.Process(ctx, req)
	if ce := requireCode(t, err, errors.PULSE_OVERLAY_PARAM_MISSING); ce.Details["param"] != "absorbing" || ce.Details["index"] != 0 {
		t.Fatalf("unknown absorbing state details %v", ce.Details)
	}
}

// TestRetention_EndToEnd: unweighted and probability-weighted g x g
// hosts (COUNT and SUM cells; frequency expansion is internal/service's
// TestRetention_FrequencyWeightEqualsExpandedRows) run, predict accepts,
// the layer is a SERIES with one entry per from-state carrying a stay
// rate, and the host crosstab is byte-identical to the overlay-free run.
// g x subj (one grouper per axis, different keys) runs too: no
// from-state has a column of its own, so every stay rate is 0.
func TestRetention_EndToEnd(t *testing.T) {
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
					req := markovCrosstab(cohort, cols, cell, w, retentionOverlay(""))
					if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
						t.Fatalf("%s: predict %+v", cell, env.Errors)
					}
					resp, err := p.Process(ctx, req)
					if err != nil {
						t.Fatalf("%s: %v", cell, err)
					}
					if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Shape != types.OverlayShapeSeries || resp.Overlays[0].Payload.Series == nil {
						t.Fatalf("%s: overlays %+v", cell, resp.Overlays)
					}
					es := resp.Overlays[0].Payload.Series.Entries
					if len(es) != 2 {
						t.Fatalf("%s: %d entries, want one per g level", cell, len(es))
					}
					for _, e := range es {
						stay := *e.Summary.Statistic
						if cols[0].Field == "g" && stay != 1 || cols[0].Field == "subj" && stay != 0 {
							t.Fatalf("%s: entry %v stay %v", cell, e.Key, stay)
						}
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
