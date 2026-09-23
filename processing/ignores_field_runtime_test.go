package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// ignoredFieldAggProbes supplies the Params an IgnoresField aggregator
// needs, plus the schema and records to run it over. Keyed by operator
// name; the gate fails on a declaration with no entry, so a new
// IgnoresField aggregator cannot land unproven.
var ignoredFieldAggProbes = map[string]struct {
	params json.RawMessage
	schema func() *encoding.Schema
	recs   func(*encoding.Schema) []*Record
}{
	string(types.AGG_RATIO): {
		params: json.RawMessage(`{"numerator_field":"score","denominator_field":"value"}`),
		schema: func() *encoding.Schema {
			return &encoding.Schema{Fields: []encoding.Field{
				{Name: "score", Type: encoding.FieldTypeF64},
				{Name: "value", Type: encoding.FieldTypeF64},
				{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Dictionary: ignoredFieldDict()},
			}}
		},
		recs: func(s *encoding.Schema) []*Record {
			num := []float64{10, 20, 30}
			den := []float64{2, 4, 4}
			brand := []float64{0, 1, 0}
			out := make([]*Record, len(num))
			for i := range num {
				out[i] = NewRecord(s, map[string]float64{
					"score": num[i], "value": den[i], "brand": brand[i],
				})
			}
			return out
		},
	},
}

func ignoredFieldDict() *encoding.Dictionary {
	d := encoding.NewDictionary()
	_, _ = d.Add("Apple")
	_, _ = d.Add("Samsung")
	return d
}

// TestAggregators_IgnoredFieldSlotIsReallyIgnored is the runtime half of
// the Operator.IgnoresField claim.
//
// # Why it exists
//
// AGG_RATIO's manifest entry declares AcceptsTypes on a Field slot its
// Description says is ignored. The honest representation of that is the
// IgnoresField flag plus params that carry the real constraint — but a
// flag is just another undefended declaration unless something runs the
// operator and proves the slot makes no difference.
//
// So: for every aggregator the manifest marks IgnoresField, construct and
// run it four ways — naming a numeric field, naming a categorical field,
// naming a field that is not in the schema at all, and naming nothing —
// and require the SAME scalar every time. If any of those changes the
// answer, the flag is false and AcceptsTypes was load-bearing after all.
//
// Both execution arms are driven: buffered Aggregate and the streaming
// UpdateRow/Finalize pair, because a Field read that survives on only one
// of them is still a Field read.
func TestAggregators_IgnoredFieldSlotIsReallyIgnored(t *testing.T) {
	m := descriptor.BuildManifest()

	declared := 0
	for _, op := range m.Components.Aggregators {
		if !op.IgnoresField {
			continue
		}
		declared++

		probe, ok := ignoredFieldAggProbes[op.Name]
		if !ok {
			t.Errorf("%s declares IgnoresField with no probe in ignoredFieldAggProbes — "+
				"the flag is unproven; add one", op.Name)
			continue
		}

		t.Run(op.Name, func(t *testing.T) {
			schema := probe.schema()
			factory, ok := aggregatorRegistry[types.AggregationType(op.Name)]
			if !ok {
				t.Fatalf("%s is not in the aggregator registry", op.Name)
			}

			fieldSlots := []struct {
				name  string
				field string
			}{
				{"numeric_field", "score"},
				{"categorical_field", "brand"},
				{"field_absent_from_schema", "no_such_field"},
				{"empty_field", ""},
			}

			var want float64
			var haveWant bool
			for _, slot := range fieldSlots {
				agg, err := factory(&types.Aggregation{
					Type: types.AggregationType(op.Name), Field: slot.field, Params: probe.params,
				}, schema)
				if err != nil {
					t.Fatalf("%s with Field=%q failed to construct: %v — "+
						"the Field slot is not ignored, it is validated", op.Name, slot.field, err)
				}

				buffered, err := agg.Aggregate(probe.recs(schema), slot.field)
				if err != nil {
					t.Fatalf("%s.Aggregate with Field=%q: %v", op.Name, slot.field, err)
				}

				streamed, ok := streamedScalar(t, factory, op.Name, schema, probe.params, slot.field, probe.recs(schema))
				if ok && !sameScalar(buffered, streamed) {
					t.Errorf("%s with Field=%q disagrees across arms: buffered %v, streaming %v",
						op.Name, slot.field, buffered, streamed)
				}

				if !haveWant {
					want, haveWant = buffered, true
					continue
				}
				if !sameScalar(buffered, want) {
					t.Errorf("%s returned %v with Field=%q but %v with the first field slot — "+
						"the Field slot is NOT ignored, so its AcceptsTypes declaration is load-bearing "+
						"and IgnoresField is false", op.Name, buffered, slot.field, want)
				}
			}
		})
	}

	if declared == 0 {
		t.Fatal("no aggregator declares IgnoresField; AGG_RATIO should")
	}
}

// streamedScalar drives the streaming arm when the aggregator offers one.
func streamedScalar(t *testing.T, factory AggregatorFactory, name string, schema *encoding.Schema,
	params json.RawMessage, field string, recs []*Record) (float64, bool) {
	t.Helper()
	agg, err := factory(&types.Aggregation{
		Type: types.AggregationType(name), Field: field, Params: params,
	}, schema)
	if err != nil {
		t.Fatalf("%s streaming construct with Field=%q: %v", name, field, err)
	}
	sa, ok := agg.(OnlineAggregator)
	if !ok {
		return 0, false
	}
	for _, r := range recs {
		if err := sa.UpdateRow(r, field); err != nil {
			t.Fatalf("%s.UpdateRow with Field=%q: %v", name, field, err)
		}
	}
	v, err := sa.Finalize()
	if err != nil {
		t.Fatalf("%s.Finalize with Field=%q: %v", name, field, err)
	}
	return v, true
}

// sameScalar compares two aggregator results treating NaN as equal to
// NaN — a denominator-zero ratio is a legitimate, reproducible answer.
func sameScalar(a, b float64) bool {
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}
	return a == b
}
