package multiplicity

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestMethodsMatchTypesVocabulary: the core's method set is exactly the
// wire vocabulary the resolver validates against
// (types.AllMultiplicityMethods), so a name the resolver accepts always
// reaches a core method and vice versa.
func TestMethodsMatchTypesVocabulary(t *testing.T) {
	core := Methods()
	wire := types.AllMultiplicityMethods()
	if len(core) != len(wire) {
		t.Fatalf("core has %d methods, types %d", len(core), len(wire))
	}
	for i := range core {
		if string(core[i]) != string(wire[i]) {
			t.Errorf("method %d: core %q, types %q", i, core[i], wire[i])
		}
		if !Method(wire[i]).Valid() {
			t.Errorf("types method %q is not a valid core method", wire[i])
		}
	}
}
