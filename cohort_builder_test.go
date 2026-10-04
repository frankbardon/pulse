package pulse

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// memBuilderEngine is a hermetic engine over a fresh MemMapFs.
func memBuilderEngine(t *testing.T) (*Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

// listFiles returns the regular files directly under root, sorted
// (every test here builds at the filesystem root).
func listFiles(t *testing.T, fsys afero.Fs, root string) []string {
	t.Helper()
	ents, err := afero.ReadDir(fsys, root)
	if err != nil {
		t.Fatalf("list %s: %v", root, err)
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// buildCohort appends rows and closes, failing the test on any error.
func buildCohort(t *testing.T, p *Pulse, target string, schema encoding.Schema, opts CohortBuilderOptions, rows []CohortRow) *CohortBuildResult {
	t.Helper()
	b, err := p.NewCohortBuilder(context.Background(), target, schema, opts)
	if err != nil {
		t.Fatalf("NewCohortBuilder: %v", err)
	}
	for i, r := range rows {
		if err := b.Append(r); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}
	res, err := b.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	return res
}

func readFile(t *testing.T, fsys afero.Fs, path string) []byte {
	t.Helper()
	b, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// TestCohortBuilder_ByteIdenticalToRawWrite: every one of the 20 field
// types, every set rung, nulls and exact extremes, built from the rows
// CohortReader returns, are byte-identical to the raw-primitive write
// of the same rows — even though the builder is handed a schema with
// bogus layout values (recomputed, never trusted) — and read back to
// identical rows; Process runs over the result.
func TestCohortBuilder_ByteIdenticalToRawWrite(t *testing.T) {
	p, fsys, cols, oracleSchema, oracle := readerFixtureEngine(t)

	schema := encoding.Schema{Fields: append([]encoding.Field(nil), oracleSchema.Fields...)}
	for i := range schema.Fields {
		schema.Fields[i].ByteOffset = 9999 - i
		schema.Fields[i].BitPosition = 7
		schema.Fields[i].CsvColumnIdx = 500 + i
		schema.Fields[i].Description = "Fixture column " + schema.Fields[i].Name + " for builder parity."
		oracleSchema.Fields[i].Description = schema.Fields[i].Description
	}
	// The oracle is rewritten with the same descriptions so only the
	// layout differs between the two inputs.
	var want bytes.Buffer
	if err := encoding.WriteHeader(&want); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&want, oracleSchema); err != nil {
		t.Fatal(err)
	}
	pre := len(oracle) - recordRegion(t, oracle)
	want.Write(oracle[pre:])

	rows := make([]CohortRow, len(cols[0].want))
	for r := range rows {
		rows[r] = wantRow(cols, r)
	}
	res := buildCohort(t, p, "built.pulse", schema, CohortBuilderOptions{}, rows)
	if res.Records != int64(len(rows)) || res.FormatVersion != encoding.FormatVersionV1 {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}
	got := readFile(t, fsys, "built.pulse")
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("builder bytes differ from the raw-primitive write (%d vs %d bytes)", len(got), want.Len())
	}

	r := openReader(t, p, "built.pulse")
	for i := int64(0); i < r.Len(); i++ {
		row, err := r.RecordAt(i)
		if err != nil {
			t.Fatal(err)
		}
		assertRowEqual(t, fmt.Sprintf("row %d", i), r.Schema(), row, rows[i])
	}

	resp, err := p.Process(context.Background(), &Request{
		Cohort:       &types.Cohort{Filename: "built.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "u8", Label: "n"}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0]["n"] != float64(3) {
		t.Fatalf("count over built cohort = %v, want 3 non-null", resp.Data)
	}
}

// recordRegion returns the byte length of a single-file cohort's record
// region (everything after header + schema).
func recordRegion(t *testing.T, data []byte) int {
	t.Helper()
	br := bytes.NewReader(data)
	v, err := encoding.ReadHeader(br)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoding.ReadSchema(br, v); err != nil {
		t.Fatal(err)
	}
	return br.Len()
}

// paritySchema is a mixed explicit schema laid out canonically (what
// an import needs, since import writes the layout it is given), with a
// pre-seeded categorical dictionary. Every call returns fresh
// dictionaries: an import interns into the schema it is handed.
func paritySchema() encoding.Schema {
	seed := encoding.NewDictionary()
	_, _ = seed.Add("north")
	fields := []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "small", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "score", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "ratio", Type: encoding.FieldTypeF32},
		{Name: "day", Type: encoding.FieldTypeDate},
		{Name: "at", Type: encoding.FieldTypeDateTime, Nullable: true},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: seed},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Nullable: true},
		{Name: "wide", Type: encoding.FieldTypeSetU128},
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 12, Scale: 3, Nullable: true},
		{Name: "big", Type: encoding.FieldTypeU64},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Parity column " + fields[i].Name + " (builder vs import)."
		if fields[i].Type.IsBitPacked() {
			off++
		} else {
			off += fields[i].Type.ByteSize()
		}
	}
	return encoding.Schema{Fields: fields}
}

const parityCSV = "id,small,flag,score,ratio,day,at,region,tags,wide,amount,big\n" +
	"1,3,true,2.25,1.5,2024-03-01,2024-03-01T12:00:00Z,south,a|b,x|y,12.345,18446744073709551615\n" +
	"2,,false,,0.25,1969-12-31,,north,,y,,9007199254740993\n" +
	"3,15,true,-1e300,-2,2000-01-01,1960-06-15T00:00:01Z,east,c,z|x,-0.5,0\n"

func parityRows(t *testing.T) []CohortRow {
	t.Helper()
	day := func(s string) any {
		d, err := encoding.ParseDate(s)
		if err != nil {
			t.Fatal(err)
		}
		return int32(d)
	}
	at := func(s string) any {
		v, err := encoding.ParseDateTime(s)
		if err != nil {
			t.Fatal(err)
		}
		return int64(v)
	}
	dec := func(s string) any {
		d, sc, err := encoding.ParseDecimal128(s)
		if err != nil {
			t.Fatal(err)
		}
		d, err = d.Rescale(sc, 3)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	return []CohortRow{
		{uint64(1), uint64(3), true, 2.25, float32(1.5), day("2024-03-01"), at("2024-03-01T12:00:00Z"), "south", []string{"a", "b"}, []string{"x", "y"}, dec("12.345"), uint64(18446744073709551615)},
		{uint64(2), nil, false, nil, float32(0.25), day("1969-12-31"), nil, "north", nil, []string{"y"}, nil, uint64(9007199254740993)},
		{uint64(3), uint64(15), true, -1e300, float32(-2), day("2000-01-01"), at("1960-06-15T00:00:01Z"), "east", []string{"c"}, []string{"z", "x"}, dec("-0.5"), uint64(0)},
	}
}

// importParity imports parityCSV with explicit schema S into path.
func importParity(t *testing.T, p *Pulse, path string, s encoding.Schema) []byte {
	t.Helper()
	src, err := pio.NewReaderFromBytes(pio.FormatCSV, []byte(parityCSV), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	job := pio.NewImportJob(src, path)
	job.Schema = &s
	rep, err := p.Import(context.Background(), job)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if rep.RowsImported != 3 || len(rep.RowErrors) != 0 {
		t.Fatalf("import report = %+v", rep)
	}
	b, err := afero.ReadFile(p.fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestCohortBuilder_ByteIdenticalToImport: the same rows built with
// schema S and imported with explicit schema S produce the same bytes —
// pre-seeded dictionary first, then first-seen growth, nulls, the
// bitmap, a wide set and a rescaled decimal included.
func TestCohortBuilder_ByteIdenticalToImport(t *testing.T) {
	p, fsys := memBuilderEngine(t)
	imported := importParity(t, p, "imported.pulse", paritySchema())

	in := paritySchema()
	res := buildCohort(t, p, "built.pulse", in, CohortBuilderOptions{}, parityRows(t))
	built := readFile(t, fsys, "built.pulse")
	if !bytes.Equal(built, imported) {
		t.Fatalf("built (%d bytes) != imported (%d bytes)", len(built), len(imported))
	}
	if got := res.Schema.Field("region").Dictionary.Values(); strings.Join(got, ",") != "north,south,east" {
		t.Fatalf("region dictionary = %v, want pre-seeded then first-seen", got)
	}
	if n := in.Field("region").Dictionary.Count(); n != 1 {
		t.Fatalf("caller's pre-seeded dictionary was mutated: %d entries", n)
	}
	r := openReader(t, p, "built.pulse")
	wantRows := parityRows(t)
	wantRows[2][9] = []string{"x", "z"} // read back in dictionary order
	for i, want := range wantRows {
		got, err := r.RecordAt(int64(i))
		if err != nil {
			t.Fatal(err)
		}
		assertRowEqual(t, fmt.Sprintf("row %d", i), r.Schema(), got, want)
	}
}

// TestCohortBuilder_RejectedRows: each rejection path is
// PULSE_IMPORT_ROW_ERROR with row / field / reason, and a rejected row
// leaves no trace — not in the records, not in a dictionary — so the
// build interleaving bad rows equals the build of the good rows alone.
func TestCohortBuilder_RejectedRows(t *testing.T) {
	full := encoding.NewDictionary()
	for i := 0; i < 256; i++ {
		_, _ = full.Add(fmt.Sprintf("c%03d", i))
	}
	schema := func() encoding.Schema {
		return encoding.Schema{Fields: []encoding.Field{
			{Name: "label", Type: encoding.FieldTypeCategoricalU16, Description: "Free label grown first-seen."},
			{Name: "tags", Type: encoding.FieldTypeSetU8, Description: "Up to eight tags per row."},
			{Name: "n", Type: encoding.FieldTypeU8, Description: "Small count, never null."},
			{Name: "nib", Type: encoding.FieldTypeU4, Nullable: true, Description: "Nibble value or null."},
			{Name: "full", Type: encoding.FieldTypeCategoricalU8, Dictionary: full, Description: "A dictionary already at its rung."},
			{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 4, Scale: 2, Description: "Amount at two places."},
			{Name: "on", Type: encoding.FieldTypePackedBool, Description: "Boolean switch per row."},
		}}
	}
	d := func(m int64) encoding.Decimal128 { return encoding.NewDecimal128FromInt(m) }
	tags8 := []string{"t0", "t1", "t2", "t3", "t4", "t5", "t6", "t7"}
	good := []CohortRow{
		{"alpha", tags8, uint64(1), uint64(2), "c000", d(1234), true},
		{"beta", []string{}, uint64(255), nil, "c255", d(-9999), false},
	}
	bad := []struct {
		name, field, reason string
		row                 CohortRow
	}{
		{"arity", "", "arity", CohortRow{"x"}},
		{"type uint", "n", "type", CohortRow{"new1", tags8, 7, nil, "c000", d(1), true}},
		{"type string", "label", "type", CohortRow{[]byte("x"), tags8, uint64(1), nil, "c000", d(1), true}},
		{"type set", "tags", "type", CohortRow{"new2", "t0", uint64(1), nil, "c000", d(1), true}},
		{"type decimal", "amt", "type", CohortRow{"new3", tags8, uint64(1), nil, "c000", 1.5, true}},
		{"type bool", "on", "type", CohortRow{"new4", tags8, uint64(1), nil, "c000", d(1), 1}},
		{"null non-nullable", "n", "null", CohortRow{"new5", tags8, nil, nil, "c000", d(1), true}},
		{"u8 overflow", "n", "overflow", CohortRow{"new6", tags8, uint64(256), nil, "c000", d(1), true}},
		{"u4 overflow", "nib", "overflow", CohortRow{"new7", tags8, uint64(1), uint64(16), "c000", d(1), true}},
		{"categorical rung", "full", "overflow", CohortRow{"new8", tags8, uint64(1), nil, "c256", d(1), true}},
		{"set rung", "tags", "overflow", CohortRow{"new9", []string{"t8"}, uint64(1), nil, "c000", d(1), true}},
		{"decimal precision", "amt", "overflow", CohortRow{"new10", tags8, uint64(1), nil, "c000", d(10000), true}},
	}

	p, fsys := memBuilderEngine(t)
	want := buildCohort(t, p, "good.pulse", schema(), CohortBuilderOptions{}, good)

	b, err := p.NewCohortBuilder(context.Background(), "mixed.pulse", schema(), CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Append(good[0]); err != nil {
		t.Fatal(err)
	}
	for k, tc := range bad {
		err := b.Append(tc.row)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_IMPORT_ROW_ERROR {
			t.Fatalf("%s: err = %v, want PULSE_IMPORT_ROW_ERROR", tc.name, err)
		}
		if ce.Details["reason"] != tc.reason || ce.Details["row"] != int64(k+2) {
			t.Errorf("%s: details = %v, want reason %s row %d", tc.name, ce.Details, tc.reason, k+2)
		}
		if tc.field != "" && ce.Details["field"] != tc.field {
			t.Errorf("%s: field = %v, want %s", tc.name, ce.Details["field"], tc.field)
		}
	}
	if err := b.Append(good[1]); err != nil {
		t.Fatalf("builder unusable after rejected rows: %v", err)
	}
	res, err := b.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.Records != 2 {
		t.Fatalf("records = %d, want 2", res.Records)
	}
	if !bytes.Equal(readFile(t, fsys, "mixed.pulse"), readFile(t, fsys, "good.pulse")) {
		t.Fatalf("rejected rows left a trace: labels %v vs %v",
			res.Schema.Field("label").Dictionary.Values(), want.Schema.Field("label").Dictionary.Values())
	}
}

// TestCohortBuilder_AbortLeavesNothing: Abort drops the spool and never
// creates the target; it is idempotent.
func TestCohortBuilder_AbortLeavesNothing(t *testing.T) {
	p, fsys := memBuilderEngine(t)
	b, err := p.NewCohortBuilder(context.Background(), "out.pulse", paritySchema(), CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range parityRows(t) {
		if err := b.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if files := listFiles(t, fsys, "/"); len(files) != 1 || !strings.Contains(files[0], ".build-spool-") {
		t.Fatalf("during the build only the spool exists, got %v", files)
	}
	if err := b.Abort(); err != nil {
		t.Fatal(err)
	}
	if err := b.Abort(); err != nil {
		t.Fatalf("second Abort: %v", err)
	}
	if files := listFiles(t, fsys, "/"); len(files) != 0 {
		t.Fatalf("Abort left %v", files)
	}
	if _, err := b.Close(); !errors.HasCode(err, errors.SERVICE_RESOURCE) {
		t.Fatalf("Close after Abort = %v, want SERVICE_RESOURCE", err)
	}
	if err := b.Append(parityRows(t)[0]); !errors.HasCode(err, errors.SERVICE_RESOURCE) {
		t.Fatalf("Append after Abort = %v, want SERVICE_RESOURCE", err)
	}
}

// renameFailFs fails every Rename: the last step of an atomic publish.
type renameFailFs struct{ afero.Fs }

func (renameFailFs) Rename(string, string) error { return fmt.Errorf("injected rename failure") }

// tempFailFs refuses to create the publish temp file.
type tempFailFs struct{ afero.Fs }

func (f tempFailFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if strings.Contains(name, ".build-") && !strings.Contains(name, ".build-spool-") {
		return nil, fmt.Errorf("injected create failure")
	}
	return f.Fs.OpenFile(name, flag, perm)
}

// TestCohortBuilder_FailedCloseLeavesNothing: a Close that fails at any
// step leaves no target, no spool and no temp file — and an existing
// cohort being overwritten stays byte-identical.
func TestCohortBuilder_FailedCloseLeavesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(afero.Fs) afero.Fs
	}{
		{"rename", func(m afero.Fs) afero.Fs { return renameFailFs{m} }},
		{"temp create", func(m afero.Fs) afero.Fs { return tempFailFs{m} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := afero.NewMemMapFs()
			p, err := New(Options{FS: tc.wrap(mem)})
			if err != nil {
				t.Fatal(err)
			}
			b, err := p.NewCohortBuilder(context.Background(), "out.pulse", paritySchema(), CohortBuilderOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range parityRows(t) {
				if err := b.Append(r); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := b.Close(); err == nil {
				t.Fatal("Close succeeded through an injected failure")
			}
			if files := listFiles(t, mem, "/"); len(files) != 0 {
				t.Fatalf("failed Close left %v", files)
			}

			// Overwrite of an existing cohort: the original survives.
			orig := []byte("original cohort bytes")
			if err := afero.WriteFile(mem, "keep.pulse", orig, 0o644); err != nil {
				t.Fatal(err)
			}
			b, err = p.NewCohortBuilder(context.Background(), "keep.pulse", paritySchema(), CohortBuilderOptions{Overwrite: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.Close(); err == nil {
				t.Fatal("Close succeeded through an injected failure")
			}
			if got := readFile(t, mem, "keep.pulse"); !bytes.Equal(got, orig) {
				t.Fatal("a failed overwrite changed the existing cohort")
			}
			if files := listFiles(t, mem, "/"); len(files) != 1 {
				t.Fatalf("failed overwrite left %v", files)
			}
		})
	}
}

// TestCohortBuilder_TargetRefusals: an existing target without
// Overwrite (at construction and when it appears mid-build), a
// directory, an anchor, a .zst target and an empty one are refused with
// SERVICE_VALIDATION; nothing is left behind.
func TestCohortBuilder_TargetRefusals(t *testing.T) {
	p, fsys := memBuilderEngine(t)
	if err := afero.WriteFile(fsys, "taken.pulse", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsys.MkdirAll("dir.pulse", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"taken.pulse", "dir.pulse", "arch.pulse#s.pulse", "out.pulse.zst", ""} {
		_, err := p.NewCohortBuilder(context.Background(), target, paritySchema(), CohortBuilderOptions{})
		if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
			t.Errorf("target %q: err = %v, want SERVICE_VALIDATION", target, err)
		}
	}
	if _, err := p.NewCohortBuilder(context.Background(), "dir.pulse", paritySchema(), CohortBuilderOptions{Overwrite: true}); !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("Overwrite onto a directory: err = %v, want SERVICE_VALIDATION", err)
	}

	b, err := p.NewCohortBuilder(context.Background(), "late.pulse", paritySchema(), CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, "late.pulse", []byte("someone else"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Close(); !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Fatalf("Close onto a target created mid-build = %v, want SERVICE_VALIDATION", err)
	}
	if got := string(readFile(t, fsys, "late.pulse")); got != "someone else" {
		t.Fatal("Close without Overwrite clobbered a target created mid-build")
	}
	for _, f := range listFiles(t, fsys, "/") {
		if strings.Contains(f, ".build-") {
			t.Fatalf("refused Close left %s", f)
		}
	}
}

// TestCohortBuilder_SchemaRefusals: a malformed schema is refused before
// anything is created; description checks share import's code; quality
// findings warn, and error under Strict.
func TestCohortBuilder_SchemaRefusals(t *testing.T) {
	p, fsys := memBuilderEngine(t)
	f := func(name string, ft encoding.FieldType) encoding.Field {
		return encoding.Field{Name: name, Type: ft, Description: "A well described field."}
	}
	dec := f("d", encoding.FieldTypeDecimal128)
	tooLongSeed := encoding.NewDictionary()
	for i := 0; i < 9; i++ {
		_, _ = tooLongSeed.Add(fmt.Sprint(i))
	}
	seeded := f("s", encoding.FieldTypeSetU8)
	seeded.Dictionary = tooLongSeed
	for _, tc := range []struct {
		name   string
		schema encoding.Schema
		code   errors.Code
	}{
		{"no fields", encoding.Schema{}, errors.SERVICE_VALIDATION},
		{"duplicate", encoding.Schema{Fields: []encoding.Field{f("a", encoding.FieldTypeU8), f("a", encoding.FieldTypeU16)}}, errors.SERVICE_VALIDATION},
		{"empty name", encoding.Schema{Fields: []encoding.Field{f("", encoding.FieldTypeU8)}}, errors.SERVICE_VALIDATION},
		{"unknown type", encoding.Schema{Fields: []encoding.Field{f("a", encoding.FieldType(200))}}, errors.SERVICE_VALIDATION},
		{"decimal precision", encoding.Schema{Fields: []encoding.Field{dec}}, errors.SERVICE_VALIDATION},
		{"seed past rung", encoding.Schema{Fields: []encoding.Field{seeded}}, errors.SERVICE_VALIDATION},
		{"groups", encoding.Schema{Fields: []encoding.Field{f("a", encoding.FieldTypeU8)}, Groups: []encoding.Group{{}}}, errors.SERVICE_VALIDATION},
		{"description", encoding.Schema{Fields: []encoding.Field{{Name: "a", Type: encoding.FieldTypeU8, Description: strings.Repeat("x", 1001)}}}, errors.PULSE_IMPORT_DESCRIPTION_TOO_LONG},
	} {
		if _, err := p.NewCohortBuilder(context.Background(), "x.pulse", tc.schema, CohortBuilderOptions{}); !errors.HasCode(err, tc.code) {
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.code)
		}
	}
	if files := listFiles(t, fsys, "/"); len(files) != 0 {
		t.Fatalf("refused schemas left %v", files)
	}

	// Code parity with import for the shared description check.
	long := paritySchema()
	long.Fields[0].Description = strings.Repeat("x", 1001)
	src, err := pio.NewReaderFromBytes(pio.FormatCSV, []byte(parityCSV), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	job := pio.NewImportJob(src, "imp.pulse")
	job.Schema = &long
	if _, err := p.Import(context.Background(), job); !errors.HasCode(err, errors.PULSE_IMPORT_DESCRIPTION_TOO_LONG) {
		t.Fatalf("import code = %v, want PULSE_IMPORT_DESCRIPTION_TOO_LONG", err)
	}

	weak := encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU8, Description: "tbd"},
		{Name: "b", Type: encoding.FieldTypeU8, Description: "A perfectly fine description."},
		{Name: "c", Type: encoding.FieldTypeU8},
	}}
	res := buildCohort(t, p, "weak.pulse", weak, CohortBuilderOptions{}, []CohortRow{{uint64(1), uint64(2), uint64(3)}})
	if len(res.Warnings) != 2 || res.Warnings[0].Code != errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY ||
		res.Warnings[0].Details["field"] != "a" || res.Warnings[1].Details["field"] != "c" {
		t.Fatalf("warnings = %v, want LOW_QUALITY for a and c", res.Warnings)
	}
	if _, err := p.NewCohortBuilder(context.Background(), "strict.pulse", weak, CohortBuilderOptions{Strict: true}); !errors.HasCode(err, errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY) {
		t.Fatalf("Strict: err = %v, want PULSE_FIELD_DESCRIPTION_LOW_QUALITY", err)
	}
}

// TestCohortBuilder_OverwriteInvalidatesIndex: replacing a cohort that
// carries a point-lookup index reports the sidecar as invalidated, and
// the index refuses the new bytes as stale.
func TestCohortBuilder_OverwriteInvalidatesIndex(t *testing.T) {
	p, _ := memBuilderEngine(t)
	ctx := context.Background()
	rows := parityRows(t)
	buildCohort(t, p, "c.pulse", paritySchema(), CohortBuilderOptions{}, rows)
	if _, err := p.BuildIndex(ctx, "c.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if _, err := p.Lookup(ctx, &LookupRequest{Cohort: &types.Cohort{Filename: "c.pulse"}, Field: "id", Value: "2"}); err != nil {
		t.Fatalf("Lookup before overwrite: %v", err)
	}

	if _, err := p.NewCohortBuilder(ctx, "c.pulse", paritySchema(), CohortBuilderOptions{}); !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Fatalf("existing target without Overwrite = %v", err)
	}
	res := buildCohort(t, p, "c.pulse", paritySchema(), CohortBuilderOptions{Overwrite: true}, rows[:2])
	if len(res.InvalidatedSidecars) == 0 || res.InvalidatedSidecars[0].Kind != SidecarKindPointLookupIndex {
		t.Fatalf("InvalidatedSidecars = %+v, want the point-lookup index", res.InvalidatedSidecars)
	}
	_, err := p.Lookup(ctx, &LookupRequest{Cohort: &types.Cohort{Filename: "c.pulse"}, Field: "id", Value: "2"})
	if !errors.HasCode(err, errors.PULSE_INDEX_STALE) {
		t.Fatalf("Lookup after overwrite = %v, want PULSE_INDEX_STALE", err)
	}

	// A fresh target replaces nothing and reports no sidecars.
	if res := buildCohort(t, p, "fresh.pulse", paritySchema(), CohortBuilderOptions{}, rows); len(res.InvalidatedSidecars) != 0 {
		t.Fatalf("fresh build reported sidecars: %+v", res.InvalidatedSidecars)
	}
}

// TestCohortBuilder_DataDirRoot: under Options{DataDir} a target at the
// data-dir root builds in place (spool and temp beside it, inside the
// data dir), leaving only the cohort.
func TestCohortBuilder_DataDirRoot(t *testing.T) {
	dir := t.TempDir()
	p, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	buildCohort(t, p, "root.pulse", paritySchema(), CohortBuilderOptions{}, parityRows(t))
	ents, err := afero.ReadDir(afero.NewOsFs(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != "root.pulse" {
		names := []string{}
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("data dir holds %v, want only root.pulse", names)
	}
	r := openReader(t, p, "root.pulse")
	if r.Len() != 3 {
		t.Fatalf("Len = %d", r.Len())
	}
}

// TestCohortBuilder_EmptyBuild: a build with no rows is a valid empty
// cohort, as an import of an empty source is.
func TestCohortBuilder_EmptyBuild(t *testing.T) {
	p, _ := memBuilderEngine(t)
	res := buildCohort(t, p, "empty.pulse", paritySchema(), CohortBuilderOptions{}, nil)
	if res.Records != 0 {
		t.Fatalf("records = %d", res.Records)
	}
	if r := openReader(t, p, "empty.pulse"); r.Len() != 0 {
		t.Fatalf("Len = %d", r.Len())
	}
}
