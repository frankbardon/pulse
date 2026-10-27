package regmmm_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/regmmm"
)

// bayes_builtin_test.go pins what being BUILT IN means for
// REG_MMM_BAYES (mmm-followups E2-S2): a stock instance lists it among
// the built-in regressions with no Extensions; params.time is decoded by
// the projection and refused when unknown, at predict and at runtime
// alike; every spec refusal is predict's too; the engine's output keys
// are the descriptor's applicability row and every one has a built-in
// Interpretation; an embedder can no longer register the name.

// TestMMMBayes_Registration: the stock manifest lists REG_MMM_BAYES
// among the built-in regressions (never among extensions): buffered,
// with its own params and outputs and no resample, holdout, save,
// weight or multiplicity slot; its factory builds a buffered,
// context-aware model.
func TestMMMBayes_Registration(t *testing.T) {
	t.Parallel()

	p := instance(t, afero.NewMemMapFs(), nil)
	m := p.Manifest(context.Background())
	for _, row := range m.Extensions.Regressions {
		if row.Name == string(regmmm.BayesName) {
			t.Fatal("manifest lists REG_MMM_BAYES as an extension")
		}
	}
	var row *struct {
		params, slots, outputs []string
		streamable             bool
	}
	for _, r := range m.Regressions {
		if r.Name != string(regmmm.BayesName) {
			continue
		}
		row = &struct {
			params, slots, outputs []string
			streamable             bool
		}{streamable: r.Streamable}
		for _, prm := range r.Params {
			if prm.Slot {
				row.slots = append(row.slots, prm.Name)
			} else {
				row.params = append(row.params, prm.Name)
			}
		}
		for _, o := range r.OutputSchema {
			row.outputs = append(row.outputs, o.Key)
		}
	}
	if row == nil {
		t.Fatal("manifest regressions lacks REG_MMM_BAYES")
	}
	if row.streamable {
		t.Error("manifest: REG_MMM_BAYES listed as streamable")
	}
	wantParams := []string{"channels", "time", "max_lag", "adstock", "priors", "intercept_prior", "control_prior", "sigma_prior",
		"chains", "warmup", "draws", "credible_level", "sampler", "target_accept", "holdout_from"}
	if !slices.Equal(row.params, wantParams) {
		t.Errorf("params = %v, want %v", row.params, wantParams)
	}
	for _, s := range row.slots {
		if slices.Contains([]string{"resample", "bootstrap_iters", "holdout", "save", "weight", "multiplicity"}, s) {
			t.Errorf("manifest lists slot %s", s)
		}
	}
	if want := []string{"seed", "roi", "mroi", "contribution", "params", "diagnostics", "holdout"}; !slices.Equal(row.outputs, want) {
		t.Errorf("outputs = %v, want %v", row.outputs, want)
	}
	model, err := regmmm.BayesFactory(regmmm.BayesSpec(regmmm.FastParams), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := model.(extend.ContextRegression); !ok {
		t.Error("the model does not implement extend.ContextRegression")
	}
	if _, ok := model.(extend.BufferedRegression); !ok {
		t.Error("the model does not implement extend.BufferedRegression")
	}
}

// TestMMMBayes_TimeFieldProjected: params.time is the one field read
// beyond the target and predictors — the projected decode carries it
// (the default-on projection would otherwise leave the record without
// it, a time_missing refusal), and an unknown one is refused by predict
// and by the runtime with the same code, message and details, before
// any sampling.
func TestMMMBayes_TimeFieldProjected(t *testing.T) {
	// serial: swaps the package-level MMM sampler hook (bayesSampler)

	c := &countingSampler{}
	withSampler(t, c)
	ctx := context.Background()
	p := bayesInstance(t, nil)
	r := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(regmmm.FastParams))).Regressions[0]
	if r.Error != nil {
		t.Fatalf("projected fit = %s", mustJSON(t, r))
	}
	req := bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(`{"channels":["tv"],"time":"wk"}`))
	runs := c.runs.Load()
	_, rerr := p.Process(ctx, req)
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) {
		t.Fatalf("runtime: %v, want a coded refusal", rerr)
	}
	pr, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(pr.Errors) == 0 || pr.Errors[0].Code != string(ce.Code) || pr.Errors[0].Message != ce.Message ||
		mustJSON(t, pr.Errors[0].Details) != mustJSON(t, ce.Details) {
		t.Fatalf("predict errors %s, runtime %s %q %s", mustJSON(t, pr.Errors), ce.Code, ce.Message, mustJSON(t, ce.Details))
	}
	if !strings.Contains(ce.Message, "wk") {
		t.Errorf("refusal %q does not name the unknown time field", ce.Message)
	}
	if n := c.runs.Load() - runs; n != 0 {
		t.Errorf("an unknown time field sampled %d times", n)
	}
}

// TestMMMBayes_SpecRefusalsPredictMatchRuntime: every factory refusal
// reads the spec alone, so predict raises it with the runtime's code,
// message and details — before, the extension form could not run its
// factory at predict and such a slot predicted clean. A data refusal
// (a gap, a negative spend), which only the rows show, stays
// runtime-only.
func TestMMMBayes_SpecRefusalsPredictMatchRuntime(t *testing.T) {
	// serial: swaps the package-level MMM sampler hook (bayesSampler)

	c := &countingSampler{}
	withSampler(t, c)
	ctx := context.Background()
	p := bayesInstance(t, nil)
	for name, params := range map[string]string{
		"no channels":             `{"channels":[],"time":"week"}`,
		"channel not a predictor": `{"channels":["radio"],"time":"week"}`,
		"channel twice":           `{"channels":["tv","tv"],"time":"week"}`,
		"max_lag 0":               `{"channels":["tv"],"time":"week","max_lag":0}`,
		"adstock":                 `{"channels":["tv"],"time":"week","adstock":"weibull"}`,
		"chains over cap":         `{"channels":["tv"],"time":"week","chains":17}`,
		"credible_level":          `{"channels":["tv"],"time":"week","credible_level":1}`,
		"prior family":            `{"channels":["tv"],"time":"week","sigma_prior":{"family":"cauchy","sigma":1}}`,
		"prior support":           `{"channels":["tv"],"time":"week","priors":{"tv":{"decay":{"family":"lognormal","mu":0,"sigma":1}}}}`,
		"prior on a control":      `{"channels":["tv"],"time":"week","priors":{"price":{"k":{"family":"halfnormal","sigma":1}}}}`,
		"roi and beta":            `{"channels":["tv"],"time":"week","priors":{"tv":{"roi":{"family":"lognormal","mu":0,"sigma":1},"beta":{"family":"halfnormal","sigma":1}}}}`,
		"predictor twice":         "twice",
	} {
		t.Run(name, func(t *testing.T) {
			s := regmmm.BayesSpec(params)
			if params == "twice" {
				s = regmmm.BayesSpec(regmmm.FastParams)
				s.Predictors = []string{"tv", "price", "price"}
			}
			req := bayesRequest(regmmm.BayesCohort, s)
			_, rerr := p.Process(ctx, req)
			var ce *perr.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != perr.PROCESSING_CONFIG {
				t.Fatalf("runtime: %v, want PROCESSING_CONFIG", rerr)
			}
			pr, err := p.Predict(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if len(pr.Errors) != 1 || pr.Errors[0].Code != string(ce.Code) || pr.Errors[0].Message != ce.Message ||
				mustJSON(t, pr.Errors[0].Details) != mustJSON(t, ce.Details) {
				t.Fatalf("predict errors %s, runtime %s %q %s", mustJSON(t, pr.Errors), ce.Code, ce.Message, mustJSON(t, ce.Details))
			}
		})
	}
	if n := c.runs.Load(); n != 0 {
		t.Errorf("a refused spec sampled %d times", n)
	}
	// Rows only: predict cannot see a gap in the series.
	rows := regmmm.BayesRows(11)
	writeBayesCohort(t, p, "gap.pulse", encoding.FieldTypeU16, append(slices.Clone(rows[:20]), rows[21:]...))
	pr, err := p.Predict(ctx, bayesRequest("gap.pulse", regmmm.BayesSpec(regmmm.FastParams)))
	if err != nil || len(pr.Errors) != 0 {
		t.Fatalf("predict on a data-only refusal: %v %+v", err, pr)
	}
}

// TestMMMBayes_OutputsMatchApplicability is the end-to-end twin of the
// engine's TestRegressionOutputsHoldAtRuntime over the stock
// instance's full path (projection, predict, envelope): on a stock instance a plain fit
// emits exactly the fit-output keys its applicability row lists (the
// echoes name and type aside, and the coded warnings a short fit
// carries).
func TestMMMBayes_OutputsMatchApplicability(t *testing.T) {
	t.Parallel()

	p := bayesInstance(t, nil)
	r := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(regmmm.FastParams))).Regressions[0]
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(mustJSON(t, r)), &keys); err != nil {
		t.Fatal(err)
	}
	var got []string
	for k := range keys {
		if k != "name" && k != "type" && k != "warnings" {
			got = append(got, k)
		}
	}
	sort.Strings(got)
	want := descx.RegressionOutputKeys(string(regmmm.BayesName))
	if !slices.Equal(got, want) {
		t.Fatalf("a plain fit emits %v, the applicability row lists %v", got, want)
	}
}

// TestMMMBayes_OutputSchemaInterpreted: the built-in Interpretation
// reads the coefficient table and Outputs through its one outputs.*
// reading (the applicability row's map pattern), which names every
// declared output key.
func TestMMMBayes_OutputSchemaInterpreted(t *testing.T) {
	t.Parallel()

	var outputs, coefs bool
	for _, in := range descx.BuiltinInterpretations()[string(regmmm.BayesName)] {
		switch in.Field {
		case "coefficients.*":
			coefs = true
		case "outputs.*":
			outputs = true
			for _, o := range regmmm.BayesOutputSchema {
				if !strings.Contains(in.Means, o.Key) {
					t.Errorf("outputs.* reading does not name output %s", o.Key)
				}
			}
		}
	}
	if !outputs || !coefs {
		t.Errorf("outputs.* read %v, coefficients.* read %v; want both", outputs, coefs)
	}
}

// TestMMMBayes_EmbedderCannotRegisterName: REG_MMM_BAYES is a built-in
// name, so an embedder registration under it (the retired pack's) is
// refused at pulse.New with PULSE_EXTENSION_NAME_COLLISION.
func TestMMMBayes_EmbedderCannotRegisterName(t *testing.T) {
	t.Parallel()

	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{
		Regressions: []pulse.RegressionRegistration{{Name: regmmm.BayesName, Factory: regmmm.BayesFactory}},
	}})
	if !perr.HasCode(err, perr.PULSE_EXTENSION_NAME_COLLISION) {
		t.Fatalf("pulse.New: %v, want PULSE_EXTENSION_NAME_COLLISION", err)
	}
}
