package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestResolveWeights_ExtensionClasses: an extension operator is classed
// by its registration's WeightAware declaration (the snapshot's
// OperatorMeta.WeightAware) — aware ⇒ applied (over a decimal128 value
// field too: the built-in decimal refusal is the built-in decimal
// path's); a non-aware aggregator is skipped under the instance default
// and PULSE_EXTENSION_NOT_WEIGHT_AWARE under an explicit weight; a
// non-aware attribute or test is refused under any weight; a hidden
// one is never-registered (skipped).
func TestResolveWeights_ExtensionClasses(t *testing.T) {
	snap := &ExtensionsSnapshot{
		Aggregators: []descriptor.OperatorMeta{{Name: "AGG_ACME_AWARE_SUM", WeightAware: true}, {Name: "AGG_ACME_PLAIN_SUM"}},
		Attributes:  []descriptor.OperatorMeta{{Name: "ATTR_ACME_AWARE_ROW", WeightAware: true}, {Name: "ATTR_ACME_PLAIN_ROW"}},
		Tests:       []descriptor.OperatorMeta{{Name: "TEST_ACME_AWARE_SUM", WeightAware: true}, {Name: "TEST_ACME_PLAIN_SUM"}},
	}
	inst := UnscopedInstanceSnapshot(snap)
	def := &types.WeightSpec{Field: "w"}

	rws, err := ResolveWeights(&types.Request{
		Aggregations: []*types.Aggregation{{Type: "AGG_ACME_AWARE_SUM", Field: "dec"}, {Type: "AGG_ACME_PLAIN_SUM", Field: "x"}},
		Attributes:   []*types.Attribute{{Type: "ATTR_ACME_AWARE_ROW", Field: "x"}},
		Tests:        []*types.Test{{Type: "TEST_ACME_AWARE_SUM", Field: "x"}},
		PostTests:    []*types.Test{{Type: "TEST_ACME_PLAIN_SUM", Field: "x", Weight: types.NullSlotWeight()}},
	}, weightSchema(), def, inst)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"aggregations[0]": descriptor.WeightStatusApplied,
		"aggregations[1]": descriptor.WeightStatusSkippedNotWeightAware,
		"attributes[0]":   descriptor.WeightStatusApplied,
		"tests[0]":        descriptor.WeightStatusApplied,
		"post_tests[0]":   descriptor.WeightStatusOptedOut,
	}
	if len(rws) != len(want) {
		t.Fatalf("weights = %+v", rws)
	}
	for _, rw := range rws {
		if want[rw.Slot] != rw.Status {
			t.Errorf("%s status = %s, want %s", rw.Slot, rw.Status, want[rw.Slot])
		}
	}

	refused := map[string]struct {
		req  *types.Request
		def  *types.WeightSpec
		slot string
	}{
		"aggregator slot":    {&types.Request{Aggregations: []*types.Aggregation{{Type: "AGG_ACME_PLAIN_SUM", Field: "x", Weight: types.SlotWeightField("w")}}}, nil, "aggregations[0]"},
		"aggregator request": {&types.Request{Weight: def, Aggregations: []*types.Aggregation{{Type: "AGG_ACME_PLAIN_SUM", Field: "x"}}}, nil, "aggregations[0]"},
		"crosstab cell":      {&types.Request{Weight: def, Crosstab: &types.CrosstabSpec{Cell: &types.Aggregation{Type: "AGG_ACME_PLAIN_SUM", Field: "x"}}}, nil, "crosstab.cell"},
		"attribute default":  {&types.Request{Attributes: []*types.Attribute{{Type: "ATTR_ACME_PLAIN_ROW", Field: "x"}}}, def, "attributes[0]"},
		"test default":       {&types.Request{Tests: []*types.Test{{Type: "TEST_ACME_PLAIN_SUM", Field: "x"}}}, def, "tests[0]"},
		"post test request":  {&types.Request{Weight: def, PostTests: []*types.Test{{Type: "TEST_ACME_PLAIN_SUM", Field: "x"}}}, nil, "post_tests[0]"},
	}
	for name, tc := range refused {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveWeights(tc.req, weightSchema(), tc.def, inst)
			ce := codeOf(t, err)
			if ce.Code != errors.PULSE_EXTENSION_NOT_WEIGHT_AWARE || ce.Details["slot"] != tc.slot || ce.Details["field"] != "w" {
				t.Fatalf("got %s %v", ce.Code, ce.Details)
			}
		})
	}

	hidden := NewInstanceSnapshot(snap, FeatureSet{Hidden: []string{"ATTR_ACME_PLAIN_ROW"}})
	rws, err = ResolveWeights(&types.Request{Attributes: []*types.Attribute{{Type: "ATTR_ACME_PLAIN_ROW", Field: "x"}}}, weightSchema(), def, hidden)
	if err != nil || len(rws) != 1 || rws[0].Status != descriptor.WeightStatusSkippedNotWeightAware {
		t.Fatalf("hidden extension: %+v, %v; want skipped", rws, err)
	}
}
