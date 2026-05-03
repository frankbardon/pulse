package io

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// stringRowsReader is a minimal Reader that emits a fixed slice of string
// rows with a known header. Lets us exercise import without depending on
// a particular tabular format.
type stringRowsReader struct {
	header []string
	rows   [][]string
	pos    int
}

func (r *stringRowsReader) ReadHeader() ([]string, error) { return r.header, nil }
func (r *stringRowsReader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	for ; r.pos < len(r.rows); r.pos++ {
		if err := fn(r.rows[r.pos]); err != nil {
			return err
		}
	}
	return nil
}
func (r *stringRowsReader) Close() error { return nil }
func (r *stringRowsReader) Reset() error { r.pos = 0; return nil }

func TestImport_DecimalGeoRoundTrip(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 6, CsvColumnIdx: 0, Description: "Amount in USD with 6 decimal places of precision."},
			{Name: "loc", Type: encoding.FieldTypePointF64, CsvColumnIdx: 1, Description: "Pickup location WKT POINT(lon lat) format."},
			{Name: "cell", Type: encoding.FieldTypeH3Cell, CsvColumnIdx: 2, H3Resolution: 9, Description: "H3 cell index in 15-char hex form."},
		},
	}
	src := &stringRowsReader{
		header: []string{"amount", "loc", "cell"},
		rows: [][]string{
			{"123.456789", "POINT(-122.418 37.775)", "89283082803ffff"},
			{"-0.000001", "POINT(0 0)", "89283082807ffff"},
		},
	}
	fs := afero.NewMemMapFs()
	job := &ImportJob{
		Source: src,
		Target: "test.pulse",
		Schema: schema,
		FS:     fs,
	}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.RowsImported != 2 {
		t.Errorf("rows imported = %d, want 2", rep.RowsImported)
	}
	if len(rep.RowErrors) != 0 {
		t.Errorf("unexpected row errors: %v", rep.RowErrors)
	}

	data, err := afero.ReadFile(fs, "test.pulse")
	if err != nil {
		t.Fatalf("read pulse: %v", err)
	}

	// Read back the schema and records.
	rdr := bytes.NewReader(data)
	if err := encoding.ReadHeader(rdr); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	gotSchema, err := encoding.ReadSchema(rdr)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if got, want := gotSchema.Fields[0].Precision, uint8(20); got != want {
		t.Errorf("precision = %d, want %d", got, want)
	}
	if got, want := gotSchema.Fields[0].Scale, uint8(6); got != want {
		t.Errorf("scale = %d, want %d", got, want)
	}
	if got, want := gotSchema.Fields[2].H3Resolution, uint8(9); got != want {
		t.Errorf("h3 res = %d, want %d", got, want)
	}

	rr := encoding.NewRecordReader(rdr, gotSchema)
	values := map[string]float64{}
	nulls := map[string]bool{}
	wide := map[string]any{}
	if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
		t.Fatalf("ReadRecord row 0: %v", err)
	}
	d, ok := wide["amount"].(encoding.Decimal128)
	if !ok {
		t.Fatalf("amount missing from wide map: %v", wide)
	}
	if got := d.String(6); got != "123.456789" {
		t.Errorf("amount = %s, want 123.456789", got)
	}
	p, ok := wide["loc"].(encoding.PointF64)
	if !ok {
		t.Fatalf("loc missing")
	}
	if p.Lat != 37.775 || p.Lon != -122.418 {
		t.Errorf("point = %+v", p)
	}
	c, ok := wide["cell"].(encoding.H3Cell)
	if !ok {
		t.Fatalf("cell missing")
	}
	if encoding.FormatH3CellHex(c) != "89283082803ffff" {
		t.Errorf("cell = %s", encoding.FormatH3CellHex(c))
	}
}

func TestImport_DecimalReject(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2, CsvColumnIdx: 0, Description: "Amount field with strict parsing rules applied."},
		},
	}
	bad := []string{"$1,234.56", "1 234.56", "1.5e3", "abc"}
	for _, raw := range bad {
		src := &stringRowsReader{header: []string{"amount"}, rows: [][]string{{raw}}}
		fs := afero.NewMemMapFs()
		job := &ImportJob{Source: src, Target: "t.pulse", Schema: schema, FS: fs}
		rep, err := job.Run(context.Background())
		if err != nil {
			t.Fatalf("Run %q: %v", raw, err)
		}
		if len(rep.RowErrors) == 0 {
			t.Errorf("Run %q: expected row error, got none", raw)
		}
	}
}
