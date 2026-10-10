package cli

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

func TestParseFieldType(t *testing.T) {
	for _, name := range []string{
		"u8", "u16", "u32", "u64", "f32", "f64", "u4", "date", "datetime",
		"packed_bool", "categorical_u8", "categorical_u16", "categorical_u32",
		"decimal128", "set_u8", "set_u16", "set_u32", "set_u64", "set_u128", "set_u256",
	} {
		ft, err := parseFieldType("col", name)
		if err != nil {
			t.Fatalf("parseFieldType(%q): %v", name, err)
		}
		if got := ft.String(); got != name {
			t.Errorf("parseFieldType(%q).String() = %q", name, got)
		}
	}
}

// An unknown type name is refused, never coerced to f64 (the old
// fallback turned a typo into a silently mistyped column).
func TestParseFieldType_UnknownRefused(t *testing.T) {
	for _, name := range []string{"unknown_type", "float", "F64", ""} {
		_, err := parseFieldType("price", name)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN {
			t.Fatalf("parseFieldType(%q) err = %v, want PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN", name, err)
		}
		if ce.Details["field"] != "price" || ce.Details["type"] != name {
			t.Errorf("details = %v, want field=price type=%q", ce.Details, name)
		}
		valid, _ := ce.Details["valid"].([]string)
		known := 0
		for ft := encoding.FieldType(0); ft.IsKnown(); ft++ {
			known++
		}
		if len(valid) != known || valid[0] != "u8" || valid[len(valid)-1] != "set_u256" {
			t.Errorf("details.valid = %v, want the %d registered names in type-byte order", valid, known)
		}
	}
}
