package pulse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// E2-S3 facade tests: effect sizes beside the marginal p-values (FR-17)
// and the supplied-model refit of the utility section (FR-16).

// utilityFixtureSpec is a numeric pair correlated through the copula, a
// second numeric and a two-level categorical, each parameterised so a
// test can shift the twin away from the source.
func utilityFixtureSpec(rows int, xMean, bigMean, pA float64) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "x", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": xMean, "std": 20.0}},
			{Name: "y", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 50.0, "std": 10.0}},
			{Name: "big", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": bigMean, "std": 1.0}},
			{Name: "segment", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"a", "b"}, "weights": []any{pA, 1 - pA}}},
		},
		Correlations: []synth.CorrelationSpec{{A: "x", B: "y", Rho: 0.6}},
	}
}

// writeUtilityReport synthesises source from sourceSpec, tops it up from
// twinSpec with opts, and returns the parsed report and its bytes.
func writeUtilityReport(t *testing.T, sourceSpec, twinSpec *synth.Spec, opts SynthOptions) (*synth.FidelityReport, []byte, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Synth(context.Background(), sourceSpec, "/source.pulse", SynthOptions{Seed: 21}); err != nil {
		t.Fatalf("source synth: %v", err)
	}
	opts.Seed = 22
	opts.SourceCohort = "/source.pulse"
	if _, err := p.Synth(context.Background(), twinSpec, "/twin.pulse", opts); err != nil {
		if ok, _ := afero.Exists(fs, "/twin.pulse"); ok {
			t.Errorf("a refused call wrote its output cohort")
		}
		return nil, nil, err
	}
	raw, err := afero.ReadFile(fs, opts.FidelityReportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	return &report, raw, nil
}

func fieldEntry(report *synth.FidelityReport, name string) *synth.FieldFidelity {
	for _, f := range report.Fields {
		if f.Field == name {
			return f
		}
	}
	return nil
}

// TestSynth_FidelityReportLargeNTrivialDifferenceIsAVerySmallEffect is
// FR-17's acceptance: at 20,000 rows per partition a shift no analysis
// would notice (0.06 SD on x, three points of share on segment) rejects at
// p < 0.05 on both tests, and the effect size beside each p-value says
// "very small"; a one-SD shift on big says "large".
func TestSynth_FidelityReportLargeNTrivialDifferenceIsAVerySmallEffect(t *testing.T) {
	t.Parallel()

	const n = 20000
	report, _, err := writeUtilityReport(t,
		utilityFixtureSpec(n, 100, 0, 0.50),
		utilityFixtureSpec(n, 101.2, 1, 0.53),
		SynthOptions{FidelityReportPath: "/report.json"})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}

	for _, c := range []struct {
		field, measure, magnitude string
		reject                    bool
	}{
		{"x", synth.EffectMeasureCohensD, "very small", true},
		{"segment", synth.EffectMeasureCramersV, "very small", true},
		{"big", synth.EffectMeasureCohensD, "large", true},
		{"y", synth.EffectMeasureCohensD, "very small", false},
	} {
		e := fieldEntry(report, c.field)
		if e == nil || e.Result == nil {
			t.Fatalf("%s: no marginal result: %+v", c.field, e)
		}
		es := e.EffectSize
		if es == nil {
			t.Fatalf("%s: no effect size beside p = %g", c.field, e.Result.PValue)
		}
		t.Logf("%s: p = %.3g, %s = %.4f (%s)", c.field, e.Result.PValue, es.Measure, es.Value, es.Magnitude)
		if es.Measure != c.measure || es.Magnitude != c.magnitude {
			t.Errorf("%s effect = %+v, want %s / %s", c.field, *es, c.measure, c.magnitude)
		}
		if c.reject && !(e.Result.PValue < 0.05) {
			t.Errorf("%s: p = %g, the fixture must reject for the effect size to be the point", c.field, e.Result.PValue)
		}
	}
	if es := fieldEntry(report, "big").EffectSize; es.Value < 0.8 {
		t.Errorf("big d = %.3f, want the planted ~ +1 (synthetic − source, signed)", es.Value)
	}
	if report.Utility != nil {
		t.Errorf("utility = %+v, want absent for a models-free spec with no supplied model", report.Utility)
	}
}

// fidelityModelRequest is an OLS of y on x and segment.
func fidelityModelRequest(t *testing.T) *types.Request {
	t.Helper()
	var req types.Request
	body := `{"cohort": {"filename": "ignored.pulse"},
	  "regressions": [{"type": "REG_OLS", "name": "y_on_x", "target": "y", "predictors": ["x"]}]}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	return &req
}

// TestSynth_FidelityModelRefitsOnBothPartitions: the supplied request is
// refit on the source cohort and on the generated rows only, both fits
// land in utility.model_fits with their ratio, and the caller's request
// is not mutated.
func TestSynth_FidelityModelRefitsOnBothPartitions(t *testing.T) {
	t.Parallel()

	req := fidelityModelRequest(t)
	before, _ := json.Marshal(req)
	report, _, err := writeUtilityReport(t,
		utilityFixtureSpec(400, 100, 0, 0.5),
		utilityFixtureSpec(3000, 100, 0, 0.5),
		SynthOptions{FidelityReportPath: "/report.json", FidelityModel: req})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	if after, _ := json.Marshal(req); string(after) != string(before) {
		t.Errorf("the caller's request was mutated:\n%s\n%s", before, after)
	}
	if report.Utility == nil || len(report.Utility.ModelFits) != 1 {
		t.Fatalf("model fits = %+v", report.Utility)
	}
	fit := report.Utility.ModelFits[0]
	t.Logf("%+v", *fit)
	if fit.SourceError != "" || fit.SyntheticError != "" {
		t.Fatalf("errors: %q / %q", fit.SourceError, fit.SyntheticError)
	}
	if fit.Name != "y_on_x" || fit.Target != "y" || fit.Statistic != synth.UtilityFitR2 {
		t.Errorf("identity = %+v", fit)
	}
	// The source refit reads the source cohort; the synthetic refit
	// reads the generated rows ONLY, never the copied source rows.
	if fit.SourceNObs != 400 || fit.SyntheticNObs != 3000 {
		t.Errorf("n_obs = %d / %d, want 400 / 3000", fit.SourceNObs, fit.SyntheticNObs)
	}
	if fit.SourceFit < 0.2 || fit.SyntheticFit < 0.2 || fit.FitRatio < 0.6 || fit.FitRatio > 1.6 {
		t.Errorf("fits %.3f / %.3f ratio %.3f, want both near rho² = 0.36", fit.SourceFit, fit.SyntheticFit, fit.FitRatio)
	}
	var x *synth.UtilityCoefficient
	for _, c := range fit.Coefficients {
		if c.Term == "x" {
			x = c
		}
	}
	if x == nil || x.Ratio < 0.6 || x.Ratio > 1.6 || x.SourceStdError == 0 {
		t.Errorf("x coefficient = %+v", x)
	}
}

// TestSynth_FidelityModelRefusals: a model that cannot be refit is
// refused before generation, under its own code — and a request the
// engine refuses fails with the engine's code, also before generation.
func TestSynth_FidelityModelRefusals(t *testing.T) {
	t.Parallel()

	src := utilityFixtureSpec(200, 100, 0, 0.5)
	twin := utilityFixtureSpec(200, 100, 0, 0.5)

	empty := &types.Request{}
	if _, _, err := writeUtilityReport(t, src, twin, SynthOptions{FidelityReportPath: "/r.json", FidelityModel: empty}); !errors.HasCode(err, errors.PULSE_SYNTH_FIDELITY_MODEL_INVALID) {
		t.Errorf("no regressions: err = %v, want PULSE_SYNTH_FIDELITY_MODEL_INVALID", err)
	}
	if _, _, err := writeUtilityReport(t, src, twin, SynthOptions{FidelityModel: fidelityModelRequest(t)}); !errors.HasCode(err, errors.PULSE_SYNTH_FIDELITY_MODEL_INVALID) {
		t.Errorf("no report path: err = %v, want PULSE_SYNTH_FIDELITY_MODEL_INVALID", err)
	}
	bad := fidelityModelRequest(t)
	bad.Regressions[0].Target = "no_such_field"
	_, _, err := writeUtilityReport(t, src, twin, SynthOptions{FidelityReportPath: "/r.json", FidelityModel: bad})
	if err == nil {
		t.Fatal("a regression on an unknown field generated")
	}
	if errors.HasCode(err, errors.PULSE_SYNTH_FIDELITY_MODEL_INVALID) || !strings.Contains(err.Error(), "no_such_field") {
		t.Errorf("unknown field: err = %v, want the engine's own refusal naming the field", err)
	}
}
