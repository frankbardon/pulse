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

// TestMMMBayes_InputsRefused: a gap, a repeat (a panel of two markets,
// grouped request or not), a non-integer or null time and a negative
// spend are PROCESSING_CONFIG naming params.time / the channel, before
// any sampling; rows written out of time order fit exactly as the
// sorted rows do.
func TestMMMBayes_InputsRefused(t *testing.T) {
	c := &countingSampler{}
	withSampler(t, c)
	p := bayesInstance(t, nil)
	base := regmmm.BayesRows(11)
	mutate := func(f func([]regmmm.BayesRow) []regmmm.BayesRow) []regmmm.BayesRow { return f(slices.Clone(base)) }
	for _, tc := range []struct {
		name, reason string
		weekType     encoding.FieldType
		rows         []regmmm.BayesRow
		groups       bool
	}{
		{"gap", "time_gap", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow { return slices.Delete(r, 20, 21) }), false},
		{"duplicate", "duplicate_time", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow { r[5].Week = r[4].Week; return r }), false},
		{"panel", "duplicate_time", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow {
			for _, row := range base {
				row.Geo = "west"
				r = append(r, row)
			}
			return r
		}), false},
		{"grouped panel", "duplicate_time", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow {
			for _, row := range base {
				row.Geo = "west"
				r = append(r, row)
			}
			return r
		}), true},
		{"fractional time", "time_not_integer", encoding.FieldTypeF64, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow { r[3].Week = 3.5; return r }), false},
		{"null time", "time_missing", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow { r[3].Week = -1; return r }), false},
		{"negative spend", "negative_spend", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow { r[7].TV = -1; return r }), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := strings.ReplaceAll(tc.name, " ", "_") + ".pulse"
			writeBayesCohort(t, p, file, tc.weekType, tc.rows)
			req := bayesRequest(file, regmmm.BayesSpec(regmmm.FastParams))
			if tc.groups {
				req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "geo"}}
			}
			_, err := p.Process(context.Background(), req)
			var ce *perr.CodedError
			if !perr.HasCode(err, perr.PROCESSING_CONFIG) || !errorsAs(err, &ce) {
				t.Fatalf("err = %v, want PROCESSING_CONFIG", err)
			}
			if ce.Details["reason"] != tc.reason {
				t.Errorf("details = %v, want reason %s", ce.Details, tc.reason)
			}
		})
	}
	if n := c.runs.Load(); n != 0 {
		t.Errorf("a refused input sampled %d times", n)
	}

	// Out-of-order rows sort by time before the model reads them.
	writeBayesCohort(t, p, "reversed.pulse", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow { slices.Reverse(r); return r }))
	sorted := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(regmmm.FastParams))).Regressions[0]
	reversed := process(t, p, bayesRequest("reversed.pulse", regmmm.BayesSpec(regmmm.FastParams))).Regressions[0]
	if a, b := mustJSON(t, sorted.Coefficients), mustJSON(t, reversed.Coefficients); a != b || sorted.Error != nil {
		t.Errorf("reversed rows fit differently:\n%s\n%s", a, b)
	}
}

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

// TestMMMBayes_Seed: rng_seed 0 derives the seed from the slot's wire
// form alone (rng_seed excluded): the sampler gets it, outputs.seed
// echoes it, the same request gives the same bytes, different data
// gives the same seed, a different slot another; a non-zero rng_seed is
// used as is.
func TestMMMBayes_Seed(t *testing.T) {
	c := &countingSampler{}
	withSampler(t, c)
	p := bayesInstance(t, nil)
	writeBayesCohort(t, p, "other.pulse", encoding.FieldTypeU16, regmmm.BayesRows(12))

	s := regmmm.BayesSpec(regmmm.FastParams)
	want := regmmm.BayesEffectiveSeed(s)
	cp := *s
	cp.RNGSeed = 0
	if body, _ := json.Marshal(&cp); want == 0 || !strings.Contains(string(body), `"params"`) {
		t.Fatalf("derived seed %d over %s", want, body)
	}
	first := process(t, p, bayesRequest(regmmm.BayesCohort, s)).Regressions[0]
	second := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(regmmm.FastParams))).Regressions[0]
	if a, b := mustJSON(t, first), mustJSON(t, second); a != b {
		t.Errorf("same request, different bytes:\n%s\n%s", a, b)
	}
	if got := first.Outputs["seed"]; got != want {
		t.Errorf("outputs.seed = %v (%T), want %d", got, got, want)
	}
	other := process(t, p, bayesRequest("other.pulse", regmmm.BayesSpec(regmmm.FastParams))).Regressions[0]
	if other.Outputs["seed"] != want {
		t.Errorf("different data changed the derived seed: %v vs %d", other.Outputs["seed"], want)
	}
	if regmmm.BayesEffectiveSeed(regmmm.BayesSpec(strings.Replace(regmmm.FastParams, `"draws":100`, `"draws":101`, 1))) == want {
		t.Error("a different slot derived the same seed")
	}
	explicit := regmmm.BayesSpec(regmmm.FastParams)
	explicit.RNGSeed = 7
	if regmmm.BayesEffectiveSeed(explicit) != 7 {
		t.Error("a non-zero rng_seed is not used as is")
	}
	seeded := process(t, p, bayesRequest(regmmm.BayesCohort, explicit)).Regressions[0]
	if seeded.Outputs["seed"] != int64(7) || mustJSON(t, seeded.Coefficients) == mustJSON(t, first.Coefficients) {
		t.Errorf("rng_seed 7: outputs.seed %v, coefficients %s", seeded.Outputs["seed"], mustJSON(t, seeded.Coefficients))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.seeds) != 4 || c.seeds[0] != uint64(want) || c.seeds[3] != 7 {
		t.Errorf("sampler seeds = %v, want derived %d ×3 then 7", c.seeds, uint64(want))
	}
}

// TestMMMBayes_FitPosterior: a fit returns the posterior: the
// coefficient table in its documented order (the vcov's row keys), each
// Estimate the mean and StdErr the SD of the pooled draws, vcov the
// draws' covariance (symmetric, StdErr² on its diagonal), the decay
// inside (0, 1), and the small cohort's truth recovered within a few
// posterior SDs.
func TestMMMBayes_FitPosterior(t *testing.T) {
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

// TestMMMBayes_FitOutputs: a fit's Outputs carry finite, ordered
// summaries of every channel's ROI, mROI and contribution and every
// coefficient, at credible_level; the params means are the coefficient
// table's; the contribution is the ROI times the summed spend; a lower
// credible_level narrows every interval; the diagnostics block reports
// the run; no holdout without params.holdout_from.
func TestMMMBayes_FitOutputs(t *testing.T) {
	p := bayesInstance(t, nil)
	r := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(regmmm.FastParams))).Regressions[0]
	if r.Error != nil {
		t.Fatalf("fit = %s", mustJSON(t, r))
	}
	out := r.Outputs
	for _, k := range []string{"seed", "roi", "mroi", "contribution", "params", "diagnostics"} {
		if _, ok := out[k]; !ok {
			t.Errorf("outputs lacks %s", k)
		}
	}
	if _, ok := out["holdout"]; ok {
		t.Error("outputs.holdout present without params.holdout_from")
	}
	roi := checkSummary(t, "roi.tv", out["roi"].(map[string]any)["tv"])
	checkSummary(t, "mroi.tv", out["mroi"].(map[string]any)["tv"])
	contrib := checkSummary(t, "contribution.tv", out["contribution"].(map[string]any)["tv"])
	spent := 0.0
	for _, row := range regmmm.BayesRows(11) {
		spent += row.TV
	}
	if want := float64(roi["mean"] * spent); math.Abs(contrib["mean"]-want) > float64(1e-9*want) {
		t.Errorf("contribution mean %v, roi mean × spend %v", contrib["mean"], want)
	}
	params := out["params"].(map[string]any)
	if len(params) != len(r.Coefficients) {
		t.Errorf("params has %d entries, the coefficient table %d", len(params), len(r.Coefficients))
	}
	for name, est := range r.Coefficients {
		s := checkSummary(t, "params."+name, params[name])
		if s["mean"] != est {
			t.Errorf("params.%s.mean %v, coefficient %v", name, s["mean"], est)
		}
		if math.Abs(s["sd"]-r.StdErrors[name]) > 1e-9*s["sd"] {
			t.Errorf("params.%s.sd %v, std error %v", name, s["sd"], r.StdErrors[name])
		}
	}

	// The same draws (an explicit rng_seed: credible_level is part of
	// the slot a derived seed hashes) at two levels.
	level := func(params string) map[string]float64 {
		s := regmmm.BayesSpec(params)
		s.RNGSeed = 5
		return process(t, p, bayesRequest(regmmm.BayesCohort, s)).Regressions[0].Outputs["roi"].(map[string]any)["tv"].(map[string]float64)
	}
	wide, nr := level(regmmm.FastParams), level(strings.Replace(regmmm.FastParams, `"draws":100`, `"draws":100,"credible_level":0.5`, 1))
	if nr["mean"] != wide["mean"] || nr["median"] != wide["median"] || !(nr["upper"]-nr["lower"] < wide["upper"]-wide["lower"]) {
		t.Errorf("credible_level 0.5 roi %v vs 0.9 %v: want the same draws, a narrower interval", nr, wide)
	}

	d := out["diagnostics"].(map[string]any)
	for k, want := range map[string]any{"chains": 2, "warmup": 150, "draws": 100, "max_tree_depth": mcmc.DefaultMaxTreeDepth} {
		if d[k] != want {
			t.Errorf("diagnostics.%s = %v, want %v", k, d[k], want)
		}
	}
	if steps := d["step_size"].([]float64); len(steps) != 2 || !(steps[0] > 0) {
		t.Errorf("diagnostics.step_size = %v", steps)
	}
	if _, ok := d["map_start"].(bool); !ok {
		t.Errorf("diagnostics.map_start = %v", d["map_start"])
	}
	for _, k := range []string{"divergences", "treedepth_saturated"} {
		if n, ok := d[k].(int); !ok || n < 0 {
			t.Errorf("diagnostics.%s = %v", k, d[k])
		}
	}
	conv := d["parameters"].(map[string]any)
	maxRHat := 0.0
	for name := range r.Coefficients {
		c, ok := conv[name].(map[string]float64)
		if !ok || !(c["rhat"] > 0.9) || !(c["ess_bulk"] > 0) || !(c["ess_tail"] > 0) {
			t.Errorf("diagnostics.parameters.%s = %v", name, conv[name])
		}
		maxRHat = max(maxRHat, c["rhat"])
	}
	if c, ok := d["roi"].(map[string]any)["tv"].(map[string]float64); !ok || !(c["rhat"] > 0.9) {
		t.Errorf("diagnostics.roi.tv = %v", d["roi"])
	}
	if d["max_rhat"].(float64) < maxRHat {
		t.Errorf("max_rhat %v below a parameter's %v", d["max_rhat"], maxRHat)
	}
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

// TestMMMBayes_HealthWarnings: an under-sampled fit (a tiny warmup and
// few draws) reports its figures with a coded ess_low warning on the
// result and the response; each of R̂, divergences and tree-depth
// saturation warns with its own reason and figures; a healthy-looking
// run reports none of those three; under Strict the first warning
// fails the run with PROCESSING_REGRESSION_DIAGNOSTIC.
func TestMMMBayes_HealthWarnings(t *testing.T) {
	const tiny = `{"channels":["tv"],"time":"week","chains":2,"warmup":10,"draws":30}`
	p := bayesInstance(t, nil)
	resp := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(tiny)))
	r := resp.Regressions[0]
	if r.Error != nil || r.Outputs["roi"] == nil {
		t.Fatalf("under-sampled fit = %s, want its figures", mustJSON(t, r))
	}
	if got := warningReasons(t, r.Warnings); !slices.Contains(got, regmmm.ReasonESSLow) {
		t.Errorf("result warnings %v, want %s", got, regmmm.ReasonESSLow)
	}
	if got := warningReasons(t, resp.Warnings); !slices.Contains(got, regmmm.ReasonESSLow) {
		t.Errorf("response warnings %v, want %s lifted", got, regmmm.ReasonESSLow)
	}
	for _, w := range r.Warnings {
		if w.Details["reason"] == regmmm.ReasonESSLow {
			if w.Details["threshold"] != regmmm.BayesESSMin || !strings.Contains(w.Message, `"bayes"`) ||
				!slices.Contains(w.Details["parameters"].([]string), "sigma") {
				t.Errorf("ess_low warning = %s", mustJSON(t, w))
			}
		}
	}

	strict := instance(t, afero.NewMemMapFs(), func(o *pulse.Options) { o.Strict = true })
	writeBayesCohort(t, strict, regmmm.BayesCohort, encoding.FieldTypeU16, regmmm.BayesRows(11))
	if _, err := strict.Process(context.Background(), bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(tiny))); !perr.HasCode(err, perr.PROCESSING_REGRESSION_DIAGNOSTIC) {
		t.Errorf("strict under-sampled fit: %v, want PROCESSING_REGRESSION_DIAGNOSTIC", err)
	}

	// One chain: R̂ is undefined, so it cannot certify the fit — a
	// rhat_high warning with a NaN max_rhat, null on the wire.
	one := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(`{"channels":["tv"],"time":"week","chains":1,"warmup":100,"draws":60}`))).Regressions[0]
	if got := warningReasons(t, one.Warnings); !slices.Contains(got, regmmm.ReasonRHatHigh) {
		t.Errorf("one chain: reasons %v, want %s", got, regmmm.ReasonRHatHigh)
	}
	if body, err := types.MarshalFinite(one); err != nil || !strings.Contains(string(body), `"max_rhat":null`) {
		t.Errorf("one chain on the wire: %v, want max_rhat null", err)
	}

	// The sampler-run diagnostics, doctored: each reason appears exactly
	// when its figure crosses (the 200-draw fit always has a low ESS and
	// some R̂ above 1.01, but not the intercept's).
	intercept := func(ws []*types.ResponseWarning) bool {
		for _, w := range ws {
			if w.Details["reason"] == regmmm.ReasonRHatHigh && slices.Contains(w.Details["parameters"].([]string), regmmm.InterceptName) {
				return w.Details["max_rhat"].(float64) > regmmm.BayesRHatMax && w.Details["threshold"] == regmmm.BayesRHatMax
			}
		}
		return false
	}
	for _, tc := range []struct {
		name      string
		s         doctoredSampler
		reasons   []string
		intercept bool
		check     func(map[string]any) bool
	}{
		{"divergences", doctoredSampler{divergences: 3}, []string{regmmm.ReasonDivergences}, false,
			func(d map[string]any) bool { return d["count"] == 3 && d["draws"] == 200 }},
		{"treedepth", doctoredSampler{saturated: 2}, []string{regmmm.ReasonTreeDepthSaturated}, false,
			func(d map[string]any) bool { return d["count"] == 2 && d["max_tree_depth"] == mcmc.DefaultMaxTreeDepth }},
		{"rhat", doctoredSampler{split: true}, nil, true, nil},
		{"baseline", doctoredSampler{}, nil, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			regmmm.SetBayesSampler(t, func(float64) mcmc.Sampler { return tc.s })
			// Pinned: the seed FastParams derived before params were
			// hashed in canonical form (E3-S7); the intercept's R̂ sits
			// under 1.01 on it, which the doctored cases rely on.
			spec := regmmm.BayesSpec(regmmm.FastParams)
			spec.RNGSeed = 8392533999830091673
			r := process(t, p, bayesRequest(regmmm.BayesCohort, spec)).Regressions[0]
			if r.Error != nil {
				t.Fatalf("fit = %s", mustJSON(t, r))
			}
			var got []string
			for _, reason := range warningReasons(t, r.Warnings) {
				if reason == regmmm.ReasonDivergences || reason == regmmm.ReasonTreeDepthSaturated {
					got = append(got, reason)
				}
			}
			if !slices.Equal(got, tc.reasons) {
				t.Fatalf("reasons = %v, want %v", got, tc.reasons)
			}
			if intercept(r.Warnings) != tc.intercept {
				t.Errorf("rhat_high on the intercept = %v, want %v: %s", !tc.intercept, tc.intercept, mustJSON(t, r.Warnings))
			}
			for _, w := range r.Warnings {
				if tc.check != nil && w.Details["reason"] == tc.reasons[0] && !tc.check(w.Details) {
					t.Errorf("warning = %s", mustJSON(t, w))
				}
			}
		})
	}
}

// TestMMMBayes_Holdout: params.holdout_from fits the rows before it and
// scores the rest: outputs.holdout reports the split, a finite RMSE and
// a coverage share; the fit is the fit of the truncated series (same
// rng_seed, same coefficient bytes); changing the held-out outcomes
// leaves the posterior untouched and moves only the scores; a cutoff
// outside the series is PROCESSING_CONFIG before any sampling, a
// fractional one a params refusal.
func TestMMMBayes_Holdout(t *testing.T) {
	c := &countingSampler{}
	withSampler(t, c)
	p := bayesInstance(t, nil)
	const holdout = `{"channels":["tv"],"time":"week","chains":2,"warmup":150,"draws":100,"holdout_from":51}`
	seeded := func(params string) *types.RegressionSpec {
		s := regmmm.BayesSpec(params)
		s.RNGSeed = 17
		return s
	}
	r := process(t, p, bayesRequest(regmmm.BayesCohort, seeded(holdout))).Regressions[0]
	if r.Error != nil {
		t.Fatalf("holdout fit = %s", mustJSON(t, r))
	}
	h, ok := r.Outputs["holdout"].(map[string]any)
	if !ok {
		t.Fatalf("outputs.holdout = %v", r.Outputs["holdout"])
	}
	if h["from"] != int64(51) || h["fit_rows"] != 50 || h["holdout_rows"] != 10 || h["credible_level"] != 0.9 {
		t.Errorf("holdout split = %v", h)
	}
	rmse, cov := h["rmse"].(float64), h["coverage"].(float64)
	if !(rmse > 0 && rmse < 10) || !(cov >= 0 && cov <= 1) || float64(cov*10) != math.Round(float64(cov*10)) {
		t.Errorf("holdout rmse %v coverage %v", rmse, cov)
	}

	// The likelihood is the first 50 weeks': the truncated cohort fits
	// to the same bytes.
	rows := regmmm.BayesRows(11)
	writeBayesCohort(t, p, "first50.pulse", encoding.FieldTypeU16, rows[:50])
	trunc := process(t, p, bayesRequest("first50.pulse", seeded(regmmm.FastParams))).Regressions[0]
	if a, b := mustJSON(t, trunc.Coefficients), mustJSON(t, r.Coefficients); a != b {
		t.Errorf("holdout fit differs from the truncated series' fit:\n%s\n%s", b, a)
	}

	// Leakage: other outcomes past the cutoff leave the posterior alone.
	shifted := slices.Clone(rows)
	for i := 50; i < len(shifted); i++ {
		shifted[i].Sales += 40
	}
	writeBayesCohort(t, p, "shifted.pulse", encoding.FieldTypeU16, shifted)
	s := process(t, p, bayesRequest("shifted.pulse", seeded(holdout))).Regressions[0]
	for _, k := range []string{"roi", "mroi", "contribution", "params", "diagnostics", "seed"} {
		if a, b := mustJSON(t, s.Outputs[k]), mustJSON(t, r.Outputs[k]); a != b {
			t.Errorf("outputs.%s moved with the held-out outcomes", k)
		}
	}
	if mustJSON(t, s.Coefficients) != mustJSON(t, r.Coefficients) {
		t.Error("coefficients moved with the held-out outcomes")
	}
	if sh := s.Outputs["holdout"].(map[string]any); !(sh["rmse"].(float64) > rmse+20) {
		t.Errorf("shifted holdout rmse %v, unshifted %v: want the scores to see the shift", sh["rmse"], rmse)
	}

	runs := c.runs.Load()
	for _, from := range []string{"1", "0", "61", "-3"} {
		_, err := p.Process(context.Background(), bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(
			`{"channels":["tv"],"time":"week","chains":2,"warmup":150,"draws":100,"holdout_from":`+from+`}`)))
		var ce *perr.CodedError
		if !perr.HasCode(err, perr.PROCESSING_CONFIG) || !errorsAs(err, &ce) ||
			ce.Details["slot"] != "params.holdout_from" || ce.Details["reason"] != "holdout_out_of_range" {
			t.Errorf("holdout_from %s: %v, want PROCESSING_CONFIG holdout_out_of_range", from, err)
		}
	}
	_, err := p.Process(context.Background(), bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(
		`{"channels":["tv"],"time":"week","holdout_from":50.5}`)))
	if !perr.HasCode(err, perr.PROCESSING_CONFIG) {
		t.Errorf("fractional holdout_from: %v, want PROCESSING_CONFIG", err)
	}
	if n := c.runs.Load() - runs; n != 0 {
		t.Errorf("a refused holdout sampled %d times", n)
	}
	// The last week alone is a valid holdout.
	last := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(strings.Replace(holdout, `51}`, `60}`, 1)))).Regressions[0]
	if lh, ok := last.Outputs["holdout"].(map[string]any); !ok || lh["holdout_rows"] != 1 || lh["fit_rows"] != 59 {
		t.Errorf("holdout_from 60 = %v", last.Outputs["holdout"])
	}
}

// TestMMMBayes_AdstockMatchesDistributedLag: the model's adstock of
// each joint-fixture channel at its true decay is bit-equal to
// WIN_DISTRIBUTED_LAG geometric with max_lag 13 and normalize false, run
// by the engine over the same cohort.
func TestMMMBayes_AdstockMatchesDistributedLag(t *testing.T) {
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
