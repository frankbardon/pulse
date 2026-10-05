package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// allPairwiseNSources is every pairwise n_source spelling (the omitted
// default first).
var allPairwiseNSources = []string{"",
	types.PairwiseNSourceCellNUnweighted, types.PairwiseNSourceCellValueWeight,
	types.PairwiseNSourceRowMarginN, types.PairwiseNSourceColumnMarginN,
	types.PairwiseNSourceRowMarginDistinct, types.PairwiseNSourceColumnMarginDistinct,
	types.PairwiseNSourceNWithin, types.PairwiseNSourceNWithinDistinct,
	types.PairwiseNSourceCellWeightSum}

// TestWeight_OverlayNSourceRefusalsMatchPredict is the weighted-host
// n_source rule (weighting.NSourceRefusal, weighting-inferential E3-S1 /
// E3-S2) over every spelling, both weight kinds, and a host weighted by
// the request or only by its own crosstab cell: a refused source is
// PROCESSING_CONFIG with one code and message at runtime and in
// predict; an admitted one runs and predicts clean. The proportion kind
// admits the omitted default everywhere and the weight-sum sources under
// frequency; a Welford kind is judged on its refusals only (predict
// refuses any n_source on it regardless of weight, a deliberate
// one-arm rule). The Compose panel's spellings are judged the same way
// against ValidateComposeWithOptions.
func TestWeight_OverlayNSourceRefusalsMatchPredict(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	weights := map[string]types.WeightSpec{
		"frequency":   {Field: "t_u8", Kind: types.WeightKindFrequency},
		"probability": {Field: "y", Kind: types.WeightKindProbability},
	}
	params := func(nSource string) json.RawMessage {
		if nSource == "" {
			return nil
		}
		return json.RawMessage(`{"n_source":"` + nSource + `"}`)
	}
	hosts := []struct {
		kind types.OverlayKind
		cell types.AggregationType
		// admits: whether the kind runs on an admitted source (false:
		// refusal arm only).
		admits bool
	}{
		{types.OverlayKindPairwisePropZ, types.AGG_COUNT, true},
		{types.OverlayKindPairwiseWelchT, types.AGG_WELFORD, false},
	}
	refusedRuns, admittedRuns := 0, 0
	for _, h := range hosts {
		for wname, w := range weights {
			for _, src := range []string{"request", "slot only"} {
				for _, mode := range allPairwiseNSources {
					req := &types.Request{
						Cohort: &types.Cohort{Filename: cohort},
						Crosstab: &types.CrosstabSpec{
							Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
							Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Interval: 20}},
							Cell:    &types.Aggregation{Type: h.cell, Field: "x", Label: "c"},
							Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
						},
						Overlays: []types.OverlaySpec{{Name: "pw", Kind: h.kind, Scope: types.OverlayScopeColumn, Params: params(mode)}},
					}
					if src == "request" {
						req.Weight = &w
					} else {
						req.Crosstab.Cell.Weight = types.SlotWeightOf(w)
					}
					where := string(h.kind) + " " + wname + " " + src + " n_source=" + mode
					_, rerr := p.Process(ctx, req)
					env := predictEnvelope(t, p, fs, cohort, req)
					if weighting.NSourceRefusal(mode, weighting.BasisOf(&w)) != "" {
						ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
						if len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
							t.Fatalf("%s: predict %+v, runtime %s %q", where, env.Errors, ce.Code, ce.Message)
						}
						refusedRuns++
						continue
					}
					if !h.admits {
						continue
					}
					if rerr != nil {
						t.Fatalf("%s: runtime refused an admitted source: %v", where, rerr)
					}
					if len(env.Errors) != 0 {
						t.Fatalf("%s: predict refused an admitted source: %+v", where, env.Errors)
					}
					admittedRuns++
				}
			}
		}
	}
	if refusedRuns == 0 || admittedRuns == 0 {
		t.Fatalf("vacuous: %d refusals, %d admitted runs", refusedRuns, admittedRuns)
	}

	// The Compose panel.
	panelParams := func(nSource string) map[string]any {
		if nSource == "" {
			return nil
		}
		return map[string]any{"n_source": nSource}
	}
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}
	panelRefused := 0
	for wname, w := range weights {
		for _, src := range []string{"request", "slot only"} {
			for _, mode := range append([]string{""}, types.PanelNSources()...) {
				slot := func(label, filter string) *types.Request {
					r := &types.Request{
						Label:  label,
						Cohort: &types.Cohort{Filename: cohort},
						Crosstab: &types.CrosstabSpec{
							Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
							Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Interval: 20}},
							Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "c"},
							Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
						},
					}
					if filter != "" {
						r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: filter}}
					}
					if src == "request" {
						r.Weight = &w
					} else {
						r.Crosstab.Cell.Weight = types.SlotWeightOf(w)
					}
					return r
				}
				req := &types.ComposedRequest{
					Requests: []*types.Request{slot("total", ""), slot("sub", "x >= 10")},
					Overlays: []types.ComposeOverlaySpec{{Name: "pp", Kind: types.OverlayKindPropZPanel, Scope: types.OverlayScopeCell,
						Reference: "total", Targets: []string{"sub"}, Params: panelParams(mode)}},
				}
				where := "panel " + wname + " " + src + " n_source=" + mode
				_, rerr := p.Compose(ctx, req)
				env := descx.ValidateComposeWithOptions(req, opts)
				if weighting.NSourceRefusal(mode, weighting.BasisOf(&w)) != "" {
					ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
					if len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
						t.Fatalf("%s: validator %+v, runtime %s %q", where, env.Errors, ce.Code, ce.Message)
					}
					panelRefused++
					continue
				}
				if rerr != nil || len(env.Errors) != 0 {
					t.Fatalf("%s: runtime %v, validator %+v", where, rerr, env.Errors)
				}
			}
		}
	}
	if panelRefused == 0 {
		t.Fatal("no panel n_source refused: the panel half is vacuous")
	}
}
