package regression

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// margins_resample_test.go pins marginal effects under the Resample
// modifier (U69 #639) by invariants the R oracle cannot see: an effect
// that IS a coefficient reports the resampled coefficient table's
// figures bit for bit, a margins block never moves the replicates (the
// coefficient table is the margins-free fit's byte for byte), the
// per-replicate GLM estimate is glmMargins' estimate bit for bit, a
// seeded bootstrap is deterministic, and the extension drivers' generic
// path summarises the replicate's own margins block.

func marginsResampleRecords(t *testing.T) []Record {
	t.Helper()
	return makeLinearRecords(t, 60, []float64{1.5, -0.8}, 0.3, 0.4, 11)
}

// stripMargins is res's wire form without its margins block.
func stripMargins(t *testing.T, res *types.RegressionResult) string {
	t.Helper()
	cp := *res
	cp.Margins = nil
	body, err := types.MarshalFinite(&cp)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestResampleMargins_CoefficientEffectsAreTheCoefficientTable: on
// REG_OLS (an identity link) and on a REG_GLM link-scale block every
// effect is its coefficient, so under jackknife (leave-one-out and
// grouped) and bootstrap (a pinned and the derived seed) its estimate,
// standard error, p-value and interval are the resampled coefficient
// table's bit for bit; the block says inference resample with no df; and
// the fit without its block is the margins-free spec's byte for byte (a
// margins block never moves the replicates, the seed included).
func TestResampleMargins_CoefficientEffectsAreTheCoefficientTable(t *testing.T) {
	t.Parallel()

	lin := marginsResampleRecords(t)
	logit := makeLogisticFixture(t, 80, []float64{1.2, -0.8}, 0.1, 53)
	cases := []struct {
		name string
		spec *types.RegressionSpec
		recs []Record
	}{
		{"ols_jackknife", &types.RegressionSpec{Type: types.REG_OLS, Resample: "jackknife"}, lin},
		{"ols_jackknife_groups", &types.RegressionSpec{Type: types.REG_OLS, Resample: "jackknife", JackknifeGroups: 6, RNGSeed: 3}, lin},
		{"ols_bootstrap_seeded_mem", &types.RegressionSpec{Type: types.REG_OLS, Resample: "bootstrap", BootstrapIters: 150, RNGSeed: 42,
			Margins: &types.RegressionMargins{Type: types.MarginsMEM}}, lin},
		{"ols_bootstrap_derived_seed", &types.RegressionSpec{Type: types.REG_OLS, Resample: "bootstrap", BootstrapIters: 150, Vcov: true}, lin},
		{"glm_link_jackknife", &types.RegressionSpec{Type: types.REG_GLM, Family: "binomial", Resample: "jackknife",
			Margins: &types.RegressionMargins{Scale: types.MarginsScaleLink}}, logit},
		{"glm_link_bootstrap", &types.RegressionSpec{Type: types.REG_GLM, Family: "binomial", Resample: "bootstrap", BootstrapIters: 120,
			Margins: &types.RegressionMargins{Scale: types.MarginsScaleLink}}, logit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := *c.spec
			spec.Name, spec.Target, spec.Predictors = c.name, "y", predictorNames(2)
			if spec.Margins == nil {
				spec.Margins = &types.RegressionMargins{}
			}
			res, err := fitModifier(t, &spec, c.recs)
			if err != nil {
				t.Fatalf("fit: %v", err)
			}
			mg := res.Margins
			if mg == nil || mg.Inference != types.MarginsInferenceResample || mg.DF != 0 {
				t.Fatalf("margins %+v, want inference resample, no df", mg)
			}
			for _, term := range spec.Predictors {
				for _, f := range []struct {
					what      string
					got, want float64
				}{
					{"estimate", mg.Estimates[term], res.Coefficients[term]},
					{"std_error", mg.StdErrors[term], res.StdErrors[term]},
					{"p_value", mg.PValues[term], res.PValues[term]},
					{"ci_lower", mg.CILower[term], res.CILower[term]},
					{"ci_upper", mg.CIUpper[term], res.CIUpper[term]},
				} {
					if math.Float64bits(f.got) != math.Float64bits(f.want) {
						t.Errorf("%s[%s] = %v, coefficient table %v", f.what, term, f.got, f.want)
					}
				}
			}
			bare := spec
			bare.Margins = nil
			plain, err := fitModifier(t, &bare, c.recs)
			if err != nil {
				t.Fatal(err)
			}
			if a, b := stripMargins(t, res), stripMargins(t, plain); a != b {
				t.Errorf("margins moved the resampled fit:\n%s\nvs\n%s", a, b)
			}
		})
	}
}

// TestResampleMargins_GLMReplicateEstimateIsGLMMargins: the replicate
// hook's response-scale estimate (glmMarginEstimates over a refit's
// design) is glmMargins' estimate on the same rows bit for bit — ame,
// mem, a binary contrast, a non-canonical link — so a replicate's effect
// is the effect an unresampled fit of its rows reports.
func TestResampleMargins_GLMReplicateEstimateIsGLMMargins(t *testing.T) {
	t.Parallel()

	logit := makeLogisticFixture(t, 90, []float64{1.1, -0.7}, 0.2, 17)
	binary := make([]Record, len(logit))
	for i, r := range logit {
		v := map[string]float64{}
		for k, x := range r.(mockRecord).values {
			v[k] = x
		}
		v["x2"] = float64(i % 2)
		binary[i] = mockRecord{values: v}
	}
	for _, c := range []struct {
		name   string
		link   string
		m      types.RegressionMargins
		recs   []Record
		hasBin bool
	}{
		{"logit_ame", "logit", types.RegressionMargins{}, logit, false},
		{"logit_mem", "logit", types.RegressionMargins{Type: types.MarginsMEM}, logit, false},
		{"probit_ame", "probit", types.RegressionMargins{}, logit, false},
		{"logit_ame_contrast", "logit", types.RegressionMargins{Binary: types.MarginsBinaryContrast}, binary, true},
		{"logit_mem_contrast", "logit", types.RegressionMargins{Type: types.MarginsMEM, Binary: types.MarginsBinaryContrast}, binary, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := c.m
			spec := &types.RegressionSpec{Name: c.name, Type: types.REG_GLM, Family: "binomial", Link: c.link, Target: "y",
				Predictors: predictorNames(2), Margins: &m}
			base := fitPlain(t, spec, c.recs)
			if got := len(base.Margins.Contrasts) > 0; got != c.hasBin {
				t.Fatalf("contrasts %v", base.Margins.Contrasts)
			}
			eng, err := newModifierEngine(spec, nil)
			if err != nil {
				t.Fatal(err)
			}
			me := eng.(*modifierEngine)
			fitter, _ := me.makeResampleFitter(spec.Predictors)
			r, err := fitter(c.recs)
			if err != nil {
				t.Fatal(err)
			}
			got := marginsDerive(spec, true, me.glmFam, spec.Predictors, base.Margins)(r)
			for i, term := range base.Margins.Terms {
				if math.Float64bits(got[i]) != math.Float64bits(base.Margins.Estimates[term]) {
					t.Errorf("%s: replicate estimate %v, glmMargins %v", term, got[i], base.Margins.Estimates[term])
				}
			}
		})
	}
}

// TestResampleMargins_GLMBootstrapDeterministic: a REG_GLM response-scale
// block under a derived-seed bootstrap is the same bits run to run, its
// replicate SE differs from the delta method's, and the coefficient
// table is the margins-free fit's byte for byte.
func TestResampleMargins_GLMBootstrapDeterministic(t *testing.T) {
	t.Parallel()

	recs := makeLogisticFixture(t, 80, []float64{1.2, -0.8}, 0.1, 53)
	spec := &types.RegressionSpec{Name: "b", Type: types.REG_GLM, Family: "binomial", Target: "y", Predictors: predictorNames(2),
		Resample: "bootstrap", BootstrapIters: 120, Margins: &types.RegressionMargins{}}
	a, err := fitModifier(t, spec, recs)
	if err != nil {
		t.Fatal(err)
	}
	b, err := fitModifier(t, spec, recs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Margins, b.Margins) {
		t.Fatal("same seed, different resampled margins")
	}
	delta := fitPlain(t, spec, recs).Margins
	for _, term := range spec.Predictors {
		if se := a.Margins.StdErrors[term]; !(se > 0) || se == delta.StdErrors[term] || a.Margins.Estimates[term] != delta.Estimates[term] {
			t.Errorf("%s: replicate se %v (delta %v), estimate %v (full fit %v)", term, se, delta.StdErrors[term], a.Margins.Estimates[term], delta.Estimates[term])
		}
		if p := a.Margins.PValues[term]; !(p > 0 && p <= 1) {
			t.Errorf("%s: percentile p %v", term, p)
		}
	}
	bare := *spec
	bare.Margins = nil
	plain, err := fitModifier(t, &bare, recs)
	if err != nil {
		t.Fatal(err)
	}
	if x, y := stripMargins(t, a), stripMargins(t, plain); x != y {
		t.Errorf("margins moved the bootstrap:\n%s\nvs\n%s", x, y)
	}
}

// TestExtensionResample_MarginsFromReplicates: the extension drivers
// (REG_MULTINOMIAL's and every registered model's) read each replicate
// fit's own margins block: a model whose effect equals a coefficient
// reports that coefficient's replicate SE and p-value bit for bit, under
// the jackknife and the bootstrap; a replicate that lacks a key reads it
// undefined.
func TestExtensionResample_MarginsFromReplicates(t *testing.T) {
	t.Parallel()

	recs := marginsResampleRecords(t)
	// A fake model: (intercept) = mean y, x1 = mean(x1·y); its margins
	// block reports x1's effect as that same figure, and a second term
	// x2 only on the full fit.
	full := len(recs)
	fit := func(rs []Record, _ []string) (*types.RegressionResult, []string, error) {
		var sy, sxy float64
		for _, r := range rs {
			y, _ := InputValue(r, "y")
			x, _ := InputValue(r, "x1")
			sy += y
			sxy += float64(x * y)
		}
		n := float64(len(rs))
		b0, b1 := sy/n, sxy/n
		est := map[string]float64{"x1": b1}
		if len(rs) == full && &rs[0] == &recs[0] {
			est["x2"] = 1 // the base fit only (a bootstrap sample is its own slice)
		}
		return &types.RegressionResult{
			Coefficients: map[string]float64{InterceptKey: b0, "x1": b1},
			StdErrors:    map[string]float64{InterceptKey: 1, "x1": 1},
			Margins: &types.RegressionMarginsResult{Type: types.MarginsAME, Scale: types.MarginsScaleResponse,
				Terms: []string{"x1", "x2"}, Estimates: est, ConfLevel: 0.95},
		}, []string{InterceptKey, "x1"}, nil
	}
	for _, resample := range []string{"jackknife", "bootstrap"} {
		t.Run(resample, func(t *testing.T) {
			spec := &types.RegressionSpec{Name: "x", Type: "REG_TEST_FAKE", Target: "y", Predictors: []string{"x1", "x2"},
				Resample: resample, BootstrapIters: 80, RNGSeed: 9, Margins: &types.RegressionMargins{}}
			res, err := RunExtensionModifiers(context.Background(), spec, true, recs, fit, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			mg := res.Margins
			if mg == nil || mg.Inference != types.MarginsInferenceResample {
				t.Fatalf("margins %+v", mg)
			}
			for _, f := range []struct {
				what      string
				got, want float64
			}{
				{"std_error", mg.StdErrors["x1"], res.StdErrors["x1"]},
				{"p_value", mg.PValues["x1"], res.PValues["x1"]},
				{"ci_lower", mg.CILower["x1"], res.CILower["x1"]},
			} {
				if math.Float64bits(f.got) != math.Float64bits(f.want) {
					t.Errorf("x1 %s = %v, coefficient %v", f.what, f.got, f.want)
				}
			}
			if !math.IsNaN(mg.StdErrors["x2"]) || mg.Estimates["x2"] != 1 {
				t.Errorf("x2 (absent from every replicate): estimate %v se %v, want 1 / NaN", mg.Estimates["x2"], mg.StdErrors["x2"])
			}
		})
	}
}
