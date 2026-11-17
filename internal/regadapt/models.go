package regadapt

import (
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/internal/regordinal"
	"github.com/frankbardon/pulse/types"
)

// models.go wires the model regressions
// (internal/model.ModelRegression): built-in REG_* types authored on the
// extend contract. Each is driven through the shared extension adapter
// (AdaptRegressionFactory, AdaptRegressionScorer) over its static
// declaration — internal/descriptor's model-regression table, the very
// declaration predict judges the slot by — and its internal/regmmm
// factory and scorer, and installed on the engine's BUILT-IN registry at
// this package's init (internal/regmmm's media-mix models,
// internal/regordinal's REG_ORDINAL). internal/service imports this package, so every
// binary that runs a request — the stock CLI, `pulse mcp`, every
// embedder, and every service test — runs them with no Extensions and
// no root package; an engine-package test imports it directly.

// ModelRegressionAdapter returns model regression t's adapter — its
// descriptor declaration, its factory and its optional scorers — and
// whether t is one.
func ModelRegressionAdapter(t types.RegressionType) (RegressionAdapter, bool) {
	decl, ok := descx.ModelRegressionDeclaration(t)
	if !ok || !model.ModelRegression(t) {
		return RegressionAdapter{}, false
	}
	factory, ok := regmmm.Factories[t]
	if !ok {
		factory = regordinal.Factories[t]
	}
	scorer, ok := regmmm.Scorers[t]
	if !ok {
		scorer = regordinal.Scorers[t]
	}
	return RegressionAdapter{
		Name:        t,
		Decl:        decl,
		WeightAware: descx.ModelRegressionWeightAware(t),
		Factory:     factory,
		Scorer:      scorer,
		BoundScorer: regmmm.BoundScorers[t],
		ParamScorer: regordinal.ParamScorers[t],
		Dispersion:  regmmm.Dispersions[t],
	}, true
}

func init() {
	for _, t := range model.ModelRegressions() {
		a, ok := ModelRegressionAdapter(t)
		if !ok || a.Factory == nil || a.Decl.Scorer != (a.Scorer != nil) {
			// Wiring drift (a missing declaration or factory, a scorer the
			// declaration disowns) surfaces at boot.
			panic("regadapt: model regression " + string(t) + " is not wired: declaration, factory and scorer must agree")
		}
		var scorer model.ScorerFactory
		if a.Scorer != nil {
			scorer = AdaptRegressionScorer(a)
		}
		regression.RegisterModel(t, AdaptRegressionFactory(a), scorer)
		if tf, ok := regmmm.Tangents[t]; ok {
			// A nonlinear least-squares fit's tangent plane: what a
			// leverage / influence slot over it reads (U71 E2-S6).
			regression.RegisterModelTangent(t, tf)
		}
	}
}
