package regression_test

import (
	"testing"

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
