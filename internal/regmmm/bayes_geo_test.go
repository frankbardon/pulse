package regmmm

import (
	"context"
	stderrors "errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// bayes_geo_test.go holds the geo panel's request and row rules
// (bayes_geo.go) to their contract: the spec refusals, the reserved
// brackets and tau: prefix, the per-geo time grid (repeats, interior
// gaps, ragged ends), a null or non-code geo, the MaxMMMGeos limit, the
// panel's holdout cut and the coefficient table's names.

// geoSpec is a REG_MMM_BAYES geo-panel slot over the simulated panel.
func geoSpec(params string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: BayesName, Name: "geo", Target: "sales", Predictors: []string{"tv", "search"},
		Params: []byte(params)}
}

const geoParams = `{"channels":["tv","search"],"time":"week","geo":"geo"`

func geoRegression(t *testing.T, params string) *bayesRegression {
	t.Helper()
	reg, err := BayesFactory(geoSpec(params), nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg.(*bayesRegression)
}

func configReason(t *testing.T, err error) string {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_CONFIG {
		t.Fatalf("err %v, want PROCESSING_CONFIG", err)
	}
	r, _ := ce.Details["reason"].(string)
	return r
}

// TestMMMBayes_GeoSpecRefusals: an empty params.geo, a geo that is the
// target, the time field or a predictor, a lift test without its geo on
// a geo panel, one with a geo on one market and params.per_geo_curves
// without params.geo are refused (PROCESSING_CONFIG); a predictor
// carrying [ or ] is
// refused with or without params.geo (the per-geo name delimiters), and
// one starting tau: only on a geo panel (the scales' prefix).
func TestMMMBayes_GeoSpecRefusals(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		params, slot, reason string
		predictors           []string
	}{
		"empty geo":          {`{"channels":["tv","search"],"time":"week","geo":""}`, "params.geo", "", nil},
		"geo is target":      {`{"channels":["tv","search"],"time":"week","geo":"sales"}`, "params.geo", reasonGeoConflict, nil},
		"geo is time":        {`{"channels":["tv","search"],"time":"week","geo":"week"}`, "params.geo", reasonGeoConflict, nil},
		"geo is a predictor": {`{"channels":["tv","search"],"time":"week","geo":"search"}`, "params.geo", reasonGeoConflict, nil},
		"lift without geo on a panel": {geoParams + `,"lift_tests":[{"channel":"tv","start":5,"end":8,"spend_delta":-10,"lift":-2,"se":1}]}`,
			"params.lift_tests", reasonLiftGeoMissing, nil},
		"lift geo on one market": {`{"channels":["tv","search"],"time":"week","lift_tests":[{"channel":"tv","geo":"1","start":5,"end":8,"spend_delta":-10,"lift":-2,"se":1}]}`,
			"params.lift_tests", reasonLiftGeo, nil},
		"per_geo_curves on one market": {`{"channels":["tv","search"],"time":"week","per_geo_curves":true}`,
			"params.per_geo_curves", reasonPerGeoCurvesNoGeo, nil},
		"bracket predictor":  {geoParams + `}`, "predictors", reasonBracketReserved, []string{"tv", "search", "x[2]"}},
		"bracket one market": {`{"channels":["tv","search"],"time":"week"}`, "predictors", reasonBracketReserved, []string{"tv", "search", "x]"}},
		"tau predictor":      {geoParams + `}`, "predictors", "", []string{"tv", "search", "tau:x"}},
	} {
		s := geoSpec(tc.params)
		if tc.predictors != nil {
			s.Predictors = tc.predictors
		}
		_, err := BayesFactory(s, nil)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_CONFIG || ce.Details["slot"] != tc.slot {
			t.Errorf("%s: %v, want PROCESSING_CONFIG slot %s", name, err, tc.slot)
			continue
		}
		if r, _ := ce.Details["reason"].(string); r != tc.reason {
			t.Errorf("%s: reason %q, want %q", name, r, tc.reason)
		}
	}
	// tau: is a geo panel's prefix only: one market keeps the name.
	s := geoSpec(`{"channels":["tv","search"],"time":"week"}`)
	s.Predictors = []string{"tv", "search", "tau:x"}
	if _, err := BayesFactory(s, nil); err != nil {
		t.Errorf("a tau: predictor on one market: %v", err)
	}
	if reg := geoRegression(t, geoParams+`}`); reg.cfg.geo != "geo" || reg.cfg.perGeoCurves {
		t.Errorf("cfg.geo = %q, perGeoCurves %v", reg.cfg.geo, reg.cfg.perGeoCurves)
	}
	if reg := geoRegression(t, geoParams+`,"per_geo_curves":true}`); !reg.cfg.perGeoCurves {
		t.Error("per_geo_curves true on a panel did not set perGeoCurves")
	}
	// false is the default, accepted with or without a panel.
	if _, err := BayesFactory(geoSpec(`{"channels":["tv","search"],"time":"week","per_geo_curves":false}`), nil); err != nil {
		t.Errorf("per_geo_curves false on one market: %v", err)
	}
	// A panel lift test names its geo; whether the geo exists is the
	// data's.
	reg := geoRegression(t, geoParams+`,"lift_tests":[{"channel":"search","geo":"2","start":5,"end":8,"spend_delta":-10,"lift":-2,"se":1}]}`)
	if len(reg.cfg.lifts) != 1 || reg.cfg.lifts[0].geo != "2" || reg.cfg.lifts[0].channel != 1 {
		t.Errorf("cfg.lifts = %+v", reg.cfg.lifts)
	}
}

// TestMMMBayes_GeoGrid: each geo's rows are their own series — ragged
// starts and ends fit, a repeat within a geo is duplicate_time and an
// interior jump geo_time_gap (each naming the geo), a null geo is
// geo_missing and a fractional one geo_not_code; the geos come out in
// code order, each in time order.
func TestMMMBayes_GeoGrid(t *testing.T) {
	t.Parallel()

	reg := geoRegression(t, geoParams+`}`)
	rows := simulateGeoPanel(1).probe()
	// Ragged: geo 1 starts at week 5, geo 3 ends at week 90; reversed so
	// the read must sort.
	var ragged probeRows
	for _, r := range rows {
		if (r["geo"] == 1 && r["week"] < 5) || (r["geo"] == 3 && r["week"] > 90) {
			continue
		}
		ragged = append(ragged, r)
	}
	slices.Reverse(ragged)
	gd, err := reg.readGeo(context.Background(), ragged)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(gd.names, []string{"1", "2", "3"}) {
		t.Fatalf("geos %v", gd.names)
	}
	if d := gd.series[0]; d.time[0] != 5 || d.time[len(d.time)-1] != geoSimWeeks {
		t.Errorf("geo 1 spans %d..%d", d.time[0], d.time[len(d.time)-1])
	}
	if d := gd.series[2]; d.time[0] != 1 || d.time[len(d.time)-1] != 90 {
		t.Errorf("geo 3 spans %d..%d", d.time[0], d.time[len(d.time)-1])
	}
	for name, tc := range map[string]struct {
		mut    func(probeRows) probeRows
		reason string
	}{
		"repeat in a geo": {func(r probeRows) probeRows {
			r[geoSimWeeks+3]["week"] = r[geoSimWeeks+2]["week"]
			return r
		}, "duplicate_time"},
		"interior gap": {func(r probeRows) probeRows { return slices.Delete(r, geoSimWeeks+10, geoSimWeeks+11) }, reasonGeoTimeGap},
		"null geo":     {func(r probeRows) probeRows { delete(r[7], "geo"); return r }, reasonGeoMissing},
		"fractional":   {func(r probeRows) probeRows { r[7]["geo"] = 1.5; return r }, reasonGeoInvalid},
	} {
		in := make(probeRows, len(rows))
		for i, r := range rows {
			cp := probeRow{}
			for k, v := range r {
				cp[k] = v
			}
			in[i] = cp
		}
		_, err := reg.readGeo(context.Background(), tc.mut(in))
		if got := configReason(t, err); got != tc.reason {
			t.Errorf("%s: reason %q, want %q (%v)", name, got, tc.reason, err)
		}
		var ce *perr.CodedError
		if stderrors.As(err, &ce) && (tc.reason == "duplicate_time" || tc.reason == reasonGeoTimeGap) && ce.Details["geo"] != "2" {
			t.Errorf("%s: details %v, want geo 2", name, ce.Details)
		}
	}
}

// TestMMMBayes_GeoLimit: more geos than Limits.MaxMMMGeos (from the
// run's ctx; the default 50 when ctx carries none) is
// PULSE_LIMIT_EXCEEDED naming max_mmm_geos, before any series check;
// at the limit, and Unlimited, it reads.
func TestMMMBayes_GeoLimit(t *testing.T) {
	t.Parallel()

	reg := geoRegression(t, geoParams+`}`)
	rows := simulateGeoPanel(1).probe()
	l := limits.Defaults()
	l.MaxMMMGeos = 2
	_, err := reg.readGeo(limits.WithContext(context.Background(), l), rows)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_LIMIT_EXCEEDED || ce.Details["limit"] != string(limits.MaxMMMGeos) ||
		ce.Details["observed"] != int64(3) || ce.Details["configured"] != int64(2) {
		t.Fatalf("over the limit: %v", err)
	}
	if !strings.Contains(ce.Message, "max_mmm_geos") {
		t.Errorf("message %q does not name the limit", ce.Message)
	}
	l.MaxMMMGeos = 3
	if _, err := reg.readGeo(limits.WithContext(context.Background(), l), rows); err != nil {
		t.Errorf("at the limit: %v", err)
	}
	l.MaxMMMGeos = limits.Unlimited
	if _, err := reg.readGeo(limits.WithContext(context.Background(), l), rows); err != nil {
		t.Errorf("unlimited: %v", err)
	}
	// No limits on ctx: the default, 50.
	many := make(probeRows, 0, 51)
	for g := range 51 {
		many = append(many, probeRow{"geo": float64(g), "week": 1, "tv": 1, "search": 1, "sales": 1})
	}
	if _, err := reg.readGeo(context.Background(), many); !perr.HasCode(err, perr.PULSE_LIMIT_EXCEEDED) {
		t.Errorf("51 geos under the default: %v", err)
	}
	if _, err := reg.readGeo(context.Background(), many[:50]); err != nil {
		t.Errorf("50 geos under the default: %v", err)
	}
}

// TestMMMBayes_GeoHoldoutCut: params.holdout_from cuts each geo at the
// same period (a geo ending before it keeps every row); a cutoff that
// leaves a geo no fitted row, or the panel no held-out row, is
// holdout_out_of_range.
func TestMMMBayes_GeoHoldoutCut(t *testing.T) {
	t.Parallel()

	rows := simulateGeoPanel(1).probe()
	var ragged probeRows
	for _, r := range rows {
		if (r["geo"] == 1 && r["week"] < 5) || (r["geo"] == 3 && r["week"] > 90) {
			continue
		}
		ragged = append(ragged, r)
	}
	for _, tc := range []struct {
		from   int
		want   []int
		reason string
	}{
		{95, []int{90, 94, 90}, ""},
		{5, nil, "holdout_out_of_range"}, // geo 1 starts at week 5: no fitted row
		{105, nil, "holdout_out_of_range"},
	} {
		reg := geoRegression(t, geoParams+`,"holdout_from":`+strconv.Itoa(tc.from)+`}`)
		gd, err := reg.readGeo(context.Background(), ragged)
		if err != nil {
			t.Fatal(err)
		}
		nf, err := reg.geoFitRows(gd)
		if tc.reason != "" {
			if got := configReason(t, err); got != tc.reason {
				t.Errorf("from %d: reason %q", tc.from, got)
			}
			continue
		}
		if err != nil || !slices.Equal(nf, tc.want) {
			t.Errorf("from %d: fitted %v (%v), want %v", tc.from, nf, err, tc.want)
		}
	}
}

// TestMMMBayes_GeoCoefNames: the panel's table names each geo's
// intercept, per channel the population β then each geo's, the
// controls, per kernel parameter the population then each geo's value,
// the shared curve, sigma, then the scales — under a two-parameter
// kernel too.
func TestMMMBayes_GeoCoefNames(t *testing.T) {
	t.Parallel()

	reg := geoRegression(t, geoParams+`}`)
	reg.cfg.channels, reg.cfg.controls = []string{"tv"}, []string{"price"}
	got := strings.Join(reg.geoCoefNames([]string{"NE", "SW"}), " ")
	want := "(intercept)[NE] (intercept)[SW] tv tv[NE] tv[SW] price decay:tv decay:tv[NE] decay:tv[SW] k:tv s:tv sigma tau:tv tau:decay:tv"
	if got != want {
		t.Errorf("names\n%s\nwant\n%s", got, want)
	}
	reg = geoRegression(t, geoParams+`,"adstock":"delayed","max_lag":6,"saturation":"logistic"}`)
	reg.cfg.channels, reg.cfg.controls = []string{"tv"}, nil
	got = strings.Join(reg.geoCoefNames([]string{"1"}), " ")
	want = "(intercept)[1] tv tv[1] decay:tv decay:tv[1] peak:tv peak:tv[1] lam:tv sigma tau:tv tau:decay:tv tau:peak:tv"
	if got != want {
		t.Errorf("delayed logistic names\n%s\nwant\n%s", got, want)
	}
	if got := strings.Join(reg.geoEffectNames([]string{"1", "2"}), " "); got != "tv tv[1] tv[2]" {
		t.Errorf("effect names %s", got)
	}
}

// TestMMMBayes_GeoHoldoutScores: a geo panel with params.holdout_from
// fits each geo's rows before the cutoff and scores every geo's rows from
// it (outputs.holdout counts the panel's rows), beside outputs.hierarchy.
func TestMMMBayes_GeoHoldoutScores(t *testing.T) {
	t.Parallel()

	reg := geoRegression(t, geoParams+`,"holdout_from":36,"chains":1,"warmup":10,"draws":5}`)
	var rows probeRows
	for _, r := range simulateGeoPanel(1).probe() {
		if r["week"] <= 40 {
			rows = append(rows, r)
		}
	}
	out, err := reg.FitContext(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	h, ok := out.Outputs["holdout"].(map[string]any)
	if !ok {
		t.Fatalf("outputs.holdout missing: %v", out.Outputs)
	}
	if h["fit_rows"] != 3*35 || h["holdout_rows"] != 3*5 || h["from"] != int64(36) {
		t.Errorf("holdout %v, want 105 fitted and 15 held-out rows from 36", h)
	}
	if rmse, _ := h["rmse"].(float64); !(rmse > 0) {
		t.Errorf("holdout rmse %v", h["rmse"])
	}
	hier := out.Outputs["hierarchy"].(map[string]any)
	pooled := hier["pooled"].(map[string]any)
	for _, k := range []string{"tv", "search", "decay:tv", "decay:search"} {
		e, ok := pooled[k].(map[string]any)
		if !ok {
			t.Fatalf("hierarchy.pooled lacks %s: %v", k, pooled)
		}
		want := "roi"
		if strings.HasPrefix(k, "decay:") {
			want = "decay"
		}
		if e["parameter"] != want || e["mu"] == nil || e["tau"] == nil {
			t.Errorf("hierarchy.pooled[%s] = %v", k, e)
		}
	}
	if !slices.Equal(hier["geos"].([]string), []string{"1", "2", "3"}) {
		t.Errorf("hierarchy.geos %v", hier["geos"])
	}
}

// geoShortRows is the simulated panel's first 40 weeks per geo.
func geoShortRows() probeRows {
	var rows probeRows
	for _, r := range simulateGeoPanel(1).probe() {
		if r["week"] <= 40 {
			rows = append(rows, r)
		}
	}
	return rows
}

// TestMMMBayes_GeoPerCurvesNames: with params.per_geo_curves the table
// names each curve parameter's population value then each geo's
// ("k:tv[NE]") and, after the kernel's scales, the curve's ("tau:k:tv").
func TestMMMBayes_GeoPerCurvesNames(t *testing.T) {
	t.Parallel()

	reg := geoRegression(t, geoParams+`,"per_geo_curves":true}`)
	reg.cfg.channels, reg.cfg.controls = []string{"tv"}, nil
	got := strings.Join(reg.geoCoefNames([]string{"NE", "SW"}), " ")
	want := "(intercept)[NE] (intercept)[SW] tv tv[NE] tv[SW] decay:tv decay:tv[NE] decay:tv[SW] k:tv k:tv[NE] k:tv[SW] s:tv s:tv[NE] s:tv[SW] sigma tau:tv tau:decay:tv tau:k:tv tau:s:tv"
	if got != want {
		t.Errorf("names\n%s\nwant\n%s", got, want)
	}
}

// TestMMMBayes_GeoPerCurvesFit: a per-geo-curve panel fits: each geo's
// curve terms are coefficients, outputs.hierarchy pools each curve
// parameter (its μ the population term's posterior, in original units),
// and the coefficient table is the draw's row, name for name.
func TestMMMBayes_GeoPerCurvesFit(t *testing.T) {
	t.Parallel()

	reg := geoRegression(t, geoParams+`,"per_geo_curves":true,"chains":1,"warmup":10,"draws":5}`)
	out, err := reg.FitContext(context.Background(), geoShortRows())
	if err != nil {
		t.Fatal(err)
	}
	est := map[string]float64{}
	for _, c := range out.Coefficients {
		est[c.Name] = c.Estimate
	}
	for _, name := range []string{"k:tv[1]", "s:search[3]", "tau:k:tv", "tau:s:search"} {
		if _, ok := est[name]; !ok {
			t.Errorf("coefficients lack %s", name)
		}
	}
	pooled := out.Outputs["hierarchy"].(map[string]any)["pooled"].(map[string]any)
	for _, term := range []string{"k:tv", "s:tv", "k:search", "s:search"} {
		e, ok := pooled[term].(map[string]any)
		if !ok {
			t.Fatalf("hierarchy.pooled lacks %s: %v", term, pooled)
		}
		if want := strings.SplitN(term, ":", 2)[0]; e["parameter"] != want {
			t.Errorf("hierarchy.pooled[%s].parameter %v, want %s", term, e["parameter"], want)
		}
		mu := e["mu"].(map[string]float64)["mean"]
		if math.Abs(mu-est[term]) > 1e-9*math.Abs(est[term]) {
			t.Errorf("hierarchy.pooled[%s].mu.mean %v, the coefficient %v", term, mu, est[term])
		}
	}
}

// TestMMMBayes_GeoLiftResolve: a panel lift test naming no geo of the
// panel is lift_geo_unknown (details.geos the panel's), a window outside
// its geo's fitted periods lift_window_out_of_range (details.geo) — both
// refused by the fit before any sampling —, and a valid one is resolved
// onto its geo's rows (its index, its window rows from the geo's first
// period, its changed spend) and enters the panel's density.
func TestMMMBayes_GeoLiftResolve(t *testing.T) {
	t.Parallel()

	rows := geoShortRows()
	reg := func(lifts string) *bayesRegression {
		return geoRegression(t, geoParams+`,"chains":1,"warmup":10,"draws":5`+lifts+`}`)
	}
	_, err := reg(`,"lift_tests":[{"channel":"tv","geo":"9","start":5,"end":8,"spend_delta":-10,"lift":-2,"se":1}]`).FitContext(context.Background(), rows)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Details["reason"] != reasonLiftGeoUnknown || !slices.Equal(ce.Details["geos"].([]string), []string{"1", "2", "3"}) {
		t.Fatalf("unknown geo: %v", err)
	}
	_, err = reg(`,"lift_tests":[{"channel":"tv","geo":"2","start":35,"end":41,"spend_delta":-10,"lift":-2,"se":1}]`).FitContext(context.Background(), rows)
	if !stderrors.As(err, &ce) || ce.Details["reason"] != reasonLiftWindowRange || ce.Details["geo"] != "2" {
		t.Fatalf("window past the geo's rows: %v", err)
	}
	r := reg(`,"lift_tests":[{"channel":"search","geo":"2","start":10,"end":20,"spend_delta":-40,"lift":-90,"se":10}]`)
	gd, err := r.readGeo(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	nf, _ := r.geoFitRows(gd)
	in := geoPanel{geos: gd.names, channels: r.cfg.channels}
	for _, d := range gd.series {
		in.y, in.spend, in.controls = append(in.y, d.y), append(in.spend, d.spend), append(in.controls, d.controls)
	}
	pr := r.cfg.priors
	pr.Kernel, pr.Curve = r.cfg.kernel, r.cfg.curve
	m, err := newBayesGeoModel(in, r.cfg.maxLag, pr)
	if err != nil {
		t.Fatal(err)
	}
	bare := m.LogDensityGrad(m.defaultInit(), make([]float64, m.Dim()))
	if err := r.addGeoLiftTests(m, gd, nf); err != nil {
		t.Fatal(err)
	}
	if len(m.lifts) != 1 {
		t.Fatalf("lifts %+v", m.lifts)
	}
	l := m.lifts[0]
	if l.geo != 1 || l.channel != 1 || l.from != 9 || l.to != 19 {
		t.Errorf("lift resolved to geo %d channel %d rows [%d, %d], want geo 1 channel 1 rows [9, 19]", l.geo, l.channel, l.from, l.to)
	}
	moved := 0.0
	for t := l.from; t <= l.to; t++ {
		moved += l.spend[t] - m.geos[1].spend[1][t]
	}
	if math.Abs(moved+40) > 1e-9 {
		t.Errorf("the window's spend moved by %v, want -40", moved)
	}
	if lifted := m.LogDensityGrad(m.defaultInit(), make([]float64, m.Dim())); lifted == bare {
		t.Error("the lift test left the panel's density unchanged")
	}
}

// TestMMMBayes_GeoDefaultTargetAccept: a geo panel without
// params.target_accept samples at defaultBayesGeoTargetAccept (0.95),
// with or without per-geo curves; an explicit target_accept wins on a
// panel, and one market keeps the sampler's default (0.8).
func TestMMMBayes_GeoDefaultTargetAccept(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		params string
		want   float64
	}{
		{geoParams + `}`, 0.95},
		{geoParams + `,"per_geo_curves":true}`, 0.95},
		{geoParams + `,"target_accept":0.8}`, 0.8},
		{geoParams + `,"target_accept":0.99}`, 0.99},
		{`{"channels":["tv","search"],"time":"week"}`, 0.8},
	} {
		if got := geoRegression(t, c.params).cfg.targetAccept; got != c.want {
			t.Errorf("%s: target_accept %v, want %v", c.params, got, c.want)
		}
	}
	if defaultBayesGeoTargetAccept != 0.95 || defaultBayesTargetAccept != 0.8 {
		t.Errorf("defaults %v (geo) and %v, want 0.95 and 0.8", defaultBayesGeoTargetAccept, defaultBayesTargetAccept)
	}
}
