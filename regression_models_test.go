package pulse

import (
	"reflect"
	"slices"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing/regression"
)

// TestModelRegressions_DeclarationMatchesDescriptor: every model
// regression is registered here, in order, and its registration's
// declaration — what the runtime adapter judges a slot by — is exactly
// the descriptor's static twin, which predict judges the same slot by;
// the engine's built-in registry and scorer table resolve it.
func TestModelRegressions_DeclarationMatchesDescriptor(t *testing.T) {
	regs := modelRegressionRegistrations()
	var names []string
	for _, reg := range regs {
		names = append(names, string(reg.Name))
	}
	var want []string
	for _, rt := range model.ModelRegressions() {
		want = append(want, string(rt))
	}
	if !slices.Equal(names, want) {
		t.Fatalf("registrations %v, model regressions %v", names, want)
	}
	for _, reg := range regs {
		decl, ok := descx.ModelRegressionDeclaration(reg.Name)
		if !ok {
			t.Fatalf("%s: no descriptor declaration", reg.Name)
		}
		if got := regressionDeclaration(reg); !reflect.DeepEqual(got, decl) {
			t.Errorf("%s: registration declares\n%+v\ndescriptor declares\n%+v", reg.Name, got, decl)
		}
		if reg.Streamable || reg.WeightAware {
			t.Errorf("%s: the descriptor declares a buffered, unweighted model", reg.Name)
		}
		if _, ok := regression.Lookup(reg.Name); !ok {
			t.Errorf("%s: the engine registry does not resolve it", reg.Name)
		}
		if _, ok := regression.BuiltinScorer(reg.Name); ok != (reg.Scorer != nil) {
			t.Errorf("%s: BuiltinScorer resolves %v, registration scorer %v", reg.Name, ok, reg.Scorer != nil)
		}
	}
}
