package optblock_test

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/types"
)

// posteriorEnv is testEnv over the demand fit plus REG_MMM_BAYES fits
// `bayes` (return_draws) and `bare` (no draws), and a second
// draw-carrying `other`.
func posteriorEnv() optblock.Env {
	env := testEnv()
	mmm := func(name, params string) types.FitSpec {
		return types.FitSpec{Name: name, Request: &types.Request{Cohort: &types.Cohort{Filename: "mmm.pulse"},
			Regressions: []*types.RegressionSpec{{Type: types.REG_MMM_BAYES, Target: "sales", Predictors: []string{"tv"}, Params: json.RawMessage(params)}}}}
	}
	env.Fits = model.CatalogFromFits(append(demandFits(),
		mmm("bayes", `{"channels":["tv"],"time":"week","return_draws":true}`),
		mmm("bare", `{"channels":["tv"],"time":"week"}`),
		mmm("other", `{"channels":["tv"],"time":"week","return_draws":true}`)))
	return env
}

// posteriorBlock is an allocation over the Bayes fit's tv response
// with posterior p.
func posteriorBlock(p *types.OptimizePosterior) types.OptimizeSpec {
	return types.OptimizeSpec{
		Name:      "alloc",
		Variables: []types.OptimizeVariable{{Name: "tv", Lower: types.BoundValue(0), Upper: types.BoundParam("budget")}},
		Objective: `response("bayes", "tv", tv) - tv`,
		Direction: types.OptimizeMaximize,
		Posterior: p,
	}
}

// TestOptimize_PosteriorRefusals: each posterior rule refuses with
// PULSE_OPTIMIZE_SPEC_INVALID, its reason and field; a block over one
// draw-carrying REG_MMM_BAYES fit passes and names it the carrier.
func TestOptimize_PosteriorRefusals(t *testing.T) {
	t.Parallel()

	env := posteriorEnv()
	for _, p := range []*types.OptimizePosterior{{}, {Objective: "mean"}, {Objective: "quantile", Q: 0.1}, {Draws: 200}} {
		spec := posteriorBlock(p)
		c, cerr := optblock.Compile(&spec, 0, env, optblockDeclared(env))
		if cerr != nil {
			t.Fatalf("%+v refused: %v", p, cerr)
		}
		if c.PosteriorFit != "bayes" {
			t.Fatalf("%+v: carrier %q", p, c.PosteriorFit)
		}
	}
	cases := []struct {
		name   string
		edit   func(s *types.OptimizeSpec)
		reason string
		field  string
	}{
		{"objective unknown", func(s *types.OptimizeSpec) { s.Posterior.Objective = "median" }, optblock.ReasonPosteriorObjectiveUnknown, "optimize[0].posterior.objective"},
		{"quantile without q", func(s *types.OptimizeSpec) { s.Posterior.Objective = "quantile" }, optblock.ReasonPosteriorQInvalid, "optimize[0].posterior.q"},
		{"quantile q of one", func(s *types.OptimizeSpec) { s.Posterior.Objective, s.Posterior.Q = "quantile", 1 }, optblock.ReasonPosteriorQInvalid, "optimize[0].posterior.q"},
		{"q with mean", func(s *types.OptimizeSpec) { s.Posterior.Q = 0.5 }, optblock.ReasonPosteriorQInvalid, "optimize[0].posterior.q"},
		{"negative draws", func(s *types.OptimizeSpec) { s.Posterior.Draws = -1 }, optblock.ReasonPosteriorDrawsInvalid, "optimize[0].posterior.draws"},
		{"no fit read", func(s *types.OptimizeSpec) { s.Objective = "tv * (100 - tv)" }, optblock.ReasonPosteriorFitMissing, "optimize[0].posterior"},
		{"two fits read", func(s *types.OptimizeSpec) {
			s.Constraints = []types.OptimizeConstraint{{Expr: `response("other", "tv", tv) - 1`, Type: types.OptimizeConstraintIneq}}
		}, optblock.ReasonPosteriorFitsMultiple, "optimize[0].posterior"},
		{"not a Bayes fit", func(s *types.OptimizeSpec) { s.Objective = `coef("demand", "price") * tv` }, optblock.ReasonPosteriorFitNotBayes, "optimize[0].posterior"},
		{"no draws", func(s *types.OptimizeSpec) { s.Objective = `response("bare", "tv", tv)` }, model.ReasonDrawsRequired, "optimize[0].posterior"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := posteriorBlock(&types.OptimizePosterior{})
			c.edit(&spec)
			cerr := optblock.Check([]types.OptimizeSpec{spec}, env)
			if cerr == nil || cerr.Code != errors.PULSE_OPTIMIZE_SPEC_INVALID || cerr.Details["reason"] != c.reason || cerr.Details["field"] != c.field {
				t.Fatalf("got %v, want reason %s at %s", cerr, c.reason, c.field)
			}
		})
	}
	// Without posterior the same reads pass (a non-Bayes fit, no fit).
	spec := posteriorBlock(nil)
	spec.Objective = `coef("demand", "price") * tv`
	if cerr := optblock.Check([]types.OptimizeSpec{spec}, env); cerr != nil {
		t.Fatalf("posterior-free block refused: %v", cerr)
	}
}

// optblockDeclared binds coef() and response() to a stub: Compile
// type-checks the calls and evaluates no bound that reads them.
func optblockDeclared(optblock.Env) exprenv.Bound {
	stub := func(...any) (any, error) { return 1.0, nil }
	return exprenv.Bound{Coef: stub, Response: stub}
}
