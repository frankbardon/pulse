package veclist

import (
	"bytes"
	"context"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/file"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	pio "github.com/frankbardon/pulse/internal/io"
	parrow "github.com/frankbardon/pulse/internal/io/arrow"
	pcsv "github.com/frankbardon/pulse/internal/io/csv"
	pexcel "github.com/frankbardon/pulse/internal/io/excel"
	pjsonarray "github.com/frankbardon/pulse/internal/io/jsonarray"
	pndjson "github.com/frankbardon/pulse/internal/io/ndjson"
	pparquet "github.com/frankbardon/pulse/internal/io/parquet"
	ptsv "github.com/frankbardon/pulse/internal/io/tsv"
)

// bufWriter is every buffer-backed adapter writer.
type bufWriter interface {
	pio.Writer
	Bytes() []byte
}

// exportCohort exports raw cohort bytes through w and returns what it
// wrote, failing unless every record was exported.
func exportCohort(t *testing.T, raw []byte, w bufWriter, records int) []byte {
	t.Helper()
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "in.pulse", raw, 0o644); err != nil {
		t.Fatal(err)
	}
	job := pio.NewExportJob("in.pulse", w)
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if rep.RowsExported != records || len(rep.RowErrors) != 0 {
		t.Fatalf("exported %d rows (%d errors), want %d", rep.RowsExported, len(rep.RowErrors), records)
	}
	return w.Bytes()
}

// siblingCSV is a battery of three f64 columns between two scalars. The
// element text is FormatFloat 'f' -1 — the export's own rendering — so the
// expected export is the source byte for byte. Row 2 has a blank element
// (NaN), row 3 is all blank (a null vector).
func siblingCSV(sep string) string {
	vals := []float64{0.1, 1.0 / 3, -2.5e-12, 123456789.123456789, math.MaxFloat64 / 7, math.Copysign(0, -1), 6}
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	lines := []string{
		strings.Join([]string{"id", "rate_a", "rate_b", "rate_c", "z"}, sep),
		strings.Join([]string{"1", f(vals[0]), f(vals[1]), f(vals[2]), "x"}, sep),
		strings.Join([]string{"2", f(vals[3]), "", f(vals[4]), "y"}, sep),
		strings.Join([]string{"3", "", "", "", "x"}, sep),
		strings.Join([]string{"4", f(vals[5]), f(vals[6]), f(vals[0]), "y"}, sep),
	}
	return strings.Join(lines, "\n") + "\n"
}

// TestVectorExport_SiblingColumnsRoundTrip: CSV / TSV columns folded with
// `--vector rate:/^rate_(.*)$/` export back to exactly the source text —
// `rate_<label>` in the vector's place, a NaN element and a null vector
// as empty cells — and re-importing that export with the same declaration
// writes a byte-identical cohort.
func TestVectorExport_SiblingColumnsRoundTrip(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		sep    string
		reader func([]byte) pio.Reader
		writer func() bufWriter
	}{
		{"csv", ",", func(b []byte) pio.Reader { return pcsv.NewReaderFromBytes(b) }, func() bufWriter { return pcsv.NewWriterToBuffer() }},
		{"tsv", "\t", func(b []byte) pio.Reader { return ptsv.NewReaderFromBytes(b) }, func() bufWriter { return ptsv.NewWriterToBuffer() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src := siblingCSV(tc.sep)
			decl := []string{"rate:/^rate_(.*)$/"}
			first := importNamed(t, tc.reader([]byte(src)), decl, "rate")
			if !first.nulls[2] || !math.IsNaN(first.vecs[1][1]) {
				t.Fatalf("fixture did not import a null vector and a NaN element: %v %v", first.vecs, first.nulls)
			}
			out := exportCohort(t, first.raw, tc.writer(), 4)
			if string(out) != src {
				t.Fatalf("export is not the source:\n got %q\nwant %q", out, src)
			}
			again := importNamed(t, tc.reader(out), decl, "rate")
			if !bytes.Equal(again.raw, first.raw) {
				t.Fatal("re-importing the export did not write a byte-identical cohort")
			}
		})
	}
}

// importNamed imports with the declarations and decodes vector field
// `name` (importRows decodes the list fixtures' "v").
func importNamed(t *testing.T, rd pio.Reader, decls []string, name string) *decoded {
	t.Helper()
	d, _, err := importRows(t, rd, decls, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(d.raw)
	ver, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := encoding.ReadSchema(r, ver)
	if err != nil {
		t.Fatal(err)
	}
	out := &decoded{schema: s, raw: d.raw}
	rr := encx.NewRecordReader(r, s)
	for {
		vm, nm, wm := map[string]float64{}, map[string]bool{}, map[string]any{}
		if err := rr.ReadRecordWithWide(vm, nm, wm); err != nil {
			break
		}
		v, _ := wm[name].([]float64)
		out.vecs = append(out.vecs, v)
		out.nulls = append(out.nulls, nm[name])
	}
	return out
}

// TestVectorExport_ExcelExpands: an Excel export expands like CSV and
// re-imports, folded the same way, to the same vectors.
func TestVectorExport_ExcelExpands(t *testing.T) {
	t.Parallel()

	first := importNamed(t, pcsv.NewReaderFromBytes([]byte(siblingCSV(","))), []string{"rate:/^rate_(.*)$/"}, "rate")
	out := exportCohort(t, first.raw, pexcel.NewWriterToBuffer(), 4)
	rd := pexcel.NewReaderFromBytes(out)
	header, err := rd.ReadHeader()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(header, ","); got != "id,rate_a,rate_b,rate_c,z" {
		t.Fatalf("excel header %s", got)
	}
	again := importNamed(t, pexcel.NewReaderFromBytes(out), []string{"rate:/^rate_(.*)$/"}, "rate")
	sameVectors(t, again, first)
}

// sameVectors compares two decoded cohorts' vectors bit for bit.
func sameVectors(t *testing.T, got, want *decoded) {
	t.Helper()
	if len(got.vecs) != len(want.vecs) {
		t.Fatalf("%d records, want %d", len(got.vecs), len(want.vecs))
	}
	for i := range want.vecs {
		if got.nulls[i] != want.nulls[i] {
			t.Fatalf("record %d: null %v, want %v", i, got.nulls[i], want.nulls[i])
		}
		if want.nulls[i] {
			continue
		}
		for e := range want.vecs[i] {
			if math.Float64bits(got.vecs[i][e]) != math.Float64bits(want.vecs[i][e]) &&
				!(math.IsNaN(got.vecs[i][e]) && math.IsNaN(want.vecs[i][e])) {
				t.Fatalf("record %d element %d: %v, want %v", i, e, got.vecs[i][e], want.vecs[i][e])
			}
		}
	}
}

// TestVectorExport_NativeListsRoundTrip: a list column imported with
// `--vector v[:f32]` exports as a native list — a JSON array (null for a
// null vector, null for a NaN element) or an Arrow / Parquet
// FixedSizeList<float32|float64, dim> — and re-importing that export
// under the same declaration writes a byte-identical cohort.
func TestVectorExport_NativeListsRoundTrip(t *testing.T) {
	t.Parallel()

	src := sources()[0] // Arrow FixedSizeList<float64>
	for _, decl := range []string{"v", "v:f32"} {
		first, _, err := importRows(t, src.make(t, fixture()), []string{decl}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name   string
			writer func() bufWriter
			reader func([]byte) pio.Reader
			check  func(t *testing.T, out []byte)
		}{
			{"ndjson", func() bufWriter { return pndjson.NewWriterToBuffer() },
				func(b []byte) pio.Reader { return pndjson.NewReaderFromBytes(b) },
				func(t *testing.T, out []byte) {
					want := `{"id":1,"v":[1,2,3]}` + "\n" + `{"id":2,"v":[4.5,null,6]}` + "\n" + `{"id":3,"v":null}` + "\n"
					if !strings.HasPrefix(string(out), want) {
						t.Fatalf("ndjson export:\n%s", out)
					}
				}},
			{"jsonarray", func() bufWriter { return pjsonarray.NewWriterToBuffer() },
				func(b []byte) pio.Reader { return pjsonarray.NewReaderFromBytes(b) },
				func(t *testing.T, out []byte) {
					if !strings.Contains(string(out), `{"id":5,"v":[7,8,9.25]}`) {
						t.Fatalf("json array export:\n%s", out)
					}
				}},
			{"arrow", func() bufWriter { return parrow.NewWriterToBuffer() },
				func(b []byte) pio.Reader { return parrow.NewReaderFromBytes(b) },
				func(t *testing.T, out []byte) {
					rd, err := ipc.NewFileReader(bytes.NewReader(out), ipc.WithAllocator(memory.NewGoAllocator()))
					if err != nil {
						t.Fatal(err)
					}
					defer rd.Close()
					checkVectorList(t, rd.Schema(), decl, true)
				}},
			{"parquet", func() bufWriter { return pparquet.NewWriterToBuffer() },
				func(b []byte) pio.Reader { return pparquet.NewReaderFromBytes(b) },
				func(t *testing.T, out []byte) {
					pf, err := file.NewParquetReader(bytes.NewReader(out))
					if err != nil {
						t.Fatal(err)
					}
					defer pf.Close()
					fr, err := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{}, memory.NewGoAllocator())
					if err != nil {
						t.Fatal(err)
					}
					sc, err := fr.Schema()
					if err != nil {
						t.Fatal(err)
					}
					checkVectorList(t, sc, decl, false)
				}},
		} {
			t.Run(tc.name+"/"+decl, func(t *testing.T) {
				out := exportCohort(t, first.raw, tc.writer(), 5)
				tc.check(t, out)
				again, _, err := importRows(t, tc.reader(out), []string{decl}, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(again.raw, first.raw) {
					sameVectors(t, again, first)
					t.Fatal("re-importing the export did not write a byte-identical cohort")
				}
			})
		}
	}
}

// checkVectorList asserts column v is FixedSizeList<elem, 3> (fixed) or
// LIST<elem>.
func checkVectorList(t *testing.T, sc *arrow.Schema, decl string, fixed bool) {
	t.Helper()
	idx := sc.FieldIndices("v")
	if len(idx) != 1 {
		t.Fatalf("no column v in %s", sc)
	}
	var elem arrow.DataType
	switch lt := sc.Field(idx[0]).Type.(type) {
	case *arrow.FixedSizeListType:
		if !fixed || lt.Len() != 3 {
			t.Fatalf("v is %s, want fixed=%v of 3", lt, fixed)
		}
		elem = lt.Elem()
	case *arrow.ListType:
		if fixed {
			t.Fatalf("v is %s, want FixedSizeList of 3", lt)
		}
		elem = lt.Elem()
	default:
		t.Fatalf("v is %s, not a list", sc.Field(idx[0]).Type)
	}
	want := arrow.FLOAT64
	if strings.HasSuffix(decl, ":f32") {
		want = arrow.FLOAT32
	}
	if elem.ID() != want {
		t.Fatalf("v elements are %s, want %s", elem, want)
	}
}

// TestVectorExport_ParquetFixedSizeList: a NON-nullable vector goes to
// Parquet as FixedSizeList<elem, dim> (a nullable one is a LIST — pqarrow
// cannot write a null fixed-size list) and re-imports byte-identically.
func TestVectorExport_ParquetFixedSizeList(t *testing.T) {
	t.Parallel()

	rows := []row{
		{id: 1, list: []any{1.0, 2.0, 3.0}},
		{id: 2, list: []any{4.5, 0.1, 6.0}},
	}
	for _, decl := range []string{"v", "v:f32"} {
		first, _, err := importRows(t, sources()[0].make(t, rows), []string{decl}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if first.schema.Fields[1].Nullable {
			t.Fatal("fixture vector imported nullable")
		}
		out := exportCohort(t, first.raw, pparquet.NewWriterToBuffer(), 2)
		pf, err := file.NewParquetReader(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		fr, err := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{}, memory.NewGoAllocator())
		if err != nil {
			t.Fatal(err)
		}
		sc, err := fr.Schema()
		if err != nil {
			t.Fatal(err)
		}
		checkVectorList(t, sc, decl, true)
		_ = pf.Close()
		again, _, err := importRows(t, pparquet.NewReaderFromBytes(out), []string{decl}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again.raw, first.raw) {
			t.Fatalf("%s: re-import is not byte-identical", decl)
		}
	}
}
