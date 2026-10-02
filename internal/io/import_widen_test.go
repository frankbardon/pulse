package io

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// widenFixture is a synthetic join-shaped source whose inference sample
// (the first 500 rows) under-sizes three columns:
//
//	parent   categorical: 100 distinct names in the sample (→ categorical_u8),
//	         400 distinct over the file — the 257th first appears at row 657
//	n        integer: 0..199 in the sample (→ u8); 70000 at row 700
//	         (→ u32) and -3 at row 900 (→ f64)
//	small    integer: 0..15 in the sample (→ u4, bit-packed); 300 at row 800
//	         (→ u16)
//
// around fields that never widen, before and after them (so re-striding
// has to move a prefix and a suffix), one of them nullable.
func widenFixture() ([]string, [][]string) {
	cols := []string{"id", "flag", "parent", "n", "small", "score", "tag"}
	var rows [][]string
	for i := 0; i < 1000; i++ {
		row := i + 1
		p := i % 100
		if i >= 500 {
			p = 100 + (i - 500) // 300 new names: parent-100 .. parent-399
		}
		n := fmt.Sprint(i % 200)
		switch row {
		case 700:
			n = "70000"
		case 900:
			n = "-3"
		}
		small := fmt.Sprint(i % 16)
		if row == 800 {
			small = "300"
		}
		score := fmt.Sprintf("%d.5", i%7)
		if i%11 == 0 {
			score = ""
		}
		rows = append(rows, []string{
			fmt.Sprint(i % 50),
			fmt.Sprint(i % 2),
			fmt.Sprintf("parent-%03d", p),
			n,
			small,
			score,
			fmt.Sprintf("t%d", i%3),
		})
	}
	return cols, rows
}

func importRaw(t *testing.T, cols []string, rows [][]string, mutate ...func(*ImportJob)) (*ImportReport, []byte) {
	t.Helper()
	rep, raw, _, err := runGroupImport(t, newMockReader(cols, rows), nil, mutate...)
	if err != nil {
		t.Fatal(err)
	}
	return rep, raw
}

func warningFor(t *testing.T, ws []*perrors.CodedError, field string) *perrors.CodedError {
	t.Helper()
	for _, w := range ws {
		if w.Details["field"] == field {
			return w
		}
	}
	t.Fatalf("no PULSE_IMPORT_WIDTH_PROMOTED warning for %q in %v", field, ws)
	return nil
}

// TestImportWiden_PromotesInsteadOfRowErrors: every row of a source that
// outgrows its sample-inferred widths imports; each outgrown field lands
// on the narrowest type that holds it, with one warning naming the
// inferred type, the written type and the first row that forced it.
func TestImportWiden_PromotesInsteadOfRowErrors(t *testing.T) {
	cols, rows := widenFixture()
	rep, _ := importRaw(t, cols, rows)
	if rep.RowsImported != len(rows) || len(rep.RowErrors) != 0 {
		t.Fatalf("imported %d rows with %d row errors, want %d / 0 (first: %v)", rep.RowsImported, len(rep.RowErrors), len(rows), rep.RowErrors)
	}
	want := map[string][3]any{
		"parent": {"categorical_u8", "categorical_u16", 657},
		"n":      {"u8", "f64", 700},
		"small":  {"u4", "u16", 800},
	}
	if len(rep.WidthWarnings) != len(want) {
		t.Fatalf("width warnings = %v, want one per field in %v", rep.WidthWarnings, want)
	}
	for field, w := range want {
		got := warningFor(t, rep.WidthWarnings, field)
		if got.Code != perrors.PULSE_IMPORT_WIDTH_PROMOTED {
			t.Errorf("%s: code %s", field, got.Code)
		}
		if got.Details["from"] != w[0] || got.Details["to"] != w[1] || got.Details["source_row"] != w[2] {
			t.Errorf("%s: details %v, want from %v to %v source_row %v", field, got.Details, w[0], w[1], w[2])
		}
		if f := fieldByName(rep.Schema, field); f.Type.String() != w[1] {
			t.Errorf("%s: written type %s, want %v", field, f.Type, w[1])
		}
	}
	for _, f := range []string{"id", "flag", "score", "tag"} {
		for _, w := range rep.WidthWarnings {
			if w.Details["field"] == f {
				t.Errorf("%s never outgrew its width but drew %v", f, w)
			}
		}
	}
}

// TestImportWiden_ByteIdenticalToFullSample: a promoted import writes
// exactly the cohort an import whose sample saw every row writes —
// re-striding the rows already held changes no value and no layout.
// The f64 arm is included: the full-sample inference types a column
// with a negative integer as f64 too.
func TestImportWiden_ByteIdenticalToFullSample(t *testing.T) {
	cols, rows := widenFixture()
	_, promoted := importRaw(t, cols, rows)
	full, fullRaw := importRaw(t, cols, rows, func(j *ImportJob) { j.SampleRows = len(rows) })
	if len(full.WidthWarnings) != 0 {
		t.Fatalf("full-sample import widened: %v", full.WidthWarnings)
	}
	if !bytes.Equal(promoted, fullRaw) {
		t.Fatalf("promoted import (%d bytes) differs from the full-sample import (%d bytes)", len(promoted), len(fullRaw))
	}
}

// TestImportWiden_ValuesRoundTrip: every value decodes as the source
// wrote it, on both sides of each promotion.
func TestImportWiden_ValuesRoundTrip(t *testing.T) {
	cols, rows := widenFixture()
	rep, _, fs, err := runGroupImport(t, newMockReader(cols, rows), nil)
	if err != nil {
		t.Fatal(err)
	}
	schema, vals, nulls := decodeAll(t, fs, "out.pulse")
	if len(vals) != len(rows) || rep.RowsImported != len(rows) {
		t.Fatalf("decoded %d records, want %d", len(vals), len(rows))
	}
	dict := fieldByName(schema, "parent").Dictionary
	for k, src := range rows {
		if got := dict.Resolve(uint32(vals[k]["parent"])); got != src[2] {
			t.Fatalf("record %d parent = %q, want %q", k, got, src[2])
		}
		var n float64
		_, _ = fmt.Sscan(src[3], &n)
		if vals[k]["n"] != n {
			t.Fatalf("record %d n = %v, want %v", k, vals[k]["n"], n)
		}
		var small float64
		_, _ = fmt.Sscan(src[4], &small)
		if vals[k]["small"] != small {
			t.Fatalf("record %d small = %v, want %v", k, vals[k]["small"], small)
		}
		if (src[5] == "") != nulls[k]["score"] {
			t.Fatalf("record %d score null = %v, source %q", k, nulls[k]["score"], src[5])
		}
		if src[6] != fieldByName(schema, "tag").Dictionary.Resolve(uint32(vals[k]["tag"])) {
			t.Fatalf("record %d tag differs", k)
		}
	}
}

// TestImportWiden_FractionalPromotesToF64: a fractional value in a
// sample-integer column promotes it to f64 (never f32, which is exact
// for integers only up to 2^24), and every earlier integer decodes
// unchanged.
func TestImportWiden_FractionalPromotesToF64(t *testing.T) {
	cols := []string{"n"}
	var rows [][]string
	for i := 0; i < 600; i++ {
		rows = append(rows, []string{fmt.Sprint(i * 1000)})
	}
	rows = append(rows, []string{"2.5"})
	rep, _, fs, err := runGroupImport(t, newMockReader(cols, rows), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RowErrors) != 0 || rep.Schema.Fields[0].Type != encoding.FieldTypeF64 {
		t.Fatalf("row errors %v, type %s; want none and f64", rep.RowErrors, rep.Schema.Fields[0].Type)
	}
	if w := warningFor(t, rep.WidthWarnings, "n"); w.Details["from"] != "u32" || w.Details["source_row"] != 601 {
		t.Errorf("warning details %v, want from u32 at source row 601", w.Details)
	}
	_, vals, _ := decodeAll(t, fs, "out.pulse")
	for i := 0; i < 600; i++ {
		if vals[i]["n"] != float64(i*1000) {
			t.Fatalf("record %d = %v, want %d", i, vals[i]["n"], i*1000)
		}
	}
	if vals[600]["n"] != 2.5 {
		t.Errorf("record 600 = %v, want 2.5", vals[600]["n"])
	}
}

// TestImportWiden_NoCleanPromotionStaysRowError: a value no promotion
// can hold losslessly keeps today's row error — a non-number in an
// integer column, and any non-integer in a u64 column (f64 is not exact
// past 2^53).
func TestImportWiden_NoCleanPromotionStaysRowError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sample string
		bad    string
		want   encoding.FieldType
	}{
		{"non-number", "7", "seven", encoding.FieldTypeU4},
		{"u64 negative", "5000000000", "-1", encoding.FieldTypeU64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rows [][]string
			for i := 0; i < 600; i++ {
				rows = append(rows, []string{tc.sample, "x"})
			}
			rows = append(rows, []string{tc.bad, "x"})
			rep, _ := importRaw(t, []string{"n", "c"}, rows)
			if len(rep.RowErrors) != 1 || rep.RowErrors[0].Row != 601 {
				t.Fatalf("row errors %v, want exactly row 601", rep.RowErrors)
			}
			if rep.Schema.Fields[0].Type != tc.want || len(rep.WidthWarnings) != 0 {
				t.Errorf("type %s warnings %v, want %s and none", rep.Schema.Fields[0].Type, rep.WidthWarnings, tc.want)
			}
		})
	}
}

// overflowRows is one column of 310 distinct values: ten repeating
// through the first 50 rows (a 50-row sample types it categorical_u8),
// then 300 fresh ones. At categorical_u8 the 257th distinct value is
// row 297, so 54 rows cannot be held.
func overflowRows() ([]string, [][]string) {
	var rows [][]string
	for i := 0; i < 350; i++ {
		v := i
		if i < 50 {
			v = i % 10
		}
		rows = append(rows, []string{fmt.Sprintf("v%03d", v)})
	}
	return []string{"c"}, rows
}

// TestImportWiden_DeclaredWidthsNeverPromote: a width the caller fixed
// is a contract. An explicit Schema and an authoritative
// (SchemaAwareReader) schema keep categorical_u8, and every value past
// its 256 entries stays a row error. A ColumnTypeOverrides column never
// promotes either, but the first value past its rung refuses the whole
// import (E4-S13) rather than skipping rows.
func TestImportWiden_DeclaredWidthsNeverPromote(t *testing.T) {
	cols, rows := overflowRows()
	explicit := func() *encoding.Schema {
		return &encoding.Schema{Fields: []encoding.Field{{Name: "c", Type: encoding.FieldTypeCategoricalU8}}}
	}
	check := func(t *testing.T, rep *ImportReport) {
		t.Helper()
		if rep.Schema.Fields[0].Type != encoding.FieldTypeCategoricalU8 || len(rep.WidthWarnings) != 0 {
			t.Fatalf("type %s warnings %v, want categorical_u8 and none", rep.Schema.Fields[0].Type, rep.WidthWarnings)
		}
		if rep.RowsImported != 296 || len(rep.RowErrors) != 54 {
			t.Fatalf("imported %d with %d row errors, want 296 / 54", rep.RowsImported, len(rep.RowErrors))
		}
	}
	t.Run("column_type_overrides", func(t *testing.T) {
		_, _, _, err := runGroupImport(t, newMockReader(cols, rows), nil, func(j *ImportJob) {
			j.SampleRows = 50
			j.ColumnTypeOverrides = map[string]encoding.FieldType{"c": encoding.FieldTypeCategoricalU8}
		})
		d := requireOverrideRefusal(t, err)
		if d["column"] != "c" || d["value"] != "v296" || d["row"] != 297 {
			t.Errorf("refusal details %v, want column c, value v296, row 297", d)
		}
	})
	t.Run("explicit schema", func(t *testing.T) {
		rep, _ := importRaw(t, cols, rows, func(j *ImportJob) { j.Schema = explicit() })
		check(t, rep)
	})
	t.Run("authoritative schema", func(t *testing.T) {
		src := &authoritativeReader{columns: cols, rows: rows, schema: explicit()}
		rep, _, _, err := runGroupImport(t, src, nil)
		if err != nil {
			t.Fatal(err)
		}
		check(t, rep)
	})
	t.Run("inferred", func(t *testing.T) {
		rep, _ := importRaw(t, cols, rows, func(j *ImportJob) { j.SampleRows = 50 })
		if rep.RowsImported != 350 || rep.Schema.Fields[0].Type != encoding.FieldTypeCategoricalU16 {
			t.Fatalf("inferred: imported %d as %s, want 350 as categorical_u16", rep.RowsImported, rep.Schema.Fields[0].Type)
		}
	})
}

// TestImportWiden_ForcingRowThatFailsElsewhere: the row that forces a
// promotion but fails on a later column is still a row error, the
// promotion stands (its dictionary entry was interned), and every
// other row imports with its value intact.
func TestImportWiden_ForcingRowThatFailsElsewhere(t *testing.T) {
	var rows [][]string
	for i := 0; i < 600; i++ {
		rows = append(rows, []string{fmt.Sprint(i % 100), fmt.Sprint(i % 3)})
	}
	rows = append(rows, []string{"999", "not-a-number"}, []string{"5", "2"})
	rep, _, fs, err := runGroupImport(t, newMockReader([]string{"a", "b"}, rows), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RowErrors) != 1 || rep.RowErrors[0].Row != 601 || rep.RowsImported != 601 {
		t.Fatalf("row errors %v imported %d, want only row 601 and 601 rows", rep.RowErrors, rep.RowsImported)
	}
	if rep.Schema.Fields[0].Type != encoding.FieldTypeU16 {
		t.Errorf("a = %s, want u16 (promotion forced by the failing row stands)", rep.Schema.Fields[0].Type)
	}
	_, vals, _ := decodeAll(t, fs, "out.pulse")
	if vals[599]["a"] != 99 || vals[600]["a"] != 5 || vals[600]["b"] != 2 {
		t.Errorf("records around the failing row: %v %v", vals[599], vals[600])
	}
}

// TestWidenTarget is the rung table.
func TestWidenTarget(t *testing.T) {
	full := func(n int) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for i := 0; i < n; i++ {
			_, _ = d.Add(fmt.Sprint(i))
		}
		return d
	}
	for _, tc := range []struct {
		ft   encoding.FieldType
		raw  string
		dict *encoding.Dictionary
		to   encoding.FieldType
		ok   bool
	}{
		{encoding.FieldTypeCategoricalU8, "x", full(256), encoding.FieldTypeCategoricalU16, true},
		{encoding.FieldTypeCategoricalU8, "x", full(10), 0, false},
		{encoding.FieldTypeCategoricalU16, "x", full(65536), encoding.FieldTypeCategoricalU32, true},
		{encoding.FieldTypeCategoricalU32, "x", full(3), 0, false},
		{encoding.FieldTypeU4, "16", nil, encoding.FieldTypeU8, true},
		{encoding.FieldTypeU4, "256", nil, encoding.FieldTypeU16, true},
		{encoding.FieldTypeU8, "65536", nil, encoding.FieldTypeU32, true},
		{encoding.FieldTypeU32, "4294967296", nil, encoding.FieldTypeU64, true},
		{encoding.FieldTypeU16, "-1", nil, encoding.FieldTypeF64, true},
		{encoding.FieldTypeU32, "1e3", nil, encoding.FieldTypeF64, true},
		{encoding.FieldTypeU8, "abc", nil, 0, false},
		{encoding.FieldTypeU64, "-1", nil, 0, false},
		{encoding.FieldTypeF32, "1e300", nil, encoding.FieldTypeF64, true},
		{encoding.FieldTypeF32, "-4e38", nil, encoding.FieldTypeF64, true},
		{encoding.FieldTypeF32, "1e-50", nil, encoding.FieldTypeF64, true},
		{encoding.FieldTypeF32, "0.1", nil, 0, false},
		{encoding.FieldTypeF32, "16777217", nil, 0, false},
		{encoding.FieldTypeF32, "0", nil, 0, false},
		{encoding.FieldTypeF32, "NaN", nil, 0, false},
		{encoding.FieldTypeF32, "1e400", nil, 0, false},
		{encoding.FieldTypeF32, "abc", nil, 0, false},
		{encoding.FieldTypeF64, "1e300", nil, 0, false},
		{encoding.FieldTypePackedBool, "2", nil, 0, false},
		{encoding.FieldTypeDate, "2020-01-01", nil, 0, false},
	} {
		to, ok := widenTarget(tc.ft, tc.raw, tc.dict)
		if to != tc.to || ok != tc.ok {
			t.Errorf("widenTarget(%s, %q) = %s, %v; want %s, %v", tc.ft, tc.raw, to, ok, tc.to, tc.ok)
		}
	}
}

// TestWidenBufferedColumn re-strides held rows in place, prefix and
// suffix bytes moved intact, the widened cell zero-extended — or
// converted to the exactly-equal f64.
func TestWidenBufferedColumn(t *testing.T) {
	types := []encoding.FieldType{encoding.FieldTypeU16, encoding.FieldTypeU8, encoding.FieldTypeU4}
	var buf bytes.Buffer
	for k := 0; k < 3; k++ {
		buf.Write([]byte{byte(0x10 + k), 0xAA, byte(200 + k), byte(k)})
	}
	if err := widenBufferedColumn(&buf, 3, types, 1, encoding.FieldTypeU32); err != nil {
		t.Fatal(err)
	}
	want := []byte{}
	for k := 0; k < 3; k++ {
		want = append(want, byte(0x10+k), 0xAA, byte(200+k), 0, 0, 0, byte(k))
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("u8→u32:\n got %v\nwant %v", buf.Bytes(), want)
	}
	types[1] = encoding.FieldTypeU32
	if err := widenBufferedColumn(&buf, 3, types, 2, encoding.FieldTypeF64); err != nil {
		t.Fatal(err)
	}
	for k := 0; k < 3; k++ {
		row := buf.Bytes()[k*14 : (k+1)*14]
		if !bytes.Equal(row[:6], want[k*7:k*7+6]) {
			t.Errorf("row %d prefix %v", k, row[:6])
		}
		var bits uint64
		for b := 7; b >= 0; b-- {
			bits = bits<<8 | uint64(row[6+b])
		}
		if math.Float64frombits(bits) != float64(k) {
			t.Errorf("row %d f64 = %v, want %d", k, math.Float64frombits(bits), k)
		}
	}
}

// widenGroupFixture: parent p = i/10 over 3000 rows (300 parents), key k
// and members name / label all under-sized by the 500-row sample (50
// parents → u8 / categorical_u8 / categorical_u8, 3 bytes: no wider than
// the 4-byte index). Over the full pass they widen to u16 /
// categorical_u16 / categorical_u16 — 6 bytes, a viable group.
func widenGroupFixture() ([]string, [][]string, []GroupDecl) {
	cols := []string{"k", "name", "label", "qty"}
	var rows [][]string
	for i := 0; i < 3000; i++ {
		p := i / 10
		rows = append(rows, []string{fmt.Sprint(p), fmt.Sprintf("name-%03d", p), fmt.Sprintf("label-%03d", p), fmt.Sprint(i % 7)})
	}
	return cols, rows, []GroupDecl{{Key: []string{"k"}, Members: []string{"name", "label"}}}
}

// TestImportWiden_GroupsJudgedOnFinalWidths: a declared group is judged
// on the promoted widths, not the sample's — admitted (not dropped as
// too narrow), with or without --strict, and predict reports exactly
// what the import reports and writes.
func TestImportWiden_GroupsJudgedOnFinalWidths(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(fmt.Sprintf("strict=%v", strict), func(t *testing.T) {
			cols, rows, decls := widenGroupFixture()
			set := func(j *ImportJob) { j.StrictDedup = strict }
			ir, raw, _, err := runGroupImport(t, newMockReader(cols, rows), decls, set)
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			g := ir.Groups[0]
			if g.Verdict != encx.GroupVerdictAdmitted || g.MemberRowBytes != 6 || g.EntryCount != 300 {
				t.Fatalf("group %+v, want admitted, 6 member bytes, 300 entries", g)
			}
			if len(ir.GroupWarnings) != 0 || len(ir.WidthWarnings) != 3 {
				t.Errorf("group warnings %v width warnings %v", ir.GroupWarnings, ir.WidthWarnings)
			}

			job := NewImportJob(newMockReader(cols, rows), "unused.pulse")
			job.Groups = decls
			set(job)
			rep, err := job.Predict(context.Background())
			if err != nil {
				t.Fatalf("predict: %v", err)
			}
			if !reflect.DeepEqual(rep.Groups, ir.Groups) {
				t.Errorf("predict groups\n%+v\nimport groups\n%+v", rep.Groups, ir.Groups)
			}
			if rep.Projection.ProjectedFileBytes != int64(len(raw)) || rep.Projection.RowsImported != 3000 {
				t.Errorf("projected %d bytes / %d rows, import wrote %d bytes / 3000", rep.Projection.ProjectedFileBytes, rep.Projection.RowsImported, len(raw))
			}
			if codes(rep.WidthWarnings) != codes(ir.WidthWarnings) {
				t.Errorf("predict width warnings %v, import %v", rep.WidthWarnings, ir.WidthWarnings)
			}
			for i := range rep.Schema.Fields {
				if rep.Schema.Fields[i].Type != ir.Schema.Fields[i].Type {
					t.Errorf("field %s: predict %s, import %s", rep.Schema.Fields[i].Name, rep.Schema.Fields[i].Type, ir.Schema.Fields[i].Type)
				}
			}
		})
	}
}

// TestImportWiden_StrictStillRefusesAGenuinelyNarrowGroup: deferring the
// strict width verdict past the pass does not lose it — a group that is
// too narrow on the FINAL widths still fails --strict, import and
// predict alike, with PULSE_GROUP_TOO_NARROW.
func TestImportWiden_StrictStillRefusesAGenuinelyNarrowGroup(t *testing.T) {
	cols, rows, _ := widenGroupFixture()
	decls := []GroupDecl{{Key: []string{"k"}, Members: []string{"qty"}}} // u16 + u4 byte: 3 bytes
	strict := func(j *ImportJob) { j.StrictDedup = true }
	_, _, _, err := runGroupImport(t, newMockReader(cols, rows), decls, strict)
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_GROUP_TOO_NARROW {
		t.Fatalf("import err = %v, want PULSE_GROUP_TOO_NARROW", err)
	}
	job := NewImportJob(newMockReader(cols, rows), "unused.pulse")
	job.Groups = decls
	strict(job)
	if _, err := job.Predict(context.Background()); !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_GROUP_TOO_NARROW {
		t.Fatalf("predict err = %v, want PULSE_GROUP_TOO_NARROW", err)
	}
}
