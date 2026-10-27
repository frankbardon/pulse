package processing

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// codeSchema is a one-column schema: a categorical_u8 "c" whose
// dictionary lists labels in order, or a packed_bool "c" when labels
// is nil.
func codeSchema(labels []string) *encoding.Schema {
	if labels == nil {
		return &encoding.Schema{Fields: []encoding.Field{{Name: "c", Type: encoding.FieldTypePackedBool}}}
	}
	d := encoding.NewDictionary()
	for _, l := range labels {
		_, _ = d.Add(l)
	}
	return &encoding.Schema{Fields: []encoding.Field{{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: d}}}
}

// codeRecords writes rows (labels) over schema's categorical column by
// dictionary code.
func codeRecords(t *testing.T, schema *encoding.Schema, rows []string) []*Record {
	t.Helper()
	d := schema.Fields[0].Dictionary
	out := make([]*Record, 0, len(rows))
	for _, l := range rows {
		id, ok := d.IDFor(l)
		if !ok {
			t.Fatalf("label %q not in the dictionary", l)
		}
		out = append(out, NewRecord(schema, map[string]float64{"c": float64(id)}))
	}
	return out
}

// codeRowValue runs one aggregation of op over "c" and returns its row
// value, ok false when the runtime refuses the slot.
func codeRowValue(t *testing.T, op types.AggregationType, params json.RawMessage, schema *encoding.Schema, records []*Record) (float64, bool) {
	t.Helper()
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: op, Field: "c", Label: "v", Params: params}}}
	resp, err := NewProcessor(schema).processRecords(context.Background(), req, records)
	if err != nil {
		return 0, false
	}
	v, ok := coerceNumericValue(resp.Data[0]["v"])
	if !ok {
		t.Fatalf("%s writes %T, not one number", op, resp.Data[0]["v"])
	}
	return v, true
}

// TestEmitsInputValueAggregationTypes_MatchRowValues holds the declared
// input-valued class (vectors.EmitsInputValueAggregationTypes — the
// overlay categorical-code refusal's built-in half) to what every
// built-in that accepts a categorical field writes. The same labelled
// rows run over two dictionaries that order the labels differently: a
// figure computed over the labels (a count, a cardinality) answers the
// same both times; a value PICKED from the column answers each
// dictionary's own code for the same label. A new picking aggregator
// fails here until the class names it.
func TestEmitsInputValueAggregationTypes_MatchRowValues(t *testing.T) {
	t.Parallel()

	rows := []string{"r", "r", "r", "q", "q", "p", "s"} // unique mode "r"
	dictA := []string{"p", "q", "r", "s"}
	dictB := []string{"s", "r", "q", "p"}
	schemaA, schemaB := codeSchema(dictA), codeSchema(dictB)
	recsA, recsB := codeRecords(t, schemaA, rows), codeRecords(t, schemaB, rows)
	params := map[types.AggregationType]json.RawMessage{types.AGG_FREQUENCY: json.RawMessage(`{"value":"r"}`)}
	declared := vectors.EmitsInputValueAggregationTypes()
	entries := manifestOperatorEntries()
	judged := 0
	for _, op := range types.AllAggregationTypes() {
		e := entries[string(op)]
		if e.IgnoresField || !slices.Contains(e.AcceptsTypes, "categorical_u8") {
			continue
		}
		t.Run(string(op), func(t *testing.T) {
			a, okA := codeRowValue(t, op, params[op], schemaA, recsA)
			b, okB := codeRowValue(t, op, params[op], schemaB, recsB)
			if !okA || !okB {
				t.Fatalf("%s accepts categorical_u8 but the runtime refused the slot", op)
			}
			judged++
			picks := a != b
			if want := slices.Contains(declared, op); picks != want {
				t.Fatalf("%s: %v over one dictionary, %v over the other (code-dependent=%v) but EmitsInputValueAggregationTypes declares it=%v", op, a, b, picks, want)
			}
			if picks && (dictA[int(a)] != "r" || dictB[int(b)] != "r") {
				t.Errorf("%s: codes %v / %v name %q / %q, want the modal label r both times", op, a, b, dictA[int(a)], dictB[int(b)])
			}
		})
	}
	if judged < 4 {
		t.Fatalf("judged %d categorical-accepting built-ins, want the count / mode family", judged)
	}
	// packed_bool: the declared picks write the modal truth value (1 for
	// a mostly-true column), a code, not a magnitude.
	pb := codeSchema(nil)
	var recs []*Record
	for _, v := range []float64{1, 1, 1, 0} {
		recs = append(recs, NewRecord(pb, map[string]float64{"c": v}))
	}
	for _, op := range declared {
		if v, ok := codeRowValue(t, op, nil, pb, recs); !ok || v != 1 {
			t.Errorf("%s over packed_bool = %v (ok %v), want the modal truth value 1", op, v, ok)
		}
	}
}
