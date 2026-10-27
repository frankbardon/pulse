package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestPairwisePSource_NormalisedHost: on a crosstab with normalize !=
// none every cell value is already a 0..1 share, so dividing it by 100
// (the cell_value_pct reading) would score every pair silently wrong.
// An omitted p_source on such a host therefore reads the share as is
// (cell_value) — the layer equals the explicit cell_value one — while
// an explicit cell_value_pct there is PULSE_OVERLAY_PARAM_MISSING with
// one code and message at runtime and in predict. Under normalize none
// the omitted default stays cell_value_pct, byte-identical. Both
// proportion kinds, unweighted and frequency-weighted hosts.
func TestPairwisePSource_NormalisedHost(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	mk := func(kind types.OverlayKind, norm types.CrosstabNormalize, pSource string, w *types.WeightSpec) *types.Request {
		var params json.RawMessage
		if pSource != "" {
			params = json.RawMessage(`{"p_source":"` + pSource + `"}`)
		}
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Weight: w,
			Crosstab: &types.CrosstabSpec{
				Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Columns:   []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Interval: 20}},
				Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "c"},
				Margins:   types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
				Normalize: norm,
			},
			Overlays: []types.OverlaySpec{{Name: "pw", Kind: kind, Scope: types.OverlayScopeColumn, Params: params}},
		}
	}
	layers := func(t *testing.T, req *types.Request) (string, int) {
		t.Helper()
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
			t.Fatalf("predict refused what the runtime ran: %+v", env.Errors)
		}
		b, err := json.Marshal(resp.Overlays)
		if err != nil {
			t.Fatal(err)
		}
		scored := 0
		if m := resp.Overlays[0].Payload.Matrix; m != nil {
			for _, row := range m.Cells {
				for _, c := range row {
					if c.Present {
						scored++
					}
				}
			}
		}
		return string(b), scored
	}
	freq := &types.WeightSpec{Field: "t_u8", Kind: types.WeightKindFrequency}
	for _, kind := range []types.OverlayKind{types.OverlayKindPairwisePropZ, types.OverlayKindPairwiseProbitT} {
		for wname, w := range map[string]*types.WeightSpec{"unweighted": nil, "frequency": freq} {
			if w != nil && kind == types.OverlayKindPairwiseProbitT {
				continue // refused on any weighted host (weighting reference)
			}
			for _, norm := range []types.CrosstabNormalize{types.CrosstabNormalizeColumn, types.CrosstabNormalizeRow, types.CrosstabNormalizeTotal} {
				t.Run(string(kind)+"/"+wname+"/"+string(norm), func(t *testing.T) {
					omitted, scored := layers(t, mk(kind, norm, "", w))
					explicit, _ := layers(t, mk(kind, norm, types.PairwisePSourceCellValue, w))
					if scored == 0 {
						t.Fatal("vacuous: no pair scored")
					}
					if omitted != explicit {
						t.Fatalf("omitted p_source on a normalised host differs from cell_value:\n%s\n%s", omitted, explicit)
					}
					req := mk(kind, norm, types.PairwisePSourceCellValuePct, w)
					_, rerr := p.Process(ctx, req)
					ce := requireCode(t, rerr, errors.PULSE_OVERLAY_PARAM_MISSING)
					if ce.Details["param"] != "p_source" || ce.Details["normalize"] != string(norm) {
						t.Fatalf("refusal details = %v", ce.Details)
					}
					env := predictEnvelope(t, p, fs, cohort, req)
					if len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
						t.Fatalf("predict %+v, runtime %s %q", env.Errors, ce.Code, ce.Message)
					}
				})
			}
			t.Run(string(kind)+"/"+wname+"/none", func(t *testing.T) {
				omitted, _ := layers(t, mk(kind, types.CrosstabNormalizeNone, "", w))
				explicit, _ := layers(t, mk(kind, types.CrosstabNormalizeNone, types.PairwisePSourceCellValuePct, w))
				if omitted != explicit {
					t.Fatalf("normalize none: omitted p_source moved off cell_value_pct:\n%s\n%s", omitted, explicit)
				}
			})
		}
	}
}
