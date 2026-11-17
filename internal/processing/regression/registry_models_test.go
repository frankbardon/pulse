package regression_test

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing/regression"
	// internal/regadapt registers the model regressions on this
	// package's built-in registry at its init.
	_ "github.com/frankbardon/pulse/internal/regadapt"
	"github.com/frankbardon/pulse/types"
)

// TestRegistry_AllTypesRegistered asserts every RegressionType returned
// by types.AllRegressionTypes() has a factory once internal/regadapt is
// linked (as it is in every binary that runs a request, through
// internal/service): the built-in engines this package's init
// registers plus the model regressions regadapt adds. Catches the
// "added REG_* to types but forgot its registration" drift — with no
// fallback stub, a forgotten type is refused as unknown at Build.
func TestRegistry_AllTypesRegistered(t *testing.T) {
	t.Parallel()

	registered := map[types.RegressionType]bool{}
	for _, rt := range regression.RegisteredTypes() {
		registered[rt] = true
	}
	for _, rt := range types.AllRegressionTypes() {
		if !registered[rt] {
			t.Errorf("regression type %s not registered", rt)
		}
		if _, ok := regression.Lookup(rt); !ok {
			t.Errorf("Lookup(%s) misses", rt)
		}
	}
	if len(registered) != len(types.AllRegressionTypes()) {
		t.Errorf("registered count = %d, want %d", len(registered), len(types.AllRegressionTypes()))
	}
}

// probeRow is a one-column scoring row.
type probeRow map[string]float64

func (r probeRow) NumericValue(name string) (float64, bool) { v, ok := r[name]; return v, ok }

// outcomeProbes are minimal bound fits of every categorical-outcome
// type, each with three outcomes over predictor x.
var outcomeProbes = map[types.RegressionType]*types.RegressionResult{
	types.REG_ORDINAL: {Coefficients: map[string]float64{"x": 0.5, "1|2": -1, "2|3": 1},
		Outputs: map[string]any{"link": "logit", "levels": []string{"1", "2", "3"}}},
	types.REG_MULTINOMIAL: {Categories: []string{"a", "b", "c"}, Reference: "a",
		Coefficients: map[string]float64{"b:(intercept)": 0.2, "b:x": 0.4, "c:(intercept)": -0.3, "c:x": 0.9}},
}

// TestBoundProbabilities_EveryOutcomeType: every categorical-outcome
// regression type (model.OutcomeType) resolves a probability-vector
// scorer once internal/regadapt is linked — REG_MULTINOMIAL's native
// one, REG_ORDINAL's registered beside its scorer — whose vector over
// its three outcomes sums to 1; a numeric type has none (a holdout over
// it folds rmse / mae / r2 instead).
func TestBoundProbabilities_EveryOutcomeType(t *testing.T) {
	t.Parallel()

	for _, typ := range types.AllRegressionTypes() {
		res, probe := outcomeProbes[typ]
		if !model.OutcomeType(typ) {
			if probe {
				t.Errorf("%s has an outcome probe but is no categorical-outcome type", typ)
			}
			if _, err := regression.BoundProbabilities(&model.Bound{Name: "m", Type: typ}); err == nil {
				t.Errorf("%s resolves a probability-vector scorer", typ)
			}
			continue
		}
		if !probe {
			t.Errorf("%s is a categorical-outcome type with no probe: add one to outcomeProbes", typ)
			continue
		}
		ps, err := regression.BoundProbabilities(&model.Bound{Name: "m", Type: typ, Target: "y", Predictors: []string{"x"}, Result: res})
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		p := make([]float64, len(ps.Outcomes()))
		if len(p) != 3 || !ps.Probabilities(probeRow{"x": 0.7}, p) {
			t.Fatalf("%s: outcomes %v, vector not scored", typ, ps.Outcomes())
		}
		if s := p[0] + p[1] + p[2]; math.Abs(s-1) > 1e-12 {
			t.Errorf("%s: probabilities %v sum to %v", typ, p, s)
		}
	}
}
