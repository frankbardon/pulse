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
	t.Parallel()

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

// TestBootstrapSeed_CarryoverSeedNeutral: REG_MMM_HILL's
// params.carryover (true or false) derives the seed leaving it out
// derives, a params body without it keeps its canonical bytes, and the
// key stays in every other type's seed.
func TestBootstrapSeed_CarryoverSeedNeutral(t *testing.T) {
	t.Parallel()

	records := makeLinearRecords(t, 20, []float64{1.5, -0.8}, 0.3, 0.4, 11)
	spec := func(typ types.RegressionType, params string) *types.RegressionSpec {
		return &types.RegressionSpec{Type: typ, Target: "y", Predictors: predictorNames(2),
			Resample: "bootstrap", BootstrapIters: 200, Params: json.RawMessage(params)}
	}
	const absent = `{"channels":["x1"],"adstock":"geometric","max_lag":6}`
	base := deriveBootstrapSeed(spec(types.REG_MMM_HILL, absent), records, predictorNames(2))
	for _, p := range []string{
		`{"channels":["x1"],"adstock":"geometric","max_lag":6,"carryover":true}`,
		`{"carryover": false, "max_lag":6, "adstock":"geometric","channels":["x1"]}`,
	} {
		if got := deriveBootstrapSeed(spec(types.REG_MMM_HILL, p), records, predictorNames(2)); got != base {
			t.Errorf("%s: seed %d, carryover absent %d", p, got, base)
		}
	}
	if got := string(seedParams(types.REG_MMM_HILL, json.RawMessage(absent))); got != absent {
		t.Errorf("params without carryover rewritten: %s", got)
	}
	if deriveBootstrapSeed(spec(types.REG_OLS, `{"carryover":true}`), records, predictorNames(2)) ==
		deriveBootstrapSeed(spec(types.REG_OLS, `{}`), records, predictorNames(2)) {
		t.Error("carryover dropped from a REG_OLS seed")
	}
}
