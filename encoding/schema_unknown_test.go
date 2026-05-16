package encoding

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestReadSchema_RejectsUnknownFieldType verifies that a schema with a
// FieldType byte beyond the known sentinel fails loud at parse time
// with ENCODING_INVALID rather than silently mis-decoding records.
func TestReadSchema_RejectsUnknownFieldType(t *testing.T) {
	var buf bytes.Buffer
	// fieldCount=1
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	// type byte = 250 (well past fieldTypeCount=17)
	buf.WriteByte(250)
	// name "x"
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	buf.WriteByte('x')
	// byte_offset
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	// bit_position
	buf.WriteByte(0)
	// csv_column_idx
	binary.Write(&buf, binary.LittleEndian, uint16(0))
	// description (zero length)
	binary.Write(&buf, binary.LittleEndian, uint16(0))

	_, err := ReadSchema(&buf)
	if err == nil {
		t.Fatal("expected ReadSchema to reject unknown FieldType byte")
	}
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Fatalf("expected ENCODING_INVALID, got %v", err)
	}
}

func TestSchema_DecimalMetadata_RoundTrip(t *testing.T) {
	original := &Schema{
		Fields: []Field{
			{Name: "amount", Type: FieldTypeDecimal128, Precision: 20, Scale: 6, Description: "Amount with 6 decimal places of precision."},
		},
	}
	var buf bytes.Buffer
	if err := WriteSchema(&buf, original); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSchema(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Fields[0].Precision != 20 || got.Fields[0].Scale != 6 {
		t.Errorf("decimal metadata lost: %+v", got.Fields[0])
	}
}
