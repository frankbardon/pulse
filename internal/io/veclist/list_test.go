package veclist

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	pio "github.com/frankbardon/pulse/internal/io"
	parrow "github.com/frankbardon/pulse/internal/io/arrow"
	pcsv "github.com/frankbardon/pulse/internal/io/csv"
	pjsonarray "github.com/frankbardon/pulse/internal/io/jsonarray"
	pndjson "github.com/frankbardon/pulse/internal/io/ndjson"
	pparquet "github.com/frankbardon/pulse/internal/io/parquet"
)

// ---------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------

// row is one source record: an id and a list cell. A nil list is a null
// cell; a nil element a null element.
type row struct {
	id   int64
	list []any
	null bool
}

// fixture: dim 3, one NaN element, one null list, one list of nulls only.
func fixture() []row {
	return []row{
		{id: 1, list: []any{1.0, 2.0, 3.0}},
		{id: 2, list: []any{4.5, nil, 6.0}},
		{id: 3, null: true},
		{id: 4, list: []any{nil, nil, nil}},
		{id: 5, list: []any{7.0, 8.0, 9.25}},
	}
}

// source builds one format's reader over rows.
type source struct {
	name string
	make func(t *testing.T, rows []row) pio.Reader
}

func sources() []source {
	return []source{
		{"arrow_fixed_size_list", func(t *testing.T, rows []row) pio.Reader {
			return parrow.NewReaderFromBytes(arrowBytes(t, rows, true, arrow.PrimitiveTypes.Float64))
		}},
		{"arrow_list", func(t *testing.T, rows []row) pio.Reader {
			return parrow.NewReaderFromBytes(arrowBytes(t, rows, false, arrow.PrimitiveTypes.Float64))
		}},
		{"parquet_list", func(t *testing.T, rows []row) pio.Reader {
			return pparquet.NewReaderFromBytes(parquetBytes(t, rows))
		}},
		{"ndjson_array", func(t *testing.T, rows []row) pio.Reader {
			return pndjson.NewReaderFromBytes([]byte(jsonLines(t, rows, "\n")))
		}},
		{"json_array_file", func(t *testing.T, rows []row) pio.Reader {
			return pjsonarray.NewReaderFromBytes([]byte("[" + jsonLines(t, rows, ",") + "]"))
		}},
	}
}

// record builds an Arrow record {id int64, v <list of elem>}; fixed
// picks FixedSizeList (length of the first non-null list) over LIST.
func record(t *testing.T, rows []row, fixed bool, elem arrow.DataType) arrow.RecordBatch {
	t.Helper()
	mem := memory.NewGoAllocator()
	var listType arrow.DataType = arrow.ListOf(elem)
	if fixed {
		n := 0
		for _, r := range rows {
			if !r.null {
				n = len(r.list)
				break
			}
		}
		listType = arrow.FixedSizeListOf(int32(n), elem)
	}
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "id", Type: arrow.PrimitiveTypes.Int64},
		{Name: "v", Type: listType, Nullable: true},
	}, nil)
	b := array.NewRecordBuilder(mem, sc)
	defer b.Release()
	ids := b.Field(0).(*array.Int64Builder)
	type lb interface {
		array.Builder
		ValueBuilder() array.Builder
	}
	var vals lb
	var appendList func()
	switch x := b.Field(1).(type) {
	case *array.FixedSizeListBuilder:
		vals, appendList = x, func() { x.Append(true) }
	case *array.ListBuilder:
		vals, appendList = x, func() { x.Append(true) }
	}
	for _, r := range rows {
		ids.Append(r.id)
		if r.null {
			vals.AppendNull()
			continue
		}
		appendList()
		for _, e := range r.list {
			eb := vals.ValueBuilder()
			if e == nil {
				eb.AppendNull()
				continue
			}
			switch eb := eb.(type) {
			case *array.Float64Builder:
				eb.Append(e.(float64))
			case *array.Float32Builder:
				eb.Append(float32(e.(float64)))
			case *array.Int32Builder:
				eb.Append(int32(e.(float64)))
			case *array.StringBuilder:
				eb.Append(e.(string))
			default:
				t.Fatalf("element builder %T", eb)
			}
		}
	}
	return b.NewRecordBatch()
}

func arrowBytes(t *testing.T, rows []row, fixed bool, elem arrow.DataType) []byte {
	t.Helper()
	rec := record(t, rows, fixed, elem)
	defer rec.Release()
	var buf bytes.Buffer
	fw, err := ipc.NewFileWriter(&buf, ipc.WithSchema(rec.Schema()))
	if err != nil {
		t.Fatal(err)
	}
	if err := fw.Write(rec); err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func parquetBytes(t *testing.T, rows []row) []byte {
	t.Helper()
	rec := record(t, rows, false, arrow.PrimitiveTypes.Float64)
	defer rec.Release()
	tbl := array.NewTableFromRecords(rec.Schema(), []arrow.RecordBatch{rec})
	defer tbl.Release()
	var buf bytes.Buffer
	if err := pqarrow.WriteTable(tbl, &buf, 2, nil, pqarrow.DefaultWriterProps()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// jsonLines renders rows as JSON objects joined by sep.
func jsonLines(t *testing.T, rows []row, sep string) string {
	t.Helper()
	parts := make([]string, len(rows))
	for i, r := range rows {
		var v any
		if !r.null {
			v = r.list
		}
		b, err := json.Marshal(map[string]any{"id": r.id, "v": v})
		if err != nil {
			t.Fatal(err)
		}
		// json.Marshal sorts keys; the header is the first object's key
		// order, so "id" leads — as in every other source.
		parts[i] = string(b)
	}
	return strings.Join(parts, sep)
}

// decoded is a cohort read back: its schema, each record's vector value
// (nil when null) and its id.
type decoded struct {
	schema *encoding.Schema
	raw    []byte
	vecs   [][]float64
	nulls  []bool
}

func importRows(t *testing.T, rd pio.Reader, decls []string, kinds []string) (*decoded, *pio.ImportReport, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	job := pio.NewImportJob(rd, "out.pulse")
	job.FS = fs
	for _, s := range decls {
		d, err := pio.ParseVectorDecl(s)
		if err != nil {
			t.Fatal(err)
		}
		job.Vectors = append(job.Vectors, d)
	}
	if err := pio.ApplyVectorKinds(job.Vectors, kinds); err != nil {
		t.Fatal(err)
	}
	rep, err := job.Run(context.Background())
	if err != nil {
		return nil, nil, err
	}
	raw, err := afero.ReadFile(fs, "out.pulse")
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(raw)
	ver, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := encoding.ReadSchema(r, ver)
	if err != nil {
		t.Fatal(err)
	}
	out := &decoded{schema: s, raw: raw}
	rr := encx.NewRecordReader(r, s)
	for {
		vm, nm, wm := map[string]float64{}, map[string]bool{}, map[string]any{}
		if err := rr.ReadRecordWithWide(vm, nm, wm); err != nil {
			break
		}
		v, _ := wm["v"].([]float64)
		out.vecs = append(out.vecs, v)
		out.nulls = append(out.nulls, nm["v"])
	}
	return out, rep, nil
}

func reason(err error) string {
	var ce *errors.CodedError
	if !errorsAs(err, &ce) {
		return ""
	}
	r, _ := ce.Details["reason"].(string)
	return r
}

func errorsAs(err error, target **errors.CodedError) bool {
	for err != nil {
		if ce, ok := err.(*errors.CodedError); ok {
			*target = ce
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ---------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------

// TestListColumn_ImportsAsVector: per format and per element target, a
// constant-length numeric list column declared `--vector v[:f32]`
// becomes ONE vec field of dim 3 at the column's position, with no
// labels; a null list and a list of nulls only are null vectors, a null
// element is NaN; --vector-kind applies.
func TestListColumn_ImportsAsVector(t *testing.T) {
	t.Parallel()

	for _, src := range sources() {
		for _, target := range []struct {
			decl string
			typ  encoding.FieldType
		}{{"v", encoding.FieldTypeVecF64}, {"v:f32", encoding.FieldTypeVecF32}} {
			t.Run(src.name+"/"+target.typ.String(), func(t *testing.T) {
				got, rep, err := importRows(t, src.make(t, fixture()), []string{target.decl}, []string{"v:measure"})
				if err != nil {
					t.Fatal(err)
				}
				if rep.RowsImported != 5 || len(rep.RowErrors) != 0 {
					t.Fatalf("rows %d, errors %v", rep.RowsImported, rep.RowErrors)
				}
				s := got.schema
				if len(s.Fields) != 2 || s.Fields[0].Name != "id" || s.Fields[1].Name != "v" {
					t.Fatalf("fields %+v", s.Fields)
				}
				if f := s.Fields[1]; f.Type != target.typ || !f.Nullable {
					t.Fatalf("v: type %s nullable %v", f.Type, f.Nullable)
				}
				meta, ok := s.Vector(1)
				if !ok || meta.Dim != 3 || meta.Labels != nil || meta.Kind != encoding.VectorKindMeasure {
					t.Fatalf("vector meta %+v", meta)
				}
				if !reflect.DeepEqual(got.vecs[0], []float64{1, 2, 3}) || !reflect.DeepEqual(got.vecs[4], []float64{7, 8, 9.25}) {
					t.Fatalf("values %v", got.vecs)
				}
				if v := got.vecs[1]; got.nulls[1] || v[0] != 4.5 || !math.IsNaN(v[1]) || v[2] != 6 {
					t.Fatalf("null element row = %v (null %v), want [4.5 NaN 6]", v, got.nulls[1])
				}
				if !got.nulls[2] {
					t.Fatal("a null list is not a null vector")
				}
				if !got.nulls[3] {
					t.Fatal("a list of nulls only is not a null vector")
				}
			})
		}
	}
}

// stripLists hides every optional interface but the ones a list source
// had before the list channel existed.
type stripLists struct{ inner pio.Reader }

func (s *stripLists) ReadHeader() ([]string, error) { return s.inner.ReadHeader() }
func (s *stripLists) ReadRows(ctx context.Context, fn func([]string) error) error {
	return s.inner.ReadRows(ctx, fn)
}
func (s *stripLists) Close() error     { return s.inner.Close() }
func (s *stripLists) Reset() error     { return s.inner.(pio.ResetReader).Reset() }
func (s *stripLists) RowNulls() []bool { return s.inner.(pio.NullAwareReader).RowNulls() }

// TestListColumn_WithoutFlagUnchanged: with no vector declaration a list
// column still imports through the set_* ladder, byte-identically to
// the same source with its list channel hidden.
func TestListColumn_WithoutFlagUnchanged(t *testing.T) {
	t.Parallel()

	for _, src := range sources() {
		t.Run(src.name, func(t *testing.T) {
			t.Parallel()

			plain, _, err := importRows(t, src.make(t, fixture()), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := src.make(t, fixture()).(pio.ListAwareReader); !ok {
				t.Fatal("source does not implement ListAwareReader")
			}
			hidden, _, err := importRows(t, &stripLists{inner: src.make(t, fixture())}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(plain.raw, hidden.raw) {
				t.Fatal("an undeclared list column imports differently with the list channel present")
			}
			if f := plain.schema.Fields[1]; !f.Type.IsSet() || plain.schema.HasVectors() {
				t.Fatalf("v without --vector = %s, want the set ladder", f.Type)
			}
		})
	}
}

// TestListColumn_Refusals: every list a vector cannot hold refuses the
// whole import with PULSE_IMPORT_VECTOR_INVALID and its reason, whether
// the probe (first non-null list) or the row pass (a later row) meets
// it.
func TestListColumn_Refusals(t *testing.T) {
	t.Parallel()

	long := make([]any, encoding.MaxVectorDim+1)
	for i := range long {
		long[i] = float64(i)
	}
	jsonSrc := sources()[3] // ndjson
	arrSrc := sources()[4]  // json array file
	cases := []struct {
		name string
		src  source
		rows []row
		decl string
		want string
	}{
		{"variable length (later row)", jsonSrc, []row{{id: 1, list: []any{1.0, 2.0}}, {id: 2, list: []any{1.0, 2.0, 3.0}}}, "v", "list_length_varies"},
		{"variable length (json array)", arrSrc, []row{{id: 1, list: []any{1.0, 2.0}}, {id: 2, list: []any{1.0}}}, "v", "list_length_varies"},
		{"string element", jsonSrc, []row{{id: 1, list: []any{1.0, "3"}}}, "v", "element_not_numeric"},
		{"string element later row", arrSrc, []row{{id: 1, list: []any{1.0, 2.0}}, {id: 2, list: []any{1.0, "x"}}}, "v", "element_not_numeric"},
		{"bool element", jsonSrc, []row{{id: 1, list: []any{1.0, true}}}, "v", "element_not_numeric"},
		{"scalar cell", jsonSrc, []row{{id: 1, list: []any{1.0, 2.0}}, {id: 2, list: nil}}, "v", ""},
		{"dim 1", jsonSrc, []row{{id: 1, list: []any{1.0}}}, "v", "dim_out_of_range"},
		{"dim 0", arrSrc, []row{{id: 1, list: []any{}}}, "v", "dim_out_of_range"},
		{"dim past max", jsonSrc, []row{{id: 1, list: long}}, "v", "dim_out_of_range"},
		{"all null column", jsonSrc, []row{{id: 1, null: true}, {id: 2, null: true}}, "v", "list_dim_unknown"},
		{"unknown column", jsonSrc, fixture(), "w", "column_unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var rd pio.Reader
			if c.name == "scalar cell" {
				rd = pndjson.NewReaderFromBytes([]byte(`{"id":1,"v":[1,2]}` + "\n" + `{"id":2,"v":5}`))
				c.want = "not_a_list"
			} else {
				rd = c.src.make(t, c.rows)
			}
			_, _, err := importRows(t, rd, []string{c.decl}, nil)
			if !errors.HasCode(err, errors.PULSE_IMPORT_VECTOR_INVALID) || reason(err) != c.want {
				t.Fatalf("err = %v (reason %q), want PULSE_IMPORT_VECTOR_INVALID %s", err, reason(err), c.want)
			}
		})
	}
}

// TestListColumn_ArrowRefusals: the Arrow side of the refusals — a
// LIST<utf8> column is non-numeric, a variable-length LIST varies.
func TestListColumn_ArrowRefusals(t *testing.T) {
	t.Parallel()

	str := []row{{id: 1, list: []any{"a", "b"}}}
	var rd pio.Reader = parrow.NewReaderFromBytes(arrowBytes(t, str, false, arrow.BinaryTypes.String))
	if _, _, err := importRows(t, rd, []string{"v"}, nil); reason(err) != "element_not_numeric" {
		t.Fatalf("LIST<utf8>: err = %v", err)
	}
	varied := []row{{id: 1, list: []any{1.0, 2.0}}, {id: 2, list: []any{1.0, 2.0, 3.0}}}
	rd = parrow.NewReaderFromBytes(arrowBytes(t, varied, false, arrow.PrimitiveTypes.Float64))
	if _, _, err := importRows(t, rd, []string{"v"}, nil); reason(err) != "list_length_varies" {
		t.Fatalf("variable LIST: err = %v", err)
	}
	rd = pparquet.NewReaderFromBytes(parquetBytes(t, varied))
	if _, _, err := importRows(t, rd, []string{"v"}, nil); reason(err) != "list_length_varies" {
		t.Fatalf("variable parquet LIST: err = %v", err)
	}
	// An integer child is numeric.
	ints := []row{{id: 1, list: []any{1.0, 2.0}}, {id: 2, list: []any{3.0, nil}}}
	rd = parrow.NewReaderFromBytes(arrowBytes(t, ints, true, arrow.PrimitiveTypes.Int32))
	got, _, err := importRows(t, rd, []string{"v"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.vecs[0], []float64{1, 2}) || got.vecs[1][0] != 3 || !math.IsNaN(got.vecs[1][1]) {
		t.Fatalf("int list = %v", got.vecs)
	}
}

// TestListColumn_TextSourceRefused: a source with no typed lists keeps
// the no_columns refusal.
func TestListColumn_TextSourceRefused(t *testing.T) {
	t.Parallel()

	rd := pcsv.NewReaderFromBytes([]byte("id,v\n1,1|2|3\n"))
	_, _, err := importRows(t, rd, []string{"v"}, nil)
	if !errors.HasCode(err, errors.PULSE_IMPORT_VECTOR_INVALID) || reason(err) != "no_columns" {
		t.Fatalf("csv: err = %v", err)
	}
}

// TestListColumn_PredictMatchesRun: the predicted schema of a list
// import is the written one.
func TestListColumn_PredictMatchesRun(t *testing.T) {
	t.Parallel()

	src := sources()[0]
	got, _, err := importRows(t, src.make(t, fixture()), []string{"v"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	job := pio.NewImportJob(src.make(t, fixture()), "out.pulse")
	d, _ := pio.ParseVectorDecl("v")
	job.Vectors = []pio.VectorDecl{d}
	rep, err := job.Predict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep.Schema.Vectors, got.schema.Vectors) || rep.Schema.Fields[1].Type != encoding.FieldTypeVecF64 {
		t.Fatalf("predicted %+v / %+v", rep.Schema.Fields, rep.Schema.Vectors)
	}
}
