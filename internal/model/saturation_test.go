package model

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// saturation_test.go holds REG_MMM_BAYES's curve record to its
// contract: params.saturation names the curve coefficients
// (ModelRegressionTerms), the bound fit carries it (Bound.Saturation),
// response() evaluates that curve, a model file and an inline body
// carry it as the `saturation` feature, and the Hill default carries
// nothing.

// bayesCurveFit is a REG_MMM_BAYES fit under saturation (raw JSON
// value, "" for absent) with the curve's coefficients.
func bayesCurveFit(saturation string) (*types.RegressionSpec, *types.RegressionResult) {
	params := `{"channels": ["tv"], "time": "week", "max_lag": 5`
	if saturation != "" {
		params += `, "saturation": "` + saturation + `"`
	}
	coefs := map[string]float64{InterceptKey: 4, "tv": 2.5, "decay:tv": 0.45, "sigma": 1}
	switch saturation {
	case "logistic":
		coefs["lam:tv"] = 0.02
	case "michaelis_menten":
		coefs["k:tv"] = 30
	default:
		coefs["k:tv"], coefs["s:tv"] = 30, 1.2
	}
	return &types.RegressionSpec{Type: types.REG_MMM_BAYES, Target: "sales", Predictors: []string{"tv"},
			Params: json.RawMessage(params + `}`)},
		&types.RegressionResult{Type: types.REG_MMM_BAYES, Coefficients: coefs}
}

// TestModelRegression_BayesCurveTerms: each curve names its own
// coefficients after the kernel's — k:, s: (hill, absent or spelled);
// lam: (logistic); k: (michaelis_menten) — and an unknown curve leaves
// the names unknown.
func TestModelRegression_BayesCurveTerms(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		saturation string
		want       []string
		ok         bool
	}{
		{"", []string{"decay:tv", "k:tv", "s:tv", "sigma"}, true},
		{"hill", []string{"decay:tv", "k:tv", "s:tv", "sigma"}, true},
		{"logistic", []string{"decay:tv", "lam:tv", "sigma"}, true},
		{"michaelis_menten", []string{"decay:tv", "k:tv", "sigma"}, true},
		{"gompertz", nil, false},
	} {
		spec, _ := bayesCurveFit(tc.saturation)
		got, ok := ModelRegressionTerms(spec)
		if ok != tc.ok || !slices.Equal(got, tc.want) {
			t.Errorf("%q: %v %v, want %v %v", tc.saturation, got, ok, tc.want, tc.ok)
		}
		want := tc.saturation
		if want == "hill" {
			want = ""
		}
		if curve := MMMBayesSaturation(spec); curve != want {
			t.Errorf("%q: curve record %q, want %q", tc.saturation, curve, want)
		}
	}
	// REG_MMM_HILL has no params.saturation: its terms stay k:, s:.
	hill := &types.RegressionSpec{Type: types.REG_MMM_HILL, Params: json.RawMessage(`{"channels": ["tv"], "saturation": "logistic"}`)}
	if got, _ := ModelRegressionTerms(hill); !slices.Equal(got, []string{"k:tv", "s:tv"}) {
		t.Errorf("REG_MMM_HILL terms %v", got)
	}
	if MMMBayesSaturation(hill) != "" {
		t.Error("REG_MMM_HILL carries a curve record")
	}
}

// TestResponse_BayesCurves: response() on a bound REG_MMM_BAYES fit
// evaluates the fit's own curve — the hand-written Hill, logistic and
// Michaelis–Menten formulas over its coefficients and the adstock's
// steady-state multiplier.
func TestResponse_BayesCurves(t *testing.T) {
	t.Parallel()

	m := (1 - math.Pow(0.45, 6)) / (1 - 0.45)
	x := m * 20
	for _, tc := range []struct {
		saturation string
		want       float64
	}{
		{"", 2.5 * handHill(x, 30, 1.2)},
		{"logistic", 2.5 * (1 - math.Exp(-0.02*x)) / (1 + math.Exp(-0.02*x))},
		{"michaelis_menten", 2.5 * x / (30 + x)},
	} {
		spec, res := bayesCurveFit(tc.saturation)
		b, err := Bind("bayes", spec, res)
		if err != nil {
			t.Fatal(err)
		}
		if b.Saturation != tc.saturation {
			t.Errorf("%q: bound curve %q", tc.saturation, b.Saturation)
		}
		b.Adstocks = AdstockLineage(nil, spec, res)
		s := &Set{}
		if err := s.Add(b); err != nil {
			t.Fatal(err)
		}
		got, cerr := Response(s, "bayes", "tv", 20)
		if cerr != nil || !near(got, tc.want, 1e-12) {
			t.Errorf("%q: response %v (%v), want %v", tc.saturation, got, cerr, tc.want)
		}
	}
}

// TestCurveResponseAt_Domain: outside each curve's domain (negative
// spend, a non-positive parameter, NaN β, the wrong parameter count, an
// unknown curve) the response is NaN.
func TestCurveResponseAt_Domain(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		curve  string
		beta   float64
		params []float64
		spend  float64
	}{
		{"logistic", 1, []float64{0.1}, -1},
		{"logistic", 1, []float64{0}, 1},
		{"logistic", math.NaN(), []float64{0.1}, 1},
		{"michaelis_menten", 1, []float64{-1}, 1},
		{"michaelis_menten", 1, []float64{1}, -1},
		{"michaelis_menten", 1, []float64{1, 2}, 1},
		{"hill", 1, []float64{1}, 1},
		{"gompertz", 1, []float64{1}, 1},
	} {
		if got := CurveResponseAt(tc.curve, tc.beta, tc.params, 1, tc.spend); !math.IsNaN(got) {
			t.Errorf("%s %v %v spend %v: %v, want NaN", tc.curve, tc.beta, tc.params, tc.spend, got)
		}
	}
	if got := CurveResponseAt("logistic", 2, []float64{0.5}, 1, 0); got != 0 {
		t.Errorf("logistic at zero spend: %v", got)
	}
}

// TestModelFile_SaturationRoundTrip: a non-Hill REG_MMM_BAYES fit's
// file carries feature saturation and binds the curve back (so
// response() evaluates it); a Hill fit's file carries no section.
func TestModelFile_SaturationRoundTrip(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		saturation string
		features   []string
	}{
		{"", []string{FeatureOutputs}},
		{"hill", []string{FeatureOutputs}},
		{"logistic", []string{FeatureOutputs, FeatureSaturation}},
		{"michaelis_menten", []string{FeatureOutputs, FeatureSaturation}},
	} {
		spec, res := bayesCurveFit(tc.saturation)
		res.Outputs = map[string]any{"seed": 1.0}
		f, err := NewFile("bayes", spec, res, Provenance{})
		if err != nil {
			t.Fatal(err)
		}
		got, data := roundTrip(t, f)
		if !slices.Equal(got.Features, tc.features) {
			t.Errorf("%q: features %v, want %v", tc.saturation, got.Features, tc.features)
		}
		if len(tc.features) == 1 && strings.Contains(string(data), `"saturation"`) {
			t.Errorf("%q: the Hill file carries a saturation key:\n%s", tc.saturation, data)
		}
		b, err := got.Bind("bayes")
		if err != nil {
			t.Fatal(err)
		}
		if want := MMMBayesSaturation(spec); b.Saturation != want {
			t.Errorf("%q: loaded curve %q, want %q", tc.saturation, b.Saturation, want)
		}
	}
}

// TestDecodeFile_SaturationRefusals: the section belongs to a
// REG_MMM_BAYES fit, names a known curve and is listed exactly when
// present (PULSE_MODEL_FILE_INVALID reason schema).
func TestDecodeFile_SaturationRefusals(t *testing.T) {
	t.Parallel()

	spec, res := bayesCurveFit("logistic")
	res.Outputs = map[string]any{"seed": 1.0}
	f, err := NewFile("bayes", spec, res, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	good, _ := f.Encode()
	if _, err := DecodeFile("m"+FileSuffix, good); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		edit func(m map[string]any)
		want string
	}{
		"unknown curve": {func(m map[string]any) { m["saturation"] = "gompertz" }, `saturation "gompertz", which is not one of`},
		"not bayes":     {func(m map[string]any) { m["type"] = "REG_MMM_HILL" }, "only a REG_MMM_BAYES fit has"},
		"unlisted":      {func(m map[string]any) { m["features"] = []any{"outputs"} }, "without listing feature saturation"},
		"listed without": {func(m map[string]any) { delete(m, "saturation") },
			"lists feature saturation without its saturation section"},
	} {
		var m map[string]any
		if err := json.Unmarshal(good, &m); err != nil {
			t.Fatal(err)
		}
		tc.edit(m)
		doc, _ := json.Marshal(m)
		_, err := DecodeFile("m"+FileSuffix, doc)
		ce := coded(t, err)
		if ce.Code != errors.PULSE_MODEL_FILE_INVALID || ce.Details["reason"] != ReasonFileSchema || !strings.Contains(ce.Message, tc.want) {
			t.Errorf("%s: %s %v %q, want schema %q", name, ce.Code, ce.Details["reason"], ce.Message, tc.want)
		}
	}
}

// TestInline_BayesCurveTerms: an inline REG_MMM_BAYES body's terms
// follow its `saturation` (hill when absent): a logistic body carries
// lam:, a Michaelis–Menten one k: alone, each binds its curve and
// response() evaluates it; a curve's terms under another curve are
// refused, and a logistic channel's adstock record keys on lam:.
func TestInline_BayesCurveTerms(t *testing.T) {
	t.Parallel()

	body := func(saturation, curve string) string {
		sec := ""
		if saturation != "" {
			sec = `, "features": ["adstock", "saturation"], "saturation": "` + saturation + `"`
		} else {
			sec = `, "features": ["adstock"]`
		}
		return `{"model_format_version": 1, "type": "REG_MMM_BAYES", "target": "sales",
	"predictors": ["tv"], "terms": ["tv"],
	"adstock": {"tv": {"kernel": "geometric", "decay": 0.5, "max_lag": 4, "normalize": false}},
	"coefficients": {"(intercept)": 10, "tv": 3, "decay:tv": 0.5, "sigma": 2` + curve + `}` + sec + `}`
	}
	m := (1 - math.Pow(0.5, 5)) / (1 - 0.5)
	for _, tc := range []struct {
		name, saturation, curve string
		ok                      bool
		want                    float64
	}{
		{"hill", "", `, "k:tv": 50, "s:tv": 1.5`, true, 3 * handHill(m*40, 50, 1.5)},
		{"logistic", "logistic", `, "lam:tv": 0.03`, true, 3 * math.Tanh(0.03*m*40/2)},
		{"michaelis_menten", "michaelis_menten", `, "k:tv": 50`, true, 3 * m * 40 / (50 + m*40)},
		{"logistic with k", "logistic", `, "lam:tv": 0.03, "k:tv": 50`, false, 0},
		{"michaelis_menten with s", "michaelis_menten", `, "k:tv": 50, "s:tv": 1.5`, false, 0},
		{"hill with lam", "", `, "k:tv": 50, "s:tv": 1.5, "lam:tv": 0.03`, false, 0},
		{"logistic without lam", "logistic", `, "k:tv": 50`, false, 0},
	} {
		rs := Refs{Models: []types.ModelRef{inlineRef("m", body(tc.saturation, tc.curve))}}
		cerr := CheckRefs(rs, nil)
		var loaded *Loaded
		if cerr == nil {
			loaded, cerr = LoadFiles(rs, nil, stubInline)
		}
		if (cerr == nil) != tc.ok {
			t.Errorf("%s: %v, want ok %v", tc.name, cerr, tc.ok)
			continue
		}
		if !tc.ok {
			continue
		}
		set, err := (*Set)(nil).WithLoaded(rs, loaded)
		if err != nil {
			t.Fatal(err)
		}
		got, rerr := Response(set, "m", "tv", 40)
		if rerr != nil || !near(got, tc.want, 1e-12) {
			t.Errorf("%s: response %v (%v), want %v", tc.name, got, rerr, tc.want)
		}
	}
}
