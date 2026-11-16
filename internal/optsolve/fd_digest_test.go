package optsolve

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/types"
)

// fdCorpus is the pre-analytic optimize suite the `gradient: "fd"`
// digest pins: every expression-only block of this package's tests
// plus the budget fixture and one block per derivative-rule family —
// objective and eq / ineq constraints, bounds active and free.
var fdCorpus = []struct {
	raw    string
	params map[string]any
}{
	{`{"name":"q","direction":"minimize","variables":[{"name":"x","lower":-10,"upper":10},{"name":"y","lower":0,"upper":5}],"objective":"(x - 3) * (x - 3) + (y + 1) * (y + 1)","starts":3,"rng_seed":5}`, nil},
	{`{"name":"b","direction":"maximize","variables":[{"name":"x","lower":0,"upper":10},{"name":"y","lower":0,"upper":10}],"objective":"4 * sqrt(x) + 2 * sqrt(y)","constraints":[{"expr":"x + y - params.budget","type":"eq"}],"starts":2,"tolerance":1e-12}`, map[string]any{"budget": 10}},
	{`{"name":"m","direction":"minimize","variables":[{"name":"x","lower":-10,"upper":10}],"objective":"(x - 5) * (x - 5)","constraints":[{"expr":"2 - x","type":"ineq"}],"starts":1}`, nil},
	{`{"name":"m","direction":"maximize","variables":[{"name":"x","lower":-10,"upper":10}],"objective":"-(x - 5) * (x - 5)","constraints":[{"expr":"2 - x","type":"ineq"}],"starts":1}`, nil},
	{`{"name":"w","direction":"minimize","variables":[{"name":"x","lower":-2,"upper":2,"start":1}],"objective":"(x * x - 1) * (x * x - 1) + 0.3 * x","starts":12,"rng_seed":3}`, nil},
	{`{"name":"w","direction":"minimize","variables":[{"name":"x","lower":-2,"upper":2},{"name":"y","lower":-2,"upper":2}],"objective":"(x * x - 1) * (x * x - 1) + (y - 0.5) * (y - 0.5) + 0.3 * x","starts":6,"rng_seed":9}`, nil},
	{`{"name":"f","direction":"minimize","variables":[{"name":"x","lower":0,"upper":10}],"objective":"x * x","constraints":[{"expr":"x - 5","type":"ineq"},{"expr":"2 - x","type":"ineq"}],"starts":3}`, nil},
	{`{"name":"f","direction":"minimize","variables":[{"name":"x","lower":-3,"upper":3},{"name":"y","lower":-3,"upper":3}],"objective":"(1 - x) * (1 - x) + 100 * (y - x * x) * (y - x * x)","max_iterations":1,"starts":2}`, nil},
	{`{"name":"allocation","direction":"maximize","variables":[{"name":"tv","lower":0,"upper":100},{"name":"search","lower":0,"upper":100},{"name":"social","lower":0,"upper":"params.cap_social"},{"name":"display","lower":0,"upper":100}],"objective":"120 * hill(tv, 40, 2) + 60 * hill(search, 10, 1) + 150 * hill(social, 60, 1.5) + 30 * hill(display, 50, 1)","constraints":[{"expr":"tv + search + social + display - params.budget","type":"eq"}],"starts":8}`, map[string]any{"budget": 120, "cap_social": 30}},
	{`{"name":"t","direction":"maximize","variables":[{"name":"a","lower":0.1,"upper":3},{"name":"b","lower":0.1,"upper":3}],"objective":"log(a) + log1p(b) - exp(a / 4) + sin(b) + cos(a) * 0.1 + atan2(a, b) * 0.05 - pow(b, 1.5) * 0.1 - a ** 2 * 0.05 + tan(a / 10) + pi() * 0 + e() * 0","constraints":[{"expr":"4 - a - b","type":"ineq"},{"expr":"a * b - 0.5","type":"ineq"}],"starts":4,"rng_seed":11}`, nil},
	{`{"name":"c","direction":"minimize","variables":[{"name":"x","lower":-4,"upper":4},{"name":"y","lower":-4,"upper":4}],"objective":"(x > 0 ? x * x : -x) + abs(y - 1) + max(x, y) * 0.1 + min(x, 0) * 0.2 + (x % 3) * 0","constraints":[{"expr":"x / (1 + y * y) - 0.25","type":"eq"}],"starts":5,"rng_seed":2}`, nil},
}

// fdDigests are the sha256 of each fdCorpus result's wire JSON as the
// central-difference solver produced it BEFORE the analytic evaluator
// landed — `gradient: "fd"` must keep reproducing these bits.
var fdDigests = []string{
	"ac1f76957c0d559b35ed788757956e7d03372ace60ee2f96651aa76553a8a03b",
	"7bfa91195298c5eb858438e4213b21cea254419c2d0a746170625ad35b0a6a84",
	"17757477c6cbc8815a108e8cc9627e81c9130bd93f5d43a7edfa60e05dd3fd0d",
	"9cec4e0937154c9befa12955438577861410a3cc41fd5644910420ad4567d085",
	"2ff492de51ef82a7eaae04e47b2d18a6840fbd27fd6ebbe4d9108b754dbb9d55",
	"d7df42b5bddfae5c1e2504f1911658177cbab8570f9a9e5cd6becd56163e8199",
	"bcd9fb913e53c3839befbc9186acebacd2ce77cfe54b1641be6324d2c90508ca",
	"621f16e3a4f5aa2bd4eec969a1c0986a8e7c49df27a4a7d928a8700cbb521082",
	"ee894a41c4e27d7176ea6d65796aa60531c5f7b4be7723648db6655d6778ed7d",
	"a77e119214fad55109a00c7836668818b54f1d75cf91782a9eb08f2795c5b6d5",
	"bbed5294c6ac5d1ca0e89d2b07d656e3545b4a32024e3b6dacd9605d0e9be7ec",
}

// failedEntry marks a corpus block that fails on its numbers (no
// gradient echo) in corpusDigests' modes.
const failedEntry types.OptimizeGradient = "failed"

// corpusDigests solves every fdCorpus block (edited by edit) and
// returns each result's digest with its gradient echo (Gradient,
// GradientFallback — a label added after the digests were taken, never
// a figure) cleared, plus each block's echoed Gradient.
func corpusDigests(t *testing.T, edit func(*types.OptimizeSpec)) ([]string, []types.OptimizeGradient) {
	t.Helper()
	out := make([]string, len(fdCorpus))
	modes := make([]types.OptimizeGradient, len(fdCorpus))
	for k, tc := range fdCorpus {
		spec := block(t, tc.raw)
		if edit != nil {
			edit(spec)
		}
		o, err := solve(t, spec, tc.params, optblock.Env{}, exprenv.Bound{})
		if err != nil {
			t.Fatalf("corpus[%d]: %v", k, err)
		}
		r := o.Result
		modes[k] = r.Gradient
		if o.Failed {
			// A failed entry keeps name, direction and error only.
			if r.Gradient != "" || r.GradientFallback != nil {
				t.Errorf("corpus[%d] failed but echoes gradient %q fallback %v", k, r.Gradient, r.GradientFallback)
			}
			modes[k] = failedEntry
		}
		r.Gradient, r.GradientFallback = "", nil
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		out[k] = hex.EncodeToString(sum[:])
	}
	return out, modes
}

// TestOptimize_GradientFDByteIdentical: every corpus block under
// `gradient: "fd"` reproduces the pre-analytic solver's result bit for
// bit (the starts too — the derived seed excludes gradient), and the
// default analytic gradient is really in play: a block every expression
// of which differentiates — hill()'s budget fixture included — solves
// to other bits (TestOptimize_GradientFallbackIsPerBlock pins a
// fallback block to the very fd bits).
func TestOptimize_GradientFDByteIdentical(t *testing.T) {
	t.Parallel()

	got, modes := corpusDigests(t, func(s *types.OptimizeSpec) { s.Gradient = types.OptimizeGradientFD })
	for k := range fdCorpus {
		if got[k] != fdDigests[k] {
			t.Errorf("corpus[%d] under gradient fd: digest %s, want the pre-analytic %s", k, got[k], fdDigests[k])
		}
		if modes[k] != types.OptimizeGradientFD && modes[k] != failedEntry {
			t.Errorf("corpus[%d] under gradient fd echoes gradient %q", k, modes[k])
		}
	}
	analytic, amodes := corpusDigests(t, nil)
	for k := range fdCorpus {
		if amodes[k] != types.OptimizeGradientAnalytic && amodes[k] != failedEntry {
			t.Errorf("corpus[%d] by default echoes gradient %q, want analytic (every expression differentiates)", k, amodes[k])
		}
	}
	if analytic[0] == fdDigests[0] {
		t.Errorf("corpus[0] solved analytically gives the fd bits: the analytic gradient is not wired")
	}
	const hillBlock = 8 // the budget fixture: hill() differentiates
	if analytic[hillBlock] == fdDigests[hillBlock] {
		t.Errorf("corpus[%d] (hill) solved analytically gives the fd bits: hill's rule is not wired", hillBlock)
	}
}
