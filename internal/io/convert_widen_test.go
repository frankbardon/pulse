package io

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// runConvert converts cols/rows through a collectWriter, optionally
// keeping the intermediate cohort, and returns the report, the target
// rows and the kept cohort's bytes (nil without keep).
func runConvert(t *testing.T, src Reader, keep bool, mutate ...func(*ConvertJob)) (*ConvertReport, *collectWriter, []byte) {
	t.Helper()
	target := &collectWriter{}
	fs := afero.NewMemMapFs()
	job := NewConvertJob(src, target)
	job.FS = fs
	if keep {
		job.KeepPulseAt = "kept.pulse"
	}
	for _, m := range mutate {
		m(job)
	}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var kept []byte
	if keep {
		if kept, err = afero.ReadFile(fs, "kept.pulse"); err != nil {
			t.Fatal(err)
		}
	}
	return rep, target, kept
}

// sameWidthWarnings asserts two warning lists name the same fields with
// the same from / to / source_row, in order.
func sameWidthWarnings(t *testing.T, got, want []*perrors.CodedError) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("width warnings %v, want %v", got, want)
	}
	for k := range want {
		if got[k].Code != perrors.PULSE_IMPORT_WIDTH_PROMOTED || !reflect.DeepEqual(got[k].Details, want[k].Details) {
			t.Errorf("warning %d = %s %v, want %s %v", k, got[k].Code, got[k].Details, want[k].Code, want[k].Details)
		}
	}
}

// targetRowsAreSource asserts the target received every source cell as
// text, unchanged — a promotion never touches what convert emits.
func targetRowsAreSource(t *testing.T, w *collectWriter, rows [][]string) {
	t.Helper()
	if len(w.rows) != len(rows) {
		t.Fatalf("target got %d rows, want %d", len(w.rows), len(rows))
	}
	for k, src := range rows {
		for c, cell := range src {
			if w.rows[k][c] != cell {
				t.Fatalf("target row %d col %d = %v, want %q", k, c, w.rows[k][c], cell)
			}
		}
	}
}

// TestConvertWiden_PromotesLikeImport: a join-shaped source whose
// parent-name column outgrows the categorical_u8 its 500-row sample
// infers (and two integer columns their widths) converts every row
// instead of failing with PULSE_IMPORT_CATEGORICAL_OVERFLOW. The report
// carries the same final types and the same warnings the import does,
// and the target receives the source text unchanged.
func TestConvertWiden_PromotesLikeImport(t *testing.T) {
	cols, rows := widenFixture()
	imp, _ := importRaw(t, cols, rows)
	rep, target, _ := runConvert(t, newMockReader(cols, rows), false)

	if rep.RowsConverted != len(rows) || len(rep.RowErrors) != 0 {
		t.Fatalf("converted %d rows with %d row errors, want %d / 0", rep.RowsConverted, len(rep.RowErrors), len(rows))
	}
	for _, f := range imp.Schema.Fields {
		if got := fieldByName(rep.Schema, f.Name); got.Type != f.Type || got.ByteOffset != f.ByteOffset {
			t.Errorf("%s: convert %s@%d, import %s@%d", f.Name, got.Type, got.ByteOffset, f.Type, f.ByteOffset)
		}
	}
	sameWidthWarnings(t, rep.WidthWarnings, imp.WidthWarnings)
	targetRowsAreSource(t, target, rows)
}

// TestConvertWiden_KeepPulseIsTheImport: the --keep-pulse intermediate
// of a promoting convert is byte-for-byte the cohort a plain import of
// the same source writes.
func TestConvertWiden_KeepPulseIsTheImport(t *testing.T) {
	cols, rows := widenFixture()
	_, want := importRaw(t, cols, rows)
	_, _, kept := runConvert(t, newMockReader(cols, rows), true)
	if !bytes.Equal(kept, want) {
		t.Fatalf("kept cohort (%d bytes) differs from the import (%d bytes)", len(kept), len(want))
	}
}

// TestConvertWiden_NoOverflowUnchanged: a source that never outgrows its
// sample-inferred widths converts exactly as before — the sample-inferred
// schema, no warnings, the text passed through, and a kept cohort equal
// to the plain import's.
func TestConvertWiden_NoOverflowUnchanged(t *testing.T) {
	cols, rows := widenFixture()
	rows = rows[:500] // the sample IS the file: nothing can outgrow it
	inferred, _, err := InferSchema(newMockReader(cols, rows), 500)
	if err != nil {
		t.Fatal(err)
	}
	rep, target, kept := runConvert(t, newMockReader(cols, rows), true)
	if len(rep.WidthWarnings) != 0 {
		t.Fatalf("width warnings on a source that fits: %v", rep.WidthWarnings)
	}
	for i, f := range inferred.Fields {
		if got := rep.Schema.Fields[i]; got.Type != f.Type || got.ByteOffset != f.ByteOffset {
			t.Errorf("%s: %s@%d, want the inferred %s@%d", f.Name, got.Type, got.ByteOffset, f.Type, f.ByteOffset)
		}
	}
	targetRowsAreSource(t, target, rows)
	if _, want := importRaw(t, cols, rows); !bytes.Equal(kept, want) {
		t.Fatal("kept cohort differs from the plain import")
	}
}

// TestConvertWiden_DeclaredSchemasStillRefuse: an authoritative
// (SchemaAwareReader) categorical_u8 is a contract — past 256 entries the
// convert still refuses with the fatal overflow. (The explicit
// ConvertJob.Schema arm is TestConvertJob_CategoricalOverflowIsReported.)
func TestConvertWiden_DeclaredSchemasStillRefuse(t *testing.T) {
	cols, rows := overflowRows()
	src := &authoritativeReader{columns: cols, rows: rows, schema: &encoding.Schema{Fields: []encoding.Field{{Name: "c", Type: encoding.FieldTypeCategoricalU8}}}}
	job := NewConvertJob(src, &collectWriter{})
	job.FS = afero.NewMemMapFs()
	_, err := job.Run(context.Background())
	if err == nil {
		t.Fatal("authoritative categorical_u8 overflow converted")
	}
	if ce := asCodedConvertErr(t, err); ce.Code != perrors.PULSE_IMPORT_CATEGORICAL_OVERFLOW || ce.Details["type"] != "categorical_u8" {
		t.Errorf("error %s %v, want PULSE_IMPORT_CATEGORICAL_OVERFLOW on categorical_u8", ce.Code, ce.Details)
	}

	// The same rows, inferred from a 50-row sample, promote.
	rep, _, _ := runConvert(t, newMockReader(cols, rows), false, func(j *ConvertJob) { j.SampleRows = 50 })
	if rep.RowsConverted != len(rows) || rep.Schema.Fields[0].Type != encoding.FieldTypeCategoricalU16 {
		t.Fatalf("inferred: converted %d as %s, want %d as categorical_u16", rep.RowsConverted, rep.Schema.Fields[0].Type, len(rows))
	}
}

// f32Fixture is a float column whose 500-row sample fits f32 (x.25 and
// x.5 values, all exact in f32) and a later value that does not:
// 1e300 at row 600 (past math.MaxFloat32), 1e-50 at row 700 (below
// math.SmallestNonzeroFloat32, which f32 flushes to zero). z's only
// out-of-range value is an underflow — 1e-50 at row 650, which parses at
// f32 WITHOUT error (to zero), so only the explicit range test catches
// it. y only ever holds f32-range values, including 0.1 (which f32
// rounds) — it never promotes.
func f32Fixture() ([]string, [][]string) {
	var rows [][]string
	for i := 0; i < 800; i++ {
		x := fmt.Sprintf("%d.25", i)
		if i%2 == 1 {
			x = fmt.Sprintf("%d.5", i)
		}
		switch i + 1 {
		case 600:
			x = "1e300"
		case 700:
			x = "1e-50"
		}
		y := fmt.Sprintf("%d.75", i%9)
		if i == 650 {
			y = "0.1"
		}
		z := fmt.Sprintf("%d.125", i%5)
		if i+1 == 650 {
			z = "1e-50"
		}
		rows = append(rows, []string{x, y, z})
	}
	return []string{"x", "y", "z"}, rows
}

// TestImportWiden_F32PromotesToF64: an inferred f32 that meets a value
// outside f32's range promotes to f64 at the first such row; every
// earlier value decodes exactly (every f32 is exact in f64), the out-of-
// range values decode as written instead of ±Inf / 0, the cohort is the
// one a whole-file sample writes, and a column whose values f32 merely
// rounds stays f32.
func TestImportWiden_F32PromotesToF64(t *testing.T) {
	cols, rows := f32Fixture()
	rep, raw, fs, err := runGroupImport(t, newMockReader(cols, rows), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RowErrors) != 0 || rep.RowsImported != len(rows) {
		t.Fatalf("imported %d, row errors %v", rep.RowsImported, rep.RowErrors)
	}
	if got := fieldByName(rep.Schema, "x").Type; got != encoding.FieldTypeF64 {
		t.Fatalf("x = %s, want f64", got)
	}
	if got := fieldByName(rep.Schema, "y").Type; got != encoding.FieldTypeF32 {
		t.Errorf("y = %s, want f32 (0.1 is in range)", got)
	}
	if len(rep.WidthWarnings) != 2 {
		t.Fatalf("width warnings %v, want one each for x and z", rep.WidthWarnings)
	}
	for field, row := range map[string]int{"x": 600, "z": 650} {
		if w := warningFor(t, rep.WidthWarnings, field); w.Details["from"] != "f32" || w.Details["to"] != "f64" || w.Details["source_row"] != row {
			t.Errorf("%s warning %v, want f32 → f64 at row %d", field, w.Details, row)
		}
	}
	_, vals, _ := decodeAll(t, fs, "out.pulse")
	for k, src := range rows {
		var want float64
		_, _ = fmt.Sscan(src[0], &want)
		if vals[k]["x"] != want {
			t.Fatalf("record %d x = %v, want %v", k, vals[k]["x"], want)
		}
	}
	if vals[699]["x"] != 1e-50 || math.IsInf(vals[599]["x"], 0) || vals[649]["z"] != 1e-50 {
		t.Errorf("out-of-range values decoded as %v / %v / %v", vals[599]["x"], vals[699]["x"], vals[649]["z"])
	}
	if _, full := importRaw(t, cols, rows, func(j *ImportJob) { j.SampleRows = len(rows) }); !bytes.Equal(raw, full) {
		t.Error("promoted cohort differs from the whole-file-sample cohort")
	}
}

// TestImportWiden_F32DeclaredNeverPromotes: a forced f32 keeps its
// width — 1e300 is never promoted to f64. Since E4-S13 a value a
// ColumnTypeOverrides column cannot hold refuses the import (naming the
// row) instead of becoming a skipped row.
func TestImportWiden_F32DeclaredNeverPromotes(t *testing.T) {
	cols, rows := f32Fixture()
	_, _, _, err := runGroupImport(t, newMockReader(cols, rows), nil, func(j *ImportJob) {
		j.ColumnTypeOverrides = map[string]encoding.FieldType{"x": encoding.FieldTypeF32, "z": encoding.FieldTypeF32}
	})
	d := requireOverrideRefusal(t, err)
	if d["column"] != "x" || d["type"] != "f32" || d["row"] != 600 {
		t.Errorf("refusal details %v, want column x, type f32, row 600", d)
	}
}

// TestImportPredictWiden_F32: the measured import predict reports the
// f64 the import writes, with the same warning.
func TestImportPredictWiden_F32(t *testing.T) {
	cols, rows := f32Fixture()
	imp, _ := importRaw(t, cols, rows)
	rep, err := predictJob(t, newMockReader(cols, rows), func(j *ImportJob) { j.ElideConstants = true })
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldByName(rep.Schema, "x").Type; got != encoding.FieldTypeF64 {
		t.Errorf("predicted x = %s, want f64", got)
	}
	sameWidthWarnings(t, rep.WidthWarnings, imp.WidthWarnings)
}

// TestConvertWiden_F32: convert reports the promoted f64 and the
// warning, and its kept cohort is the import's.
func TestConvertWiden_F32(t *testing.T) {
	cols, rows := f32Fixture()
	imp, want := importRaw(t, cols, rows)
	rep, target, kept := runConvert(t, newMockReader(cols, rows), true)
	if got := fieldByName(rep.Schema, "x").Type; got != encoding.FieldTypeF64 {
		t.Errorf("x = %s, want f64", got)
	}
	sameWidthWarnings(t, rep.WidthWarnings, imp.WidthWarnings)
	targetRowsAreSource(t, target, rows)
	if !bytes.Equal(kept, want) {
		t.Error("kept cohort differs from the import")
	}
}

// TestImportWiden_PackedBoolStaysRowError: no type holds a boolean and an
// arbitrary value losslessly, so a non-boolean in a sample-inferred
// packed_bool column is a row error with no promotion — on import, and
// convert passes the cell through as text with no warning.
func TestImportWiden_PackedBoolStaysRowError(t *testing.T) {
	var rows [][]string
	for i := 0; i < 600; i++ {
		rows = append(rows, []string{fmt.Sprint(i%2 == 0)})
	}
	rows = append(rows, []string{"maybe"})
	cols := []string{"b"}
	rep, _ := importRaw(t, cols, rows)
	if rep.Schema.Fields[0].Type != encoding.FieldTypePackedBool || len(rep.WidthWarnings) != 0 {
		t.Fatalf("b = %s warnings %v, want packed_bool and none", rep.Schema.Fields[0].Type, rep.WidthWarnings)
	}
	if len(rep.RowErrors) != 1 || rep.RowErrors[0].Row != 601 {
		t.Errorf("row errors %v, want exactly row 601", rep.RowErrors)
	}
	crep, target, _ := runConvert(t, newMockReader(cols, rows), false)
	if crep.Schema.Fields[0].Type != encoding.FieldTypePackedBool || len(crep.WidthWarnings) != 0 {
		t.Errorf("convert b = %s warnings %v", crep.Schema.Fields[0].Type, crep.WidthWarnings)
	}
	targetRowsAreSource(t, target, rows)
}

// TestConvertOrWiden_F32Edges: an inferred f32 promotes on exactly the
// values fitsF32 would have refused into the sample — including the ones
// that parse at f32 without error by rounding to an edge (zero, the
// smallest subnormal, MaxFloat32, Inf) — and on nothing else.
func TestConvertOrWiden_F32Edges(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		promote bool
	}{
		{"1.5", false}, {"0", false}, {"-0", false}, {"0.1", false}, {"NaN", false},
		{"1.401298464324817e-45", false}, {"3.4028234663852886e38", false},
		{"1e-45", true}, {"1e-50", true}, {"-1e-50", true},
		{"3.4028235e38", true}, {"1e39", true}, {"Inf", true}, {"-Inf", true},
	} {
		schema := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF32}}}
		_, steps, err := convertOrWiden(schema, 0, tc.raw, nil, "", true, 7, nil)
		if err != nil {
			t.Errorf("%s: %v", tc.raw, err)
			continue
		}
		if got := len(steps) > 0; got != tc.promote || got != !fitsF32([]string{tc.raw}) {
			t.Errorf("%s: promoted %v, want %v (fitsF32 %v)", tc.raw, got, tc.promote, fitsF32([]string{tc.raw}))
		}
		if tc.promote && schema.Fields[0].Type != encoding.FieldTypeF64 {
			t.Errorf("%s: type %s, want f64", tc.raw, schema.Fields[0].Type)
		}
	}
}
