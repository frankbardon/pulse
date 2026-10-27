package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// extensions_regression_nls_test.go runs the reference extension
// REG_TESTNLS_EXP (extensions_testnls_exp_test.go) through the public
// facade: an iterative fit recovers its truth and is worker-invariant,
// its Scorer feeds ATTR_REG_PREDICT and a saved model reloads into a
// fresh instance, resample: bootstrap refits it, and the manifest,
// predict and a feature profile treat it as any registered model.

const (
	nlsCohort = "nls.pulse"
	nlsRows   = 60
)

// nlsTruth is the generating curve: y = 2 + 5·(1 − exp(−x/1.5)) plus a
// small deterministic wiggle.
var nlsTruth = struct{ a, b, tau float64 }{2, 5, 1.5}

// nlsInstance opens an instance over fsys with REG_TESTNLS_EXP
// registered; mut (when set) edits the options first.
func nlsInstance(t testing.TB, fsys afero.Fs, mut func(*pulse.Options)) *pulse.Pulse {
	t.Helper()
	opts := pulse.Options{FS: fsys, Extensions: pulse.Extensions{Regressions: []pulse.RegressionRegistration{testNLSExpRegistration()}}}
	if mut != nil {
		mut(&opts)
	}
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p
}

// writeNLSCohort writes the cohort: i (the row index), x = 0.1·i and y.
func writeNLSCohort(t testing.TB, p *pulse.Pulse) {
	t.Helper()
	f64 := func(name string) encoding.Field {
		return encoding.Field{Name: name, Type: encoding.FieldTypeF64, Description: "Reference nonlinear-fit column " + name}
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "i", Type: encoding.FieldTypeU16, Description: "Row index of the reference nonlinear-fit cohort"}, f64("x"), f64("y"),
	}}
	b, err := p.NewCohortBuilder(context.Background(), nlsCohort, schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range nlsRows {
		x := float64(0.1 * float64(i))
		y := nlsTruth.a + float64(nlsTruth.b*(1-math.Exp(-x/nlsTruth.tau))) + float64(0.01*float64((i*7)%5-2))
		if err := b.Append(pulse.CohortRow{uint64(i), x, y}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func nlsSpec() *types.RegressionSpec {
	return &types.RegressionSpec{Type: regTestNLSExp, Name: "curve", Target: "y", Predictors: []string{"x"}}
}

func nlsFitRequest(spec *types.RegressionSpec) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: nlsCohort}, Regressions: []*types.RegressionSpec{spec}}
}

func fitNLS(t *testing.T, p *pulse.Pulse, spec *types.RegressionSpec) *types.RegressionResult {
	t.Helper()
	resp, err := p.Process(context.Background(), nlsFitRequest(spec))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
		t.Fatalf("regressions = %s", mustJSON(t, resp.Regressions))
	}
	return resp.Regressions[0]
}

// TestExtensions_RegressionNLSRecoversTruth: the iterative fit recovers
// the generating curve (the wiggle is ±0.02 on a gain of 5), reports
// its iterations and τ output, leaves the standard errors null, and is
// bit-identical across DecodeWorkers.
func TestExtensions_RegressionNLSRecoversTruth(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	p := nlsInstance(t, fsys, nil)
	writeNLSCohort(t, p)
	r := fitNLS(t, p, nlsSpec())
	tau, _ := r.Outputs["tau"].(float64)
	for name, got := range map[string][2]float64{
		"(intercept)": {r.Coefficients["(intercept)"], nlsTruth.a},
		"x":           {r.Coefficients["x"], nlsTruth.b},
		"tau":         {tau, nlsTruth.tau},
	} {
		if math.Abs(got[0]-got[1]) > 0.01*got[1] {
			t.Errorf("%s = %v, want %v within 1%%", name, got[0], got[1])
		}
	}
	if r.NObs != nlsRows || r.ConvergedIters < 1 {
		t.Errorf("n_obs %d (want %d), converged_iters %d", r.NObs, nlsRows, r.ConvergedIters)
	}
	for name, se := range r.StdErrors {
		if !math.IsNaN(se) {
			t.Errorf("std_errors.%s = %v, want null (the model reports none)", name, se)
		}
	}
	want := mustJSON(t, r)
	for _, dw := range []int{1, 8} {
		q := nlsInstance(t, fsys, func(o *pulse.Options) { o.DecodeWorkers = dw })
		if got := mustJSON(t, fitNLS(t, q, nlsSpec())); got != want {
			t.Fatalf("DecodeWorkers %d fit differs:\n%s\n%s", dw, got, want)
		}
	}
}

// nlsScoreSlot scores every row two ways, summed per row index: p by
// ATTR_REG_PREDICT through the registration's Scorer, f by an
// ATTR_FORMULA writing the same curve over the fit's reported figures.
func nlsScoreSlot(fit *types.RegressionResult, models ...types.ModelRef) *types.Request {
	formula := fmt.Sprintf("%.17g + %.17g*(1 - exp(-x / %.17g))",
		fit.Coefficients["(intercept)"], fit.Coefficients["x"], fit.Outputs["tau"].(float64))
	return &types.Request{
		Label:  "score",
		Cohort: &types.Cohort{Filename: nlsCohort},
		Models: models,
		Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "i", Interval: 1}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"curve"}`)},
			{Type: types.ATTR_FORMULA, Label: "f", Expression: formula},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "p", Label: "p"},
			{Type: types.AGG_SUM, Field: "f", Label: "f"},
		},
	}
}

// TestExtensions_RegressionNLSScorerSavesReloads: ATTR_REG_PREDICT
// scores a Compose fit through the Scorer as the curve written as an
// expression does (relative 1e-12); the fit saves as a model file
// carrying its tau output, and a fresh instance reloads it and scores
// bit-identically, predict accepting the load.
func TestExtensions_RegressionNLSScorerSavesReloads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	p := nlsInstance(t, fsys, nil)
	writeNLSCohort(t, p)
	const save = "models/curve.pulse-model.json"
	direct := fitNLS(t, p, nlsSpec())
	out, err := p.Compose(ctx, &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "curve", Request: nlsFitRequest(nlsSpec()), Save: save}},
		Requests: []*types.Request{nlsScoreSlot(direct)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mustJSON(t, out.Fits["curve"].RegressionResult), mustJSON(t, direct); got != want {
		t.Fatalf("the Compose fit differs from Process':\n%s\n%s", got, want)
	}
	data := out.Responses[0].Data
	if len(data) != nlsRows {
		t.Fatalf("%d score rows, want %d", len(data), nlsRows)
	}
	for _, row := range data {
		pv, fv := row["p"].(float64), row["f"].(float64)
		if math.Abs(pv-fv) > 1e-12*math.Abs(fv) {
			t.Fatalf("row %v: scorer %v, formula %v", row["i"], pv, fv)
		}
	}

	raw, err := afero.ReadFile(fsys, save)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if outs, _ := doc["outputs"].(map[string]any); doc["type"] != string(regTestNLSExp) || outs["tau"] == nil {
		t.Fatalf("saved model: type %v outputs %v", doc["type"], doc["outputs"])
	}

	fresh := nlsInstance(t, fsys, nil)
	loadReq := nlsScoreSlot(direct, types.ModelRef{Name: "curve", Path: save})
	loaded, err := fresh.Process(ctx, loadReq)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := mustJSON(t, loaded.Data), mustJSON(t, data); a != b {
		t.Fatalf("the reloaded model scores differently:\n%s\n%s", a, b)
	}
	cohort, err := afero.ReadFile(fsys, nlsCohort)
	if err != nil {
		t.Fatal(err)
	}
	env, err := fresh.PredictBytes(ctx, cohort, loadReq)
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict on the load: %v %+v", err, env.Errors)
	}
}

// TestExtensions_RegressionNLSBootstrap: resample: bootstrap refits the
// model on every replicate — each coefficient gains a finite, positive
// replicate standard error and a p-value where the plain fit has none,
// the estimates stay the full fit's, and the run is reproducible.
func TestExtensions_RegressionNLSBootstrap(t *testing.T) {
	t.Parallel()

	p := nlsInstance(t, afero.NewMemMapFs(), nil)
	writeNLSCohort(t, p)
	spec := func() *types.RegressionSpec {
		s := nlsSpec()
		s.Resample, s.BootstrapIters, s.RNGSeed = "bootstrap", 12, 11
		return s
	}
	boot := fitNLS(t, p, spec())
	plain := fitNLS(t, p, nlsSpec())
	if boot.BootstrapRequested != 12 || boot.BootstrapSucceeded != 12 {
		t.Errorf("bootstrap requested %d succeeded %d, want 12 / 12", boot.BootstrapRequested, boot.BootstrapSucceeded)
	}
	for name, est := range plain.Coefficients {
		if boot.Coefficients[name] != est {
			t.Errorf("%s: bootstrap estimate %v, full fit %v", name, boot.Coefficients[name], est)
		}
		se, pv := boot.StdErrors[name], boot.PValues[name]
		if !(se > 0) || math.IsInf(se, 0) || math.IsNaN(pv) {
			t.Errorf("%s: replicate se %v p %v, want finite positive se and a p-value", name, se, pv)
		}
	}
	if a, b := mustJSON(t, boot), mustJSON(t, fitNLS(t, p, spec())); a != b {
		t.Fatalf("bootstrap not reproducible:\n%s\n%s", a, b)
	}
}

// TestExtensions_RegressionNLSSelfDescribed: the manifest lists
// REG_TESTNLS_EXP as a buffered model with its declared tau output, and
// predict accepts a fit as non-streamable; a feature profile that
// leaves it out hides it from the manifest and refuses it at Process
// and Predict exactly as a never-registered model.
func TestExtensions_RegressionNLSSelfDescribed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	p := nlsInstance(t, fsys, nil)
	writeNLSCohort(t, p)

	var meta *descriptor.RegressionMeta
	regs := p.Manifest(ctx).Extensions.Regressions
	for i := range regs {
		if regs[i].Name == string(regTestNLSExp) {
			meta = &regs[i]
		}
	}
	if meta == nil {
		t.Fatalf("manifest extensions.regressions lacks %s", regTestNLSExp)
	}
	var outputs []string
	for _, o := range meta.OutputSchema {
		outputs = append(outputs, o.Key)
	}
	if meta.Streamable || !slices.Equal(outputs, []string{"tau"}) {
		t.Errorf("manifest: streamable %v, output_schema keys %v; want buffered, [tau]", meta.Streamable, outputs)
	}
	req := nlsFitRequest(nlsSpec())
	pr, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !pr.Valid || pr.Streamable {
		t.Errorf("predict valid %v streamable %v, want valid and buffered (errors %+v)", pr.Valid, pr.Streamable, pr.Errors)
	}

	hidden := nlsInstance(t, fsys, func(o *pulse.Options) {
		o.FeatureProfile = &pulse.FeatureProfile{Features: []string{"capability:process", string(types.REG_OLS)}}
	})
	never, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range hidden.Manifest(ctx).Extensions.Regressions {
		if m.Name == string(regTestNLSExp) {
			t.Error("profile-hidden model still in the manifest")
		}
	}
	_, hiddenErr := hidden.Process(ctx, req)
	_, neverErr := never.Process(ctx, req)
	if hiddenErr == nil || neverErr == nil || hiddenErr.Error() != neverErr.Error() || mustJSON(t, hiddenErr) != mustJSON(t, neverErr) {
		t.Errorf("hidden refusal %v, never-registered %v; want the same refusal", hiddenErr, neverErr)
	}
	hp, err := hidden.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	np, err := never.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if hp.Valid || mustJSON(t, hp.Errors) != mustJSON(t, np.Errors) {
		t.Errorf("hidden predict errors %+v, never-registered %+v", hp.Errors, np.Errors)
	}
}
