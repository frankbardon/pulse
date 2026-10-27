package regmmm_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
)

// bayes_engine_test.go holds the built-in REG_MMM_BAYES operator to
// its contract through the engine, on a stock instance (nothing
// registered) and a small one-channel cohort: every params / input
// refusal, the shared-slot refusals identical in predict and at
// runtime, the rng_seed-0 derivation, the posterior coefficient table
// and covariance, the outputs and their diagnostics, the coded
// sampler-health warnings, the model-owned holdout, cancellation
// through RequestTimeout, a sampler failure that fails its slot alone,
// and the model's adstock equal to WIN_DISTRIBUTED_LAG. These are
// operator tests: no media-mix workflow (sweep, budget allocation,
// ROI pass bar) is tested here.

func bayesSchema(weekType encoding.FieldType) encoding.Schema {
	geo := encoding.NewDictionary()
	for _, g := range []string{"east", "west"} {
		_, _ = geo.Add(g)
	}
	fields := []encoding.Field{
		{Name: "geo", Type: encoding.FieldTypeCategoricalU8, Dictionary: geo},
		{Name: "week", Type: weekType, Nullable: true},
		{Name: "tv", Type: encoding.FieldTypeF64},
		{Name: "price", Type: encoding.FieldTypeF64},
		{Name: "sales", Type: encoding.FieldTypeF64},
	}
	s := encoding.Schema{Fields: fields}
	off := 0
	for i := range s.Fields {
		s.Fields[i].CsvColumnIdx = i
		s.Fields[i].ByteOffset = off
		s.Fields[i].Description = "Bayesian media-mix test column " + s.Fields[i].Name + "."
		off += s.FieldWidth(i)
	}
	return s
}

// writeBayesCohort writes rows to name; a negative week is written null.
func writeBayesCohort(t testing.TB, p *pulse.Pulse, name string, weekType encoding.FieldType, rows []regmmm.BayesRow) {
	t.Helper()
	b, err := p.NewCohortBuilder(context.Background(), name, bayesSchema(weekType), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		var week any = uint64(r.Week)
		switch {
		case r.Week < 0:
			week = nil
		case weekType == encoding.FieldTypeF64:
			week = r.Week
		}
		if err := b.Append(pulse.CohortRow{r.Geo, week, r.TV, r.Price, r.Sales}); err != nil {
			t.Fatalf("Append row %d: %v", i, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

// bayesInstance is a stock instance (REG_MMM_BAYES built in) holding
// the small cohort.
func bayesInstance(t testing.TB, mut func(*pulse.Options)) *pulse.Pulse {
	t.Helper()
	p := instance(t, afero.NewMemMapFs(), mut)
	writeBayesCohort(t, p, regmmm.BayesCohort, encoding.FieldTypeU16, regmmm.BayesRows(11))
	return p
}

func bayesRequest(file string, specs ...*types.RegressionSpec) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: file}, Regressions: specs}
}

// countingSampler is NUTS at the run's target_accept, counting runs and
// recording each seed; fail, when set, replaces the run's result.
type countingSampler struct {
	mu           sync.Mutex
	runs         atomic.Int64
	seeds        []uint64
	targetAccept float64
	fail         error
}

func (c *countingSampler) Sample(ctx context.Context, m mcmc.Model, seed uint64, opts mcmc.Options) (mcmc.Result, error) {
	c.runs.Add(1)
	c.mu.Lock()
	c.seeds = append(c.seeds, seed)
	ta := c.targetAccept
	c.mu.Unlock()
	if c.fail != nil {
		return mcmc.Result{}, c.fail
	}
	return mcmc.NUTS{TargetAccept: ta}.Sample(ctx, m, seed, opts)
}

// withSampler swaps the model's sampler for c for the test's duration.
func withSampler(t *testing.T, c *countingSampler) {
	t.Helper()
	regmmm.SetBayesSampler(t, func(targetAccept float64) mcmc.Sampler {
		c.mu.Lock()
		c.targetAccept = targetAccept
		c.mu.Unlock()
		return c
	})
}

// TestMMMBayes_ParamsRefused: every params refusal is PROCESSING_CONFIG
// naming its params slot, at run, before any sampling; a top-level
// unknown key and a missing time are the engine's own refusals, at
// predict and at runtime alike.
func TestMMMBayes_ParamsRefused(t *testing.T) {
	// serial: swaps the package-level MMM sampler hook (bayesSampler)

	c := &countingSampler{}
	withSampler(t, c)
	p := bayesInstance(t, nil)
	for _, tc := range []struct {
		name, params, slot string
	}{
		{"no channels", `{"channels":[],"time":"week"}`, "params.channels"},
		{"channel not a predictor", `{"channels":["radio"],"time":"week"}`, "params.channels"},
		{"channel twice", `{"channels":["tv","tv"],"time":"week"}`, "params.channels"},
		{"empty time", `{"channels":["tv"],"time":""}`, "params.time"},
		{"max_lag 0", `{"channels":["tv"],"time":"week","max_lag":0}`, "params.max_lag"},
		{"max_lag fractional", `{"channels":["tv"],"time":"week","max_lag":1.5}`, "params"},
		{"adstock weibull", `{"channels":["tv"],"time":"week","adstock":"weibull"}`, "params.adstock"},
		{"sampler hmc", `{"channels":["tv"],"time":"week","sampler":"hmc"}`, "params.sampler"},
		{"chains 0", `{"channels":["tv"],"time":"week","chains":0}`, "params.chains"},
		{"chains over cap", `{"channels":["tv"],"time":"week","chains":17}`, "params.chains"},
		{"warmup over cap", `{"channels":["tv"],"time":"week","warmup":20001}`, "params.warmup"},
		{"draws over cap", `{"channels":["tv"],"time":"week","draws":20001}`, "params.draws"},
		{"credible_level 1", `{"channels":["tv"],"time":"week","credible_level":1}`, "params.credible_level"},
		{"target_accept 0", `{"channels":["tv"],"time":"week","target_accept":0}`, "params.target_accept"},
		{"unknown prior family", `{"channels":["tv"],"time":"week","sigma_prior":{"family":"cauchy","sigma":1}}`, "params.sigma_prior"},
		{"prior key unknown", `{"channels":["tv"],"time":"week","control_prior":{"family":"normal","mu":0,"sigma":1,"nu":3}}`, "params"},
		{"prior key of another family", `{"channels":["tv"],"time":"week","control_prior":{"family":"normal","mu":0,"sigma":1,"alpha":3}}`, "params.control_prior"},
		{"prior key missing", `{"channels":["tv"],"time":"week","intercept_prior":{"family":"normal","mu":0}}`, "params.intercept_prior"},
		{"prior value invalid", `{"channels":["tv"],"time":"week","sigma_prior":{"family":"halfnormal","sigma":-1}}`, "params.sigma_prior"},
		{"prior support", `{"channels":["tv"],"time":"week","priors":{"tv":{"decay":{"family":"lognormal","mu":0,"sigma":1}}}}`, "params.priors"},
		{"priors on a control", `{"channels":["tv"],"time":"week","priors":{"price":{"k":{"family":"halfnormal","sigma":1}}}}`, "params.priors"},
		{"roi and beta", `{"channels":["tv"],"time":"week","priors":{"tv":{"roi":{"family":"lognormal","mu":0,"sigma":1},"beta":{"family":"halfnormal","sigma":1}}}}`, "params.priors"},
		{"channel prior key unknown", `{"channels":["tv"],"time":"week","priors":{"tv":{"effect":{"family":"halfnormal","sigma":1}}}}`, "params"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := regmmm.BayesSpec(tc.params)
			_, err := p.Process(context.Background(), bayesRequest(regmmm.BayesCohort, s))
			var ce *perr.CodedError
			if !perr.HasCode(err, perr.PROCESSING_CONFIG) || !errorsAs(err, &ce) {
				t.Fatalf("err = %v, want PROCESSING_CONFIG", err)
			}
			if ce.Details["slot"] != tc.slot || !strings.Contains(ce.Message, `"bayes"`) {
				t.Errorf("details = %v message %q, want slot %s and the slot's name", ce.Details, ce.Message, tc.slot)
			}
		})
	}
	if n := c.runs.Load(); n != 0 {
		t.Errorf("a refused slot sampled %d times", n)
	}

	// An undeclared key is the engine's own allowlist refusal; a missing
	// time is the factory's (a built-in's Required is its factory's to
	// enforce), which predict raises too (BayesSpecRefusal). Both are
	// refused identically in predict.
	for _, tc := range []struct {
		params string
		code   perr.Code
	}{
		{`{"channels":["tv"],"time":"week","chain":2}`, perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{`{"channels":["tv"]}`, perr.PROCESSING_CONFIG},
	} {
		req := bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(tc.params))
		_, err := p.Process(context.Background(), req)
		if !perr.HasCode(err, tc.code) {
			t.Errorf("%s: run err = %v, want %s", tc.params, err, tc.code)
		}
		pr, perrv := p.Predict(context.Background(), req)
		if perrv != nil || len(pr.Errors) == 0 || pr.Errors[0].Code != string(tc.code) {
			t.Errorf("%s: predict = %v %+v, want %s", tc.params, perrv, pr, tc.code)
		}
	}
}

func errorsAs(err error, ce **perr.CodedError) bool { return stderrors.As(err, ce) }

// sharedSlotValues sets each shared RegressionSpec slot an extension
// model may refuse; holdout and save ride the Scorer.
var sharedSlotValues = map[string]string{
	"penalty": `"l2"`, "alpha": `0.5`, "l1_ratio": `0.5`, "unpenalized": `["price"]`,
	"bounds": `{"nonnegative":["tv"]}`, "family": `"gaussian"`, "link": `"identity"`, "max_iters": `10`, "tol": `0.001`,
	"prior": `"normal"`, "prior_mu": `[0,0]`, "prior_precision": `1`, "prior_shape": `1`, "prior_rate": `1`, "credible_level": `0.9`,
	"resample": `"bootstrap"`, "jackknife_groups": `5`, "jackknife_by": `"geo"`, "bootstrap_iters": `10`,
	"selection": `"forward"`, "criterion": `"aic"`, "covariance": `{"type":"hc1"}`, "save": `"m.pulse-model.json"`,
	"holdout": `"week > 50"`,
}

// TestMMMBayes_SharedSlotsRefusedLikePredict: every shared slot the
// registration does not declare — penalty, unpenalized, bounds,
// selection, covariance, family / link / max_iters / tol, prior* and
// credible_level, resample and its knobs, save, holdout — is refused at
// run with the same code, message and details predict's first error
// carries, and never samples. Every such RegressionSpec key is covered,
// so a new one fails here until it is.
func TestMMMBayes_SharedSlotsRefusedLikePredict(t *testing.T) {
	// serial: swaps the package-level MMM sampler hook (bayesSampler)

	c := &countingSampler{}
	withSampler(t, c)
	p := bayesInstance(t, nil)
	rt := reflect.TypeOf(types.RegressionSpec{})
	for i := range rt.NumField() {
		key, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		class, _, ok := descx.RegressionFieldClassOf(key)
		if !ok {
			t.Errorf("RegressionSpec key %q is unclassified", key)
			continue
		}
		if class != descx.RegressionFieldDeclared && class != descx.RegressionFieldOptOut && key != "holdout" {
			continue
		}
		if key == "vcov" {
			continue // declared: the model returns its posterior covariance
		}
		val, ok := sharedSlotValues[key]
		if !ok {
			t.Errorf("shared slot %q has no test value", key)
			continue
		}
		t.Run(key, func(t *testing.T) {
			var m map[string]json.RawMessage
			raw, _ := json.Marshal(regmmm.BayesSpec(regmmm.FastParams))
			_ = json.Unmarshal(raw, &m)
			m[key] = json.RawMessage(val)
			raw, _ = json.Marshal(m)
			var s types.RegressionSpec
			if err := json.Unmarshal(raw, &s); err != nil {
				t.Fatal(err)
			}
			req := bayesRequest(regmmm.BayesCohort, &s)
			_, err := p.Process(context.Background(), req)
			var ce *perr.CodedError
			if err == nil || !errorsAs(err, &ce) {
				t.Fatalf("run accepted %s: %v", key, err)
			}
			pr, perrv := p.Predict(context.Background(), req)
			if perrv != nil || len(pr.Errors) == 0 {
				t.Fatalf("predict accepted %s: %v", key, perrv)
			}
			got := pr.Errors[0]
			if got.Code != string(ce.Code) || got.Message != ce.Message || !reflect.DeepEqual(got.Details, ce.Details) {
				t.Errorf("predict first error %+v, runtime %s %q %v", got, ce.Code, ce.Message, ce.Details)
			}
		})
	}
	if n := c.runs.Load(); n != 0 {
		t.Errorf("a refused shared slot sampled %d times", n)
	}
}

// TestMMMBayes_FitPosterior: a fit returns the posterior: the
// coefficient table in its documented order (the vcov's row keys), each
// Estimate the mean and StdErr the SD of the pooled draws, vcov the
// draws' covariance (symmetric, StdErr² on its diagonal), the decay
// inside (0, 1), and the small cohort's truth recovered within a few
// posterior SDs.
func TestMMMBayes_FitPosterior(t *testing.T) {
	t.Parallel()

	p := bayesInstance(t, nil)
	s := regmmm.BayesSpec(regmmm.FastParams)
	s.Vcov = true
	start := time.Now()
	resp := process(t, p, bayesRequest(regmmm.BayesCohort, s))
	t.Logf("fast fit (60 weeks, 1 channel, 2 chains × (150 + 100)): %v", time.Since(start))
	r := resp.Regressions[0]
	if r.Error != nil || r.Vcov == nil {
		t.Fatalf("fit = %s", mustJSON(t, r))
	}
	want := []string{"(intercept)", "tv", "price", "decay:tv", "k:tv", "s:tv", "sigma"}
	if !slices.Equal(r.Vcov.RowKeys, want) || !slices.Equal(r.Vcov.ColumnKeys, want) || len(r.Coefficients) != len(want) {
		t.Fatalf("vcov keys = %v / %v, coefficients %v; want %v", r.Vcov.RowKeys, r.Vcov.ColumnKeys, r.Coefficients, want)
	}
	v := r.Vcov.Values
	for i, name := range want {
		est, se := r.Coefficients[name], r.StdErrors[name]
		if !(se > 0) || math.IsNaN(est) || math.IsInf(est, 0) {
			t.Errorf("%s: estimate %v, SD %v", name, est, se)
		}
		if se2 := float64(se * se); math.Abs(v[i][i]-se2) > float64(1e-9*se2) {
			t.Errorf("%s: vcov diagonal %v, SD² %v", name, v[i][i], se2)
		}
		for j := range want {
			if v[i][j] != v[j][i] {
				t.Errorf("vcov not symmetric at %d,%d", i, j)
			}
		}
	}
	if d := r.Coefficients["decay:tv"]; !(d > 0 && d < 1) {
		t.Errorf("decay %v outside (0, 1)", d)
	}
	for name, truth := range map[string]float64{"(intercept)": 200, "price": -5, "decay:tv": regmmm.SmallDecay, "sigma": 1} {
		if est, se := r.Coefficients[name], r.StdErrors[name]; math.Abs(est-truth) > float64(4*se)+float64(0.05*math.Abs(truth)) {
			t.Errorf("%s: posterior %v ± %v, truth %v", name, est, se, truth)
		}
	}
	for name, pv := range r.PValues {
		if !math.IsNaN(pv) {
			t.Errorf("p_values[%s] = %v, want none (null)", name, pv)
		}
	}
}

// TestMMMBayes_RequestTimeoutStopsSampling: Limits.RequestTimeout stops
// a long sampler run promptly with PULSE_LIMIT_EXCEEDED
// (request_timeout).
func TestMMMBayes_RequestTimeoutStopsSampling(t *testing.T) {
	// serial: swaps the package-level MMM sampler hook (bayesSampler); wall-clock bound; CPU contention from parallel tests would make it flaky

	c := &countingSampler{}
	withSampler(t, c)
	const d = 300 * time.Millisecond
	p := bayesInstance(t, func(o *pulse.Options) { o.Limits.RequestTimeout = d })
	start := time.Now()
	_, err := p.Process(context.Background(), bayesRequest(regmmm.BayesCohort,
		regmmm.BayesSpec(`{"channels":["tv"],"time":"week","chains":4,"warmup":20000,"draws":20000}`)))
	elapsed := time.Since(start)
	var ce *perr.CodedError
	if !perr.HasCode(err, perr.PULSE_LIMIT_EXCEEDED) || !errorsAs(err, &ce) || ce.Details["limit"] != "request_timeout" {
		t.Fatalf("err = %v, want PULSE_LIMIT_EXCEEDED request_timeout", err)
	}
	if c.runs.Load() != 1 {
		t.Errorf("sampler runs = %d, want the run the timeout stopped", c.runs.Load())
	}
	if elapsed > 5*time.Second {
		t.Errorf("returned after %v, want promptly after the %v timeout", elapsed, d)
	}
}

// TestMMMBayes_SamplerFailureFailsSlotAlone: a sampler that cannot run
// on the posterior (PULSE_MCMC_FAILED, or a start with a non-finite
// density) is PROCESSING_REGRESSION_NO_CONVERGE on the slot, which
// fails alone (its identity kept, PULSE_SLOT_FAILED warned, the other
// slot fitted); under Strict the run fails with that code
// (TestMMMBayes_SamplerErrorPassThrough: any other sampler error passes
// through).
func TestMMMBayes_SamplerFailureFailsSlotAlone(t *testing.T) {
	// serial: swaps the package-level MMM sampler hook (bayesSampler)

	for _, fail := range []error{
		perr.NewCodedErrorWithDetails(perr.PULSE_MCMC_FAILED, "mcmc: no start", map[string]any{"reason": "init_not_found", "chain": 0}),
		perr.NewCodedErrorWithDetails(perr.PULSE_MCMC_INVALID, "mcmc: init", map[string]any{"reason": "init_density_not_finite", "chain": 1}),
	} {
		c := &countingSampler{fail: fail}
		withSampler(t, c)
		p := bayesInstance(t, nil)
		req := func() *types.Request {
			return bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(regmmm.FastParams),
				&types.RegressionSpec{Type: types.REG_OLS, Name: "ols", Target: "sales", Predictors: []string{"tv"}})
		}
		resp := process(t, p, req())
		b, ols := resp.Regressions[0], resp.Regressions[1]
		if b.Error == nil || b.Error.Code != string(perr.PROCESSING_REGRESSION_NO_CONVERGE) || b.Name != "bayes" || b.Type != regmmm.BayesName {
			t.Fatalf("bayes slot = %s, want its identity and NO_CONVERGE", mustJSON(t, b))
		}
		ce := fail.(*perr.CodedError)
		if b.Error.Details["reason"] != ce.Details["reason"] || !strings.Contains(b.Error.Message, `"bayes"`) {
			t.Errorf("slot error = %s, want reason %v and the slot's name", mustJSON(t, b.Error), ce.Details["reason"])
		}
		if ols.Error != nil {
			t.Errorf("ols slot = %s, want a fit", mustJSON(t, ols))
		}
		warned := false
		for _, w := range resp.Warnings {
			warned = warned || w.Code == string(perr.PULSE_SLOT_FAILED)
		}
		if !warned {
			t.Errorf("warnings = %s, want PULSE_SLOT_FAILED", mustJSON(t, resp.Warnings))
		}
		strict := instance(t, afero.NewMemMapFs(), func(o *pulse.Options) { o.Strict = true })
		writeBayesCohort(t, strict, regmmm.BayesCohort, encoding.FieldTypeU16, regmmm.BayesRows(11))
		if _, err := strict.Process(context.Background(), req()); !perr.HasCode(err, perr.PROCESSING_REGRESSION_NO_CONVERGE) {
			t.Errorf("strict: %v, want NO_CONVERGE", err)
		}
	}
}

// summaryKeys is every posterior summary's key set.
var summaryKeys = []string{"lower", "mean", "median", "sd", "upper"}

// checkSummary fails unless s is a finite, ordered summary.
func checkSummary(t *testing.T, what string, s any) map[string]float64 {
	t.Helper()
	m, ok := s.(map[string]float64)
	if !ok {
		t.Fatalf("%s = %T %v, want a summary", what, s, s)
	}
	keys := make([]string, 0, len(m))
	for k, v := range m {
		keys = append(keys, k)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("%s.%s = %v, want finite", what, k, v)
		}
	}
	slices.Sort(keys)
	if !slices.Equal(keys, summaryKeys) {
		t.Errorf("%s keys = %v, want %v", what, keys, summaryKeys)
	}
	if !(m["lower"] <= m["median"] && m["median"] <= m["upper"] && m["lower"] < m["upper"]) || !(m["sd"] > 0) {
		t.Errorf("%s = %v: want lower ≤ median ≤ upper, lower < upper, sd > 0", what, m)
	}
	return m
}

// doctoredSampler runs NUTS, then marks divergences and tree-depth hits
// on chain 0 and, when split, shifts one chain of the intercept's θ so
// the chains disagree.
type doctoredSampler struct {
	divergences, saturated int
	split                  bool
}

func (s doctoredSampler) Sample(ctx context.Context, m mcmc.Model, seed uint64, opts mcmc.Options) (mcmc.Result, error) {
	res, err := mcmc.NUTS{}.Sample(ctx, m, seed, opts)
	if err != nil {
		return res, err
	}
	res.Diagnostics.Chains[0].Divergences = s.divergences
	res.Diagnostics.Chains[0].TreeDepthSaturated = s.saturated
	if s.split {
		for d := range res.Draws[1][0] {
			res.Draws[1][0][d] += 5
		}
	}
	return res, nil
}

// warningReasons is the PROCESSING_REGRESSION_DIAGNOSTIC reasons in ws.
func warningReasons(t *testing.T, ws []*types.ResponseWarning) []string {
	t.Helper()
	var out []string
	for _, w := range ws {
		if w.Code != string(perr.PROCESSING_REGRESSION_DIAGNOSTIC) {
			continue
		}
		reason, _ := w.Details["reason"].(string)
		out = append(out, reason)
	}
	return out
}

// TestMMMBayes_AdstockMatchesDistributedLag: the model's adstock of
// each joint-fixture channel at its true decay is bit-equal to
// WIN_DISTRIBUTED_LAG geometric with max_lag 13 and normalize false, run
// by the engine over the same cohort.
func TestMMMBayes_AdstockMatchesDistributedLag(t *testing.T) {
	t.Parallel()

	p := instance(t, afero.NewMemMapFs(), nil)
	d := mmmfixture.Joint()
	b, err := p.NewCohortBuilder(context.Background(), mmmfixture.JointCohortFile, mmmfixture.JointSchema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range d.Rows {
		if err := b.Append(pulse.CohortRow(r)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	req := &types.Request{Cohort: &types.Cohort{Filename: mmmfixture.JointCohortFile}}
	for _, c := range mmmfixture.JointChannels {
		req.Windows = append(req.Windows, &types.Window{Type: types.WIN_DISTRIBUTED_LAG, Field: c.Name, Label: c.Name + "_ad",
			OrderBy: []types.OrderKey{{Field: "week"}},
			Params:  json.RawMessage(fmt.Sprintf(`{"kernel":"geometric","decay":%v,"max_lag":%d,"normalize":false}`, c.Decay, regmmm.DefaultMaxLag))})
	}
	resp := process(t, p, req)
	if len(resp.Data) != mmmfixture.JointWeeks {
		t.Fatalf("got %d rows, want %d", len(resp.Data), mmmfixture.JointWeeks)
	}
	slices.SortFunc(resp.Data, func(a, b map[string]any) int { return int(toF(a["week"]) - toF(b["week"])) })
	for _, c := range mmmfixture.JointChannels {
		w, dw := regmmm.GeometricWeights(c.Decay, regmmm.DefaultMaxLag)
		a := make([]float64, mmmfixture.JointWeeks)
		regmmm.Adstock(d.Spend[c.Name], w, dw, a, make([]float64, len(a)))
		for i, row := range resp.Data {
			want := toF(row[c.Name+"_ad"])
			if math.Float64bits(a[i]) != math.Float64bits(want) {
				t.Fatalf("%s week %d: model adstock %v (%#x), WIN_DISTRIBUTED_LAG %v (%#x)",
					c.Name, i+1, a[i], math.Float64bits(a[i]), want, math.Float64bits(want))
			}
		}
	}
}

func toF(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case uint64:
		return float64(x)
	case int64:
		return float64(x)
	case int:
		return float64(x)
	}
	panic(fmt.Sprintf("not a number: %T", v))
}
