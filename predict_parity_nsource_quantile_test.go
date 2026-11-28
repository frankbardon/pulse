package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// predict_parity_nsource_quantile_test.go pins two refusals predict now
// shares with the runtime: an unknown n_source on a proportion
// OVERLAY_PAIRWISE_* kind is PULSE_OVERLAY_PARAM_MISSING on both sides,
// never read as the omitted default; a GROUP_QUANTILE interval that is no
// whole bucket count (negative, NaN, infinite, fractional) is
// PROCESSING_CONFIG on both sides, never silently 4 buckets.

// TestPairwiseUnknownNSource_PredictMatchesRuntime: Predict and Process
// refuse an unknown n_source with one code, message and details (the
// spec index aside, which only predict carries); a known mode runs.
func TestPairwiseUnknownNSource_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()
	p := opConfigPulse(t)
	build := func(kind types.OverlayKind, params string) func() *Request {
		return func() *Request {
			return &Request{Cohort: &types.Cohort{Filename: opConfigCohort},
				Crosstab: &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "quarter"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "age"}, Normalize: "column"},
				Overlays: []types.OverlaySpec{{Kind: kind, Scope: types.OverlayScopeRow,
					Params: json.RawMessage(params)}}}
		}
	}
	ctx := context.Background()
	for _, kind := range []types.OverlayKind{types.OverlayKindPairwisePropZ, types.OverlayKindPairwiseProbitT} {
		for _, params := range []string{`{"n_source":"cell"}`, `{"n_source":"cell","n_within_depth":1}`} {
			t.Run(string(kind)+"/"+params, func(t *testing.T) {
				res, err := p.Predict(ctx, build(kind, params)())
				if err != nil {
					t.Fatal(err)
				}
				_, rerr := p.Process(ctx, build(kind, params)())
				var ce *perr.CodedError
				if !stderrors.As(rerr, &ce) {
					t.Fatalf("runtime err = %v, want a coded refusal", rerr)
				}
				if len(res.Errors) != 1 {
					t.Fatalf("predict errors = %+v, want the runtime's one", res.Errors)
				}
				pe := res.Errors[0]
				pd := normalizeDetails(pe.Details)
				delete(pd, "index")
				if string(ce.Code) != pe.Code || ce.Code != perr.PULSE_OVERLAY_PARAM_MISSING || ce.Message != pe.Message ||
					!reflect.DeepEqual(normalizeDetails(ce.Details), pd) {
					t.Errorf("runtime %s %q %v\n predict %s %q %v", ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
				}
			})
		}
		t.Run(string(kind)+"/known", func(t *testing.T) {
			assertPredictRuntimeParityWith(t, p, build(kind, `{"n_source":"row_margin_n"}`), false)
		})
	}
}

// TestQuantileInterval_PredictMatchesRuntime: a GROUP_QUANTILE interval
// that is no whole bucket count is refused PROCESSING_CONFIG by Predict
// and Process with one message and details, in Request.Groups and a
// crosstab axis; 0 (unset) and a whole count run.
func TestQuantileInterval_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()
	p := opConfigPulse(t)
	groups := func(interval float64) func() *Request {
		return func() *Request {
			return &Request{Cohort: &types.Cohort{Filename: opConfigCohort},
				Groups:       []*types.Group{{Type: types.GROUP_QUANTILE, Field: "age", Interval: interval}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "age", Label: "v"}}}
		}
	}
	axis := func(interval float64) func() *Request {
		return func() *Request {
			return &Request{Cohort: &types.Cohort{Filename: opConfigCohort},
				Crosstab: &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_QUANTILE, Field: "age", Interval: interval}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "quarter"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "age"}}}
		}
	}
	cases := []struct {
		name   string
		refuse bool
		build  func() *Request
	}{
		{"groups/negative", true, groups(-4)},
		{"groups/nan", true, groups(math.NaN())},
		{"groups/inf", true, groups(math.Inf(1))},
		{"groups/fraction_below_one", true, groups(0.5)},
		{"groups/fraction", true, groups(2.5)},
		{"groups/too_many", true, groups(1e12)},
		{"groups/unset", false, groups(0)},
		{"groups/deciles", false, groups(10)},
		{"axis/negative", true, axis(-1)},
		{"axis/quartiles", false, axis(4)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPredictRuntimeParity(t, p, tc.build, tc.refuse)
		})
	}
}
