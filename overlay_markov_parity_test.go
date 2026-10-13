package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_MARKOV refuses identically in predict and at runtime (code,
// message and details — index included) through the shared overlayhost
// refusals, runs end to end on a weighted host, and leaves the host
// crosstab byte-identical to the overlay-free run.

func markovCrosstab(cohort string, cols []*types.Group, cell types.AggregationType, w *types.WeightSpec, overlays ...types.OverlaySpec) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Weight: w,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Columns: cols,
			Cell:    &types.Aggregation{Type: cell, Field: "x", Label: "n"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
		Overlays: overlays,
	}
}

func markovOverlay(params string) types.OverlaySpec {
	s := types.OverlaySpec{Name: "flow", Kind: types.OverlayKindMarkov, Scope: types.OverlayScopeMatrix}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

func TestMarkov_RefusalsMatchPredict(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	square := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
	twoCols := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}, {Type: types.GROUP_CATEGORY, Field: "subj"}}
	for _, c := range []struct {
		name string
		req  *types.Request
		want errors.Code
	}{
		{"average cell", markovCrosstab(cohort, square, types.AGG_AVERAGE, nil, markovOverlay("")), errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"columns carry two groupers", markovCrosstab(cohort, twoCols, types.AGG_COUNT, nil, markovOverlay("")), errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE},
		{"step out of range", markovCrosstab(cohort, square, types.AGG_COUNT, nil, markovOverlay(`{"steps":[0]}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown initial form", markovCrosstab(cohort, square, types.AGG_COUNT, nil, markovOverlay(`{"initial":"uniform"}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown param", markovCrosstab(cohort, square, types.AGG_COUNT, nil, markovOverlay(`{"horizon":12}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := p.Process(ctx, c.req)
			ce := requireCode(t, err, c.want)
			if ce.Details["index"] != 0 {
				t.Fatalf("runtime details %v lack the spec index", ce.Details)
			}
			sameEntry(t, predictEnvelope(t, p, fs, cohort, c.req), err)
		})
	}

	// The key-set refusal is runtime-only (predict reads no record): g x
	// subj has one grouper per axis but different keys.
	req := markovCrosstab(cohort, []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}}, types.AGG_COUNT, nil, markovOverlay(""))
	if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
		t.Fatalf("predict refused a one-grouper-per-axis host: %+v", env.Errors)
	}
	_, err := p.Process(ctx, req)
	requireCode(t, err, errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE)
}

func TestMarkov_EndToEnd(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	square := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
	spec := markovOverlay(`{"steps":[2],"initial":"margin"}`)
	for name, w := range map[string]*types.WeightSpec{
		"unweighted":  nil,
		"probability": {Field: "y", Kind: types.WeightKindProbability},
	} {
		t.Run(name, func(t *testing.T) {
			for _, cell := range []types.AggregationType{types.AGG_COUNT, types.AGG_SUM} {
				req := markovCrosstab(cohort, square, cell, w, spec)
				if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
					t.Fatalf("%s: predict %+v", cell, env.Errors)
				}
				resp, err := p.Process(ctx, req)
				if err != nil {
					t.Fatalf("%s: %v", cell, err)
				}
				if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Matrix == nil || resp.Overlays[0].Payload.Parts["step_2"] == nil || resp.Overlays[0].Payload.Parts["distribution_2"] == nil {
					t.Fatalf("%s: overlays %+v", cell, resp.Overlays)
				}
				if _, err := json.Marshal(resp); err != nil {
					t.Fatalf("%s: marshal: %v", cell, err)
				}

				// The host crosstab is byte-identical to the overlay-free run.
				bare, err := p.Process(ctx, markovCrosstab(cohort, square, cell, w))
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
