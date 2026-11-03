package synth

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestSuggestRulesProbes_ShareTheLookupEnv: both --suggest-rules
// detectors compile their candidates against the row env a spec's rules
// are validated against, so lookup() is declared there too (parity) —
// a candidate calling it would compile exactly as the loader compiles it.
// The detectors never render one (TestSuggestRules_NeverProposesLookup).
func TestSuggestRulesProbes_ShareTheLookupEnv(t *testing.T) {
	t.Parallel()

	gate := &gateDetector{schema: &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
	}}}
	if err := gate.predicateCompiler()(`lookup("t", region, flag) > 0`); err != nil {
		t.Fatalf("gate detector env lacks lookup(): %v", err)
	}
	dep := &depDetector{fields: []depField{{name: "region", typ: "categorical_u8"}, {name: "flag", typ: "packed_bool"}}}
	if err := dep.exprChecker()("flag", `lookup("t", region) > 0`); err != nil {
		t.Fatalf("dependency detector env lacks lookup(): %v", err)
	}
}
