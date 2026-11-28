package pulse

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// hidden_fit_on_pairwise_depth_test.go pins two refusals predict now
// shares with the runtime: a fitted attribute's params.fit_on filterer
// whose type the instance hides is "unknown filter type" exactly as a
// never-registered one, as the run's filter build refuses it (#745 (1));
// and an OVERLAY_PAIRWISE_* n_within_depth the n_source would ignore is
// PULSE_OVERLAY_PARAM_MISSING on both sides, never a silent no-op (#743).

func fitOnRequest(filter string) func() *Request {
	return func() *Request {
		return &Request{Cohort: &types.Cohort{Filename: paramColCohort},
			Attributes: []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "m", Fields: []string{"x", "y"},
				Params: json.RawMessage(`{"fit_on":[` + filter + `]}`)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "m", Label: "s"}}}
	}
}

// TestHiddenFitOn_PredictMatchesRuntime: on a profile hiding the fit_on
// filterer's type, Predict refuses with the runtime's code and message,
// identically to a never-registered type on an unprofiled instance; the
// other filterer still runs on both sides. A feature's params.fit_on is
// its own expression param, not a filterer slot, so hiding
// FILTER_EXPRESSION leaves it running on both sides alike.
func TestHiddenFitOn_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()
	base := paramColPulse(t)
	expr := `{"type":"FILTER_EXPRESSION","expression":"z >= 40"}`
	rng := `{"type":"FILTER_RANGE","field":"z","values":["40","100"]}`
	cases := []struct {
		hide          string
		hidden, other string
	}{
		{"FILTER_EXPRESSION", expr, rng},
		{"FILTER_RANGE", rng, expr},
	}
	for _, tc := range cases {
		t.Run(tc.hide, func(t *testing.T) {
			p, err := New(Options{FS: base.fsys, FeatureProfile: &FeatureProfile{Features: allFeaturesBut(tc.hide)}})
			if err != nil {
				t.Fatal(err)
			}
			assertPredictRuntimeParityWith(t, p, fitOnRequest(tc.hidden), true)
			assertPredictRuntimeParityWith(t, p, fitOnRequest(tc.other), false)
			res, err := p.Predict(context.Background(), fitOnRequest(tc.hidden)())
			if err != nil {
				t.Fatal(err)
			}
			never, err := base.Predict(context.Background(), fitOnRequest(`{"type":"FILTER_NEVER_REGISTERED","field":"z"}`)())
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Errors) != 1 || len(never.Errors) != 1 || res.Errors[0].Code != never.Errors[0].Code ||
				res.Errors[0].Details["slot"] != never.Errors[0].Details["slot"] {
				t.Errorf("hidden %+v, never-registered %+v", res.Errors, never.Errors)
			}
		})
	}
	t.Run("feature_fit_on", func(t *testing.T) {
		p, err := New(Options{FS: base.fsys, FeatureProfile: &FeatureProfile{Features: allFeaturesBut("FILTER_EXPRESSION")}})
		if err != nil {
			t.Fatal(err)
		}
		assertPredictRuntimeParityWith(t, p, func() *Request {
			return &Request{Cohort: &types.Cohort{Filename: paramColCohort},
				Features: []*types.Feature{{Type: types.FEAT_TARGET_ENCODE, Field: "region", Label: "te",
					Params: json.RawMessage(`{"target":"x","fit_on":"z >= 40"}`)}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "te", Label: "s"}}}
		}, false)
	})
	t.Run("never_registered", func(t *testing.T) {
		assertPredictRuntimeParityWith(t, base, fitOnRequest(`{"type":"FILTER_NEVER_REGISTERED","field":"z"}`), true)
	})
}

// TestPairwiseWithinDepth_PredictMatchesRuntime: a non-zero
// n_within_depth under an n_source that does not read it, and a negative
// one, are refused PULSE_OVERLAY_PARAM_MISSING by Predict and Process
// with one message; the slab modes and a zero depth still run.
func TestPairwiseWithinDepth_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()
	p := opConfigPulse(t)
	build := func(params string) func() *Request {
		return func() *Request {
			return &Request{Cohort: &types.Cohort{Filename: opConfigCohort},
				Crosstab: &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "quarter"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "age"}, Normalize: "column"},
				Overlays: []types.OverlaySpec{{Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeRow,
					Params: json.RawMessage(params)}}}
		}
	}
	cases := []struct {
		params string
		refuse bool
	}{
		{`{"n_within_depth":1}`, true},
		{`{"n_source":"row_margin_n","n_within_depth":2}`, true},
		{`{"n_source":"cell_n_unweighted","n_within_depth":1}`, true},
		{`{"n_within_depth":-1}`, true},
		{`{"n_source":"n_within","n_within_depth":-1}`, true},
		{`{}`, false},
		{`{"n_within_depth":0}`, false},
		{`{"n_source":"n_within","n_within_depth":0}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.params, func(t *testing.T) {
			assertPredictRuntimeParityWith(t, p, build(tc.params), tc.refuse)
		})
	}
}
