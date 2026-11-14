package optsolve

import (
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/types"
)

// posteriorModel is an inline REG_MMM_BAYES model whose tv coefficient
// has four pooled draws 2, 4, 2.5, 3.5 (two chains of two; mean 3, the
// coefficient table's) at credible level 0.9.
const posteriorModel = `{"name": "m", "inline": {"model_format_version": 1, "type": "REG_MMM_BAYES", "target": "sales",
	"predictors": ["tv"], "terms": ["tv"], "features": ["adstock", "outputs"],
	"adstock": {"tv": {"kernel": "geometric", "decay": 0.5, "max_lag": 4, "normalize": false}},
	"coefficients": {"(intercept)": 10, "tv": 3, "decay:tv": 0.5, "k:tv": 50, "s:tv": 1.5, "sigma": 2},
	"outputs": {"draws": {"names": ["(intercept)", "tv", "decay:tv", "k:tv", "s:tv", "sigma"],
		"draws": [[[9, 11], [2, 4], [0.4, 0.6], [40, 60], [1.4, 1.6], [2, 2]],
		          [[10, 10], [2.5, 3.5], [0.3, 0.7], [45, 55], [1.5, 1.5], [2, 2]]],
		"thin": 1, "credible_level": 0.9}}}}`

// solvePosterior compiles a block loading posteriorModel and solves it
// over the model's draws (a draws:N subsample through SubsampleDraws),
// the coef() reader switched per draw as the runtime switches it.
func solvePosterior(t *testing.T, raw string) *types.OptimizeResult {
	t.Helper()
	spec := block(t, raw)
	loaded, cerr := model.LoadFiles(model.Refs{Models: spec.Models}, nil, nil)
	if cerr != nil {
		t.Fatal(cerr)
	}
	set, err := (*model.Set)(nil).WithLoaded(model.Refs{Models: spec.Models}, loaded)
	if err != nil {
		t.Fatal(err)
	}
	readers := []func(args ...any) (any, error){model.CoefBuiltin(set)}
	cur := 0
	c, cerr := optblock.Compile(spec, 0, optblock.Env{Loaded: []*model.Loaded{loaded}},
		exprenv.Bound{Coef: func(args ...any) (any, error) { return readers[cur](args...) }})
	if cerr != nil {
		t.Fatal(cerr)
	}
	if c.PosteriorFit != "m" {
		t.Fatalf("carrier %q", c.PosteriorFit)
	}
	d, err := set.MMMDrawsOf("m")
	if err != nil {
		t.Fatal(err)
	}
	seed := optblock.EffectiveSeed(spec)
	picks := SubsampleDraws(seed, d.Len(), spec.Posterior.Draws)
	for _, k := range picks {
		v, err := set.WithMMMDraw("m", d, k)
		if err != nil {
			t.Fatal(err)
		}
		readers = append(readers, model.CoefBuiltin(v))
	}
	out, err := SolvePosterior(spec, 0, c, seed, &Draws{Fit: "m", Count: len(picks), Available: d.Len(), Level: d.Level,
		Select: func(k int) { cur = k + 1 }})
	if err != nil {
		t.Fatal(err)
	}
	if out.Failed {
		t.Fatalf("failed: %+v", out.Result.Error)
	}
	if cur != 0 {
		t.Errorf("the solve left draw %d selected", cur-1)
	}
	return &out.Result
}

// posteriorBlock is a block over posteriorModel with the given
// objective, constraints and posterior.
func posteriorBlock(objective, constraints, posterior string) string {
	return `{"name":"p","direction":"maximize","variables":[{"name":"x","lower":0,"upper":20}],"objective":"` + objective + `"` +
		constraints + `,"starts":2,"rng_seed":3,"models":[` + posteriorModel + `],"posterior":` + posterior + `}`
}

// TestOptimize_PosteriorMeanObjectiveAndInterval: under objective mean
// the optimum of −(x − β)² is the posterior mean of β (3) and f* the
// mean of the per-draw values (−0.625); each draw's own optimum is its
// β, so the 90% allocation interval is the type-7 pair of {2, 2.5, 3.5,
// 4} at 0.05 / 0.95 (2.075, 3.925), every re-solve converging.
func TestOptimize_PosteriorMeanObjectiveAndInterval(t *testing.T) {
	t.Parallel()

	r := solvePosterior(t, posteriorBlock(`-(x - coef(\"m\", \"tv\")) * (x - coef(\"m\", \"tv\"))`, ``, `{}`))
	x := r.Variables[0]
	if !near(x.Value, 3, 1e-6) || !near(r.Objective, -0.625, 1e-9) {
		t.Errorf("x %+v f* %v, want 3 and -0.625", x, r.Objective)
	}
	if x.Interval == nil || !near(x.Interval.Lower, 2.075, 1e-6) || !near(x.Interval.Upper, 3.925, 1e-6) {
		t.Errorf("interval %+v, want [2.075, 3.925]", x.Interval)
	}
	want := types.OptimizePosteriorResult{Fit: "m", Objective: types.OptimizePosteriorMean, Draws: 4, DrawsAvailable: 4, DrawsResolved: 4, CredibleLevel: 0.9}
	if r.Posterior == nil || *r.Posterior != want {
		t.Errorf("posterior %+v, want %+v", r.Posterior, want)
	}
}

// TestOptimize_PosteriorConstraintOnExpectation: a constraint holds on
// its mean over the draws — β² − x ≥ 0 caps x at E[β²] = 9.625, not at
// the posterior mean's square 9.
func TestOptimize_PosteriorConstraintOnExpectation(t *testing.T) {
	t.Parallel()

	r := solvePosterior(t, posteriorBlock(`x`, `,"constraints":[{"expr":"coef(\"m\", \"tv\") * coef(\"m\", \"tv\") - x","type":"ineq"}]`, `{}`))
	if x := r.Variables[0]; !near(x.Value, 9.625, 1e-6) {
		t.Errorf("x %+v, want 9.625", x)
	}
	// Each draw alone caps x at its β²: {4, 6.25, 12.25, 16}.
	if iv := r.Variables[0].Interval; iv == nil || !near(iv.Lower, 4+0.15*2.25, 1e-6) || !near(iv.Upper, 12.25+0.85*3.75, 1e-6) {
		t.Errorf("interval %+v", iv)
	}
}

// TestOptimize_PosteriorQuantileObjective: under objective quantile f*
// is the q-quantile (type 7) of the per-draw objective values at x*,
// and the echo carries q.
func TestOptimize_PosteriorQuantileObjective(t *testing.T) {
	t.Parallel()

	r := solvePosterior(t, posteriorBlock(`coef(\"m\", \"tv\") * x - x * x / 4`, ``, `{"objective":"quantile","q":0.1}`))
	x := r.Variables[0].Value
	vals := []float64{2*x - x*x/4, 4*x - x*x/4, 2.5*x - x*x/4, 3.5*x - x*x/4}
	slices.Sort(vals)
	if want := model.Quantile7(vals, 0.1); !near(r.Objective, want, 1e-9) {
		t.Errorf("f* %v, want the 0.1-quantile %v", r.Objective, want)
	}
	// The 0.1-quantile of β·x − x²/4 (x ≥ 0) is (2 + 0.3·0.5)·x − x²/4,
	// maximised at x = 2·2.15.
	if !near(x, 4.3, 1e-5) {
		t.Errorf("x %v, want 4.3", x)
	}
	if r.Posterior == nil || r.Posterior.Objective != types.OptimizePosteriorQuantile || r.Posterior.Q != 0.1 {
		t.Errorf("posterior %+v", r.Posterior)
	}
}

// TestOptimize_PosteriorSubsampleSeeded: draws:N evaluates N of the
// fit's draws, the same N every run, and reports how many it holds.
func TestOptimize_PosteriorSubsampleSeeded(t *testing.T) {
	t.Parallel()

	raw := posteriorBlock(`-(x - coef(\"m\", \"tv\")) * (x - coef(\"m\", \"tv\"))`, ``, `{"draws":2}`)
	a, b := solvePosterior(t, raw), solvePosterior(t, raw)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("two runs differ: %+v %+v", a, b)
	}
	if a.Posterior.Draws != 2 || a.Posterior.DrawsAvailable != 4 {
		t.Errorf("posterior %+v", a.Posterior)
	}
	// x* is the mean of the two picked β.
	beta := []float64{2, 4, 2.5, 3.5}
	picks := SubsampleDraws(a.RNGSeed, 4, 2)
	if want := (beta[picks[0]] + beta[picks[1]]) / 2; !near(a.Variables[0].Value, want, 1e-6) {
		t.Errorf("x %v, want %v (draws %v)", a.Variables[0].Value, want, picks)
	}
}

// TestSubsampleDraws: n distinct ascending draws, a pure function of
// the seed; 0 or n ≥ total keeps every draw.
func TestSubsampleDraws(t *testing.T) {
	t.Parallel()

	all := []int{0, 1, 2, 3, 4}
	for _, n := range []int{0, 5, 9} {
		if got := SubsampleDraws(7, 5, n); !slices.Equal(got, all) {
			t.Errorf("n %d: %v", n, got)
		}
	}
	got := SubsampleDraws(7, 1000, 50)
	if len(got) != 50 || !slices.IsSorted(got) || len(slices.Compact(slices.Clone(got))) != 50 || got[49] >= 1000 {
		t.Fatalf("subsample %v", got)
	}
	if !slices.Equal(got, SubsampleDraws(7, 1000, 50)) {
		t.Error("the same seed drew differently")
	}
	if slices.Equal(got, SubsampleDraws(8, 1000, 50)) {
		t.Error("another seed drew the same draws")
	}
	// A frozen draw: integer PCG arithmetic only, so every architecture
	// draws these.
	if f := SubsampleDraws(42, 20, 4); !slices.Equal(f, frozenSubsample) {
		t.Errorf("seed 42: %v, want %v", f, frozenSubsample)
	}
}

// frozenSubsample is SubsampleDraws(42, 20, 4).
var frozenSubsample = []int{2, 12, 15, 16}
