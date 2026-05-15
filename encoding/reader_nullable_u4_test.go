package encoding

import (
	"bytes"
	"testing"
)

// TestReadRecord_NullableU4_NullSentinel asserts the reader recognises
// 0x0F as the null sentinel for nullable_u4 cells and routes the field
// into the null map. The non-null neighbour value must still decode
// untouched. Each ReadNibble consumes a full byte; the two fields use
// distinct BitPosition so each picks its half of its own byte.
func TestReadRecord_NullableU4_NullSentinel(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "lo", Type: FieldTypeNullableU4, BitPosition: 0, Description: "Low nibble"},
			{Name: "hi", Type: FieldTypeNullableU4, BitPosition: 4, Description: "High nibble"},
		},
	}

	// Byte 0: low nibble = 0x07 → lo reads 7.
	// Byte 1: high nibble = 0x0F → hi reads sentinel.
	buf := bytes.NewReader([]byte{0x07, 0xF0})
	rr := NewRecordReader(buf, schema)

	values := map[string]float64{}
	nulls := map[string]bool{}
	if err := rr.ReadRecord(values, nulls); err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}

	if got, want := values["lo"], float64(7); got != want {
		t.Errorf("values[lo] = %v, want %v", got, want)
	}
	if nulls["lo"] {
		t.Errorf("nulls[lo] = true, want false")
	}
	if !nulls["hi"] {
		t.Errorf("nulls[hi] = false, want true (0x0F is null sentinel)")
	}
	if got, want := values["hi"], float64(0); got != want {
		t.Errorf("values[hi] = %v, want %v (null cells store 0)", got, want)
	}
}

// TestReadRecord_NullableU4_NonSentinelValues confirms ordinary nibble
// values (0..14) decode as the raw integer without triggering null.
func TestReadRecord_NullableU4_NonSentinelValues(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "nib", Type: FieldTypeNullableU4, BitPosition: 0, Description: "Nibble"},
		},
	}

	for raw := uint8(0); raw < 15; raw++ {
		buf := bytes.NewReader([]byte{raw})
		rr := NewRecordReader(buf, schema)
		values := map[string]float64{}
		nulls := map[string]bool{}
		if err := rr.ReadRecord(values, nulls); err != nil {
			t.Fatalf("raw=%d: ReadRecord: %v", raw, err)
		}
		if nulls["nib"] {
			t.Errorf("raw=%d: nulls[nib]=true, want false", raw)
		}
		if got, want := values["nib"], float64(raw); got != want {
			t.Errorf("raw=%d: values[nib]=%v, want %v", raw, got, want)
		}
	}
}
