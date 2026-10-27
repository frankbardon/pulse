package io

import (
	"bytes"
	"context"
	stderrors "errors"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// collectingWriter keeps every exported row.
type collectingWriter struct{ rows [][]any }

func (w *collectingWriter) WriteHeader([]string) error { return nil }
func (w *collectingWriter) WriteRow(values []any) error {
	w.rows = append(w.rows, append([]any(nil), values...))
	return nil
}
func (w *collectingWriter) Close() error { return nil }

// corruptExportCohort imports four rows through the encoder (region
// categorical, tags set, seg a nullable categorical that is null on
// every row — so its dictionary is EMPTY and every row stores the
// placeholder 0 under the null bit), then patches field off of row 2 to
// v (off < 0: no patch).
func corruptExportCohort(t *testing.T, off int, v byte) afero.Fs {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: 0, Dictionary: encoding.NewDictionary()},
		{Name: "tags", Type: encoding.FieldTypeSetU8, CsvColumnIdx: 1, Dictionary: encoding.NewDictionary()},
		{Name: "seg", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: 2, Nullable: true, Dictionary: encoding.NewDictionary()},
	}}
	rows := [][]string{{"north", "VISA", ""}, {"south", "MC", ""}, {"north", "VISA|MC", ""}, {"south", EmptySetCell, ""}}
	fs := importedCohort(t, schema, []string{"region", "tags", "seg"}, rows)
	b, err := afero.ReadFile(fs, "c.pulse")
	if err != nil {
		t.Fatal(err)
	}
	ver, err := encoding.ReadHeader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(b[encoding.HeaderSize:])
	got, err := encoding.ReadSchema(r, ver)
	if err != nil {
		t.Fatal(err)
	}
	if n := got.Field("seg").Dictionary.Count(); n != 0 {
		t.Fatalf("seg dictionary holds %d entries; the null-placeholder case needs it empty", n)
	}
	if off >= 0 {
		stride := got.RecordByteSize()
		b[len(b)-len(rows)*stride+2*stride+off] = v
	}
	if err := afero.WriteFile(fs, "c.pulse", b, 0o644); err != nil {
		t.Fatal(err)
	}
	return fs
}

// TestCorruptCategorical_ExportRefuses (U51 E5-S8): export refuses a
// non-null categorical code past its dictionary (which used to export as
// an empty cell) and a set bit past its dictionary (which used to drop
// the member), and still exports a NULL cell whose placeholder code
// addresses no entry — the fault is judged after the null bitmap.
//
// Falsified by deleting the faults[i] = nil reset in the bitmap loop
// (the clean cohort's null seg cells then refuse) and by deleting the
// categorical faults[i] assignment (the region case then exports).
func TestCorruptCategorical_ExportRefuses(t *testing.T) {
	t.Parallel()

	run := func(fs afero.Fs) (*collectingWriter, error) {
		w := &collectingWriter{}
		_, err := (&ExportJob{Source: "c.pulse", Target: w, FS: fs}).Run(context.Background())
		return w, err
	}
	w, err := run(corruptExportCohort(t, -1, 0))
	if err != nil {
		t.Fatalf("clean export: %v", err)
	}
	if len(w.rows) != 4 || w.rows[0][2] != nil {
		t.Fatalf("clean export rows = %v; want 4 rows with a nil seg cell", w.rows)
	}
	for name, tc := range map[string]struct {
		off    int
		v      byte
		field  string
		reason string
	}{
		"categorical": {0, 9, "region", encx.ReasonCategoricalIndexOutOfRange},
		"set":         {1, 0x10, "tags", encx.ReasonSetMemberOutOfRange},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := run(corruptExportCohort(t, tc.off, tc.v))
			var ce *perrors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perrors.ENCODING_INVALID ||
				ce.Details["field"] != tc.field || ce.Details["reason"] != tc.reason {
				t.Fatalf("err = %v, want ENCODING_INVALID on %q (%s)", err, tc.field, tc.reason)
			}
		})
	}
}
