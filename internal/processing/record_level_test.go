package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestRecord_LevelValue pins the one shared level read: a categorical
// reads its dictionary label (StringValue's answer), a packed_bool
// reads "0" / "1", and a null or a non-level type reads not-ok. A
// packed_bool row must never read as missing — StringValue alone
// answers ("", false) for it, the silent drop LevelValue exists for.
func TestRecord_LevelValue(t *testing.T) {
	t.Parallel()

	dict := encoding.NewDictionary()
	yes, err := dict.Add("yes")
	if err != nil {
		t.Fatal(err)
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "x", Type: encoding.FieldTypeF64},
	}}
	for _, tc := range []struct {
		name   string
		vals   map[string]float64
		nulls  map[string]bool
		field  string
		want   string
		wantOK bool
	}{
		{"packed_bool true", map[string]float64{"flag": 1}, nil, "flag", "1", true},
		{"packed_bool false", map[string]float64{"flag": 0}, nil, "flag", "0", true},
		{"packed_bool null", nil, map[string]bool{"flag": true}, "flag", "", false},
		{"categorical", map[string]float64{"cat": float64(yes)}, nil, "cat", "yes", true},
		{"categorical null", nil, map[string]bool{"cat": true}, "cat", "", false},
		{"numeric", map[string]float64{"x": 1}, nil, "x", "", false},
		{"unknown", map[string]float64{"x": 1}, nil, "nope", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRecordWithNulls(schema, tc.vals, tc.nulls)
			got, ok := r.LevelValue(tc.field)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("LevelValue(%q) = (%q, %v), want (%q, %v)", tc.field, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
