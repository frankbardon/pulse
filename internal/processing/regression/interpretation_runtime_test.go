package regression_test

import (
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing/regression"
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
	},
	types.REG_BAYES_LINEAR: {
		{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x1", "x2"}},
	},
}

// probeRows is a deterministic noisy fixture: a numeric target with
// non-zero residuals (so r2, p-values and the residual SE are all
// defined and non-zero) and a 0/1 target that is not separable.
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
		out[i] = probeRecord{"x1": x1, "x2": x2, "y": 3 + 2*x1 - x2 + noise, "b": b}
	}
	return out
}

// TestRegressionOutputsHoldAtRuntime holds the descriptor's per-type
// applicability table (which every regression Interpretation is
// validated against) to what the engines really emit on the plain fit
// path: every listed key appears in at least one probe of the type, and
// no probe emits a fit-output key another type owns but this row omits
// (no r2 on REG_GLM, no p_values on REG_BAYES_LINEAR, pseudo_r2 on
// REG_GLM only). Echo fields (type, family, penalty, ...) are ignored.
func TestRegressionOutputsHoldAtRuntime(t *testing.T) {
	universe := map[string]bool{}
	for _, r := range types.AllRegressionTypes() {
		for _, k := range descx.RegressionOutputKeys(string(r)) {
			universe[k] = true
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64}, {Name: "b", Type: encoding.FieldTypeF64},
		{Name: "x1", Type: encoding.FieldTypeF64}, {Name: "x2", Type: encoding.FieldTypeF64},
	}}
	rows := probeRows()
	for _, r := range types.AllRegressionTypes() {
		specs, ok := regressionProbes[r]
		if !ok {
			t.Errorf("%s has no runtime probe; add one to regressionProbes", r)
			continue
		}
		want := descx.RegressionOutputKeys(string(r))
		emitted := map[string]bool{}
		for i := range specs {
			spec := specs[i]
			res, err := regression.FitBuffered([]*types.RegressionSpec{&spec}, schema, rows)
			if err != nil {
				t.Fatalf("%s probe %d: %v", r, i, err)
			}
			raw, err := json.Marshal(res[0])
			if err != nil {
				t.Fatalf("%s probe %d: marshal: %v", r, i, err)
			}
			var keys map[string]json.RawMessage
			if err := json.Unmarshal(raw, &keys); err != nil {
				t.Fatal(err)
			}
			for k := range keys {
				if !universe[k] {
					continue // an echo of the spec, not a fit output
				}
				emitted[k] = true
				if !slices.Contains(want, k) {
					t.Errorf("%s probe %d emits %q, which its applicability row %v omits", r, i, k, want)
				}
			}
		}
		var missing []string
		for _, k := range want {
			if !emitted[k] {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s: applicability row lists %s, but no probe emits them", r, strings.Join(missing, ", "))
		}
	}
}
