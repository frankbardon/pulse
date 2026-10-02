package io

import (
	"context"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// requireOverrideRefusal asserts err is the PULSE_IMPORT_OVERRIDE_INVALID
// refusal and returns its details.
func requireOverrideRefusal(t *testing.T, err error) map[string]any {
	t.Helper()
	if err == nil {
		t.Fatal("err = nil, want PULSE_IMPORT_OVERRIDE_INVALID")
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want a *errors.CodedError", err, err)
	}
	if ce.Code != errors.PULSE_IMPORT_OVERRIDE_INVALID {
		t.Fatalf("code = %s (%v), want PULSE_IMPORT_OVERRIDE_INVALID", ce.Code, err)
	}
	return ce.Details
}

// smallIntRows is n = 1..4 cycling over count rows — a column inference
// alone narrows to u4.
func smallIntRows(count int) [][]string {
	rows := make([][]string, count)
	for i := range rows {
		rows[i] = []string{fmt.Sprint(i%4 + 1), "x"}
	}
	return rows
}

// TestImportJob_OverrideToU8Wins is the reported defect: FieldTypeU8 is
// the iota zero of FieldType, so an override to u8 was read as "no
// override" and the column narrowed to u4. Every integer rung, and every
// other type a 1..4 column can hold, must come back exactly as forced.
func TestImportJob_OverrideToU8Wins(t *testing.T) {
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeU8, encoding.FieldTypeU4, encoding.FieldTypeU16,
		encoding.FieldTypeU32, encoding.FieldTypeU64, encoding.FieldTypeF32,
		encoding.FieldTypeF64, encoding.FieldTypeCategoricalU8,
		encoding.FieldTypeCategoricalU16, encoding.FieldTypeSetU8,
		encoding.FieldTypeDecimal128,
	} {
		t.Run(ft.String(), func(t *testing.T) {
			fs := afero.NewMemMapFs()
			job := NewImportJob(newMockReader([]string{"n", "s"}, smallIntRows(60)), "out.pulse")
			job.FS = fs
			job.ColumnTypeOverrides = map[string]encoding.FieldType{"n": ft}
			rep, err := job.Run(context.Background())
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if rep.RowsImported != 60 || len(rep.RowErrors) != 0 {
				t.Fatalf("imported %d rows, %d row errors (%v); want 60, 0", rep.RowsImported, len(rep.RowErrors), rep.RowErrors)
			}
			schema, _, _ := decodeAll(t, fs, "out.pulse")
			if got := schema.Fields[0].Type; got != ft {
				t.Errorf("n type on disk = %s, want %s (forced)", got, ft)
			}
		})
	}
}

// TestImportJob_OverrideAbsentColumnStillInfers pins the other half of
// the presence fix: a column with no override keeps inference, so a
// map that names only one column does not force u8 onto the rest.
func TestImportJob_OverrideAbsentColumnStillInfers(t *testing.T) {
	rows := make([][]string, 60)
	for i := range rows {
		rows[i] = []string{fmt.Sprint(i%4 + 1), fmt.Sprint(i%3 + 1)}
	}
	fs := afero.NewMemMapFs()
	job := NewImportJob(newMockReader([]string{"n", "m"}, rows), "out.pulse")
	job.FS = fs
	job.ColumnTypeOverrides = map[string]encoding.FieldType{"n": encoding.FieldTypeU16}
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	schema, _, _ := decodeAll(t, fs, "out.pulse")
	if got := schema.Fields[0].Type; got != encoding.FieldTypeU16 {
		t.Errorf("n = %s, want u16", got)
	}
	if got := schema.Fields[1].Type; got != encoding.FieldTypeU4 {
		t.Errorf("m = %s, want u4 (inferred, not overridden)", got)
	}
}

// TestImportJob_OverrideUnrepresentableRefused: a value the forced type
// cannot hold refuses the whole import naming column, value and type —
// inside the inference sample and past it alike, and with no cohort
// left behind. Before, an in-sample overflow became a skipped row.
func TestImportJob_OverrideUnrepresentableRefused(t *testing.T) {
	cases := []struct {
		name      string
		ft        encoding.FieldType
		bad       string
		badRow    int // 0-based row index the bad value lands on
		sampleCap int
	}{
		{"u8 in sample", encoding.FieldTypeU8, "300", 3, 0},
		{"u8 past sample", encoding.FieldTypeU8, "300", 120, 50},
		{"u4 in sample", encoding.FieldTypeU4, "16", 0, 0},
		{"u16 fraction", encoding.FieldTypeU16, "2.5", 7, 0},
		{"u32 negative", encoding.FieldTypeU32, "-1", 59, 0},
		{"date non-date", encoding.FieldTypeDate, "banana", 10, 0},
		{"date non-date past sample", encoding.FieldTypeDate, "banana", 150, 50},
		{"datetime non-datetime", encoding.FieldTypeDateTime, "2024-13-45", 2, 0},
		{"packed_bool non-bool", encoding.FieldTypePackedBool, "maybe", 5, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := make([][]string, 200)
			for i := range rows {
				switch c.ft {
				case encoding.FieldTypeDate:
					rows[i] = []string{"1950-06-01", "x"}
				case encoding.FieldTypeDateTime:
					rows[i] = []string{"1950-06-01T00:00:00Z", "x"}
				case encoding.FieldTypePackedBool:
					rows[i] = []string{"true", "x"}
				default:
					rows[i] = []string{fmt.Sprint(i%4 + 1), "x"}
				}
			}
			rows[c.badRow][0] = c.bad
			fs := afero.NewMemMapFs()
			job := NewImportJob(newMockReader([]string{"n", "s"}, rows), "out.pulse")
			job.FS = fs
			job.SampleRows = c.sampleCap
			job.ColumnTypeOverrides = map[string]encoding.FieldType{"n": c.ft}
			rep, err := job.Run(context.Background())
			if rep != nil {
				t.Errorf("report = %+v, want nil on refusal", rep)
			}
			d := requireOverrideRefusal(t, err)
			if d["column"] != "n" || d["type"] != c.ft.String() || d["value"] != c.bad || d["row"] != c.badRow+1 {
				t.Errorf("details = %v, want column n, type %s, value %q, row %d", d, c.ft, c.bad, c.badRow+1)
			}
			if ok, _ := afero.Exists(fs, "out.pulse"); ok {
				t.Error("a refused import left out.pulse behind")
			}

			// Predict refuses the in-sample case identically.
			if c.sampleCap == 0 {
				pj := NewImportJob(newMockReader([]string{"n", "s"}, rows), "out.pulse")
				pj.FS = fs
				pj.ColumnTypeOverrides = job.ColumnTypeOverrides
				_, perr := pj.Predict(context.Background())
				requireOverrideRefusal(t, perr)
			}
		})
	}
}

// TestImportJob_OverridePreEpochDate: dates are signed epoch days, so a
// forced date column accepts pre-1970 values.
func TestImportJob_OverridePreEpochDate(t *testing.T) {
	rows := make([][]string, 60)
	for i := range rows {
		rows[i] = []string{"1950-06-01", "x"}
	}
	rows[1][0] = "1969-12-31"
	fs := afero.NewMemMapFs()
	job := NewImportJob(newMockReader([]string{"d", "s"}, rows), "out.pulse")
	job.FS = fs
	job.ColumnTypeOverrides = map[string]encoding.FieldType{"d": encoding.FieldTypeDate}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.RowsImported != 60 {
		t.Fatalf("RowsImported = %d, want 60", rep.RowsImported)
	}
	if got := rep.Schema.Fields[0].Type; got != encoding.FieldTypeDate {
		t.Errorf("d = %s, want date", got)
	}
}

// TestImportJob_OverrideUnknownColumnRefused: an override naming a
// column the header does not carry is refused, not silently dropped.
// Matching is exact — a case or whitespace variant is unknown too.
func TestImportJob_OverrideUnknownColumnRefused(t *testing.T) {
	for _, name := range []string{"missing", "N", " n", "n "} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			job := NewImportJob(newMockReader([]string{"n", "s"}, smallIntRows(60)), "out.pulse")
			job.FS = afero.NewMemMapFs()
			job.ColumnTypeOverrides = map[string]encoding.FieldType{name: encoding.FieldTypeU8}
			_, err := job.Run(context.Background())
			d := requireOverrideRefusal(t, err)
			if d["column"] != name {
				t.Errorf("details.column = %v, want %q", d["column"], name)
			}
		})
	}
}

// TestImportJob_OverrideWithExplicitSchemaRefused: an explicit Schema
// leaves nothing to override; the conflicting instruction is refused
// instead of being ignored.
func TestImportJob_OverrideWithExplicitSchemaRefused(t *testing.T) {
	job := NewImportJob(newMockReader([]string{"n", "s"}, smallIntRows(60)), "out.pulse")
	job.FS = afero.NewMemMapFs()
	job.Schema = &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeU4, CsvColumnIdx: 0},
	}}
	job.ColumnTypeOverrides = map[string]encoding.FieldType{"n": encoding.FieldTypeU8}
	_, err := job.Run(context.Background())
	requireOverrideRefusal(t, err)
	_, err = job.Predict(context.Background())
	requireOverrideRefusal(t, err)
}

// TestImportJob_OverrideDecimalScaleRefusedNotRounded: a forced
// decimal128 takes the sample's largest scale; a later value with more
// fractional digits is refused, never silently rounded to fit.
func TestImportJob_OverrideDecimalScaleRefusedNotRounded(t *testing.T) {
	rows := make([][]string, 120)
	for i := range rows {
		rows[i] = []string{"1.5", "x"}
	}
	rows[100][0] = "1.25"
	job := NewImportJob(newMockReader([]string{"amt", "s"}, rows), "out.pulse")
	job.FS = afero.NewMemMapFs()
	job.SampleRows = 50
	job.ColumnTypeOverrides = map[string]encoding.FieldType{"amt": encoding.FieldTypeDecimal128}
	_, err := job.Run(context.Background())
	if d := requireOverrideRefusal(t, err); d["value"] != "1.25" || d["row"] != 101 {
		t.Errorf("details = %v, want value 1.25 at row 101", d)
	}
}

// TestImportJob_OverrideMeasuredPredictRefusesPastSample: the measured
// predict pass (constant elision / groups) converts every row as Run
// does, so it refuses an out-of-sample value exactly as Run does.
func TestImportJob_OverrideMeasuredPredictRefusesPastSample(t *testing.T) {
	rows := smallIntRows(200)
	rows[150][0] = "300"
	job := NewImportJob(newMockReader([]string{"n", "s"}, rows), "out.pulse")
	job.FS = afero.NewMemMapFs()
	job.SampleRows = 50
	job.ElideConstants = true
	job.ColumnTypeOverrides = map[string]encoding.FieldType{"n": encoding.FieldTypeU8}
	_, err := job.Predict(context.Background())
	if d := requireOverrideRefusal(t, err); d["value"] != "300" || d["row"] != 151 {
		t.Errorf("details = %v, want value 300 at row 151", d)
	}
}
