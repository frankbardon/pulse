package mmmpulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"math/rand/v2"
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
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
)

// bayes_test.go holds REG_MMM_BAYES's registration and fit (E3-S2) to
// its contract: the registration's declarations, every params / input
// refusal, the shared-slot refusals identical in predict and at
// runtime, a probe that never samples, the rng_seed-0 derivation, the
// posterior coefficient table and covariance, cancellation through
// RequestTimeout and a sampler failure that fails its slot alone.

// The small cohort: one market, bayesWeeks weeks, one channel (tv) with
// geometric adstock and a Hill curve, a price control, Gaussian noise.
// Every product feeding an add is barriered and the normal draws are
// detmath's, so the cohort's bits are the seed's alone on every
// architecture.
const (
	bayesWeeks  = 60
	bayesCohort = "bayes.pulse"
	smallDecay  = 0.5
	smallK      = 120.0
	smallS      = 1.5
	smallBeta   = 40.0
)

type bayesRow struct {
	geo       string
	week      float64
	tv, price float64
	sales     float64
}

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

// bayesRows generates one market's series.
func bayesRows(seed uint64) []bayesRow {
	rng := rand.New(rand.NewPCG(seed, 0x6261796573))
	spend, price := make([]float64, bayesWeeks), make([]float64, bayesWeeks)
	for w := range spend {
		if rng.Float64() < 0.6 {
			spend[w] = float64(rng.Float64()*150) + 20
		}
		price[w] = 2 * (float64(rng.Float64()) - 0.5)
	}
	rows := make([]bayesRow, bayesWeeks)
	for w := range spend {
		ad := 0.0
		for l := 0; l <= defaultMaxLag && l <= w; l++ {
			ad += float64(detmath.Pow(smallDecay, float64(l)) * spend[w-l])
		}
		y := 200 + float64(smallBeta*mmmfixture.JointHill(ad, smallK, smallS)) + float64(-5*price[w]) + float64(1*detmath.NormFloat64(rng))
		rows[w] = bayesRow{geo: "east", week: float64(w + 1), tv: spend[w], price: price[w], sales: y}
	}
	return rows
}

// writeBayesCohort writes rows to name; a negative week is written null.
func writeBayesCohort(t testing.TB, p *pulse.Pulse, name string, weekType encoding.FieldType, rows []bayesRow) {
	t.Helper()
	b, err := p.NewCohortBuilder(context.Background(), name, bayesSchema(weekType), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		var week any = uint64(r.week)
		switch {
		case r.week < 0:
			week = nil
		case weekType == encoding.FieldTypeF64:
			week = r.week
		}
		if err := b.Append(pulse.CohortRow{r.geo, week, r.tv, r.price, r.sales}); err != nil {
			t.Fatalf("Append row %d: %v", i, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

// bayesInstance is an instance with the pack and the small cohort.
func bayesInstance(t testing.TB, mut func(*pulse.Options)) *pulse.Pulse {
	t.Helper()
	p := instance(t, afero.NewMemMapFs(), mut)
	writeBayesCohort(t, p, bayesCohort, encoding.FieldTypeU16, bayesRows(11))
	return p
}

// fastParams is a fit small enough for a unit test.
const fastParams = `{"channels":["tv"],"time":"week","chains":2,"warmup":150,"draws":100}`

// bayesSpec is a REG_MMM_BAYES slot over the small cohort.
func bayesSpec(params string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: BayesName, Name: "bayes", Target: "sales", Predictors: []string{"tv", "price"},
		Params: json.RawMessage(params)}
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

// withSampler swaps bayesSampler for c for the test's duration.
func withSampler(t *testing.T, c *countingSampler) {
	t.Helper()
	prev := bayesSampler
	bayesSampler = func(targetAccept float64) mcmc.Sampler {
		c.mu.Lock()
		c.targetAccept = targetAccept
		c.mu.Unlock()
		return c
	}
	t.Cleanup(func() { bayesSampler = prev })
}

// TestMMMBayes_Registration: the registration declares a buffered,
// non-resampled, scorer-free model with vcov, no weights and no
// p-values; its FieldInputs reads params.time; its params are declared;
// the model implements FitContext; the manifest lists it without a
// resample, holdout-scoring or save slot.
func TestMMMBayes_Registration(t *testing.T) {
	reg := BayesRegistration()
	if reg.Name != BayesName || reg.Streamable || !reg.NoResample || reg.Scorer != nil || !reg.Vcov || reg.WeightAware || reg.PValues ||
		reg.Penalty || reg.SupportsSelection || len(reg.CovarianceTypes) != 0 {
		t.Fatalf("registration flags = %+v", reg)
	}
	if got := reg.FieldInputs(json.RawMessage(`{"channels":["tv"],"time":"week"}`)); !slices.Equal(got, []string{"week"}) {
		t.Errorf("FieldInputs = %v, want [week]", got)
	}
	var params []string
	for _, prm := range reg.Params {
		params = append(params, prm.Name)
	}
	want := []string{"channels", "time", "max_lag", "adstock", "priors", "intercept_prior", "control_prior", "sigma_prior",
		"chains", "warmup", "draws", "credible_level", "sampler", "target_accept", "holdout_from"}
	if !slices.Equal(params, want) {
		t.Errorf("params = %v, want %v", params, want)
	}
	model, err := reg.Factory(bayesSpec(fastParams), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := model.(extend.ContextRegression); !ok {
		t.Error("the model does not implement extend.ContextRegression")
	}
	if _, ok := model.(extend.BufferedRegression); !ok {
		t.Error("the model does not implement extend.BufferedRegression")
	}

	p := instance(t, afero.NewMemMapFs(), nil)
	found := false
	for _, row := range p.Manifest(context.Background()).Extensions.Regressions {
		if row.Name != string(BayesName) {
			continue
		}
		found = true
		if row.Streamable {
			t.Error("manifest: REG_MMM_BAYES listed as streamable")
		}
		for _, prm := range row.Params {
			if prm.Slot && slices.Contains([]string{"resample", "save", "weight", "multiplicity"}, prm.Name) {
				t.Errorf("manifest lists slot %s", prm.Name)
			}
		}
	}
	if !found {
		t.Fatal("manifest extensions.regressions lacks REG_MMM_BAYES")
	}
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
			s := bayesSpec(tc.params)
			_, err := p.Process(context.Background(), bayesRequest(bayesCohort, s))
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

	// A predictor named like a coefficient the model adds would collide
	// in the table (the engine refuses an unknown field first, so the
	// factory is called directly).
	for _, x := range []string{"(intercept)", "sigma", "decay:x", "k:x", "s:x"} {
		s := bayesSpec(`{"channels":["tv"],"time":"week"}`)
		s.Predictors = []string{"tv", x}
		var ce *perr.CodedError
		if _, err := bayesFactory(s, nil); !errorsAs(err, &ce) || ce.Code != perr.PROCESSING_CONFIG || ce.Details["slot"] != "predictors" {
			t.Errorf("predictor %q: %v, want PROCESSING_CONFIG slot predictors", x, err)
		}
	}
	if _, err := bayesFactory(&types.RegressionSpec{Type: BayesName, Name: "bayes", Target: "sales", Predictors: []string{"tv", "kx"},
		Params: json.RawMessage(`{"channels":["tv"],"time":"week"}`)}, nil); err != nil {
		t.Errorf("a predictor merely starting with k: %v", err)
	}

	// The engine's own params rules: an undeclared key and a missing
	// Required one, refused identically in predict.
	for _, tc := range []struct {
		params string
		code   perr.Code
	}{
		{`{"channels":["tv"],"time":"week","chain":2}`, perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{`{"channels":["tv"]}`, perr.PULSE_REQUEST_PARAM_INVALID},
	} {
		req := bayesRequest(bayesCohort, bayesSpec(tc.params))
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
	base := bayesRows(11)
	mutate := func(f func([]bayesRow) []bayesRow) []bayesRow { return f(slices.Clone(base)) }
	for _, tc := range []struct {
		name, reason string
		weekType     encoding.FieldType
		rows         []bayesRow
		groups       bool
	}{
		{"gap", "time_gap", encoding.FieldTypeU16, mutate(func(r []bayesRow) []bayesRow { return slices.Delete(r, 20, 21) }), false},
		{"duplicate", "duplicate_time", encoding.FieldTypeU16, mutate(func(r []bayesRow) []bayesRow { r[5].week = r[4].week; return r }), false},
		{"panel", "duplicate_time", encoding.FieldTypeU16, mutate(func(r []bayesRow) []bayesRow {
			for _, row := range base {
				row.geo = "west"
				r = append(r, row)
			}
			return r
		}), false},
		{"grouped panel", "duplicate_time", encoding.FieldTypeU16, mutate(func(r []bayesRow) []bayesRow {
			for _, row := range base {
				row.geo = "west"
				r = append(r, row)
			}
			return r
		}), true},
		{"fractional time", "time_not_integer", encoding.FieldTypeF64, mutate(func(r []bayesRow) []bayesRow { r[3].week = 3.5; return r }), false},
		{"null time", "time_missing", encoding.FieldTypeU16, mutate(func(r []bayesRow) []bayesRow { r[3].week = -1; return r }), false},
		{"negative spend", "negative_spend", encoding.FieldTypeU16, mutate(func(r []bayesRow) []bayesRow { r[7].tv = -1; return r }), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := strings.ReplaceAll(tc.name, " ", "_") + ".pulse"
			writeBayesCohort(t, p, file, tc.weekType, tc.rows)
			req := bayesRequest(file, bayesSpec(fastParams))
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
	writeBayesCohort(t, p, "reversed.pulse", encoding.FieldTypeU16, mutate(func(r []bayesRow) []bayesRow { slices.Reverse(r); return r }))
	sorted := process(t, p, bayesRequest(bayesCohort, bayesSpec(fastParams))).Regressions[0]
	reversed := process(t, p, bayesRequest("reversed.pulse", bayesSpec(fastParams))).Regressions[0]
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
			raw, _ := json.Marshal(bayesSpec(fastParams))
			_ = json.Unmarshal(raw, &m)
			m[key] = json.RawMessage(val)
			raw, _ = json.Marshal(m)
			var s types.RegressionSpec
			if err := json.Unmarshal(raw, &s); err != nil {
				t.Fatal(err)
			}
			req := bayesRequest(bayesCohort, &s)
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

// probeRows is pulse.New's probe shape: 8 rows of probe_y / probe_x1 /
// probe_x2, no time column.
type probeRow map[string]float64

func (r probeRow) Schema() *encoding.Schema                     { return nil }
func (r probeRow) IsNull(f string) bool                         { _, ok := r[f]; return !ok }
func (r probeRow) NumericValue(f string) (float64, bool)        { v, ok := r[f]; return v, ok }
func (r probeRow) Uint64Value(f string) (uint64, bool)          { v, ok := r[f]; return uint64(v), ok }
func (r probeRow) StringValue(string) (string, bool)            { return "", false }
func (r probeRow) SetMaskValue(string) (encoding.SetMask, bool) { return encoding.SetMask{}, false }
func (r probeRow) DecimalValue(string) (encoding.Decimal128, bool) {
	return encoding.Decimal128{}, false
}
func (r probeRow) VectorValue(string) ([]float64, bool) { return nil, false }
func (r probeRow) Weight() (float64, bool)              { return 0, false }

type probeRows []probeRow

func (r probeRows) Len() int               { return len(r) }
func (r probeRows) At(i int) extend.Record { return r[i] }

// TestMMMBayes_ProbeNeverSamples: pulse.New's probe fit of the pack is a
// cheap coded data refusal — the sampler never runs — and so is any
// FitContext over 8 rows without a time column.
func TestMMMBayes_ProbeNeverSamples(t *testing.T) {
	c := &countingSampler{}
	withSampler(t, c)
	if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: Extensions()}); err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	s := &types.RegressionSpec{Type: BayesName, Target: "probe_y", Predictors: []string{"probe_x1", "probe_x2"},
		Params: json.RawMessage(`{"channels":["probe_x1","probe_x2"],"time":"week"}`)}
	model, err := bayesFactory(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := make(probeRows, 8)
	for i := range rows {
		rows[i] = probeRow{"probe_y": float64(i), "probe_x1": float64(i % 3), "probe_x2": float64(i % 2)}
	}
	_, err = model.(extend.ContextRegression).FitContext(context.Background(), rows)
	if !perr.HasCode(err, perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA) {
		t.Errorf("probe-shaped fit: %v, want PROCESSING_REGRESSION_INSUFFICIENT_DATA", err)
	}
	if n := c.runs.Load(); n != 0 {
		t.Errorf("the probe sampled %d times", n)
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
	writeBayesCohort(t, p, "other.pulse", encoding.FieldTypeU16, bayesRows(12))

	s := bayesSpec(fastParams)
	want := bayesEffectiveSeed(s)
	cp := *s
	cp.RNGSeed = 0
	if body, _ := json.Marshal(&cp); want == 0 || !strings.Contains(string(body), `"params"`) {
		t.Fatalf("derived seed %d over %s", want, body)
	}
	first := process(t, p, bayesRequest(bayesCohort, s)).Regressions[0]
	second := process(t, p, bayesRequest(bayesCohort, bayesSpec(fastParams))).Regressions[0]
	if a, b := mustJSON(t, first), mustJSON(t, second); a != b {
		t.Errorf("same request, different bytes:\n%s\n%s", a, b)
	}
	if got := first.Outputs["seed"]; got != want {
		t.Errorf("outputs.seed = %v (%T), want %d", got, got, want)
	}
	other := process(t, p, bayesRequest("other.pulse", bayesSpec(fastParams))).Regressions[0]
	if other.Outputs["seed"] != want {
		t.Errorf("different data changed the derived seed: %v vs %d", other.Outputs["seed"], want)
	}
	if bayesEffectiveSeed(bayesSpec(strings.Replace(fastParams, `"draws":100`, `"draws":101`, 1))) == want {
		t.Error("a different slot derived the same seed")
	}
	explicit := bayesSpec(fastParams)
	explicit.RNGSeed = 7
	if bayesEffectiveSeed(explicit) != 7 {
		t.Error("a non-zero rng_seed is not used as is")
	}
	seeded := process(t, p, bayesRequest(bayesCohort, explicit)).Regressions[0]
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
	s := bayesSpec(fastParams)
	s.Vcov = true
	start := time.Now()
	resp := process(t, p, bayesRequest(bayesCohort, s))
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
	for name, truth := range map[string]float64{"(intercept)": 200, "price": -5, "decay:tv": smallDecay, "sigma": 1} {
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

// TestMMMBayes_PosteriorMoments: the coefficient table's mean, SD and
// covariance are the pooled draws' (chain order, divisor N − 1).
func TestMMMBayes_PosteriorMoments(t *testing.T) {
	draws := [][][]float64{
		{{1, 2, 3}, {2, 4, 7}},
		{{4, 5, 6}, {1, 0, 2}},
	}
	mean, cov := posteriorMoments(draws)
	if mean[0] != 3.5 || mean[1] != 16.0/6 {
		t.Fatalf("mean = %v", mean)
	}
	var v00, v01 float64
	a, b := []float64{1, 2, 3, 4, 5, 6}, []float64{2, 4, 7, 1, 0, 2}
	for i := range a {
		v00 += float64((a[i] - mean[0]) * (a[i] - mean[0]))
		v01 += float64((a[i] - mean[0]) * (b[i] - mean[1]))
	}
	if math.Abs(cov[0][0]-v00/5) > 1e-12 || math.Abs(cov[0][1]-v01/5) > 1e-12 || cov[0][1] != cov[1][0] {
		t.Errorf("cov = %v, want %v / %v", cov, v00/5, v01/5)
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
	_, err := p.Process(context.Background(), bayesRequest(bayesCohort,
		bayesSpec(`{"channels":["tv"],"time":"week","chains":4,"warmup":20000,"draws":20000}`)))
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
// slot fitted); under Strict the run fails with that code. A sampler
// error of any other kind passes through.
func TestMMMBayes_SamplerFailureFailsSlotAlone(t *testing.T) {
	for _, fail := range []error{
		perr.NewCodedErrorWithDetails(perr.PULSE_MCMC_FAILED, "mcmc: no start", map[string]any{"reason": "init_not_found", "chain": 0}),
		perr.NewCodedErrorWithDetails(perr.PULSE_MCMC_INVALID, "mcmc: init", map[string]any{"reason": "init_density_not_finite", "chain": 1}),
	} {
		c := &countingSampler{fail: fail}
		withSampler(t, c)
		p := bayesInstance(t, nil)
		req := func() *types.Request {
			return bayesRequest(bayesCohort, bayesSpec(fastParams),
				&types.RegressionSpec{Type: types.REG_OLS, Name: "ols", Target: "sales", Predictors: []string{"tv"}})
		}
		resp := process(t, p, req())
		b, ols := resp.Regressions[0], resp.Regressions[1]
		if b.Error == nil || b.Error.Code != string(perr.PROCESSING_REGRESSION_NO_CONVERGE) || b.Name != "bayes" || b.Type != BayesName {
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
		writeBayesCohort(t, strict, bayesCohort, encoding.FieldTypeU16, bayesRows(11))
		if _, err := strict.Process(context.Background(), req()); !perr.HasCode(err, perr.PROCESSING_REGRESSION_NO_CONVERGE) {
			t.Errorf("strict: %v, want NO_CONVERGE", err)
		}
	}

	r := &bayesRegression{cfg: &bayesConfig{name: "bayes"}}
	other := perr.NewCodedErrorWithDetails(perr.PULSE_MCMC_INVALID, "mcmc: chains", map[string]any{"reason": "chains"})
	if got := r.samplerError(other); got != other {
		t.Errorf("an options refusal mapped to %v", got)
	}
	if got := r.samplerError(context.Canceled); got != context.Canceled {
		t.Errorf("a ctx error mapped to %v", got)
	}
}

// TestMMMBayes_FitMatchesFitContext: Fit is FitContext under a
// background context, byte for byte.
func TestMMMBayes_FitMatchesFitContext(t *testing.T) {
	model, err := bayesFactory(bayesSpec(fastParams), nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := make(probeRows, 0, bayesWeeks)
	for _, r := range bayesRows(11) {
		rows = append(rows, probeRow{"week": r.week, "tv": r.tv, "price": r.price, "sales": r.sales})
	}
	a, errA := model.(extend.BufferedRegression).Fit(rows)
	b, errB := model.(extend.ContextRegression).FitContext(context.Background(), rows)
	if errA != nil || errB != nil {
		t.Fatalf("Fit %v, FitContext %v", errA, errB)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Fit and FitContext differ:\n%s\n%s", mustJSON(t, a), mustJSON(t, b))
	}
}
