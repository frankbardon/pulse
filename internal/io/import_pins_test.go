package io

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// pinnedReader is a text source that also declares some columns' widths
// (DeclaredTypeReader), the shape the Arrow and Parquet readers take.
type pinnedReader struct {
	*mockReader
	pins map[string]encoding.Field
}

func (p *pinnedReader) DeclaredColumnTypes() (map[string]encoding.Field, error) {
	return p.pins, nil
}

var _ DeclaredTypeReader = (*pinnedReader)(nil)

// pinFixture: 100 rows. "small" holds 0..15 (inference: u4) with 300 at
// row 80, past a 50-row sample; "n" holds 0..15 with -3 at row 90;
// "name" is text. Only "small" is pinned (u8).
func pinFixture() *pinnedReader {
	cols := []string{"small", "n", "name"}
	var rows [][]string
	for i := 0; i < 100; i++ {
		small, n := fmt.Sprint(i%16), fmt.Sprint(i%16)
		switch i + 1 {
		case 80:
			small = "300"
		case 90:
			n = "-3"
		}
		rows = append(rows, []string{small, n, fmt.Sprintf("p%d", i%3)})
	}
	return &pinnedReader{
		mockReader: newMockReader(cols, rows),
		pins:       map[string]encoding.Field{"small": {Name: "small", Type: encoding.FieldTypeU8}},
	}
}

// TestImportPins_PinnedColumnKeepsDeclaredWidth: a pinned column takes
// its declared type instead of the sample's (u8, not u4), never widens,
// and a value past it is a per-row PULSE_IMPORT_ROW_ERROR. An unpinned
// column in the same import still infers and widens exactly as before.
func TestImportPins_PinnedColumnKeepsDeclaredWidth(t *testing.T) {
	t.Parallel()

	rep, _, _, err := runGroupImport(t, pinFixture(), nil, func(j *ImportJob) { j.SampleRows = 50 })
	if err != nil {
		t.Fatal(err)
	}
	if f := fieldByName(rep.Schema, "small"); f.Type != encoding.FieldTypeU8 {
		t.Errorf("pinned small = %s, want u8", f.Type)
	}
	if f := fieldByName(rep.Schema, "n"); f.Type != encoding.FieldTypeF64 {
		t.Errorf("unpinned n = %s, want f64 (inferred u4, widened by -3)", f.Type)
	}
	if f := fieldByName(rep.Schema, "name"); f.Type != encoding.FieldTypeCategoricalU8 {
		t.Errorf("unpinned name = %s, want categorical_u8", f.Type)
	}
	if rep.RowsImported != 99 || len(rep.RowErrors) != 1 {
		t.Fatalf("imported %d with %d row errors, want 99 / 1", rep.RowsImported, len(rep.RowErrors))
	}
	re := rep.RowErrors[0]
	ce, ok := re.Err.(*perrors.CodedError)
	if !ok || ce.Code != perrors.PULSE_IMPORT_ROW_ERROR || re.Row != 80 || ce.Details["column"] != "small" {
		t.Fatalf("row error %+v, want PULSE_IMPORT_ROW_ERROR on small at row 80", re)
	}
	if len(rep.WidthWarnings) != 1 || rep.WidthWarnings[0].Details["field"] != "n" {
		t.Fatalf("width warnings %v, want exactly one, for n", rep.WidthWarnings)
	}

	// Predict reports the schema Run writes.
	job := NewImportJob(pinFixture(), "p.pulse")
	job.FS = afero.NewMemMapFs()
	job.SampleRows = 50
	pr, err := job.Predict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f := fieldByName(pr.Schema, "small"); f.Type != encoding.FieldTypeU8 {
		t.Errorf("predicted small = %s, want u8", f.Type)
	}
}

// TestImportPins_OverrideBeatsPin: a column_type_overrides entry wins
// over the source's pin for its column.
func TestImportPins_OverrideBeatsPin(t *testing.T) {
	t.Parallel()

	rep, _, _, err := runGroupImport(t, pinFixture(), nil, func(j *ImportJob) {
		j.SampleRows = 50
		j.ColumnTypeOverrides = map[string]encoding.FieldType{"small": encoding.FieldTypeU16}
	})
	if err != nil {
		t.Fatal(err)
	}
	if f := fieldByName(rep.Schema, "small"); f.Type != encoding.FieldTypeU16 {
		t.Errorf("overridden small = %s, want u16", f.Type)
	}
	if rep.RowsImported != 100 {
		t.Errorf("imported %d rows, want 100 (300 fits the override)", rep.RowsImported)
	}
}

// TestImportPins_ExplicitSchemaMakesPinsInert: an explicit Schema is
// the caller's whole declaration; pins never touch it.
func TestImportPins_ExplicitSchemaMakesPinsInert(t *testing.T) {
	t.Parallel()

	rep, _, _, err := runGroupImport(t, pinFixture(), nil, func(j *ImportJob) {
		j.Schema = &encoding.Schema{Fields: []encoding.Field{
			{Name: "small", Type: encoding.FieldTypeU16},
			{Name: "n", Type: encoding.FieldTypeF64},
			{Name: "name", Type: encoding.FieldTypeCategoricalU8},
		}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if f := fieldByName(rep.Schema, "small"); f.Type != encoding.FieldTypeU16 || rep.RowsImported != 100 {
		t.Errorf("small = %s with %d rows, want u16 / 100", f.Type, rep.RowsImported)
	}
}

// TestImportPins_DecimalAndNullability: a decimal128 pin carries the
// source's precision and scale; a pinned column's nullability is
// inference's — nullable from a sampled null, promoted on a later one.
func TestImportPins_DecimalAndNullability(t *testing.T) {
	t.Parallel()

	cols := []string{"price", "late"}
	var rows [][]string
	for i := 0; i < 80; i++ {
		price, late := fmt.Sprintf("%d.25", i), fmt.Sprint(i)
		if i == 3 {
			price = ""
		}
		if i == 70 {
			late = ""
		}
		rows = append(rows, []string{price, late})
	}
	src := &pinnedReader{mockReader: newMockReader(cols, rows), pins: map[string]encoding.Field{
		"price": {Name: "price", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2},
		"late":  {Name: "late", Type: encoding.FieldTypeU32},
	}}
	rep, _, _, err := runGroupImport(t, src, nil, func(j *ImportJob) { j.SampleRows = 50 })
	if err != nil {
		t.Fatal(err)
	}
	p := fieldByName(rep.Schema, "price")
	if p.Type != encoding.FieldTypeDecimal128 || p.Precision != 10 || p.Scale != 2 || !p.Nullable {
		t.Errorf("price = %s p%d s%d nullable=%v, want decimal128 p10 s2 nullable", p.Type, p.Precision, p.Scale, p.Nullable)
	}
	l := fieldByName(rep.Schema, "late")
	if l.Type != encoding.FieldTypeU32 || !l.Nullable {
		t.Errorf("late = %s nullable=%v, want u32 promoted to nullable", l.Type, l.Nullable)
	}
	if len(rep.PromotedFields) != 1 || rep.PromotedFields[0] != "late" {
		t.Errorf("promoted fields %v, want [late]", rep.PromotedFields)
	}
}

// TestConvertPins_PinnedColumnKeepsDeclaredWidth: convert reaches the
// same pins — the declared type and no promotion. As with any declared
// width, convert hands the target the cell text and the overflow is a
// row error of the KeepPulseAt intermediate (stage keep_pulse), whose
// cohort is the one a plain import writes.
func TestConvertPins_PinnedColumnKeepsDeclaredWidth(t *testing.T) {
	t.Parallel()

	rep, _, kept := runConvert(t, pinFixture(), true, func(j *ConvertJob) { j.SampleRows = 50 })
	if f := fieldByName(rep.Schema, "small"); f.Type != encoding.FieldTypeU8 {
		t.Errorf("convert small = %s, want u8", f.Type)
	}
	if rep.RowsConverted != 100 || len(rep.DroppedWarnings) != 1 || rep.DroppedWarnings[0].Details["stage"] != "keep_pulse" {
		t.Fatalf("converted %d with dropped warnings %v, want 100 and one keep_pulse drop", rep.RowsConverted, rep.DroppedWarnings)
	}
	for _, w := range rep.WidthWarnings {
		if w.Details["field"] == "small" {
			t.Errorf("pinned small promoted: %v", w)
		}
	}
	_, imported, _, err := runGroupImport(t, pinFixture(), nil, func(j *ImportJob) { j.SampleRows = 50 })
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kept, imported) {
		t.Errorf("kept cohort (%d bytes) differs from the import (%d bytes)", len(kept), len(imported))
	}

	job := NewConvertJob(pinFixture(), &collectWriter{})
	job.FS = afero.NewMemMapFs()
	job.SampleRows = 50
	pr, err := job.Predict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f := fieldByName(pr.Schema, "small"); f.Type != encoding.FieldTypeU8 {
		t.Errorf("predicted convert small = %s, want u8", f.Type)
	}
}
