package vectors

import (
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestVectorAggregatorProseNamesFullSet: every refusal that steers a
// caller to "a vector aggregator" names the whole built-in set
// (VectorAggregationTypes), not a hand-kept subset. The
// PULSE_VECTOR_FIELD_NOT_SCALAR message is derived from the set; the
// errors package cannot import vectors, so its aggregation fixup lists
// the set by hand and this test keeps it in step.
func TestVectorAggregatorProseNamesFullSet(t *testing.T) {
	set := VectorAggregationTypes()
	if len(set) < 3 {
		t.Fatalf("VectorAggregationTypes = %v; expected at least AGG_VEC_MEAN / SUM / SD", set)
	}

	f := &encoding.Field{Name: "emb", Type: encoding.FieldTypeVecF64}
	msg := FieldNotScalar(f, 4, "aggregations[0] (AGG_SUM) field", SuggestAggregation, nil).Message
	for _, typ := range set {
		if !strings.Contains(msg, string(typ)) {
			t.Errorf("FieldNotScalar aggregation message omits %s: %q", typ, msg)
		}
	}
	if want := "AGG_VEC_MEAN, AGG_VEC_SUM or AGG_VEC_SD"; !strings.Contains(msg, want) {
		t.Errorf("FieldNotScalar aggregation message = %q, want it to list %q", msg, want)
	}
	if got := vectorAggregationList(" / ", " / "); got != "AGG_VEC_MEAN / AGG_VEC_SUM / AGG_VEC_SD" {
		t.Errorf("vectorAggregationList(/) = %q", got)
	}

	meta, ok := errors.MetadataFor(errors.PULSE_VECTOR_FIELD_NOT_SCALAR)
	if !ok {
		t.Fatal("PULSE_VECTOR_FIELD_NOT_SCALAR has no metadata")
	}
	var found bool
	for _, fx := range meta.Fixups {
		if len(fx.Path) < 1 || fx.Path[0] != "aggregations" {
			continue
		}
		found = true
		var examples []types.AggregationType
		for _, e := range fx.Examples {
			s, _ := e.(string)
			examples = append(examples, types.AggregationType(s))
		}
		if !slices.Equal(examples, set) {
			t.Errorf("aggregation fixup Examples = %v, want VectorAggregationTypes %v", examples, set)
		}
		for _, typ := range set {
			if !strings.Contains(fx.Hint, string(typ)) {
				t.Errorf("aggregation fixup hint omits %s: %q", typ, fx.Hint)
			}
		}
	}
	if !found {
		t.Fatal("PULSE_VECTOR_FIELD_NOT_SCALAR has no aggregations fixup")
	}
}
