package pulse_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_RAKE refuses identically in predict and at runtime (code,
// message and details — index included) through the shared overlayhost
// refusals, runs end to end on a weighted host, reports a capped fit as
// a layer warning, and leaves the host crosstab byte-identical to the
// overlay-free run.

// rakeCrosstab is the g (a, b) x subj (s0..s23) crosstab of the
// acceptance cohort: every cell holds exactly one record.
func rakeCrosstab(cohort string, cell types.AggregationType, w *types.WeightSpec, overlays ...types.OverlaySpec) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Weight: w,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}},
			Cell:    &types.Aggregation{Type: cell, Field: "x", Label: "n"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
		Overlays: overlays,
	}
}

// rakeOverlay builds an OVERLAY_RAKE spec from row targets, column
// targets (nil = one share per subj key) and extra params.
func rakeOverlay(rows map[string]float64, cols map[string]float64, extra map[string]any) types.OverlaySpec {
	if cols == nil {
		cols = map[string]float64{}
		for i := range 24 {
			cols["s"+strconv.Itoa(i)] = float64(1 + i%3)
		}
	}
	params := map[string]any{"row_targets": rows, "col_targets": cols}
	for k, v := range extra {
		params[k] = v
	}
	raw, _ := json.Marshal(params)
	return types.OverlaySpec{Name: "fit", Kind: types.OverlayKindRake, Scope: types.OverlayScopeMatrix, Params: raw}
}

func TestRake_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	ab := map[string]float64{"a": 0.3, "b": 0.7}
	for _, c := range []struct {
		name string
		req  *types.Request
		want errors.Code
	}{
		{"average cell", rakeCrosstab(cohort, types.AGG_AVERAGE, nil, rakeOverlay(ab, nil, nil)), errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"row target outside the dictionary", rakeCrosstab(cohort, types.AGG_COUNT, nil, rakeOverlay(map[string]float64{"a": 1, "b": 1, "zz": 1}, nil, nil)), errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH},
		{"counts totals differ", rakeCrosstab(cohort, types.AGG_COUNT, nil, rakeOverlay(map[string]float64{"a": 10, "b": 10}, map[string]float64{"s0": 5}, map[string]any{"targets_as": "counts"})), errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH},
		{"missing col targets", rakeCrosstab(cohort, types.AGG_COUNT, nil, types.OverlaySpec{Name: "fit", Kind: types.OverlayKindRake, Scope: types.OverlayScopeMatrix, Params: json.RawMessage(`{"row_targets":{"a":1}}`)}), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown param", rakeCrosstab(cohort, types.AGG_COUNT, nil, rakeOverlay(ab, nil, map[string]any{"write_weights": true})), errors.PULSE_REQUEST_UNKNOWN_FIELD},
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

	// A host key the targets leave out is runtime-only (predict cannot
	// know which dictionary entries the records hold).
	req := rakeCrosstab(cohort, types.AGG_COUNT, nil, rakeOverlay(map[string]float64{"a": 1}, nil, nil))
	if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
		t.Fatalf("predict refused a host-key gap it cannot see: %+v", env.Errors)
	}
	_, err := p.Process(ctx, req)
	ce := requireCode(t, err, errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH)
	if got, _ := json.Marshal(ce.Details["host_only"]); string(got) != `["b"]` {
		t.Fatalf("host_only %s, want [\"b\"]", got)
	}
}

func TestRake_EndToEnd(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	spec := rakeOverlay(map[string]float64{"a": 0.3, "b": 0.7}, nil, nil)
	for name, w := range map[string]*types.WeightSpec{
		"unweighted":  nil,
		"probability": {Field: "y", Kind: types.WeightKindProbability},
	} {
		t.Run(name, func(t *testing.T) {
			for _, cell := range []types.AggregationType{types.AGG_COUNT, types.AGG_SUM} {
				req := rakeCrosstab(cohort, cell, w, spec)
				if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
					t.Fatalf("%s: predict %+v", cell, env.Errors)
				}
				resp, err := p.Process(ctx, req)
				if err != nil {
					t.Fatalf("%s: %v", cell, err)
				}
				if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Matrix == nil || resp.Overlays[0].Payload.Parts["factors"] == nil {
					t.Fatalf("%s: overlays %+v", cell, resp.Overlays)
				}
				if s := resp.Overlays[0].Summary.Parameters; s["converged"] != 1 {
					t.Fatalf("%s: summary %v", cell, s)
				}
				if _, err := json.Marshal(resp); err != nil {
					t.Fatalf("%s: marshal: %v", cell, err)
				}

				// The host crosstab is byte-identical to the overlay-free run.
				bare, err := p.Process(ctx, rakeCrosstab(cohort, cell, w))
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

// TestRake_CappedFitShipsWithWarning: a fit stopped by max_iter still
// ships its layer, converged 0, and its NOT_CONVERGED warning surfaces
// on Response.Warnings (the per-Request overlay path's carrier, as for
// every crosstab overlay kind).
func TestRake_CappedFitShipsWithWarning(t *testing.T) {
	t.Parallel()

	p, _, cohort := acceptanceCohort(t)
	// The x sums are not a rank-one table, so one sweep cannot land on a
	// 1e-15 tolerance.
	spec := rakeOverlay(map[string]float64{"a": 0.3, "b": 0.7}, nil, map[string]any{"max_iter": 1, "tolerance": 1e-15})
	resp, err := p.Process(context.Background(), rakeCrosstab(cohort, types.AGG_SUM, nil, spec))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("overlays %+v", resp.Overlays)
	}
	layer := resp.Overlays[0]
	if layer.Summary.Parameters["converged"] != 0 || layer.Payload.Matrix == nil {
		t.Fatalf("layer %+v", layer)
	}
	found := false
	for _, w := range resp.Warnings {
		found = found || (w.Code == string(errors.PULSE_OVERLAY_RAKE_NOT_CONVERGED) && w.Details["iterations"] == 1)
	}
	if !found {
		t.Fatalf("response warnings %+v lack PULSE_OVERLAY_RAKE_NOT_CONVERGED", resp.Warnings)
	}
}
