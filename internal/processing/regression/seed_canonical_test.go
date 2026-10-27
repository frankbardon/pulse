package regression

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestBootstrapSeed_ParamsKeyOrder: at rng_seed 0 the bootstrap seed
// hashes the raw params in canonical form — key order (any depth) and
// whitespace never move it, a value change does.
func TestBootstrapSeed_ParamsKeyOrder(t *testing.T) {
	records := makeLinearRecords(t, 20, []float64{1.5, -0.8}, 0.3, 0.4, 11)
	spec := func(params string) *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: predictorNames(2),
			Resample: "bootstrap", BootstrapIters: 200, Params: json.RawMessage(params)}
	}
	a := deriveBootstrapSeed(spec(`{"a":{"x":1,"y":0.10},"b":[2,1]}`), records, predictorNames(2))
	b := deriveBootstrapSeed(spec(`{ "b":[2, 1], "a":{"y":0.10,"x":1}}`), records, predictorNames(2))
	if a != b {
		t.Fatalf("params key order moved the seed: %d vs %d", a, b)
	}
	if deriveBootstrapSeed(spec(`{"a":{"x":1,"y":0.1},"b":[2,1]}`), records, predictorNames(2)) == a {
		t.Fatal("a params value change did not move the seed")
	}
}
