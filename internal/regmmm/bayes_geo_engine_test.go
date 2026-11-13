package regmmm_test

import (
	"context"
	stderrors "errors"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/types"
)

// bayes_geo_engine_test.go holds REG_MMM_BAYES's geo panel (params.geo)
// to its contract through the engine on a stock instance: the geo field
// is decoded by the projection and refused when unknown, at predict and
// at runtime alike; a two-market panel fits (where without params.geo it
// is duplicate_time); and Limits.MaxMMMGeos is a Possible predict
// finding on the categorical's dictionary and the runtime's refusal on
// the geos the fit reads.

// panelRows is the small cohort's east series and a second market
// (west) beside it.
func panelRows() []regmmm.BayesRow {
	rows := regmmm.BayesRows(11)
	for _, r := range regmmm.BayesRows(12) {
		r.Geo = "west"
		rows = append(rows, r)
	}
	return rows
}

// geoBayesSpec is a tiny geo-panel slot: enough draws to run the path,
// not to read the posterior.
func geoBayesSpec(geo string) *types.RegressionSpec {
	return regmmm.BayesSpec(`{"channels":["tv"],"time":"week","geo":"` + geo + `","chains":1,"warmup":10,"draws":5}`)
}

// TestMMMBayes_GeoFieldProjected: params.geo is read beyond the target,
// predictors and time — the projected decode carries it (else every row
// would be geo_missing) and the panel fits with each market's entries;
// an unknown geo field is refused by predict and the runtime with the
// same code, message and details, before any sampling.
func TestMMMBayes_GeoFieldProjected(t *testing.T) {
	// serial: swaps the package-level MMM sampler hook (bayesSampler)

	c := &countingSampler{}
	withSampler(t, c)
	ctx := context.Background()
	p := bayesInstance(t, nil)
	writeBayesCohort(t, p, "panel.pulse", encoding.FieldTypeU16, panelRows())
	r := process(t, p, bayesRequest("panel.pulse", geoBayesSpec("geo"))).Regressions[0]
	if r.Error != nil {
		t.Fatalf("panel fit = %s", mustJSON(t, r))
	}
	h, _ := r.Outputs["hierarchy"].(map[string]any)
	if h == nil || !slices.Equal(h["geos"].([]string), []string{"east", "west"}) {
		t.Fatalf("hierarchy = %s", mustJSON(t, r.Outputs["hierarchy"]))
	}
	roi := r.Outputs["roi"].(map[string]any)
	for _, k := range []string{"tv", "tv[east]", "tv[west]"} {
		if _, ok := roi[k]; !ok {
			t.Errorf("outputs.roi lacks %s: %s", k, mustJSON(t, roi))
		}
	}
	var names []string
	for name := range r.Coefficients {
		names = append(names, name)
	}
	slices.Sort(names)
	want := strings.Fields("(intercept)[east] (intercept)[west] tv tv[east] tv[west] price decay:tv decay:tv[east] decay:tv[west] k:tv s:tv sigma tau:tv tau:decay:tv")
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("coefficients %v\nwant %v", names, want)
	}

	req := bayesRequest("panel.pulse", geoBayesSpec("market"))
	runs := c.runs.Load()
	_, rerr := p.Process(ctx, req)
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) {
		t.Fatalf("runtime: %v, want a coded refusal", rerr)
	}
	pr, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(pr.Errors) == 0 || pr.Errors[0].Code != string(ce.Code) || pr.Errors[0].Message != ce.Message ||
		mustJSON(t, pr.Errors[0].Details) != mustJSON(t, ce.Details) {
		t.Fatalf("predict errors %s, runtime %s %q %s", mustJSON(t, pr.Errors), ce.Code, ce.Message, mustJSON(t, ce.Details))
	}
	if !strings.Contains(ce.Message, "market") {
		t.Errorf("refusal %q does not name the unknown geo field", ce.Message)
	}
	if n := c.runs.Load() - runs; n != 0 {
		t.Errorf("an unknown geo field sampled %d times", n)
	}
}

// TestMMMBayes_GeoLimitPredictMatchesRuntime: under Limits.MaxMMMGeos 1
// a two-market panel is predict's Possible max_mmm_geos finding (the
// categorical's dictionary bounds the geos; no error, the run may
// filter) and the runtime's PULSE_LIMIT_EXCEEDED naming the limit with
// the geos it read, before any sampling.
func TestMMMBayes_GeoLimitPredictMatchesRuntime(t *testing.T) {
	// serial: swaps the package-level MMM sampler hook (bayesSampler)

	c := &countingSampler{}
	withSampler(t, c)
	ctx := context.Background()
	p := bayesInstance(t, func(o *pulse.Options) { o.Limits.MaxMMMGeos = 1 })
	writeBayesCohort(t, p, "panel.pulse", encoding.FieldTypeU16, panelRows())
	req := bayesRequest("panel.pulse", geoBayesSpec("geo"))
	pr, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(pr.Errors) != 0 {
		t.Fatalf("predict errors %s", mustJSON(t, pr.Errors))
	}
	var found bool
	for _, f := range pr.LimitFindings {
		if f.Limit == "max_mmm_geos" {
			found = true
			if f.Grade != "possible" || f.Estimated != 2 || f.Configured != 1 {
				t.Errorf("finding %+v, want possible, estimated 2, configured 1", f)
			}
		}
	}
	if !found {
		t.Fatalf("predict findings %s lack max_mmm_geos", mustJSON(t, pr.LimitFindings))
	}
	_, rerr := p.Process(ctx, req)
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) || ce.Code != perr.PULSE_LIMIT_EXCEEDED || ce.Details["limit"] != "max_mmm_geos" ||
		ce.Details["observed"] != int64(2) || ce.Details["configured"] != int64(1) {
		t.Fatalf("runtime: %v, want PULSE_LIMIT_EXCEEDED max_mmm_geos observed 2", rerr)
	}
	if n := c.runs.Load(); n != 0 {
		t.Errorf("a refused panel sampled %d times", n)
	}
	// Without params.geo the same panel is two rows per week.
	_, rerr = p.Process(ctx, bayesRequest("panel.pulse", regmmm.BayesSpec(regmmm.FastParams)))
	if !stderrors.As(rerr, &ce) || ce.Details["reason"] != "duplicate_time" || !strings.Contains(ce.Message, "params.geo") {
		t.Errorf("a panel without params.geo: %v, want duplicate_time pointing at params.geo", rerr)
	}
}
