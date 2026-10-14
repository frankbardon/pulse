package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_CORRESPONDENCE refuses identically in predict and at runtime
// (code, message and details — index included) through the shared
// overlayhost refusals, runs end to end on a weighted host, and leaves
// the host crosstab byte-identical to the overlay-free run.

func caCrosstab(cohort string, cols *types.Group, cell types.AggregationType, w *types.WeightSpec, overlays ...types.OverlaySpec) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Weight: w,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Columns: []*types.Group{cols},
			Cell:    &types.Aggregation{Type: cell, Field: "x", Label: "n"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
		Overlays: overlays,
	}
}

func TestCorrespondence_RefusalsMatchPredict(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	byRange := &types.Group{Type: types.GROUP_RANGE, Field: "x", Interval: 20}
	bySubject := &types.Group{Type: types.GROUP_CATEGORY, Field: "subj"} // g x subj: 2 x 24, K = 1
	ca := func(params string) types.OverlaySpec {
		s := types.OverlaySpec{Name: "map", Kind: types.OverlayKindCorrespondence, Scope: types.OverlayScopeMatrix}
		if params != "" {
			s.Params = json.RawMessage(params)
		}
		return s
	}
	for _, c := range []struct {
		name string
		req  *types.Request
		want errors.Code
	}{
		{"average cell", caCrosstab(cohort, byRange, types.AGG_AVERAGE, nil, ca("")), errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"max cell", caCrosstab(cohort, byRange, types.AGG_MAX, nil, ca("")), errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"dimensions over cap", caCrosstab(cohort, bySubject, types.AGG_COUNT, nil, ca(`{"dimensions":2}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown normalization", caCrosstab(cohort, byRange, types.AGG_COUNT, nil, ca(`{"normalization":"biplot"}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown param", caCrosstab(cohort, byRange, types.AGG_COUNT, nil, ca(`{"supplementary":[0]}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
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
}

func TestCorrespondence_EndToEnd(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	// The parts the manifest declares are exactly the parts a run emits
	// (the row_distance / column_distance pair included).
	declared := map[string]bool{}
	for _, o := range p.Manifest(ctx).Overlays {
		if o.Kind == types.OverlayKindCorrespondence {
			for _, part := range o.Parts {
				declared[part.Name] = true
			}
		}
	}
	if len(declared) != 8 || !declared["row_distance"] || !declared["column_distance"] {
		t.Fatalf("manifest declares parts %v", declared)
	}
	bySubject := &types.Group{Type: types.GROUP_CATEGORY, Field: "subj"}
	spec := types.OverlaySpec{Name: "map", Kind: types.OverlayKindCorrespondence, Scope: types.OverlayScopeMatrix}
	for name, w := range map[string]*types.WeightSpec{
		"unweighted":  nil,
		"probability": {Field: "y", Kind: types.WeightKindProbability},
	} {
		t.Run(name, func(t *testing.T) {
			for _, cell := range []types.AggregationType{types.AGG_COUNT, types.AGG_SUM} {
				req := caCrosstab(cohort, bySubject, cell, w, spec)
				if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
					t.Fatalf("%s: predict %+v", cell, env.Errors)
				}
				resp, err := p.Process(ctx, req)
				if err != nil {
					t.Fatalf("%s: %v", cell, err)
				}
				if len(resp.Overlays) != 1 || len(resp.Overlays[0].Payload.Parts) != len(declared) || len(resp.Overlays[0].Payload.Series.Entries) != 1 {
					t.Fatalf("%s: overlays %+v", cell, resp.Overlays)
				}
				for name := range resp.Overlays[0].Payload.Parts {
					if !declared[name] {
						t.Errorf("%s: runtime part %q is not declared in the manifest", cell, name)
					}
				}
				if _, err := json.Marshal(resp); err != nil {
					t.Fatalf("%s: marshal: %v", cell, err)
				}

				// The host crosstab is byte-identical to the overlay-free run.
				bare, err := p.Process(ctx, caCrosstab(cohort, bySubject, cell, w))
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
