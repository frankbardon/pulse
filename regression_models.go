package pulse

import (
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/types"
)

// regression_models.go wires the model regressions
// (internal/model.ModelRegression): built-in REG_* types authored on the
// extension contract. Each is a RegressionRegistration driven through
// the extension adapter (adaptRegressionFactory, adaptRegressionScorer)
// and installed on the engine's BUILT-IN registry at package init, so
// every instance — the stock CLI and `pulse mcp` included — runs it with
// no Extensions. Its declaration is the descriptor's static twin
// (descx.ModelRegressionDeclaration), which predict reads; the parity
// test pins the two.

// modelRegressionRegistrations returns the model regressions'
// registrations, in types.AllRegressionTypes order.
func modelRegressionRegistrations() []RegressionRegistration {
	return []RegressionRegistration{mmmHillRegistration()}
}

// mmmHillRegistration is REG_MMM_HILL's registration: the media-mix
// model of internal/regmmm (its Factory doc carries the model, params
// and coefficient table).
func mmmHillRegistration() RegressionRegistration {
	return RegressionRegistration{
		Name: types.REG_MMM_HILL,
		Params: []ParamMeta{
			{Name: "channels", JSONType: "array", Required: true, Description: "Predictors entering through the Hill curve (adstocked spend, at least one); the other predictors are linear controls."},
			{Name: "nonnegative", JSONType: "boolean", Default: true, Description: "Bound every channel β below by 0, so an unsupported channel is exactly 0 (default true)."},
			{Name: "prior", JSONType: "object", Description: "Gaussian MAP prior {channel, mean, precision} on one channel's β (an experiment-measured effect)."},
		},
		OutputSchema: []RegressionOutputMeta{
			{Key: "k", JSONType: "object", Description: "Hill half-saturation per channel (the coefficient k:<channel>)."},
			{Key: "s", JSONType: "object", Description: "Hill shape per channel (the coefficient s:<channel>)."},
		},
		Factory: regmmm.Factories[types.REG_MMM_HILL],
		Scorer:  regmmm.Scorer,
		Vcov:    true,
		// The model reports no p-value itself; under resample the engine
		// writes the replicate ones.
		PValues: true,
	}
}

func init() {
	for _, reg := range modelRegressionRegistrations() {
		var scorer model.ScorerFactory
		if reg.Scorer != nil {
			scorer = adaptRegressionScorer(reg)
		}
		regression.RegisterModel(reg.Name, adaptRegressionFactory(reg), scorer)
	}
}
