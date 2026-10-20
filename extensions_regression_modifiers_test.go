package pulse_test

import (
	"context"
	stderrors "errors"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Modifiers on custom fits (U53 E2-S4): the engine's jackknife,
// bootstrap and selection drivers refit an extension model through its
// registration — resample on every model, selection only where the
// registration declares SupportsSelection. The parity clones
// (REG_PARITY_OLS streaming, REG_PARITY_OLS_BUF buffered) are REG_OLS,
// so every resampled and selected figure must match the built-in's
// under the same rows and seed.

const regParitySelect types.RegressionType = "REG_PARITY_OLS_SELECT"

// devianceOLS is the streaming clone reporting a Gaussian deviance
// n·ln(RSS/n) — −2·log L up to a constant every candidate shares — so
// its AIC / BIC differ from REG_OLS's by one constant and the
// selection picks the same predictors.
type devianceOLS struct{ streamOLS }

func (d *devianceOLS) Merge(o extend.StreamingRegression) error {
	d.m.merge(o.(*devianceOLS).m)
	return nil
}

func (d *devianceOLS) Finalize() (extend.RegressionOutput, error) {
	out, err := d.m.output(d.kind)
	if err != nil {
		return out, err
	}
	df, rse := *out.Fit.DF, *out.Fit.ResidualStdErr
	n := df + float64(len(out.Coefficients))
	rss := rse * rse * df
	out.Fit.Deviance = extend.Figure(n * math.Log(rss/n))
	return out, nil
}

func modifierRegistrations() []pulse.RegressionRegistration {
	regs := regressionParityRegistrations()
	return append(regs, pulse.RegressionRegistration{
		Name: regParitySelect, Description: "Test-only streaming REG_OLS clone reporting a deviance, selectable.",
		Streamable: true, SupportsSelection: true,
		Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
			return &devianceOLS{streamOLS{m: newOLSMoments(spec, false)}}, nil
		},
	})
}

func modifierInstance(t *testing.T, regs ...pulse.RegressionRegistration) (*pulse.Pulse, *types.Cohort) {
	t.Helper()
	if regs == nil {
		regs = modifierRegistrations()
	}
	return openE2E(t, pulse.Extensions{Regressions: regs})
}

// fitOne runs spec alone and returns its result, failing on any error.
func fitOne(t *testing.T, p *pulse.Pulse, cohort *types.Cohort, spec *types.RegressionSpec) *types.RegressionResult {
	t.Helper()
	resp := processE2E(t, p, &types.Request{Cohort: cohort, Regressions: []*types.RegressionSpec{spec}})
	if len(resp.Regressions) != 1 || resp.Regressions[0] == nil || resp.Regressions[0].Error != nil {
		t.Fatalf("regression failed: %+v", resp.Regressions)
	}
	return resp.Regressions[0]
}

// assertModifierParity holds a custom fit's modifier figures to
// REG_OLS's: the coefficients, the replicate standard errors and
// p-values, the echoes (resample, bootstrap counts, jackknife groups,
// selection), n_obs.
func assertModifierParity(t *testing.T, b, e *types.RegressionResult) {
	t.Helper()
	if b.NObs != e.NObs || b.Resample != e.Resample || b.BootstrapRequested != e.BootstrapRequested ||
		b.BootstrapSucceeded != e.BootstrapSucceeded || b.JackknifeGroups != e.JackknifeGroups ||
		b.Selection != e.Selection || b.Criterion != e.Criterion || !slices.Equal(b.SelectedFeatures, e.SelectedFeatures) {
		t.Fatalf("echo: builtin {n %d resample %q B %d/%d g %d sel %q %q %v} extension {n %d resample %q B %d/%d g %d sel %q %q %v}",
			b.NObs, b.Resample, b.BootstrapRequested, b.BootstrapSucceeded, b.JackknifeGroups, b.Selection, b.Criterion, b.SelectedFeatures,
			e.NObs, e.Resample, e.BootstrapRequested, e.BootstrapSucceeded, e.JackknifeGroups, e.Selection, e.Criterion, e.SelectedFeatures)
	}
	for _, m := range []struct {
		name string
		b, e map[string]float64
	}{{"coefficients", b.Coefficients, e.Coefficients}, {"std_errors", b.StdErrors, e.StdErrors}, {"p_values", b.PValues, e.PValues}} {
		if len(m.b) != len(m.e) {
			t.Errorf("%s keys: builtin %v extension %v", m.name, m.b, m.e)
		}
		for k, v := range m.b {
			if got, ok := m.e[k]; !ok || !closeRel(v, got) {
				t.Errorf("%s[%s]: builtin %v extension %v", m.name, k, v, got)
			}
		}
	}
	for k, se := range e.StdErrors {
		if want := e.Coefficients[k] / se; !closeRel(e.Statistics[k], want) {
			t.Errorf("statistics[%s] = %v, want the Wald z %v", k, e.Statistics[k], want)
		}
	}
}

// TestExtensions_RegressionResampleMatchesBuiltin: jackknife (leave one
// out, jackknife_groups, jackknife_by, weighted JK1) and a seeded
// bootstrap on the streaming and the buffered clone equal REG_OLS's own
// resample — the same rows, the same delete groups, the same draws.
func TestExtensions_RegressionResampleMatchesBuiltin(t *testing.T) {
	p, cohort := modifierInstance(t)
	freq := types.SlotWeightOf(types.WeightSpec{Field: "qty", Kind: types.WeightKindFrequency})
	cases := []struct {
		name string
		mut  func(*types.RegressionSpec)
	}{
		{"jackknife", func(s *types.RegressionSpec) { s.Resample = "jackknife" }},
		{"jackknife_groups", func(s *types.RegressionSpec) { s.Resample, s.JackknifeGroups, s.RNGSeed = "jackknife", 6, 7 }},
		{"jackknife_by", func(s *types.RegressionSpec) { s.Resample, s.JackknifeBy = "jackknife", "region" }},
		{"jackknife_weighted", func(s *types.RegressionSpec) { s.Resample, s.Weight, s.Predictors = "jackknife", freq, []string{"day"} }},
		{"bootstrap", func(s *types.RegressionSpec) { s.Resample, s.BootstrapIters, s.RNGSeed = "bootstrap", 200, 11 }},
	}
	for _, ext := range []types.RegressionType{regParityOLS, regParityOLSBuf} {
		for _, tc := range cases {
			t.Run(string(ext)+"/"+tc.name, func(t *testing.T) {
				spec := func(rt types.RegressionType) *types.RegressionSpec {
					s := &types.RegressionSpec{Type: rt, Name: "fit", Target: "score", Predictors: []string{"qty", "day"}}
					tc.mut(s)
					return s
				}
				b, e := fitOne(t, p, cohort, spec(types.REG_OLS)), fitOne(t, p, cohort, spec(ext))
				if e.Resample == "" || e.NObs == 0 {
					t.Fatalf("extension result not resampled: %+v", e)
				}
				assertModifierParity(t, b, e)
			})
		}
	}
}

// TestExtensions_RegressionResampleDeterministic: a bootstrap at
// rng_seed 0 (the data-derived seed) and a jackknife are bit-identical
// across runs and across DecodeWorkers, streaming and buffered.
func TestExtensions_RegressionResampleDeterministic(t *testing.T) {
	large := parityLargeCohort(t.TempDir())(t)
	specs := []*types.RegressionSpec{
		{Type: regParityOLS, Name: "s", Target: "score", Predictors: []string{"qty", "day"}, Resample: "bootstrap", BootstrapIters: 50},
		{Type: regParityOLSBuf, Name: "b", Target: "score", Predictors: []string{"qty"}, Resample: "jackknife", JackknifeGroups: 10},
	}
	run := func(t *testing.T, workers int) string {
		t.Helper()
		p, err := pulse.New(pulse.Options{DataDir: large, DecodeWorkers: workers, Extensions: pulse.Extensions{Regressions: modifierRegistrations()}})
		if err != nil {
			t.Fatal(err)
		}
		resp := processE2E(t, p, &types.Request{Cohort: &types.Cohort{Filename: "large.pulse"}, Regressions: specs})
		for _, r := range resp.Regressions {
			if r == nil || r.Error != nil || r.Resample == "" {
				t.Fatalf("regression failed: %+v", r)
			}
		}
		return mustJSON(t, resp.Regressions)
	}
	want := run(t, 1)
	if again := run(t, 1); again != want {
		t.Fatalf("a rerun differs:\n%s\n%s", again, want)
	}
	for _, w := range []int{2, 4, 7} {
		if got := run(t, w); got != want {
			t.Errorf("DecodeWorkers %d differs from 1:\n%s\n%s", w, got, want)
		}
	}
}

// TestExtensions_RegressionSelectionMatchesBuiltin: forward, backward
// and stepwise selection (aic and bic, alone and under a jackknife) on a
// SupportsSelection model keep REG_OLS's predictors and report its fit.
func TestExtensions_RegressionSelectionMatchesBuiltin(t *testing.T) {
	p, cohort := modifierInstance(t)
	strict := false // some selection kept a strict, non-empty subset
	defer func() {
		if !strict {
			t.Error("every selection kept all or none of the predictors; the fixture no longer exercises selection")
		}
	}()
	for _, sel := range []string{"forward", "backward", "stepwise"} {
		for _, crit := range []string{"aic", "bic"} {
			for _, resample := range []string{"", "jackknife"} {
				t.Run(sel+"_"+crit+"_"+resample, func(t *testing.T) {
					spec := func(rt types.RegressionType) *types.RegressionSpec {
						return &types.RegressionSpec{Type: rt, Name: "fit", Target: "qty", Predictors: []string{"score", "day", "ts"},
							Selection: sel, Criterion: crit, Resample: resample}
					}
					b, e := fitOne(t, p, cohort, spec(types.REG_OLS)), fitOne(t, p, cohort, spec(regParitySelect))
					if n := len(e.SelectedFeatures); n > 0 && n < 3 {
						strict = true
					}
					assertModifierParity(t, b, e)
				})
			}
		}
	}
}

// TestExtensions_RegressionModifierRefusalsParity: every modifier
// refusal on a custom model is the same coded error at predict and at
// runtime — selection without SupportsSelection (PROCESSING_CONFIG
// {regression, slot: selection}), selection without a criterion
// (SERVICE_VALIDATION), incoherent jackknife knobs (regresample), a
// weighted bootstrap (the weight resolver) — and REG_BAYES_LINEAR stays
// refused under both modifiers at runtime (its predict-side refusal is a
// pre-existing gap: runtimeOnly).
func TestExtensions_RegressionModifierRefusalsParity(t *testing.T) {
	p, cohort := modifierInstance(t)
	freq := types.SlotWeightOf(types.WeightSpec{Field: "qty", Kind: types.WeightKindFrequency})
	for _, tc := range []struct {
		name        string
		spec        types.RegressionSpec
		code        perr.Code
		runtimeOnly bool
	}{
		{"selection_undeclared", types.RegressionSpec{Type: regParityOLS, Selection: "forward", Criterion: "aic"}, perr.PROCESSING_CONFIG, false},
		{"selection_no_criterion", types.RegressionSpec{Type: regParitySelect, Selection: "forward"}, perr.SERVICE_VALIDATION, false},
		{"jackknife_knobs", types.RegressionSpec{Type: regParityOLSBuf, Resample: "jackknife", JackknifeGroups: 4, JackknifeBy: "region"}, perr.PROCESSING_CONFIG, false},
		{"jackknife_groups_on_bootstrap", types.RegressionSpec{Type: regParityOLS, Resample: "bootstrap", JackknifeGroups: 4}, perr.PROCESSING_CONFIG, false},
		{"weighted_bootstrap", types.RegressionSpec{Type: regParityOLS, Resample: "bootstrap", Weight: freq}, "", false},
		{"bayes_resample", types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Resample: "bootstrap"}, perr.PROCESSING_CONFIG, true},
		{"bayes_selection", types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Selection: "forward", Criterion: "aic"}, perr.PROCESSING_CONFIG, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.spec
			spec.Name, spec.Target, spec.Predictors = "fit", "score", []string{"qty", "day"}
			req := &types.Request{Cohort: cohort, Regressions: []*types.RegressionSpec{&spec}}
			_, err := p.Process(context.Background(), req)
			var ce *perr.CodedError
			if tc.code != "" {
				ce = requireCodedError(t, err, tc.code)
			} else if !stderrors.As(err, &ce) {
				t.Fatalf("runtime accepted or uncoded: %v", err)
			}
			if tc.runtimeOnly {
				return
			}
			pr, perrv := p.Predict(context.Background(), req)
			if perrv != nil || len(pr.Errors) == 0 {
				t.Fatalf("predict accepted: %v %+v", perrv, pr)
			}
			found := false
			for _, got := range pr.Errors {
				if got.Code == string(ce.Code) && got.Message == ce.Message && reflect.DeepEqual(got.Details, ce.Details) {
					found = true
				}
			}
			if !found {
				t.Errorf("predict errors %+v lack the runtime's %s %q %v", pr.Errors, ce.Code, ce.Message, ce.Details)
			}
			if tc.name == "selection_undeclared" && ce.Details["slot"] != "selection" {
				t.Errorf("details = %v, want slot selection", ce.Details)
			}
		})
	}
}

// renamedModel reports an extra coefficient on any fit over fewer rows
// than the first one it saw — a replicate whose coefficient set
// differs from the full fit's.
type renamedModel struct {
	bufferedOLS
	first *int
}

func (r renamedModel) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	out, err := r.bufferedOLS.Fit(rows)
	if err != nil {
		return out, err
	}
	if *r.first == 0 {
		*r.first = rows.Len()
	}
	if rows.Len() < *r.first {
		out.Coefficients = append(out.Coefficients, extend.RegressionCoefficient{Name: "drift", Estimate: 1})
	}
	return out, nil
}

// slowModel sleeps before every fit.
type slowModel struct{ bufferedOLS }

func (s slowModel) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	time.Sleep(2 * time.Millisecond)
	return s.bufferedOLS.Fit(rows)
}

// TestExtensions_RegressionModifierContract: a replicate reporting
// other coefficients than the full fit is
// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH (replicate_coefficients),
// a SupportsSelection registration whose fit reports no deviance is
// refused at pulse.New (missing_deviance), and the request deadline
// (Options.Limits.RequestTimeout) bounds a slow model's replicate loop.
func TestExtensions_RegressionModifierContract(t *testing.T) {
	t.Run("replicate_coefficients", func(t *testing.T) {
		first := 0
		reg := pulse.RegressionRegistration{Name: "REG_PARITY_RENAMED", Description: "renames on replicates",
			Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
				return renamedModel{bufferedOLS: bufferedOLS{spec: spec}, first: &first}, nil
			}}
		p, cohort := modifierInstance(t, reg)
		first = 0
		_, err := p.Process(context.Background(), &types.Request{Cohort: cohort, Regressions: []*types.RegressionSpec{
			{Type: reg.Name, Name: "fit", Target: "score", Predictors: []string{"qty"}, Resample: "jackknife"}}})
		ce := requireCodedError(t, err, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH)
		if ce.Details["reason"] != "replicate_coefficients" {
			t.Errorf("details = %v", ce.Details)
		}
	})
	t.Run("missing_deviance", func(t *testing.T) {
		regs := regressionParityRegistrations()[:1]
		regs[0].SupportsSelection = true
		_, err := pulse.New(pulse.Options{Extensions: pulse.Extensions{Regressions: regs}})
		ce := requireCodedError(t, err, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH)
		if ce.Details["reason"] != "missing_deviance" {
			t.Errorf("details = %v", ce.Details)
		}
	})
	t.Run("request_timeout_builtin", func(t *testing.T) {
		// The same deadline bounds a built-in modifier's refit loop.
		fsys := afero.NewMemMapFs()
		writeParityCohort(t, fsys, "e2e.pulse", paritySchema(t), 0, paritySmallRows)
		p, err := pulse.New(pulse.Options{FS: fsys, Limits: pulse.Limits{RequestTimeout: 100 * time.Millisecond}})
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		_, err = p.Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "e2e.pulse"}, Regressions: []*types.RegressionSpec{
			{Type: types.REG_OLS, Name: "fit", Target: "score", Predictors: []string{"qty"}, Resample: "bootstrap", BootstrapIters: 5_000_000}}})
		requireCodedError(t, err, perr.PULSE_LIMIT_EXCEEDED)
		if el := time.Since(start); el > 3*time.Second {
			t.Errorf("the deadline did not stop the bootstrap: %v", el)
		}
	})
	t.Run("request_timeout", func(t *testing.T) {
		reg := pulse.RegressionRegistration{Name: "REG_PARITY_SLOW", Description: "sleeps per fit",
			Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
				return slowModel{bufferedOLS{spec: spec}}, nil
			}}
		fsys := afero.NewMemMapFs()
		writeParityCohort(t, fsys, "e2e.pulse", paritySchema(t), 0, paritySmallRows)
		p, err := pulse.New(pulse.Options{FS: fsys, Limits: pulse.Limits{RequestTimeout: 150 * time.Millisecond},
			Extensions: pulse.Extensions{Regressions: []pulse.RegressionRegistration{reg}}})
		if err != nil {
			t.Fatal(err)
		}
		cohort := &types.Cohort{Filename: "e2e.pulse"}
		start := time.Now()
		_, err = p.Process(context.Background(), &types.Request{Cohort: cohort, Regressions: []*types.RegressionSpec{
			{Type: reg.Name, Name: "fit", Target: "score", Predictors: []string{"qty"}, Resample: "bootstrap", BootstrapIters: 5000}}})
		requireCodedError(t, err, perr.PULSE_LIMIT_EXCEEDED)
		if el := time.Since(start); el > 3*time.Second {
			t.Errorf("the deadline did not stop the bootstrap: %v", el)
		}
	})
}
