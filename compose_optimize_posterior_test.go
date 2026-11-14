package pulse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/internal/optfixture"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// compose_optimize_posterior_test.go pins a Compose optimize block's
// `posterior` at the facade (mmm-bayes-depth E3-S3): a block over a
// saved REG_MMM_BAYES file's draws solves the same bytes serial and
// parallel with a per-variable interval, every posterior refusal is
// predict's and both orchestrators' alike, and a block without
// `posterior` answers the bytes it answered before the slot existed.

// TestOptimize_PosteriorAbsentByteIdentical: the posterior-free
// fixtures answer exactly the bytes they answered before `posterior`
// existed (digests taken on the pre-change code; the optimize path is
// FMA-free, its saved model and hill() curves fit-free).
func TestOptimize_PosteriorAbsentByteIdentical(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	for _, tc := range []struct {
		raw  []byte
		want string
	}{
		{optfixture.PriceModel, "98cd115fb6cd40f4d81c6f864c5bfa084bab27ffc5848ac85af70c38196bbc74"},
		{optfixture.Budget, "30f406b71e6590b0b9afeb732a222c373b6aa05db68ee9124fb3bed6d5d2965c"},
	} {
		for name, run := range composeRuns(p) {
			resp, err := run(decodeOpt(t, tc.raw, nil))
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(resp.Optimizations)
			if err != nil {
				t.Fatal(err)
			}
			if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != tc.want {
				t.Errorf("%s: optimizations changed: %s", name, b)
			}
		}
	}
}

// TestOptimize_PosteriorSavedModel: the posterior block over the saved
// Bayes file solves on both orchestrators to the same bytes, run after
// run; it reads every one of the file's six draws, echoes the fit, the
// aggregation and the file's credible level, and each variable carries
// an ordered interval; predict accepts it. f* is the 0.2-quantile of the
// per-draw objective at x* (each draw's response() read off a
// WithMMMDraw view of the file), and the allocation spends the budget.
func TestOptimize_PosteriorSavedModel(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	var want []byte
	for name, run := range composeRuns(p) {
		for k := 0; k < 2; k++ {
			resp, err := run(decodeOpt(t, optfixture.Posterior, nil))
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			b, err := json.Marshal(resp.Optimizations)
			if err != nil {
				t.Fatal(err)
			}
			if want == nil {
				want = b
			} else if !bytes.Equal(b, want) {
				t.Fatalf("%s run %d differs:\n%s\n%s", name, k, b, want)
			}
		}
	}
	resp, err := p.Compose(context.Background(), decodeOpt(t, optfixture.Posterior, nil))
	if err != nil {
		t.Fatal(err)
	}
	got := oneOptimum(t, "posterior", resp)
	echo := types.OptimizePosteriorResult{Fit: "mmm", Objective: types.OptimizePosteriorQuantile, Q: 0.2, Draws: 6, DrawsAvailable: 6, DrawsResolved: 6, CredibleLevel: 0.8}
	if got.Posterior == nil || *got.Posterior != echo {
		t.Fatalf("posterior echo %+v, want %+v", got.Posterior, echo)
	}
	tv, search := got.Variables[0], got.Variables[1]
	for _, v := range got.Variables {
		if v.Interval == nil || !(v.Interval.Lower <= v.Interval.Upper) {
			t.Errorf("%s interval %+v", v.Name, v.Interval)
		}
	}
	if d := tv.Value + search.Value - 100; d > 1e-6 || d < -1e-6 {
		t.Errorf("allocation %v + %v does not spend the budget", tv.Value, search.Value)
	}
	// f* recomputed off the file's draws, outside the solver.
	set := bayesFixtureSet(t)
	draws, err := set.MMMDrawsOf("mmm")
	if err != nil {
		t.Fatal(err)
	}
	vals := make([]float64, draws.Len())
	for k := range vals {
		v, err := set.WithMMMDraw("mmm", draws, k)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := model.Response(v, "mmm", "tv", tv.Value)
		b, _ := model.Response(v, "mmm", "search", search.Value)
		vals[k] = a + b
	}
	slices.Sort(vals)
	if f := model.Quantile7(vals, 0.2); got.Objective != f {
		t.Errorf("f* %v, want the per-draw 0.2-quantile %v", got.Objective, f)
	}
	env, err := p.PredictCompose(context.Background(), decodeOpt(t, optfixture.Posterior, nil))
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict: %v %+v", err, env)
	}
}

// bayesFixtureSet binds the saved Bayes model file as "mmm".
func bayesFixtureSet(t *testing.T) *model.Set {
	t.Helper()
	refs := model.Refs{Models: []types.ModelRef{{Name: "mmm", Inline: json.RawMessage(optfixture.BayesModel)}}}
	loaded, cerr := model.LoadFiles(refs, nil, nil)
	if cerr != nil {
		t.Fatal(cerr)
	}
	set, err := (*model.Set)(nil).WithLoaded(refs, loaded)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// TestOptimize_PosteriorRefusalParity: predict and both orchestrators
// refuse every posterior fault with the same code, message and
// details, before any fit runs — a malformed posterior, a block reading
// no fit, a non-Bayes fit (a Compose OLS fit) or two fits, and a Bayes
// carrier without draws: a Compose fit without params.return_draws and
// a saved file that kept none.
func TestOptimize_PosteriorRefusalParity(t *testing.T) {
	t.Parallel()

	fsys := optFixtureFS(t)
	var doc map[string]any
	if err := json.Unmarshal(optfixture.BayesModel, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "outputs")
	doc["features"] = []string{"adstock"}
	bare, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, "models/bare.pulse-model.json", bare, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fit := func(name, typ, params string) types.FitSpec {
		return types.FitSpec{Name: name, Request: &types.Request{Cohort: &types.Cohort{Filename: optfixture.CohortFile},
			Regressions: []*types.RegressionSpec{{Type: types.RegressionType(typ), Target: "units", Predictors: []string{"price"}, Params: json.RawMessage(params)}}}}
	}
	cases := []struct {
		name   string
		edit   func(c *ComposedRequest)
		reason string
	}{
		{"q with mean", func(c *ComposedRequest) { c.Optimize[0].Posterior = &types.OptimizePosterior{Q: 0.5} }, optblock.ReasonPosteriorQInvalid},
		{"quantile without q", func(c *ComposedRequest) { c.Optimize[0].Posterior.Q = 0 }, optblock.ReasonPosteriorQInvalid},
		{"negative draws", func(c *ComposedRequest) { c.Optimize[0].Posterior.Draws = -2 }, optblock.ReasonPosteriorDrawsInvalid},
		{"no fit read", func(c *ComposedRequest) {
			c.Optimize[0].Objective = "tv * (100 - tv) + search"
		}, optblock.ReasonPosteriorFitMissing},
		{"OLS fit", func(c *ComposedRequest) {
			c.Fits = []types.FitSpec{fit("demand", "REG_OLS", "")}
			c.Optimize[0].Models = nil
			c.Optimize[0].Objective = `coef("demand", "price") * tv + search`
		}, optblock.ReasonPosteriorFitNotBayes},
		{"two fits", func(c *ComposedRequest) {
			c.Fits = []types.FitSpec{fit("demand", "REG_OLS", "")}
			c.Optimize[0].Objective += ` + coef("demand", "price") * tv`
		}, optblock.ReasonPosteriorFitsMultiple},
		{"Compose fit without draws", func(c *ComposedRequest) {
			c.Fits = []types.FitSpec{fit("mmm", "REG_MMM_BAYES", `{"channels":["price"],"time":"week"}`)}
			c.Optimize[0].Models = nil
			c.Optimize[0].Objective = `response("mmm", "price", tv) + search`
		}, model.ReasonDrawsRequired},
		{"saved file without draws", func(c *ComposedRequest) {
			c.Optimize[0].Models = []types.ModelRef{{Name: "mmm", Path: "models/bare.pulse-model.json"}}
		}, model.ReasonDrawsRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := decodeOpt(t, optfixture.Posterior, tc.edit)
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_OPTIMIZE_SPEC_INVALID) || env.Errors[0].Details["reason"] != tc.reason {
				t.Fatalf("predict errors %+v, want reason %s", env.Errors, tc.reason)
			}
			for name, run := range composeRuns(p) {
				resp, rerr := run(decodeOpt(t, optfixture.Posterior, tc.edit))
				ce := requireCoded(t, rerr, perr.PULSE_OPTIMIZE_SPEC_INVALID)
				if resp != nil {
					t.Fatalf("%s: a refused batch returned a response", name)
				}
				if env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}
}
