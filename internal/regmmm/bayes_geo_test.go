package regmmm

import (
	"context"
	stderrors "errors"
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
// target, the time field or a predictor, and a lift test on a geo panel
// are refused (PROCESSING_CONFIG); a predictor carrying [ or ] is
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
		"lift on a panel": {geoParams + `,"lift_tests":[{"channel":"tv","start":5,"end":8,"spend_delta":-10,"lift":-2,"se":1}]}`,
			"params.lift_tests", reasonGeoLift, nil},
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
	if reg := geoRegression(t, geoParams+`}`); reg.cfg.geo != "geo" {
		t.Errorf("cfg.geo = %q", reg.cfg.geo)
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
