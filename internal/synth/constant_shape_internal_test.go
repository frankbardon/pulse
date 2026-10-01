package synth

import (
	"testing"
)

// TestConstantRowValue_MatchesTheRowShapeForEveryClass is the folded
// E1-S1 finding: `constant` is the one sampler whose value comes from the
// document rather than from arithmetic, so it is the one that can put the
// wrong Go type into the row. sentinelFor types the expression
// environment per FIELD TYPE, so a `constant` emitting a different shape
// than every other sampler for that type breaks every expression over
// the field at run time — a JSON `true` on a packed_bool was exactly
// that, the inverse of issue #258.
//
// The expectation is sentinelFor's own answer rather than a second
// hardcoded column, so the two cannot drift.
func TestConstantRowValue_MatchesTheRowShapeForEveryClass(t *testing.T) {
	cases := []struct {
		name      string
		typeName  string
		value     any
		wantValue any
	}{
		{"bool on packed_bool becomes 1", "packed_bool", true, float64(1)},
		{"bool false on packed_bool becomes 0", "packed_bool", false, float64(0)},
		{"bool on a numeric becomes 1", "u8", true, float64(1)},
		{"number on a numeric stays", "f64", 2.5, float64(2.5)},
		{"int on a numeric widens", "u16", 7, float64(7)},
		{"string on a categorical stays", "categorical_u8", "west", "west"},
		{"array on a set becomes a mask map", "set_u8", []any{"acme"}, map[string]bool{"acme": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := FieldSpec{Name: "f", Type: tc.typeName, Distribution: DistConstant,
				Params: map[string]any{"value": tc.value}}
			smp, err := buildSampler(f)
			if err != nil {
				t.Fatalf("buildSampler: %v", err)
			}
			got, isNull := smp.next(nil)
			if isNull {
				t.Fatal("constant must never draw null")
			}
			// The row shape the expression environment promises.
			wantShape := sentinelFor(tc.typeName)
			if gotT, wantT := typeName(got), typeName(wantShape); gotT != wantT {
				t.Fatalf("row value is %s, but sentinelFor(%s) promises %s — an expression over this field would fail at run time",
					gotT, tc.typeName, wantT)
			}
			switch want := tc.wantValue.(type) {
			case map[string]bool:
				gotMap, ok := got.(map[string]bool)
				if !ok {
					t.Fatalf("got %T", got)
				}
				if len(gotMap) != len(want) {
					t.Fatalf("mask = %v, want %v", gotMap, want)
				}
				for k, v := range want {
					if gotMap[k] != v {
						t.Fatalf("mask = %v, want %v", gotMap, want)
					}
				}
			default:
				if got != tc.wantValue {
					t.Fatalf("value = %#v, want %#v", got, tc.wantValue)
				}
			}
		})
	}
}

// TestConstantRowValue_RefusesAShapeTheRowCannotHold asserts the shapes
// that used to reach the row (or the encoder) and produce a silent wrong
// value are refused at spec compile time instead.
func TestConstantRowValue_RefusesAShapeTheRowCannotHold(t *testing.T) {
	cases := []struct {
		name     string
		typeName string
		value    any
	}{
		// toFloat64's string arm returned 0 — a silent wrong value.
		{"string on a numeric", "u8", "12"},
		{"string on a float", "f64", "1.5"},
		// ENCODING_TYPE_MISMATCH at row time, one row in.
		{"number on a categorical", "categorical_u8", 1.0},
		{"bool on a categorical", "categorical_u8", true},
		{"scalar on a set", "set_u8", "acme"},
		{"non-string set option", "set_u8", []any{1.0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := FieldSpec{Name: "f", Type: tc.typeName, Distribution: DistConstant,
				Params: map[string]any{"value": tc.value}}
			if _, err := buildSampler(f); err == nil {
				t.Fatal("want a spec-compile-time refusal, got nil")
			}
		})
	}
}

// TestConstantRowValue_DecimalStringSurvives pins the one scalar
// exception: a decimal128 constant keeps its string form, because
// encoding.ParseDecimal128 is exact and routing it through float64 would
// lose digits the type exists to preserve.
func TestConstantRowValue_DecimalStringSurvives(t *testing.T) {
	f := FieldSpec{Name: "f", Type: "decimal128", Scale: 2, Distribution: DistConstant,
		Params: map[string]any{"value": "1.25"}}
	smp, err := buildSampler(f)
	if err != nil {
		t.Fatalf("buildSampler: %v", err)
	}
	got, _ := smp.next(nil)
	if got != "1.25" {
		t.Fatalf("value = %#v, want the verbatim string \"1.25\"", got)
	}
}

func typeName(v any) string {
	switch v.(type) {
	case float64:
		return "float64"
	case string:
		return "string"
	case map[string]bool:
		return "map[string]bool"
	}
	return "other"
}
