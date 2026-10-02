package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

func TestRecord_DecimalValue(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "d", Type: encoding.FieldTypeDecimal128},
		{Name: "x", Type: encoding.FieldTypeF64},
	}}
	want := encoding.NewDecimal128FromInt(12345)

	r := NewRecordWithWide(schema, map[string]float64{"x": 1}, nil, map[string]any{"d": want})
	if got, ok := r.DecimalValue("d"); !ok || got != want {
		t.Fatalf("DecimalValue(d) = %v, %v; want %v, true", got, ok, want)
	}
	if _, ok := r.DecimalValue("x"); ok {
		t.Error("DecimalValue on a non-decimal field reported ok")
	}
	if _, ok := r.DecimalValue("missing"); ok {
		t.Error("DecimalValue on a missing field reported ok")
	}

	nulled := NewRecordWithWide(schema, nil, map[string]bool{"d": true}, nil)
	if _, ok := nulled.DecimalValue("d"); ok {
		t.Error("DecimalValue on a null field reported ok")
	}

	setSchema := &encoding.Schema{Fields: []encoding.Field{{Name: "s", Type: encoding.FieldTypeSetU8}}}
	setRec := NewRecordWithWide(setSchema, nil, nil, map[string]any{"s": uint64(3)})
	if _, ok := setRec.DecimalValue("s"); ok {
		t.Error("DecimalValue on a set field reported ok")
	}
}
