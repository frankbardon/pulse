package regadapt

import (
	"testing"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing/regression"
)

// TestModelRegressions_RegisteredByEngine: every model regression is
// installed on the engine's built-in registry by this package's init —
// no root package, no Extensions — driven by its descriptor declaration
// (the one predict reads), and the engine's scorer table resolves it
// exactly when the declaration says it has a scorer.
func TestModelRegressions_RegisteredByEngine(t *testing.T) {
	if len(model.ModelRegressions()) == 0 {
		t.Fatal("no model regressions")
	}
	for _, rt := range model.ModelRegressions() {
		a, ok := ModelRegressionAdapter(rt)
		if !ok || a.Factory == nil {
			t.Fatalf("%s: no adapter (declaration %v, factory %v)", rt, ok, a.Factory != nil)
		}
		if _, ok := regression.Lookup(rt); !ok {
			t.Errorf("%s: the engine registry does not resolve it", rt)
		}
		if _, ok := regression.BuiltinScorer(rt); ok != a.Decl.Scorer {
			t.Errorf("%s: BuiltinScorer resolves %v, declaration scorer %v", rt, ok, a.Decl.Scorer)
		}
		if a.Streamable || a.WeightAware {
			t.Errorf("%s: a model regression is buffered and unweighted", rt)
		}
	}
}
