package regression_test

import (
	"encoding/json"
	"math"

	"github.com/frankbardon/pulse/internal/processing/regression"
	// internal/regadapt registers the model regressions (REG_MMM_HILL,
	// REG_MMM_BAYES) on this package's built-in registry at its init —
	// the same import every engine binary carries (internal/service) —
	// so FitBuffered below runs them as every engine does.
	_ "github.com/frankbardon/pulse/internal/regadapt"
	"github.com/frankbardon/pulse/types"
)

type probeRecord map[string]float64

func (r probeRecord) NumericValue(name string) (float64, bool) {
	v, ok := r[name]
	return v, ok
}

// regressionProbes are the plain-fit fixtures per REG_* type. A type
// may need more than one: converged_iters is emitted by an iterative
// fit only (OLS l1, GLM IRLS), never by a closed-form one.
var regressionProbes = map[types.RegressionType][]types.RegressionSpec{
	types.REG_OLS: {
		{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2"}},
		{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2"}, Penalty: "l1", Alpha: 0.01},
	},
	types.REG_GLM: {
		{Type: types.REG_GLM, Target: "b", Predictors: []string{"x1", "x2"}, Family: "binomial"},
		// gamma estimates its dispersion: df (its t reference) is emitted.
		{Type: types.REG_GLM, Target: "g", Predictors: []string{"x1", "x2"}, Family: "gamma"},
		// negative_binomial with θ estimated: theta, theta_std_err,
		// two_log_lik and aic are emitted (k an over-dispersed count).
		{Type: types.REG_GLM, Target: "k", Predictors: []string{"x1", "x2"}, Family: "negative_binomial"},
	},
	types.REG_BAYES_LINEAR: {
		{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x1", "x2"}},
	},
	// The model regressions: a saturating spend channel c and a control
	// x2 over the target m; REG_MMM_BAYES reads the contiguous time t.
	types.REG_MMM_HILL: {
		{Type: types.REG_MMM_HILL, Target: "m", Predictors: []string{"c", "x2"}, Params: json.RawMessage(`{"channels":["c"]}`)},
	},
	types.REG_MMM_BAYES: {
		{Type: types.REG_MMM_BAYES, Target: "m", Predictors: []string{"c", "x2"}, RNGSeed: 1,
			Params: json.RawMessage(`{"channels":["c"],"time":"t","chains":2,"warmup":100,"draws":50}`)},
	},
}

// probeRows is a deterministic noisy fixture: a numeric target with
// non-zero residuals (so r2, p-values and the residual SE are all
// defined and non-zero), a 0/1 target that is not separable and a
// positive target g for the gamma family.
func probeRows() []regression.Record {
	const n = 80
	out := make([]regression.Record, n)
	for i := 0; i < n; i++ {
		x1 := (float64(i) - 40) * 0.1
		x2 := math.Sin(0.31*float64(i) + 0.2)
		noise := 0.9 * math.Sin(1.7*float64(i))
		b := 0.0
		if -0.5+1.2*x1+0.8*x2+noise > 0 {
			b = 1
		}
		c := 60 + 50*math.Sin(0.53*float64(i))
		out[i] = probeRecord{"x1": x1, "x2": x2, "y": 3 + 2*x1 - x2 + noise, "b": b,
			"g": 1 / (0.8 + 0.05*x1 + 0.1*x2) * (1 + 0.3*noise),
			"c": c, "t": float64(i + 1), "m": 5 + 8*c/(c+40) - 0.5*x2 + 0.3*noise,
			"k": math.Floor(math.Exp(1 + 0.3*x1 + 0.4*x2 + 1.2*math.Sin(2.3*float64(i))))}
	}
	return out
}
