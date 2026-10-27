package processing

import (
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

func chainIterSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeCategoricalU32, Dictionary: encoding.NewDictionary()},
		{Name: "v", Type: encoding.FieldTypeF64},
		{Name: "j", Type: encoding.FieldTypeCategoricalU32, Dictionary: encoding.NewDictionary()},
	}}
}

// chainIterCell is one field of one yielded record: its value, or null.
type chainIterCell struct {
	v    float64
	null bool
}

func chainIterCells(r *Record) []chainIterCell {
	var out []chainIterCell
	for _, f := range []string{"k", "v", "j"} {
		v, ok := r.NumericValue(f)
		out = append(out, chainIterCell{v: v, null: r.IsNull(f) || !ok})
	}
	return out
}

// TestChainRowIterator_YieldsRowRecords (U51 E5-S12, #499): the lazy
// chain-stage input yields, row by row, exactly the record the eager
// form built — a categorical cell its first-seen dictionary index (a
// string, a number or a bool key alike), an f64 cell its value, and
// null for a missing, nil, non-numeric or NaN cell — with each
// dictionary complete, in first-seen row order, BEFORE the first Next.
// Reset replays the same records; each Next builds a fresh record.
func TestChainRowIterator_YieldsRowRecords(t *testing.T) {
	rows := []map[string]any{
		{"k": "b", "v": 2.5, "j": 7.0},
		{"k": "a", "v": math.NaN(), "j": "x"},
		{"k": nil, "v": int64(3)},
		{"v": "not a number", "j": true},
		{"k": "b", "v": nil, "j": 7.0},
	}
	schema := chainIterSchema()
	it, err := NewChainRowIterator(rows, schema)
	if err != nil {
		t.Fatal(err)
	}
	if got := schema.Fields[0].Dictionary.Values(); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Errorf("k dictionary before the first Next = %v, want [b a]", got)
	}
	if got := schema.Fields[2].Dictionary.Values(); !reflect.DeepEqual(got, []string{"7", "x", "true"}) {
		t.Errorf("j dictionary before the first Next = %v, want [7 x true]", got)
	}
	if it.Len() != len(rows) {
		t.Errorf("Len %d, want %d", it.Len(), len(rows))
	}
	null := chainIterCell{null: true}
	want := [][]chainIterCell{
		{{v: 0}, {v: 2.5}, {v: 0}},
		{{v: 1}, null, {v: 1}},
		{null, {v: 3}, null},
		{null, null, {v: 2}},
		{{v: 0}, null, {v: 0}},
	}
	var first []*Record
	for pass := range 2 {
		var got [][]chainIterCell
		for i := 0; it.Next(); i++ {
			r := it.Record()
			if pass == 0 {
				first = append(first, r)
			} else if r == first[i] {
				t.Errorf("pass 2 row %d: the same record as pass 1, want a fresh one", i)
			}
			got = append(got, chainIterCells(r))
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("pass %d: records %+v, want %+v", pass+1, got, want)
		}
		if it.Next() {
			t.Errorf("pass %d: Next past the end", pass+1)
		}
		it.Reset()
	}
	// The eager form is the iterator drained.
	eager, err := RecordsFromChainRows(rows, chainIterSchema())
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range eager {
		if got := chainIterCells(r); !reflect.DeepEqual(got, want[i]) {
			t.Errorf("RecordsFromChainRows row %d = %+v, want %+v", i, got, want[i])
		}
	}
}

// TestChainRowIterator_RefusesUnsupportedFieldAtFirstValue: a field of
// a type the chain schema never synthesises is refused at the first row
// carrying a value for it, as the eager form did — and not at all when
// no row does.
func TestChainRowIterator_RefusesUnsupportedFieldAtFirstValue(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "u", Type: encoding.FieldTypeU8}}}
	if _, err := NewChainRowIterator([]map[string]any{{"x": 1.0}, {"u": nil}}, schema); err != nil {
		t.Errorf("no row carries u: %v", err)
	}
	if _, err := NewChainRowIterator([]map[string]any{{"x": 1.0}, {"u": 3.0}}, schema); err == nil {
		t.Error("a row carrying u: no refusal")
	}
}
