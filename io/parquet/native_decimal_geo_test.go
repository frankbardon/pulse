package parquet

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

func TestParquet_NativeDecimalRoundTrip(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 6},
		{Name: "loc", Type: encoding.FieldTypePointF64},
		{Name: "cell", Type: encoding.FieldTypeH3Cell, H3Resolution: 9},
	}}
	w := NewWriterToBuffer()
	w.SetPulseSchema(schema)
	if err := w.WriteHeader([]string{"amount", "loc", "cell"}); err != nil {
		t.Fatal(err)
	}
	d, _, _ := encoding.ParseDecimal128("123.456789")
	if err := w.WriteRow([]any{
		d,
		encoding.PointF64{Lat: 37.775, Lon: -122.418},
		encoding.H3Cell(0x89283082803ffff),
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	r := NewReaderFromBytes(w.Bytes())
	defer r.Close()
	got, err := r.InferPulseSchema()
	if err != nil {
		t.Fatal(err)
	}
	if got.Fields[0].Type != encoding.FieldTypeDecimal128 {
		t.Errorf("amount type = %s, want decimal128", got.Fields[0].Type)
	}
	if got.Fields[0].Precision != 20 || got.Fields[0].Scale != 6 {
		t.Errorf("amount precision/scale lost: %d/%d", got.Fields[0].Precision, got.Fields[0].Scale)
	}
	if got.Fields[1].Type != encoding.FieldTypePointF64 {
		t.Errorf("loc type = %s, want point_f64", got.Fields[1].Type)
	}
	if got.Fields[2].Type != encoding.FieldTypeH3Cell {
		t.Errorf("cell type = %s, want h3_cell", got.Fields[2].Type)
	}

	if _, err := r.ReadHeader(); err != nil {
		t.Fatal(err)
	}
	rows := [][]string{}
	if err := r.ReadRows(context.Background(), func(row []string) error {
		cp := make([]string, len(row))
		copy(cp, row)
		rows = append(rows, cp)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0][0] != "123.456789" {
		t.Errorf("amount = %q, want 123.456789", rows[0][0])
	}
	if rows[0][1] != "POINT(-122.418 37.775)" {
		t.Errorf("loc = %q", rows[0][1])
	}
}
