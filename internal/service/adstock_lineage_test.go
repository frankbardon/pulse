package service

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// adstock_lineage_test.go pins where a media-mix fit's adstock record
// (model.AdstockLineage) is taken: in the fit's own run, over its
// windows as that run resolved them — on the Compose fit's binding, its
// `save`, and a plain request's regression `save` alike.

const hillSavePath = "models/hill.pulse-model.json"

// hillFitRequest is a REG_MMM_HILL of y on the adstocked x_ad, whose
// geometric decay is the expression decay (over the request's params d
// = 0.6), plus the hand-built lag column x_l1 entering as a second
// channel.
func hillFitRequest(decay string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: fitsCohort},
		Params: types.Params{"d": json.Number("0.6")},
		Windows: []*types.Window{
			{Type: types.WIN_DISTRIBUTED_LAG, Field: "x", Label: "x_ad", OrderBy: []types.OrderKey{{Field: "week"}},
				Params: json.RawMessage(`{"kernel": "geometric", "max_lag": 3, "decay": ` + decay + `}`)},
			{Type: types.WIN_LAG, Field: "x", Label: "x_l1", OrderBy: []types.OrderKey{{Field: "week"}}},
		},
		Regressions: []*types.RegressionSpec{{Type: types.REG_MMM_HILL, Name: "m", Target: "y",
			Predictors: []string{"x_ad", "x_l1"}, Params: json.RawMessage(`{"channels": ["x_ad", "x_l1"]}`)}},
	}
}

// wantHillAdstock is x_ad's record at decay 0.6.
func wantHillAdstock() map[string]model.Adstock {
	d := 0.6
	return map[string]model.Adstock{"x_ad": {Kernel: "geometric", Decay: &d, MaxLag: 3, Normalize: true}}
}

// readSavedAdstock decodes the model file at path and returns its
// features and adstock section.
func readSavedAdstock(t *testing.T, fsys afero.Fs, path string) ([]string, map[string]model.Adstock) {
	t.Helper()
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := model.DecodeFile(path, data)
	if err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	return f.Features, f.Adstock
}

// TestAdstockLineage_ComposeFitBindsAndSaves: a Compose REG_MMM_HILL fit
// whose window decay is the expression params.d binds x_ad's record at
// the resolved 0.6 (never the string) and writes it as feature adstock
// in its save; the WIN_LAG-built channel x_l1 has none.
func TestAdstockLineage_ComposeFitBindsAndSaves(t *testing.T) {
	t.Parallel()

	svc := fitsService(t)
	ctx := context.Background()
	set, _, _, err := svc.runComposeFits(ctx, []types.FitSpec{{Name: "hill", Request: hillFitRequest(`"params.d"`), Save: hillSavePath}}, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := set.Lookup("hill")
	if err != nil {
		t.Fatal(err)
	}
	if b.Result.Error != nil {
		t.Fatalf("the Hill fit failed: %+v", b.Result.Error)
	}
	if !reflect.DeepEqual(b.Adstocks, wantHillAdstock()) {
		t.Fatalf("bound adstock %+v, want %+v", b.Adstocks, wantHillAdstock())
	}
	if _, ok := b.Adstock("x_l1"); ok {
		t.Fatal("the WIN_LAG-built channel x_l1 has a record")
	}
	features, saved := readSavedAdstock(t, svc.fs.Fs(), hillSavePath)
	if !reflect.DeepEqual(features, []string{model.FeatureOutputs, model.FeatureAdstock}) || !reflect.DeepEqual(saved, wantHillAdstock()) {
		t.Fatalf("saved features %v adstock %+v", features, saved)
	}
}

// TestAdstockLineage_RegressionSave: a plain request's REG_MMM_HILL
// `save` writes the same record (the literal decay reads as itself);
// an OLS save, and a Hill fit with no WIN_DISTRIBUTED_LAG channel, write
// no adstock section.
func TestAdstockLineage_RegressionSave(t *testing.T) {
	t.Parallel()

	svc := fitsService(t)
	ctx := context.Background()
	req := hillFitRequest(`0.6`)
	req.Regressions[0].Save = hillSavePath
	if _, err := svc.Process(ctx, req); err != nil {
		t.Fatal(err)
	}
	if features, saved := readSavedAdstock(t, svc.fs.Fs(), hillSavePath); !reflect.DeepEqual(saved, wantHillAdstock()) ||
		!reflect.DeepEqual(features, []string{model.FeatureOutputs, model.FeatureAdstock}) {
		t.Fatalf("saved features %v adstock %+v", features, saved)
	}

	lagOnly := hillFitRequest(`0.6`)
	lagOnly.Windows = lagOnly.Windows[1:]
	lagOnly.Regressions[0].Predictors, lagOnly.Regressions[0].Params = []string{"x_l1"}, json.RawMessage(`{"channels": ["x_l1"]}`)
	lagOnly.Regressions[0].Save = "models/lag.pulse-model.json"
	ols := fitRequest("0", "90")
	ols.Regressions[0].Save = "models/ols.pulse-model.json"
	for _, r := range []*types.Request{lagOnly, ols} {
		if _, err := svc.Process(ctx, r); err != nil {
			t.Fatal(err)
		}
		if features, saved := readSavedAdstock(t, svc.fs.Fs(), r.Regressions[0].Save); saved != nil {
			t.Errorf("%s: features %v adstock %+v, want none", r.Regressions[0].Save, features, saved)
		}
	}
}

// TestAdstockLineage_BayesFitBinds: a Compose REG_MMM_BAYES fit binds
// each channel's record — geometric, its decay:<ch> posterior mean bit
// for bit, the fit's params.max_lag, unnormalised — with no window in
// the request.
func TestAdstockLineage_BayesFitBinds(t *testing.T) {
	t.Parallel()

	svc := fitsService(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: fitsCohort},
		Regressions: []*types.RegressionSpec{{Type: types.REG_MMM_BAYES, Name: "m", Target: "y", Predictors: []string{"x"},
			Params: json.RawMessage(`{"channels": ["x"], "time": "week", "max_lag": 4, "chains": 1, "warmup": 20, "draws": 20}`), RNGSeed: 7}},
	}
	set, _, _, err := svc.runComposeFits(context.Background(), []types.FitSpec{{Name: "bayes", Request: req}}, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := set.Lookup("bayes")
	if err != nil {
		t.Fatal(err)
	}
	if b.Result.Error != nil {
		t.Fatalf("the Bayes fit failed: %+v", b.Result.Error)
	}
	d := b.Result.Coefficients["decay:x"]
	a, ok := b.Adstock("x")
	if !ok || a.Kernel != "geometric" || a.Decay == nil || *a.Decay != d || a.MaxLag != 4 || a.Normalize || a.Shape != nil || a.Scale != nil || a.Peak != nil {
		t.Fatalf("bound adstock %+v %v, want geometric decay %v max_lag 4 unnormalised", a, ok, d)
	}

	// A delayed and a Weibull fit bind their kernel at the posterior
	// means of its own coefficients.
	for _, tc := range []struct {
		kernel string
		check  func(a model.Adstock, c map[string]float64) bool
	}{
		{"delayed", func(a model.Adstock, c map[string]float64) bool {
			return a.Decay != nil && *a.Decay == c["decay:x"] && a.Peak != nil && *a.Peak == c["peak:x"] && a.Shape == nil && a.Scale == nil
		}},
		{"weibull_pdf", func(a model.Adstock, c map[string]float64) bool {
			return a.Shape != nil && *a.Shape == c["shape:x"] && a.Scale != nil && *a.Scale == c["scale:x"] && a.Decay == nil && a.Peak == nil
		}},
	} {
		kreq := &types.Request{
			Cohort: &types.Cohort{Filename: fitsCohort},
			Regressions: []*types.RegressionSpec{{Type: types.REG_MMM_BAYES, Name: "m", Target: "y", Predictors: []string{"x"},
				Params: json.RawMessage(`{"channels": ["x"], "time": "week", "max_lag": 4, "adstock": "` + tc.kernel + `", "chains": 1, "warmup": 20, "draws": 20}`), RNGSeed: 7}},
		}
		set, _, _, err := svc.runComposeFits(context.Background(), []types.FitSpec{{Name: "bayes", Request: kreq}}, nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		b, err := set.Lookup("bayes")
		if err != nil || b.Result.Error != nil {
			t.Fatalf("%s: %v %+v", tc.kernel, err, b.Result.Error)
		}
		a, ok := b.Adstock("x")
		if !ok || a.Kernel != tc.kernel || a.MaxLag != 4 || a.Normalize || !tc.check(a, b.Result.Coefficients) {
			t.Fatalf("%s: bound adstock %s %v, coefficients %v", tc.kernel, mustJSON(t, a), ok, b.Result.Coefficients)
		}
	}
}

// TestAdstockLineage_BayesCurveBinds: a Compose fit of REG_MMM_BAYES
// under params.saturation binds its curve record, and response() over
// the bound fit evaluates that curve at the posterior means — the
// logistic λ and the Michaelis–Menten k, never a Hill reading.
func TestAdstockLineage_BayesCurveBinds(t *testing.T) {
	t.Parallel()

	svc := fitsService(t)
	for _, tc := range []struct {
		curve string
		terms []string
	}{
		{"logistic", []string{"lam:x"}},
		{"michaelis_menten", []string{"k:x"}},
	} {
		req := &types.Request{
			Cohort: &types.Cohort{Filename: fitsCohort},
			Regressions: []*types.RegressionSpec{{Type: types.REG_MMM_BAYES, Name: "m", Target: "y", Predictors: []string{"x"},
				Params: json.RawMessage(`{"channels": ["x"], "time": "week", "max_lag": 4, "saturation": "` + tc.curve + `", "chains": 1, "warmup": 20, "draws": 20}`), RNGSeed: 7}},
		}
		set, _, _, err := svc.runComposeFits(context.Background(), []types.FitSpec{{Name: "bayes", Request: req}}, nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		b, err := set.Lookup("bayes")
		if err != nil || b.Result.Error != nil {
			t.Fatalf("%s: %v %+v", tc.curve, err, b.Result.Error)
		}
		if b.Saturation != tc.curve {
			t.Fatalf("%s: bound curve %q", tc.curve, b.Saturation)
		}
		c := b.Result.Coefficients
		if _, ok := c["s:x"]; ok {
			t.Fatalf("%s: a Hill shape in %v", tc.curve, c)
		}
		a, _ := b.Adstock("x")
		m, err := a.SteadyState()
		if err != nil {
			t.Fatal(err)
		}
		params := make([]float64, len(tc.terms))
		for i, term := range tc.terms {
			params[i] = c[term]
		}
		got, cerr := model.Response(set, "bayes", "x", 10)
		if want := model.CurveResponseAt(tc.curve, c["x"], params, m, 10); cerr != nil || math.Float64bits(got) != math.Float64bits(want) || math.IsNaN(got) {
			t.Fatalf("%s: response %v (%v), want %v", tc.curve, got, cerr, want)
		}
	}
}
