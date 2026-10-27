package io

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// vecFixture is a wide survey source: id, five rating columns, region.
// Row 3 has every rating empty (a null vector), row 4 two of them (NaN
// elements).
func vecFixture() ([]string, [][]string) {
	cols := []string{"id", "rate_a", "rate_b", "rate_c", "rate_d", "rate_e", "region"}
	rows := [][]string{
		{"1", "1", "2", "3", "4", "5", "north"},
		{"2", "7", "6", "5", "4", "3", "south"},
		{"3", "", "", "", "", "", "north"},
		{"4", "2", "", "4", "", "6", "east"},
		{"5", "1.5", "2.5", "3.5", "4.5", "5.5", "west"},
	}
	return cols, rows
}

// readVecCohort decodes every record of the cohort at path: the schema,
// each record's vector values (wide map) and nulls.
func readVecCohort(t *testing.T, fs afero.Fs, path string) (*encoding.Schema, []map[string]any, []map[string]bool, []map[string]float64) {
	t.Helper()
	raw, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(raw)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatal(err)
	}
	rr := encx.NewRecordReader(r, s)
	var wides []map[string]any
	var nulls []map[string]bool
	var vals []map[string]float64
	for {
		vm, nm, wm := map[string]float64{}, map[string]bool{}, map[string]any{}
		if err := rr.ReadRecordWithWide(vm, nm, wm); err != nil {
			break
		}
		wides, nulls, vals = append(wides, wm), append(nulls, nm), append(vals, vm)
	}
	return s, wides, nulls, vals
}

func runVecImport(t *testing.T, cols []string, rows [][]string, mutate func(*ImportJob)) (*ImportReport, afero.Fs, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	job := NewImportJob(newMockReader(cols, rows), "out.pulse")
	job.FS = fs
	mutate(job)
	rep, err := job.Run(context.Background())
	return rep, fs, err
}

func mustDecl(t *testing.T, s string) VectorDecl {
	t.Helper()
	d, err := ParseVectorDecl(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestParseVectorDecl(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want VectorDecl
	}{
		{"v:a,b,c", VectorDecl{Name: "v", Columns: []string{"a", "b", "c"}}},
		{" v : a , b ", VectorDecl{Name: "v", Columns: []string{"a", "b"}}},
		{"v:/^rate_(.*)$/", VectorDecl{Name: "v", Pattern: "^rate_(.*)$"}},
		{"v:/a:b,c/", VectorDecl{Name: "v", Pattern: "a:b,c"}},
		{"v:f32:a,b", VectorDecl{Name: "v", Element: "f32", Columns: []string{"a", "b"}}},
		{"v:f64:/x/", VectorDecl{Name: "v", Element: "f64", Pattern: "x"}},
		{"v", VectorDecl{Name: "v"}},
		{"v:f32", VectorDecl{Name: "v", Element: "f32"}},
	}
	for _, c := range cases {
		got, err := ParseVectorDecl(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%q: got %+v, want %+v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", ":a,b", "v:a,,b", "v://", "v:"} {
		_, err := ParseVectorDecl(bad)
		if !errors.HasCode(err, errors.PULSE_IMPORT_VECTOR_INVALID) || vecReason(err) != "malformed" {
			t.Fatalf("%q: err = %v, want PULSE_IMPORT_VECTOR_INVALID malformed", bad, err)
		}
	}
}

func vecReason(err error) string {
	ce, ok := err.(*errors.CodedError)
	if !ok {
		return ""
	}
	r, _ := ce.Details["reason"].(string)
	return r
}

func TestApplyVectorKinds(t *testing.T) {
	t.Parallel()

	decls := []VectorDecl{{Name: "v", Columns: []string{"a", "b"}}, {Name: "w", Columns: []string{"c", "d"}}}
	if err := ApplyVectorKinds(decls, []string{"v:scale:1:7", "w:composition"}); err != nil {
		t.Fatal(err)
	}
	if decls[0].Kind != "scale" || *decls[0].ScaleMin != 1 || *decls[0].ScaleMax != 7 || decls[1].Kind != "composition" || decls[1].ScaleMin != nil {
		t.Fatalf("kinds not applied: %+v", decls)
	}
	for _, c := range []struct{ in, reason string }{
		{"x:scale", "kind_unknown_vector"},
		{"v:ordinal", "kind_invalid"},
		{"v:scale:7:1", "scale_invalid"},
		{"v:scale:a:7", "scale_invalid"},
		{"v:scale:1", "malformed"},
		{"v", "malformed"},
	} {
		d := []VectorDecl{{Name: "v", Columns: []string{"a", "b"}}}
		err := ApplyVectorKinds(d, []string{c.in})
		if !errors.HasCode(err, errors.PULSE_IMPORT_VECTOR_INVALID) || vecReason(err) != c.reason {
			t.Fatalf("%q: err = %v (reason %q), want %s", c.in, err, vecReason(err), c.reason)
		}
	}
	d := []VectorDecl{{Name: "v", Columns: []string{"a", "b"}}}
	if err := ApplyVectorKinds(d, []string{"v:scale", "v:measure"}); vecReason(err) != "kind_duplicate" {
		t.Fatalf("duplicate kind: err = %v", err)
	}
}

// TestImportJob_VectorFoldRegex is the core fold: the regex members
// become one vec_f64 at the first member's position, labels from the
// capture group, the kind as declared; all-empty is a null vector (the
// inferred field promoted to nullable), some-empty NaN elements.
func TestImportJob_VectorFoldRegex(t *testing.T) {
	t.Parallel()

	cols, rows := vecFixture()
	decl := mustDecl(t, "brand_rating:/^rate_(.*)$/")
	decls := []VectorDecl{decl}
	if err := ApplyVectorKinds(decls, []string{"brand_rating:scale:1:7"}); err != nil {
		t.Fatal(err)
	}
	rep, fs, err := runVecImport(t, cols, rows, func(j *ImportJob) { j.Vectors = decls })
	if err != nil {
		t.Fatal(err)
	}
	if rep.RowsImported != 5 || len(rep.RowErrors) != 0 {
		t.Fatalf("rows %d errors %v", rep.RowsImported, rep.RowErrors)
	}
	s, wides, nulls, _ := readVecCohort(t, fs, "out.pulse")
	var names []string
	for _, f := range s.Fields {
		names = append(names, f.Name)
	}
	if !reflect.DeepEqual(names, []string{"id", "brand_rating", "region"}) {
		t.Fatalf("fields = %v", names)
	}
	f := s.Fields[1]
	if f.Type != encoding.FieldTypeVecF64 || !f.Nullable {
		t.Fatalf("brand_rating: type %s nullable %v", f.Type, f.Nullable)
	}
	v, ok := s.Vector(1)
	if !ok || v.Dim != 5 || !reflect.DeepEqual(v.Labels, []string{"a", "b", "c", "d", "e"}) ||
		v.Kind != encoding.VectorKindScale || !v.HasScale || v.ScaleMin != 1 || v.ScaleMax != 7 {
		t.Fatalf("vector meta = %+v", v)
	}
	if len(wides) != 5 {
		t.Fatalf("decoded %d records", len(wides))
	}
	if got := wides[1]["brand_rating"].([]float64); !reflect.DeepEqual(got, []float64{7, 6, 5, 4, 3}) {
		t.Fatalf("row 2 = %v", got)
	}
	if !nulls[2]["brand_rating"] {
		t.Fatal("all-empty row is not a null vector")
	}
	got := wides[3]["brand_rating"].([]float64)
	if got[0] != 2 || !math.IsNaN(got[1]) || got[2] != 4 || !math.IsNaN(got[3]) || got[4] != 6 || nulls[3]["brand_rating"] {
		t.Fatalf("partial row = %v (null %v), want NaN elements", got, nulls[3]["brand_rating"])
	}
	if !reflect.DeepEqual(wides[4]["brand_rating"].([]float64), []float64{1.5, 2.5, 3.5, 4.5, 5.5}) {
		t.Fatalf("row 5 = %v", wides[4]["brand_rating"])
	}
	if rep.Schema == nil || !rep.Schema.HasVectors() {
		t.Fatal("report schema lost its vectors")
	}
}

// TestImportJob_VectorFoldListedOrder: a listed declaration keeps the
// LISTED order (labels are the full column names), the vector sits at
// its earliest member's source position, and f32 stores vec_f32.
func TestImportJob_VectorFoldListedOrder(t *testing.T) {
	t.Parallel()

	cols, rows := vecFixture()
	_, fs, err := runVecImport(t, cols, rows[:2], func(j *ImportJob) {
		j.Vectors = []VectorDecl{mustDecl(t, "pair:f32:rate_c,rate_a")}
	})
	if err != nil {
		t.Fatal(err)
	}
	s, wides, _, vals := readVecCohort(t, fs, "out.pulse")
	var names []string
	for _, f := range s.Fields {
		names = append(names, f.Name)
	}
	if !reflect.DeepEqual(names, []string{"id", "pair", "rate_b", "rate_d", "rate_e", "region"}) {
		t.Fatalf("fields = %v", names)
	}
	v, _ := s.Vector(1)
	if s.Fields[1].Type != encoding.FieldTypeVecF32 || !reflect.DeepEqual(v.Labels, []string{"rate_c", "rate_a"}) || v.Kind != encoding.VectorKindNone {
		t.Fatalf("pair: %s %+v", s.Fields[1].Type, v)
	}
	if got := wides[1]["pair"].([]float64); !reflect.DeepEqual(got, []float64{5, 7}) {
		t.Fatalf("row 2 pair = %v, want [5 7]", got)
	}
	if vals[1]["rate_b"] != 6 {
		t.Fatalf("scalar rate_b = %v", vals[1]["rate_b"])
	}
}

// TestImportJob_VectorNonNumericCellIsRowError: a non-numeric element
// fails its row under the ordinary PULSE_IMPORT_ROW_ERROR rule; the
// other rows import.
func TestImportJob_VectorNonNumericCellIsRowError(t *testing.T) {
	t.Parallel()

	cols, rows := vecFixture()
	rows[1][3] = "high"
	rep, _, err := runVecImport(t, cols, rows, func(j *ImportJob) {
		j.Vectors = []VectorDecl{mustDecl(t, "r:/^rate_/")}
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.RowsImported != 4 || len(rep.RowErrors) != 1 || rep.RowErrors[0].Row != 2 ||
		!errors.HasCode(rep.RowErrors[0].Err, errors.PULSE_IMPORT_ROW_ERROR) ||
		!strings.Contains(rep.RowErrors[0].Err.Error(), "rate_c") {
		t.Fatalf("rows %d, errors %+v", rep.RowsImported, rep.RowErrors)
	}
}

// TestImportJob_VectorRefusals: every import-time refusal carries
// PULSE_IMPORT_VECTOR_INVALID and its reason, before a row is read.
func TestImportJob_VectorRefusals(t *testing.T) {
	t.Parallel()

	cols, rows := vecFixture()
	cases := []struct {
		name   string
		decls  []string
		kinds  []string
		groups []GroupDecl
		reason string
	}{
		{"dim 1", []string{"v:rate_a"}, nil, nil, "dim_out_of_range"},
		{"no match", []string{"v:/^score_/"}, nil, nil, "pattern_no_match"},
		{"bad regex", []string{"v:/(/"}, nil, nil, "pattern_invalid"},
		{"unknown column", []string{"v:rate_a,rate_z"}, nil, nil, "column_unknown"},
		{"two vectors", []string{"v:rate_a,rate_b", "w:rate_b,rate_c"}, nil, nil, "column_claimed"},
		{"repeated in one", []string{"v:rate_a,rate_a"}, nil, nil, "column_claimed"},
		{"vector and group", []string{"v:/^rate_/"}, nil, []GroupDecl{{Members: []string{"region", "rate_e"}}}, "column_claimed"},
		{"name collision", []string{"region:rate_a,rate_b"}, nil, nil, "name_collision"},
		{"duplicate name", []string{"v:rate_a,rate_b", "v:rate_c,rate_d"}, nil, nil, "duplicate_name"},
		{"no columns", []string{"v"}, nil, nil, "no_columns"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var decls []VectorDecl
			for _, s := range c.decls {
				decls = append(decls, mustDecl(t, s))
			}
			for _, verb := range []string{"run", "predict"} {
				src := &countingRowsReader{mockReader: newMockReader(cols, rows)}
				job := NewImportJob(src, "out.pulse")
				job.FS = afero.NewMemMapFs()
				job.Vectors = decls
				job.Groups = c.groups
				var err error
				if verb == "run" {
					_, err = job.Run(context.Background())
				} else {
					_, err = job.Predict(context.Background())
				}
				if !errors.HasCode(err, errors.PULSE_IMPORT_VECTOR_INVALID) || vecReason(err) != c.reason {
					t.Fatalf("%s: err = %v (reason %q), want %s", verb, err, vecReason(err), c.reason)
				}
				if src.rowsRead != 0 {
					t.Fatalf("%s: read %d rows before refusing", verb, src.rowsRead)
				}
				if ok, _ := afero.Exists(job.FS, "out.pulse"); ok {
					t.Fatalf("%s: wrote a cohort", verb)
				}
			}
		})
	}
	// The dim ceiling: 1025 columns.
	wide := make([]string, 1025)
	for i := range wide {
		wide[i] = "c" + strconv.Itoa(i)
	}
	_, _, err := runVecImport(t, wide, nil, func(j *ImportJob) { j.Vectors = []VectorDecl{{Name: "v", Pattern: "^c"}} })
	if vecReason(err) != "dim_out_of_range" {
		t.Fatalf("1025 columns: err = %v", err)
	}
}

// countingRowsReader counts the rows its source hands out.
type countingRowsReader struct {
	*mockReader
	rowsRead int
}

func (c *countingRowsReader) ReadRows(ctx context.Context, fn func([]string) error) error {
	return c.mockReader.ReadRows(ctx, func(r []string) error {
		c.rowsRead++
		return fn(r)
	})
}

// TestImportJob_VectorAtMaxDim: a 1024-element f64 vector is 8 KiB per
// row — 256 times the wideFieldBytes scratch — and imports without
// truncation.
func TestImportJob_VectorAtMaxDim(t *testing.T) {
	t.Parallel()

	cols := []string{"id"}
	for i := 0; i < encoding.MaxVectorDim; i++ {
		cols = append(cols, fmt.Sprintf("e%04d", i))
	}
	var rows [][]string
	for r := 0; r < 3; r++ {
		row := []string{strconv.Itoa(r)}
		for i := 0; i < encoding.MaxVectorDim; i++ {
			row = append(row, strconv.FormatFloat(float64(r*10000+i)+0.25, 'f', -1, 64))
		}
		rows = append(rows, row)
	}
	_, fs, err := runVecImport(t, cols, rows, func(j *ImportJob) { j.Vectors = []VectorDecl{{Name: "emb", Pattern: "^e"}} })
	if err != nil {
		t.Fatal(err)
	}
	s, wides, _, _ := readVecCohort(t, fs, "out.pulse")
	if s.FieldWidth(1) != 8*encoding.MaxVectorDim {
		t.Fatalf("width %d", s.FieldWidth(1))
	}
	for r, w := range wides {
		got := w["emb"].([]float64)
		for i, v := range got {
			if v != float64(r*10000+i)+0.25 {
				t.Fatalf("row %d element %d = %v", r, i, v)
			}
		}
	}
}

// TestImportJob_VectorExplicitSchema: an explicit schema types the
// folded field vec_* with its dim; labels absent from the schema come
// from the fold, a declared kind wins. A vec field nobody folds, a dim
// mismatch and a folded name typed as a scalar are refused.
func TestImportJob_VectorExplicitSchema(t *testing.T) {
	t.Parallel()

	cols, rows := vecFixture()
	schema, err := ParseSchemaJSON([]byte(`[
		{"name":"id","type":"u8"},
		{"name":"r","type":"vec_f64","dim":5,"nullable":true,"kind":"measure"},
		{"name":"region","type":"categorical_u8"}]`))
	if err != nil {
		t.Fatal(err)
	}
	decls := []VectorDecl{mustDecl(t, "r:/^rate_(.)$/")}
	_ = ApplyVectorKinds(decls, []string{"r:scale:1:7"})
	_, fs, err := runVecImport(t, cols, rows, func(j *ImportJob) { j.Schema = schema; j.Vectors = decls })
	if err != nil {
		t.Fatal(err)
	}
	s, wides, nulls, _ := readVecCohort(t, fs, "out.pulse")
	v, _ := s.Vector(1)
	if v.Dim != 5 || !reflect.DeepEqual(v.Labels, []string{"a", "b", "c", "d", "e"}) || v.Kind != encoding.VectorKindScale || v.ScaleMax != 7 {
		t.Fatalf("meta %+v", v)
	}
	if !nulls[2]["r"] || wides[0]["r"].([]float64)[4] != 5 {
		t.Fatalf("values: %v / null %v", wides[0]["r"], nulls[2]["r"])
	}
	if len(schema.Vectors[0].Labels) != 0 {
		t.Fatal("caller's schema mutated")
	}

	for _, c := range []struct {
		name, doc, decl, reason string
	}{
		{"not declared", `[{"name":"id","type":"u8"},{"name":"rate_a","type":"vec_f64","dim":2}]`, "", "not_declared"},
		{"dim mismatch", `[{"name":"id","type":"u8"},{"name":"r","type":"vec_f64","dim":4,"nullable":true},{"name":"region","type":"categorical_u8"}]`, "r:/^rate_/", "dim_mismatch"},
		{"type mismatch", `[{"name":"id","type":"u8"},{"name":"r","type":"f64","nullable":true},{"name":"region","type":"categorical_u8"}]`, "r:/^rate_/", "type_mismatch"},
		{"element mismatch", `[{"name":"id","type":"u8"},{"name":"r","type":"vec_f64","dim":5,"nullable":true},{"name":"region","type":"categorical_u8"}]`, "r:f32:/^rate_/", "type_mismatch"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sc, err := ParseSchemaJSON([]byte(c.doc))
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = runVecImport(t, cols, rows, func(j *ImportJob) {
				j.Schema = sc
				j.IgnoreExtra = true
				if c.decl != "" {
					j.Vectors = []VectorDecl{mustDecl(t, c.decl)}
				}
			})
			if !errors.HasCode(err, errors.PULSE_IMPORT_VECTOR_INVALID) || vecReason(err) != c.reason {
				t.Fatalf("err = %v (reason %q), want %s", err, vecReason(err), c.reason)
			}
		})
	}
}

// TestImportJob_VectorOverrideRefused: a column type override to a vec
// type has no dim to give the column — refused before any row, on Run
// and Predict.
func TestImportJob_VectorOverrideRefused(t *testing.T) {
	t.Parallel()

	cols, rows := vecFixture()
	for _, verb := range []string{"run", "predict"} {
		job := NewImportJob(newMockReader(cols, rows), "out.pulse")
		job.FS = afero.NewMemMapFs()
		job.ColumnTypeOverrides = map[string]encoding.FieldType{"rate_a": encoding.FieldTypeVecF64}
		var err error
		if verb == "run" {
			_, err = job.Run(context.Background())
		} else {
			_, err = job.Predict(context.Background())
		}
		if !errors.HasCode(err, errors.PULSE_IMPORT_OVERRIDE_INVALID) || vecReason(err) != "vector_type" {
			t.Fatalf("%s: err = %v", verb, err)
		}
	}
}

// TestImportJob_VectorPredict: plain and measured predict report the
// schema Run writes, the vector's null promotion included.
func TestImportJob_VectorPredict(t *testing.T) {
	t.Parallel()

	cols, rows := vecFixture()
	_, fs, err := runVecImport(t, cols, rows, func(j *ImportJob) { j.Vectors = []VectorDecl{{Name: "r", Pattern: "^rate_"}} })
	if err != nil {
		t.Fatal(err)
	}
	written, _, _, _ := readVecCohort(t, fs, "out.pulse")
	for _, measured := range []bool{false, true} {
		job := NewImportJob(newMockReader(cols, rows), "out.pulse")
		job.SampleRows = 50
		job.Vectors = []VectorDecl{{Name: "r", Pattern: "^rate_"}}
		job.ElideConstants = measured
		rep, err := job.Predict(context.Background())
		if err != nil {
			t.Fatalf("measured=%v: %v", measured, err)
		}
		if !reflect.DeepEqual(rep.Schema.Vectors, written.Vectors) || rep.Schema.Fields[1].Type != encoding.FieldTypeVecF64 || !rep.Schema.Fields[1].Nullable {
			t.Fatalf("measured=%v: predicted %+v / %+v", measured, rep.Schema.Fields[1], rep.Schema.Vectors)
		}
		if measured {
			// The measured pass converts every row exactly as Run does —
			// the vector elements included — and sizes the same file.
			raw, _ := afero.ReadFile(fs, "out.pulse")
			if pr := rep.Projection; pr == nil || pr.RowsImported != 5 || pr.RowErrors != 0 || pr.FlatFileBytes != int64(len(raw)) {
				t.Fatalf("measured projection %+v, want 5 rows, 0 errors, %d bytes", rep.Projection, len(raw))
			}
		}
	}
}

// nullAwareMock adds a declared-null channel to mockReader: every cell
// equal to "<null>" is declared null and handed over as the text "0" —
// a stale value under a validity bit — so only the channel says null.
type nullAwareMock struct {
	*mockReader
	cur []bool
}

func (m *nullAwareMock) ReadRows(ctx context.Context, fn func([]string) error) error {
	return m.mockReader.ReadRows(ctx, func(r []string) error {
		m.cur = make([]bool, len(r))
		out := make([]string, len(r))
		for i, c := range r {
			if c == "<null>" {
				m.cur[i] = true
				out[i] = "0"
				continue
			}
			out[i] = c
		}
		return fn(out)
	})
}

func (m *nullAwareMock) RowNulls() []bool { return m.cur }

// TestImportJob_VectorDeclaredNulls: a source's declared-null channel
// folds too — a declared-null element is NaN, all declared null is a
// null vector — and the scalar columns keep their own declared nulls
// at their folded positions.
func TestImportJob_VectorDeclaredNulls(t *testing.T) {
	t.Parallel()

	cols := []string{"x", "a", "b", "label"}
	rows := [][]string{
		{"1", "1", "2", "p"},
		{"2", "<null>", "3", "<null>"},
		{"3", "<null>", "<null>", ""},
	}
	src := &nullAwareMock{mockReader: newMockReader(cols, rows)}
	fs := afero.NewMemMapFs()
	job := NewImportJob(src, "out.pulse")
	job.FS = fs
	job.Vectors = []VectorDecl{{Name: "v", Columns: []string{"a", "b"}}}
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, wides, nulls, _ := readVecCohort(t, fs, "out.pulse")
	if s.Fields[1].Name != "v" || s.Fields[2].Name != "label" {
		t.Fatalf("fields %+v", s.Fields)
	}
	got := wides[1]["v"].([]float64)
	if !math.IsNaN(got[0]) || got[1] != 3 || !nulls[1]["label"] {
		t.Fatalf("row 2: v %v, label null %v", got, nulls[1]["label"])
	}
	if !nulls[2]["v"] || nulls[2]["label"] {
		t.Fatalf("row 3: v null %v, label null %v (a present empty categorical is a value)", nulls[2]["v"], nulls[2]["label"])
	}
}

// TestImportJob_VectorAuthoritativeSourceRefused: an SPSS-style source
// hands over a dictionary of the unfolded columns; folding it is
// refused.
func TestImportJob_VectorAuthoritativeSourceRefused(t *testing.T) {
	t.Parallel()

	src := &authoritativeReader{
		columns: []string{"a", "b"},
		rows:    [][]string{{"1", "2"}},
		schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "a", Type: encoding.FieldTypeF64, CsvColumnIdx: 0},
			{Name: "b", Type: encoding.FieldTypeF64, CsvColumnIdx: 1},
		}},
	}
	job := NewImportJob(src, "out.pulse")
	job.FS = afero.NewMemMapFs()
	job.Vectors = []VectorDecl{{Name: "v", Columns: []string{"a", "b"}}}
	if _, err := job.Run(context.Background()); vecReason(err) != "authoritative_source" {
		t.Fatalf("err = %v", err)
	}
}

// TestImportJob_NoVectorsUnchanged: without declarations the fold is
// not installed and the cohort bytes are the plain import's.
func TestImportJob_NoVectorsUnchanged(t *testing.T) {
	t.Parallel()

	cols, rows := vecFixture()
	_, a, err := runVecImport(t, cols, rows, func(*ImportJob) {})
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := runVecImport(t, cols, rows, func(j *ImportJob) { j.Vectors = []VectorDecl{} })
	if err != nil {
		t.Fatal(err)
	}
	ra, _ := afero.ReadFile(a, "out.pulse")
	rb, _ := afero.ReadFile(b, "out.pulse")
	if !bytes.Equal(ra, rb) || ra[8] != encoding.FormatVersionV1 {
		t.Fatal("empty Vectors changed the cohort")
	}
}
