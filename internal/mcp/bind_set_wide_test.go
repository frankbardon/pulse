package mcp

import (
	"slices"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestClassifyFields_WideSetsLandInTheSetBucket pins the bind half of
// the agent-facing surface: the per-field enums a bound session hands an
// LLM are keyed off FieldType.IsSet(), so a wide rung must classify as a
// SET field and not fall through to the numeric bucket. A misclassified
// set_u256 column would be offered to the LLM as a valid AGG_SUM target.
func TestClassifyFields_WideSetsLandInTheSetBucket(t *testing.T) {
	dict := encoding.NewDictionary()
	for i := range 206 {
		if _, err := dict.Add("m" + strconv.Itoa(i)); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "narrow", Type: encoding.FieldTypeSetU8, Dictionary: encoding.NewDictionary()},
		{Name: "wide128", Type: encoding.FieldTypeSetU128, Dictionary: encoding.NewDictionary()},
		{Name: "wide256", Type: encoding.FieldTypeSetU256, Dictionary: dict},
		{Name: "age", Type: encoding.FieldTypeU8},
	}}

	c := classifyFields(schema)
	for _, name := range []string{"narrow", "wide128", "wide256"} {
		if !slices.Contains(c.Set, name) {
			t.Errorf("field %q missing from the Set bucket: %v", name, c.Set)
		}
		if slices.Contains(c.Numeric, name) {
			t.Errorf("set field %q classified numeric — it would be offered as an AGG_SUM target", name)
		}
		if slices.Contains(c.Categorical, name) {
			t.Errorf("set field %q classified categorical", name)
		}
	}
	if !slices.Contains(c.Numeric, "age") {
		t.Errorf("u8 field dropped out of the Numeric bucket: %v", c.Numeric)
	}
}
