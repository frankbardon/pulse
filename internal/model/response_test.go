package model

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// response_test.go pins response(fit, channel, spend) (response.go): the
// steady-state multiplier per kernel against its closed form or an
// independent sum, the response against the hand-written
// beta·hill(m·spend, k, s), CheckResponse's refusals, and the static
// channel catalog (StaticAdstockChannels) against the lineage the run
// records.

// near reports |got − want| ≤ tol·max(1, |want|).
func near(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol*math.Max(1, math.Abs(want))
}

// handHill is the Hill curve written out with the standard library.
func handHill(x, k, s float64) float64 { return math.Pow(x, s) / (math.Pow(x, s) + math.Pow(k, s)) }

// TestAdstock_SteadyState: geometric unnormalised is (1 − d^(L+1)) /
// (1 − d); every normalised kernel sums to 1; the Weibull kernels match
// their densities summed independently over lags 0..L.
func TestAdstock_SteadyState(t *testing.T) {
	t.Parallel()

	const d, L = 0.6, 8
	weibullCDF, weibullPDF := 0.0, 0.0
	const k, lambda = 1.5, 3.0
	for l := 0; l <= L; l++ {
		weibullCDF += math.Exp(-math.Pow(float64(l)/lambda, k))
		z := float64(l+1) / lambda
		weibullPDF += k / lambda * math.Pow(z, k-1) * math.Exp(-math.Pow(z, k))
	}
	cases := []struct {
		name string
		a    Adstock
		want float64
	}{
		{"geometric", Adstock{Kernel: "geometric", Decay: f64(d), MaxLag: L}, (1 - math.Pow(d, L+1)) / (1 - d)},
		{"geometric normalised", Adstock{Kernel: "geometric", Decay: f64(d), MaxLag: L, Normalize: true}, 1},
		{"weibull_cdf", Adstock{Kernel: "weibull_cdf", Shape: f64(k), Scale: f64(lambda), MaxLag: L}, weibullCDF},
		{"weibull_pdf", Adstock{Kernel: "weibull_pdf", Shape: f64(k), Scale: f64(lambda), MaxLag: L}, weibullPDF},
		{"weibull_pdf normalised", Adstock{Kernel: "weibull_pdf", Shape: f64(k), Scale: f64(lambda), MaxLag: L, Normalize: true}, 1},
	}
	for _, tc := range cases {
		got, err := tc.a.SteadyState()
		if err != nil || !near(got, tc.want, 1e-12) {
			t.Errorf("%s: m = %v (%v), want %v", tc.name, got, err, tc.want)
		}
	}
	if _, err := (Adstock{Kernel: "geometric", Decay: f64(1.5), MaxLag: 3}).SteadyState(); err == nil {
		t.Fatal("decay 1.5 gave a multiplier")
	}
}

// responseSet binds the Hill fit "mmm" (hillFit) with tv_ad's lineage
// from a geometric window (decay 0.6, max_lag 8, unnormalised) and
// radio_ad's from a normalised Weibull one, plus the OLS "ols".
func responseSet(t *testing.T) *Set {
	t.Helper()
	spec, res := hillFit()
	req := &types.Request{Windows: []*types.Window{
		distLag("tv", "tv_ad", `{"kernel": "geometric", "decay": 0.6, "max_lag": 8, "normalize": false}`),
		distLag("radio", "radio_ad", `{"kernel": "weibull_pdf", "shape": 1.5, "scale": 3, "max_lag": 6}`),
	}}
	b, err := Bind("mmm", spec, res)
	if err != nil {
		t.Fatal(err)
	}
	b.Adstocks = AdstockLineage(req, spec, res)
	s := &Set{}
	if err := s.Add(b); err != nil {
		t.Fatal(err)
	}
	ols, err := Bind("ols", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"tv_ad"}},
		&types.RegressionResult{Type: types.REG_OLS, Coefficients: map[string]float64{InterceptKey: 1, "tv_ad": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(ols); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestResponse_EqualsHandFormula: response() is the library's
// hand-written beta·hill(m·spend, k, s) — geometric m in closed form, a
// normalised kernel's m = 1 — at every spend, 0 at spend 0 and NaN
// (null) for a negative or NaN spend.
func TestResponse_EqualsHandFormula(t *testing.T) {
	t.Parallel()

	s := responseSet(t)
	mTV := (1 - math.Pow(0.6, 9)) / (1 - 0.6)
	for _, spend := range []float64{0.5, 10, 37.25, 400} {
		got, cerr := Response(s, "mmm", "tv_ad", spend)
		if want := 3.5 * handHill(mTV*spend, 40, 1.5); cerr != nil || !near(got, want, 1e-12) {
			t.Errorf("tv_ad at %v: %v (%v), want %v", spend, got, cerr, want)
		}
		got, cerr = Response(s, "mmm", "radio_ad", spend)
		if want := 1.25 * handHill(spend, 12, 2); cerr != nil || !near(got, want, 1e-12) {
			t.Errorf("radio_ad at %v: %v (%v), want %v", spend, got, cerr, want)
		}
	}
	if got, cerr := Response(s, "mmm", "tv_ad", 0); cerr != nil || got != 0 {
		t.Errorf("spend 0: %v %v", got, cerr)
	}
	for _, spend := range []float64{-1, math.NaN()} {
		if got, cerr := Response(s, "mmm", "tv_ad", spend); cerr != nil || !math.IsNaN(got) {
			t.Errorf("spend %v: %v %v, want NaN", spend, got, cerr)
		}
	}
}

// TestResponse_BayesPosteriorMean: a REG_MMM_BAYES fit's response reads
// its posterior-mean coefficients — the decay:<ch> mean through the
// lineage's geometric record over params.max_lag, the β, k and s means
// through coef().
func TestResponse_BayesPosteriorMean(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.REG_MMM_BAYES, Target: "sales", Predictors: []string{"tv"},
		Params: json.RawMessage(`{"channels": ["tv"], "max_lag": 5}`)}
	res := &types.RegressionResult{Type: types.REG_MMM_BAYES, Coefficients: map[string]float64{InterceptKey: 4,
		"tv": 2.5, "decay:tv": 0.45, "k:tv": 30, "s:tv": 1.2}}
	b, err := Bind("bayes", spec, res)
	if err != nil {
		t.Fatal(err)
	}
	b.Adstocks = AdstockLineage(nil, spec, res)
	s := &Set{}
	if err := s.Add(b); err != nil {
		t.Fatal(err)
	}
	m := (1 - math.Pow(0.45, 6)) / (1 - 0.45)
	got, cerr := Response(s, "bayes", "tv", 20)
	if want := 2.5 * handHill(m*20, 30, 1.2); cerr != nil || !near(got, want, 1e-12) {
		t.Fatalf("bayes response %v (%v), want %v", got, cerr, want)
	}
}

// TestCheckResponse: an unknown fit, a vector fit, and a channel with no
// record (a control, a regression with no lineage) are refused with
// details.channel and the channels that have one; a catalog that does
// not know the records (AdstocksKnown false) passes any channel.
func TestCheckResponse(t *testing.T) {
	t.Parallel()

	s := responseSet(t)
	if err := s.AddVector(&VectorFit{Name: "vec", Labels: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if cerr := CheckResponse(s, "mmm", "tv_ad"); cerr != nil {
		t.Fatalf("tv_ad: %v", cerr)
	}
	cases := []struct {
		fit, channel string
		code         errors.Code
		channels     []string
	}{
		{"nope", "tv_ad", errors.PULSE_MODEL_UNKNOWN, nil},
		{"vec", "a", errors.PULSE_MODEL_KIND_MISMATCH, nil},
		{"mmm", "price", errors.PULSE_MODEL_TERM_UNKNOWN, []string{"radio_ad", "tv_ad"}},
		{"ols", "tv_ad", errors.PULSE_MODEL_TERM_UNKNOWN, []string{}},
	}
	for _, tc := range cases {
		cerr := CheckResponse(s, tc.fit, tc.channel)
		if cerr == nil || cerr.Code != tc.code {
			t.Fatalf("%s/%s: %v, want %s", tc.fit, tc.channel, cerr, tc.code)
		}
		if tc.channels != nil && (cerr.Details["reason"] != ReasonAdstockUnknown || cerr.Details["channel"] != tc.channel ||
			!reflect.DeepEqual(cerr.Details["channels"], tc.channels)) {
			t.Fatalf("%s/%s: details %v", tc.fit, tc.channel, cerr.Details)
		}
		if _, rerr := Response(s, tc.fit, tc.channel, 1); rerr == nil || rerr.Code != tc.code {
			t.Fatalf("Response %s/%s: %v, want %s", tc.fit, tc.channel, rerr, tc.code)
		}
	}
	unknown := FitCatalog{"sweep": {Name: "sweep", Type: types.REG_MMM_HILL}}
	if cerr := CheckResponse(unknown, "sweep", "anything"); cerr != nil {
		t.Fatalf("unknown records refused: %v", cerr)
	}
}

// TestResponse_FailedFit: a failed fit's response() is the fit's own
// refusal once the names pass.
func TestResponse_FailedFit(t *testing.T) {
	t.Parallel()

	s := &Set{}
	se := &types.SlotError{Code: string(errors.PULSE_FIT_UNAVAILABLE), Message: "no rows"}
	if err := s.AddFailed(Signature{Name: "dead", Type: types.REG_MMM_HILL, Kind: types.FitKindRegression}, se); err != nil {
		t.Fatal(err)
	}
	if _, cerr := Response(s, "dead", "tv", 1); cerr == nil || cerr.Code != errors.PULSE_FIT_UNAVAILABLE {
		t.Fatalf("failed fit: %v", cerr)
	}
}

// TestStaticAdstockChannels_MatchesLineage: for every request shape the
// lineage tests cover, the static channel set equals the channels the
// fit's lineage records; a window whose params are still expressions is
// judged whole statically, and records once resolved.
func TestStaticAdstockChannels_MatchesLineage(t *testing.T) {
	t.Parallel()

	spec, res := hillFit()
	geo := `{"kernel": "geometric", "decay": 0.5, "max_lag": 4}`
	reqs := map[string]*types.Request{
		"both": {Windows: []*types.Window{distLag("tv", "tv_ad", geo),
			distLag("radio", "radio_ad", `{"kernel": "weibull_cdf", "shape": 2, "scale": 3, "max_lag": 5}`)}},
		"one":       {Windows: []*types.Window{distLag("tv", "tv_ad", geo)}},
		"none":      {},
		"two":       {Windows: []*types.Window{distLag("tv", "tv_ad", geo), distLag("tv2", "tv_ad", geo)}},
		"feature":   {Windows: []*types.Window{distLag("tv", "tv_ad", geo)}, Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "x", Label: "tv_ad"}}},
		"prefixed":  {Windows: []*types.Window{distLag("tv", "tv_ad", geo)}, Features: []*types.Feature{{Type: types.FEAT_ONE_HOT, Field: "tv"}}},
		"attribute": {Windows: []*types.Window{distLag("tv", "tv_ad", geo)}, Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "tv_ad", Expression: "1"}}},
		"no kernel": {Windows: []*types.Window{distLag("tv", "tv_ad", `{"decay": 0.5, "max_lag": 4}`)}},
	}
	for name, req := range reqs {
		lineage := AdstockLineage(req, spec, res)
		want := []string{}
		for ch := range lineage {
			want = append(want, ch)
		}
		slices.Sort(want)
		if got := StaticAdstockChannels(req, spec); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: static %v, lineage %v", name, got, want)
		}
	}

	expr := &types.Request{Windows: []*types.Window{distLag("tv", "tv_ad", `{"kernel": "geometric", "decay": "params.d", "max_lag": 4}`)}}
	if got := StaticAdstockChannels(expr, spec); !reflect.DeepEqual(got, []string{"tv_ad"}) {
		t.Fatalf("expression decay: static %v, want [tv_ad]", got)
	}
	expr.Windows[0].Params = json.RawMessage(`{"kernel": "geometric", "decay": 0.5, "max_lag": 4}`)
	if _, ok := AdstockLineage(expr, spec, res)["tv_ad"]; !ok {
		t.Fatal("resolved decay records nothing")
	}

	bayes := &types.RegressionSpec{Type: types.REG_MMM_BAYES, Predictors: []string{"tv", "price"}, Params: json.RawMessage(`{"channels": ["tv", "radio"]}`)}
	if got := StaticAdstockChannels(nil, bayes); !reflect.DeepEqual(got, []string{"tv"}) {
		t.Fatalf("bayes: %v", got)
	}
	if got := StaticAdstockChannels(nil, &types.RegressionSpec{Type: types.REG_OLS, Predictors: []string{"tv"}}); got == nil || len(got) != 0 {
		t.Fatalf("ols: %v", got)
	}
}

// TestFeatureMayWrite: a multi-output feature's prefixed output, a
// single-output feature's default name and an unlabelled attribute's
// default label each block the record; an unrelated slot does not.
func TestFeatureMayWrite(t *testing.T) {
	t.Parallel()

	spec, res := hillFit()
	blocks := map[string]func(r *types.Request){
		"date features under label": func(r *types.Request) {
			r.Features = []*types.Feature{{Type: types.FEAT_DATE_FEATURES, Field: "d", Label: "tv"}}
		},
		"one-hot under field": func(r *types.Request) {
			r.Features = []*types.Feature{{Type: types.FEAT_ONE_HOT, Field: "tv"}}
		},
		"poly default prefix": func(r *types.Request) {
			r.Features = []*types.Feature{{Type: types.FEAT_POLY, Field: "tv_ad"[:2]}}
			r.Windows[0].Label, r.Windows[0].Field = "tv_poly_2", "spend"
		},
		"log default name": func(r *types.Request) {
			r.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "ad"}}
			r.Windows[0].Label, r.Windows[0].Field = "LOG_ad", "spend"
		},
		"extension feature prefix": func(r *types.Request) {
			r.Features = []*types.Feature{{Type: "FEAT_ACME_SPLIT", Field: "x", Label: "tv"}}
		},
		"unlabelled attribute": func(r *types.Request) {
			r.Attributes = []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "tv"}}
			r.Windows[0].Label = "ATTR_ZSCORE_tv"
		},
	}
	for name, mut := range blocks {
		r := &types.Request{Windows: []*types.Window{distLag("tv", "tv_ad", `{"kernel": "geometric", "decay": 0.5, "max_lag": 4}`)}}
		mut(r)
		ch := r.Windows[0].Label
		s := *spec
		s.Predictors, s.Params = []string{ch}, json.RawMessage(`{"channels": ["`+ch+`"]}`)
		if got := AdstockLineage(r, &s, res); got != nil {
			t.Errorf("%s: recorded %v", name, got)
		}
	}
	r := &types.Request{Windows: []*types.Window{distLag("tv", "tv_ad", `{"kernel": "geometric", "decay": 0.5, "max_lag": 4}`)},
		Features:   []*types.Feature{{Type: types.FEAT_LOG, Field: "tv"}, {Type: types.FEAT_ONE_HOT, Field: "region"}},
		Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "tv", Label: "z"}}}
	if _, ok := AdstockLineage(r, spec, res)["tv_ad"]; !ok {
		t.Fatal("unrelated feature and attribute blocked the record")
	}
}

// TestCatalog_Adstocks: a `fits` entry's static catalog lists the
// channels its lineage will record; a from_sweep entry leaves them
// unknown; a loaded file lists its adstock section's.
func TestCatalog_Adstocks(t *testing.T) {
	t.Parallel()

	spec := hillSpec()
	req := &types.Request{Windows: []*types.Window{distLag("tv", "tv_ad", `{"kernel": "geometric", "decay": 0.5, "max_lag": 4}`)},
		Regressions: []*types.RegressionSpec{spec}}
	swept := &types.Request{Windows: req.Windows, Regressions: []*types.RegressionSpec{hillSpec()}}
	cat := CatalogFromFits([]types.FitSpec{{Name: "hill", Request: req}, {Name: "won", Request: swept, FromSweep: &types.FitFromSweep{Regression: "mmm"}}})
	if sig := cat["hill"]; !sig.AdstocksKnown || !reflect.DeepEqual(sig.Adstocks, []string{"tv_ad"}) {
		t.Fatalf("hill: %+v", sig)
	}
	if sig := cat["won"]; sig.AdstocksKnown {
		t.Fatalf("from_sweep entry knows its records: %+v", sig)
	}
	f := &File{Type: types.REG_MMM_HILL, Adstock: map[string]Adstock{"tv": {Kernel: "geometric", Decay: f64(0.5), MaxLag: 2}}}
	loaded := CatalogWithModels(nil, []types.ModelRef{{Name: "saved", Path: "x.pulse-model.json"}}, []*File{f})
	if sig := loaded["saved"]; !sig.AdstocksKnown || !reflect.DeepEqual(sig.Adstocks, []string{"tv"}) {
		t.Fatalf("loaded: %+v", sig)
	}
}
