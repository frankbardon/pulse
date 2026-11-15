package regmmm_test

import (
	"context"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/types"
)

// bayes_geo_compose_test.go holds a geo panel's fit to its Compose
// reads: coef() reads a per-geo term (tv[east]) and a pooling scale
// (tau:tv) off a `fits` entry, and response(fit, channel, spend, geo)
// is that geo's own steady-state response — β, decay — while without a
// geo it is the population's; a geo on a fit that is no panel is refused
// by predict and the runtime alike.

// geoComposeSlot reads the "geo" fit's terms and responses per week.
func geoComposeSlot(exprs map[string]string) *types.Request {
	req := &types.Request{Label: "read", Cohort: &types.Cohort{Filename: "panel.pulse"},
		Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "week", Interval: 1}}}
	for label, src := range exprs {
		req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_FORMULA, Label: label, Expression: src})
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MAX, Field: label, Label: label})
	}
	return req
}

// TestMMMBayes_GeoComposeReads: see the file comment.
func TestMMMBayes_GeoComposeReads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := bayesInstance(t, nil)
	writeBayesCohort(t, p, "panel.pulse", encoding.FieldTypeU16, panelRows())
	one := regmmm.BayesSpec(`{"channels":["tv"],"time":"week","chains":1,"warmup":10,"draws":5}`)
	fits := []types.FitSpec{
		{Name: "geo", Request: bayesRequest("panel.pulse", geoBayesSpec("geo"))},
		{Name: "one", Request: bayesRequest(regmmm.BayesCohort, one)},
	}
	req := &pulse.ComposedRequest{Fits: fits, Requests: []*types.Request{geoComposeSlot(map[string]string{
		"b_east": `coef("geo", "tv[east]")`,
		"tau":    `coef("geo", "tau:tv")`,
		"r_east": `response("geo", "tv", 100, "east")`,
		"r_pop":  `response("geo", "tv", 100)`,
	})}}
	if env, err := p.PredictCompose(ctx, req); err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict compose: %v %+v", err, env)
	}
	out, err := p.Compose(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	c := out.Fits["geo"].RegressionResult.Coefficients
	m := func(d float64) float64 { return (1 - math.Pow(d, regmmm.DefaultMaxLag+1)) / (1 - d) }
	hill := func(x, k, s float64) float64 { return math.Pow(x, s) / (math.Pow(x, s) + math.Pow(k, s)) }
	wantEast := float64(c["tv[east]"] * hill(float64(m(c["decay:tv[east]"])*100), c["k:tv"], c["s:tv"]))
	wantPop := float64(c["tv"] * hill(float64(m(c["decay:tv"])*100), c["k:tv"], c["s:tv"]))
	if math.Abs(wantEast-wantPop) < float64(1e-9*math.Abs(wantPop)) {
		t.Fatalf("east %v and population %v responses coincide: the fixture no longer discriminates", wantEast, wantPop)
	}
	rows := out.Responses[0].Data
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, row := range rows {
		if row["b_east"] != c["tv[east]"] || row["tau"] != c["tau:tv"] {
			t.Fatalf("week %v: coef() read tv[east] %v tau:tv %v, fit %v %v", row["week"], row["b_east"], row["tau"], c["tv[east]"], c["tau:tv"])
		}
		for k, want := range map[string]float64{"r_east": wantEast, "r_pop": wantPop} {
			if got, _ := row[k].(float64); !relClose(got, want, 1e-12) {
				t.Fatalf("week %v: %s %v, want %v", row["week"], k, row[k], want)
			}
		}
	}

	// A geo on a fit that is no panel: the same refusal from predict
	// (the fit request shows it) and the runtime.
	bad := &pulse.ComposedRequest{Fits: fits, Requests: []*types.Request{geoComposeSlot(map[string]string{
		"r": `response("one", "tv", 100, "east")`,
	})}}
	_, rerr := p.Compose(ctx, bad)
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) || ce.Code != perr.PULSE_MODEL_TERM_UNKNOWN || ce.Details["reason"] != "geo_unknown" {
		t.Fatalf("runtime: %v, want PULSE_MODEL_TERM_UNKNOWN geo_unknown", rerr)
	}
	env, err := p.PredictCompose(ctx, bad)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message ||
		mustJSON(t, env.Errors[0].Details) != mustJSON(t, ce.Details) {
		t.Fatalf("predict errors %s, runtime %s %q %s", mustJSON(t, env.Errors), ce.Code, ce.Message, mustJSON(t, ce.Details))
	}
	// A geo the panel does not hold is the data's: the runtime alone.
	nowhere := &pulse.ComposedRequest{Fits: fits, Requests: []*types.Request{geoComposeSlot(map[string]string{
		"r": `response("geo", "tv", 100, "north")`,
	})}}
	if env, err := p.PredictCompose(ctx, nowhere); err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict on a data-only geo: %v %+v", err, env)
	}
	if _, rerr := p.Compose(ctx, nowhere); !stderrors.As(rerr, &ce) || ce.Details["reason"] != "geo_unknown" {
		t.Fatalf("runtime on an unknown geo: %v", rerr)
	}
}
