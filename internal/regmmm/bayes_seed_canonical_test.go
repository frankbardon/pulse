package regmmm

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestBayesEffectiveSeed_ParamsKeyOrder: at rng_seed 0 the REG_MMM_BAYES
// seed hashes the raw params in canonical form — key order (any depth)
// and whitespace never move it, a value change does, a pinned seed is
// used as given.
func TestBayesEffectiveSeed_ParamsKeyOrder(t *testing.T) {
	spec := func(params string) *types.RegressionSpec {
		return &types.RegressionSpec{Type: "REG_MMM_BAYES", Target: "y", Predictors: []string{"tv"}, Params: json.RawMessage(params)}
	}
	a := bayesEffectiveSeed(spec(`{"draws":100,"media":{"tv":{"max_lag":4,"decay":0.5}}}`))
	b := bayesEffectiveSeed(spec(`{"media":{"tv":{"decay":0.5, "max_lag":4}}, "draws":100}`))
	if a != b {
		t.Fatalf("params key order moved the seed: %d vs %d", a, b)
	}
	if bayesEffectiveSeed(spec(`{"draws":101,"media":{"tv":{"max_lag":4,"decay":0.5}}}`)) == a {
		t.Fatal("a params value change did not move the seed")
	}
	pinned := spec(`{"draws":100}`)
	pinned.RNGSeed = 7
	if bayesEffectiveSeed(pinned) != 7 {
		t.Fatal("a pinned rng_seed must be used as given")
	}
}
