package io

import (
	"bytes"
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
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

func TestImport_DecimalRoundTrip(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 6, CsvColumnIdx: 0, Description: "Amount in USD with 6 decimal places of precision."},
		},
	}
	src := &stringRowsReader{
		header: []string{"amount"},
		rows: [][]string{
			{"123.456789"},
			{"-0.000001"},
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
}

// TestImport_DecimalReject pins the four decimal literals the parser
// refuses.
//
// Each fixture is a ONE-row source, so every one of them is now a TOTAL
// import failure and Run returns a coded error instead of a report with
// a lone RowErrors entry — see totalRowFailure. Before E3-S7 this test
// asserted `err == nil` and reached into rep.RowErrors; that shape is
// exactly what let a wholly-failed import pass for success, so the
// assertion moved onto the error return.
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
		if err == nil {
			t.Fatalf("Run %q: nil error with RowsImported = %d; the only row failed", raw, rep.RowsImported)
		}
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_ROW_ERROR {
			t.Errorf("Run %q: err = %v, want a %s CodedError", raw, err, perr.PULSE_IMPORT_ROW_ERROR)
		}
	}
}
