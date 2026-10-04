package processing

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestStampWeightsWith_ExtensionAwareness: StampWeightsWith stamps the
// resolved weight onto a WeightAware extension aggregator, test and
// attribute slot, `null` onto a non-aware extension aggregator, and
// treats a hidden extension as never-registered; the invalid-row tally
// then reads exactly the stamped slots. A nil registry is the
// built-in-only StampWeights.
func TestStampWeightsWith_ExtensionAwareness(t *testing.T) {
	const (
		aware     = "AGG_ACME_AWARE_SUM"
		plain     = "AGG_ACME_PLAIN_SUM"
		hidden    = "AGG_ACME_HIDDEN_SUM"
		awareTest = "TEST_ACME_AWARE_SUM"
		awareAttr = "ATTR_ACME_AWARE_ROW"
	)
	reg := (&ExtensionRegistry{WeightAware: map[string]bool{
		StreamabilityKey("aggregator", aware):    true,
		StreamabilityKey("aggregator", plain):    false,
		StreamabilityKey("aggregator", hidden):   true,
		StreamabilityKey("test", awareTest):      true,
		StreamabilityKey("attribute", awareAttr): true,
	}}).WithHidden(func(name string) bool { return name == hidden })

	def := &types.WeightSpec{Field: "w"}
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: aware, Field: "x"},
			{Type: plain, Field: "x"},
			{Type: hidden, Field: "x"},
		},
		Tests:      []*types.Test{{Type: awareTest, Field: "x"}},
		PostTests:  []*types.Test{{Type: awareTest, Field: "x", Weight: types.NullSlotWeight()}},
		Attributes: []*types.Attribute{{Type: awareAttr, Field: "x"}, {Type: types.ATTR_FORMULA, Field: "x", Weight: types.SlotWeightField("w")}},
	}
	got := StampWeightsWith(req, def, reg)

	applied := func(w types.SlotWeight) bool {
		s := w.Spec()
		return s != nil && s.Field == "w" && s.Kind == types.WeightKindProbability
	}
	if !applied(got.Aggregations[0].Weight) || !got.Aggregations[1].Weight.IsNull() || !got.Aggregations[2].Weight.IsNull() {
		t.Fatalf("aggregation weights = %+v / %+v / %+v; want applied, null (not aware), null (hidden)",
			got.Aggregations[0].Weight, got.Aggregations[1].Weight, got.Aggregations[2].Weight)
	}
	if !applied(got.Tests[0].Weight) || !got.PostTests[0].Weight.IsNull() {
		t.Fatalf("test weights = %+v / %+v; want applied, opted out", got.Tests[0].Weight, got.PostTests[0].Weight)
	}
	if !applied(got.Attributes[0].Weight) || !got.Attributes[1].Weight.IsNull() {
		t.Fatalf("attribute weights = %+v / %+v; want applied, null (built-in row-local)", got.Attributes[0].Weight, got.Attributes[1].Weight)
	}
	// The caller's request is never mutated.
	if req.Tests[0].Weight.Spec() != nil || req.Attributes[0].Weight.Spec() != nil || req.Attributes[1].Weight.Spec() == nil {
		t.Fatal("StampWeightsWith mutated the caller's slots")
	}
	if tally := NewWeightRowTally(got); tally == nil || len(tally.entries) != 1 || tally.entries[0].field != "w" {
		t.Fatalf("tally = %+v, want one entry for w", tally)
	}

	if builtin := StampWeights(req, def); !builtin.Aggregations[0].Weight.IsNull() || builtin.Tests[0].Weight.Spec() != nil {
		t.Fatal("StampWeights (no registry) stamped an extension slot")
	}
	if (&ExtensionRegistry{}).IsExtensionWeightAware("aggregator", aware) || (*ExtensionRegistry)(nil).IsExtensionWeightAware("aggregator", aware) {
		t.Fatal("IsExtensionWeightAware answered true for an unregistered name or a nil registry")
	}
}
