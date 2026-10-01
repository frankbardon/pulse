package io

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// joinFixture is a synthetic denormalised parent/child join: every line
// row repeats its customer's block (cust_id → name, region, tier) and
// its product's block (prod_id → category, price). 40 customers at ~13
// lines each, 10 products; cust_tier is null for every fifth customer,
// so a group member carries null bits. Both blocks are wider than the
// 4-byte index, so both pass the viability gate's width floor.
func joinFixture(n int) ([]string, [][]string) {
	cols := []string{"line_id", "cust_id", "cust_name", "cust_region", "cust_tier", "prod_id", "prod_cat", "prod_price", "qty"}
	regions := []string{"north", "south", "east", "west"}
	cats := []string{"widget", "gadget", "gizmo"}
	var rows [][]string
	for i := 0; i < n; i++ {
		c := (i / 13) % 40
		p := (i * 7) % 10
		tier := fmt.Sprint(1 + c%3)
		if c%5 == 0 {
			tier = ""
		}
		rows = append(rows, []string{
			fmt.Sprint(i + 1),
			fmt.Sprint(1000 + c),
			fmt.Sprintf("cust-%02d", c),
			regions[c%4],
			tier,
			fmt.Sprint(500 + p),
			cats[p%3],
			fmt.Sprintf("%d.25", 10+p),
			fmt.Sprint(1 + i%9),
		})
	}
	return cols, rows
}

var joinGroups = []GroupDecl{
	{Key: []string{"cust_id"}, Members: []string{"cust_name", "cust_region", "cust_tier"}},
	{Key: []string{"prod_id"}, Members: []string{"prod_cat", "prod_price"}},
}

// runGroupImport imports rows from src with the given declarations and
// returns the report, the file bytes and the filesystem.
func runGroupImport(t *testing.T, src Reader, groups []GroupDecl, mutate ...func(*ImportJob)) (*ImportReport, []byte, afero.Fs, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	job := NewImportJob(src, "out.pulse")
	job.FS = fs
	job.Groups = groups
	for _, m := range mutate {
		m(job)
	}
	report, err := job.Run(context.Background())
	if err != nil {
		return nil, nil, fs, err
	}
	raw, rerr := afero.ReadFile(fs, "out.pulse")
	if rerr != nil {
		t.Fatal(rerr)
	}
	return report, raw, fs, nil
}

// TestImportJob_Groups: two declared groups are each encoded with their
// own dictionary — one entry per distinct key tuple — into a 0x02 cohort
// whose logical fields, values and nulls equal the flat import's, and
// whose stride and file are smaller. The report carries each group's
// entry count and widths.
func TestImportJob_Groups(t *testing.T) {
	cols, rows := joinFixture(600)
	flatRep, flatRaw, flatFS, err := runGroupImport(t, newMockReader(cols, rows), nil)
	if err != nil {
		t.Fatal(err)
	}
	rep, raw, fs, err := runGroupImport(t, newMockReader(cols, rows), joinGroups)
	if err != nil {
		t.Fatal(err)
	}
	if raw[encoding.HeaderSize-1] != encoding.FormatVersionV2 {
		t.Fatalf("version 0x%02x, want 0x02", raw[encoding.HeaderSize-1])
	}
	schema, vals, nulls := decodeAll(t, fs, "out.pulse")
	_, flatVals, flatNulls := decodeAll(t, flatFS, "out.pulse")
	if len(schema.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(schema.Groups))
	}
	for g, want := range []int{40, 10} {
		if schema.Groups[g].Kind != encoding.GroupKindIndexed || schema.GroupEntryCount(g) != want {
			t.Fatalf("group %d: kind %d, %d entries; want indexed, %d", g, schema.Groups[g].Kind, schema.GroupEntryCount(g), want)
		}
	}
	// The key member carries the key flag; the determined members do not.
	for _, m := range schema.Groups[0].Members {
		if isKey := schema.Fields[m.Field].Name == "cust_id"; m.Key != isKey {
			t.Fatalf("member %s key flag = %v", schema.Fields[m.Field].Name, m.Key)
		}
	}
	if !reflect.DeepEqual(schema.Fields, flatRep.Schema.Fields) {
		t.Fatal("grouped cohort's logical fields differ from the flat import's")
	}
	if len(vals) != 600 || !reflect.DeepEqual(vals, flatVals) || !reflect.DeepEqual(nulls, flatNulls) {
		t.Fatalf("decoded %d records; values/nulls must equal the flat import's", len(vals))
	}
	if rep.Schema.RecordByteSize() >= flatRep.Schema.RecordByteSize() || len(raw) >= len(flatRaw) {
		t.Fatalf("stride %d / file %d not smaller than flat %d / %d",
			rep.Schema.RecordByteSize(), len(raw), flatRep.Schema.RecordByteSize(), len(flatRaw))
	}
	want := []GroupReport{
		{Label: "group 1 [key: cust_id]", Key: []string{"cust_id"}, Members: []string{"cust_name", "cust_region", "cust_tier"},
			Fields: []string{"cust_id", "cust_name", "cust_region", "cust_tier"}, EntryCount: 40,
			EntryWidth: schema.GroupEntryWidth(0), MemberRowBytes: schema.GroupMemberRowBytes(0), IndexWidth: 4},
		{Label: "group 2 [key: prod_id]", Key: []string{"prod_id"}, Members: []string{"prod_cat", "prod_price"},
			Fields: []string{"prod_id", "prod_cat", "prod_price"}, EntryCount: 10,
			EntryWidth: schema.GroupEntryWidth(1), MemberRowBytes: schema.GroupMemberRowBytes(1), IndexWidth: 4},
	}
	// Both groups pass the viability gate: the report carries the
	// measured numbers, and there is no warning.
	for g, rows := range []int64{600, 600} {
		v := encoding.AssessGroup(schema, g, rows, encoding.DefaultDedupRatioFloor)
		want[g].Verdict = encoding.GroupVerdictAdmitted
		want[g].DictionaryBytes = v.DictionaryBytes
		want[g].Ratio = v.Ratio
		want[g].BreakEvenRatio = v.BreakEvenRatio
		want[g].RatioFloor = encoding.DefaultDedupRatioFloor
		want[g].ByteDelta = v.ByteDelta
	}
	if want[0].Ratio != 15 || want[1].Ratio != 60 || want[0].ByteDelta >= 0 || len(rep.GroupWarnings) != 0 {
		t.Fatalf("ratios %v/%v, delta %d, warnings %v", want[0].Ratio, want[1].Ratio, want[0].ByteDelta, rep.GroupWarnings)
	}
	if !reflect.DeepEqual(rep.Groups, want) {
		t.Fatalf("report groups = %+v\nwant %+v", rep.Groups, want)
	}
	// cust_tier is nullable, so group 0's entry carries a member bitmap
	// byte beyond its member bytes; group 1's does not.
	if want[0].EntryWidth != want[0].MemberRowBytes+1 || want[1].EntryWidth != want[1].MemberRowBytes {
		t.Fatalf("entry widths %d/%d vs member bytes %d/%d", want[0].EntryWidth, want[1].EntryWidth, want[0].MemberRowBytes, want[1].MemberRowBytes)
	}
}

// TestImportJob_Groups_RoundTrip: export of the grouped cohort
// reproduces the source table row for row, cell for cell, exactly as
// the flat cohort's export does.
func TestImportJob_Groups_RoundTrip(t *testing.T) {
	cols, rows := joinFixture(600)
	export := func(groups []GroupDecl) *collectWriter {
		_, _, fs, err := runGroupImport(t, newMockReader(cols, rows), groups)
		if err != nil {
			t.Fatal(err)
		}
		w := &collectWriter{}
		ej := NewExportJob("out.pulse", w)
		ej.FS = fs
		if _, err := ej.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		return w
	}
	flat, grouped := export(nil), export(joinGroups)
	if !reflect.DeepEqual(grouped.header, cols) || !reflect.DeepEqual(grouped.rows, flat.rows) {
		t.Fatal("grouped export differs from the flat export")
	}
	if len(grouped.rows) != len(rows) {
		t.Fatalf("exported %d rows, want %d", len(grouped.rows), len(rows))
	}
	for i, r := range grouped.rows {
		for c, v := range r {
			got := ""
			if v != nil {
				got = fmt.Sprint(v)
			}
			if got != rows[i][c] {
				t.Fatalf("row %d col %s: exported %q, source %q", i, cols[c], got, rows[i][c])
			}
		}
	}
}

// TestImportJob_Groups_NoneIsByteIdentical: no declaration and an empty
// declaration list both write the 0x01 cohort, byte for byte.
func TestImportJob_Groups_NoneIsByteIdentical(t *testing.T) {
	cols, rows := joinFixture(600)
	_, base, _, err := runGroupImport(t, newMockReader(cols, rows), nil)
	if err != nil {
		t.Fatal(err)
	}
	rep, empty, _, err := runGroupImport(t, newMockReader(cols, rows), []GroupDecl{})
	if err != nil {
		t.Fatal(err)
	}
	if base[encoding.HeaderSize-1] != encoding.FormatVersion || !bytes.Equal(base, empty) || rep.Groups != nil {
		t.Fatalf("empty declaration list changed the cohort (version 0x%02x, %d vs %d bytes, groups %v)",
			base[encoding.HeaderSize-1], len(base), len(empty), rep.Groups)
	}
}

// TestImportJob_Groups_DeclarationErrors: a field in two groups, an
// unknown field and a key that is also listed as a member are refused
// with their own codes before any cohort is written.
func TestImportJob_Groups_DeclarationErrors(t *testing.T) {
	cols, rows := joinFixture(100)
	for _, tc := range []struct {
		name    string
		groups  []GroupDecl
		code    perrors.Code
		details map[string]any
	}{
		{"conflict", []GroupDecl{
			{Key: []string{"cust_id"}, Members: []string{"cust_name"}},
			{Key: []string{"prod_id"}, Members: []string{"prod_cat", "cust_name"}},
		}, perrors.PULSE_GROUP_FIELD_CONFLICT, map[string]any{
			"field": "cust_name", "group_labels": []string{"group 1 [key: cust_id]", "group 2 [key: prod_id]"},
		}},
		{"unknown", []GroupDecl{{Key: []string{"cust_id"}, Members: []string{"cust_nmae"}}},
			perrors.PULSE_GROUP_FIELD_UNKNOWN, map[string]any{"field": "cust_nmae"}},
		{"twice", []GroupDecl{{Key: []string{"cust_id"}, Members: []string{"cust_id"}}},
			perrors.PULSE_GROUP_DECLARATION_INVALID, map[string]any{"field": "cust_id"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := newMockReader(cols, rows)
			_, _, fs, err := runGroupImport(t, src, tc.groups)
			var ce *perrors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			for k, v := range tc.details {
				if !reflect.DeepEqual(ce.Details[k], v) {
					t.Fatalf("details[%s] = %v, want %v", k, ce.Details[k], v)
				}
			}
			if ok, _ := afero.Exists(fs, "out.pulse"); ok {
				t.Fatal("a refused declaration left a cohort behind")
			}
			// Fails fast: the refusal comes before the row pass, so the
			// source is read no further than the inference sample.
			if src.pos != 0 {
				t.Fatalf("row pass ran (reader at %d) before a declaration error", src.pos)
			}
		})
	}
}

// TestImportJob_Groups_KeyViolation: a declared group whose member is
// NOT constant within its key is refused — naming the member, the
// record index and the SOURCE row (which skips an earlier row error) —
// never silently accepted as a bigger dictionary.
func TestImportJob_Groups_KeyViolation(t *testing.T) {
	cols := []string{"line_id", "cust_id", "cust_name", "qty"}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "line_id", Type: encoding.FieldTypeU16, CsvColumnIdx: 0},
		{Name: "cust_id", Type: encoding.FieldTypeU32, CsvColumnIdx: 1}, // u32 + u8: wider than the index
		{Name: "cust_name", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: 2},
		{Name: "qty", Type: encoding.FieldTypeU8, CsvColumnIdx: 3},
	}}
	rows := [][]string{
		{"1", "7", "acme", "1"},
		{"2", "7", "acme", "not-a-number"}, // row error: source row 2 is skipped
		{"3", "8", "bolt", "2"},
		{"4", "7", "acme", "3"},
		{"5", "7", "acme-renamed", "4"}, // record 3, source row 5
		{"6", "8", "bolt", "bad"},       // a LATER row error must not shift it
	}
	_, _, fs, err := runGroupImport(t, newMockReader(cols, rows),
		[]GroupDecl{{Key: []string{"cust_id"}, Members: []string{"cust_name"}}},
		func(j *ImportJob) { j.Schema = schema })
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_GROUP_MEMBER_NOT_CONSTANT {
		t.Fatalf("err = %v, want PULSE_GROUP_MEMBER_NOT_CONSTANT", err)
	}
	if ce.Details["field"] != "cust_name" || ce.Details["row"] != int64(3) || ce.Details["source_row"] != 5 ||
		ce.Details["group_label"] != "group 1 [key: cust_id]" {
		t.Fatalf("details = %v, want field cust_name, row 3, source_row 5", ce.Details)
	}
	if ok, _ := afero.Exists(fs, "out.pulse"); ok {
		t.Fatal("a refused group left a cohort behind")
	}

	// The same rows as a plain tuple group (no key) are legitimate: two
	// distinct (cust_id, cust_name) tuples for customer 7.
	rep, _, _, err := runGroupImport(t, newMockReader(cols, rows),
		[]GroupDecl{{Members: []string{"cust_id", "cust_name"}}},
		func(j *ImportJob) { j.Schema = schema })
	if err != nil || rep.Groups[0].EntryCount != 3 {
		t.Fatalf("tuple group: err %v, report %+v; want 3 entries", err, rep)
	}
}

// TestImportJob_Groups_EntriesExhausted: outgrowing the u32 index space
// (shrunk here) is a coded error, never a wraparound.
func TestImportJob_Groups_EntriesExhausted(t *testing.T) {
	old := encoding.MaxGroupEntries
	encoding.MaxGroupEntries = 39
	t.Cleanup(func() { encoding.MaxGroupEntries = old })
	cols, rows := joinFixture(600)
	_, _, _, err := runGroupImport(t, newMockReader(cols, rows), joinGroups)
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_GROUP_ENTRIES_EXHAUSTED || ce.Details["max_entries"] != uint64(39) {
		t.Fatalf("err = %v, want PULSE_GROUP_ENTRIES_EXHAUSTED at 39", err)
	}
}

// TestImportJob_Groups_WithElision: declared groups and constant
// elision compose — a constant field that is a declared member stays in
// its group, a constant field outside every group is elided into a
// trailing constant group, and the cohort still decodes to the flat one.
func TestImportJob_Groups_WithElision(t *testing.T) {
	cols, rows := joinFixture(600)
	cols = append(cols, "batch", "src")
	for i := range rows {
		rows[i] = append(rows[i], "b-1", "feed")
	}
	decls := []GroupDecl{{Key: []string{"cust_id"}, Members: []string{"cust_name", "cust_region", "src"}}}
	elide := func(j *ImportJob) { j.ElideConstants = true }
	rep, _, fs, err := runGroupImport(t, newMockReader(cols, rows), decls, elide)
	if err != nil {
		t.Fatal(err)
	}
	_, _, flatFS, err := runGroupImport(t, newMockReader(cols, rows), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep.ElidedConstants, []string{"batch"}) {
		t.Fatalf("elided %v, want [batch] (src is a declared member)", rep.ElidedConstants)
	}
	schema, vals, nulls := decodeAll(t, fs, "out.pulse")
	_, flatVals, flatNulls := decodeAll(t, flatFS, "out.pulse")
	if len(schema.Groups) != 2 || schema.Groups[0].Kind != encoding.GroupKindIndexed || schema.Groups[1].Kind != encoding.GroupKindConstant {
		t.Fatalf("groups = %+v, want declared indexed group then the constant group", schema.Groups)
	}
	if len(rep.Groups) != 1 || rep.Groups[0].EntryCount != 40 {
		t.Fatalf("report groups = %+v", rep.Groups)
	}
	if !reflect.DeepEqual(vals, flatVals) || !reflect.DeepEqual(nulls, flatNulls) {
		t.Fatal("grouped+elided cohort decodes differently from the flat import")
	}
}

// TestImportJob_Groups_AuthoritativeSchema: a SchemaAwareReader source
// (no inference pass at all) takes group declarations exactly as an
// inferred import does.
func TestImportJob_Groups_AuthoritativeSchema(t *testing.T) {
	src := func() *authoritativeReader {
		return &authoritativeReader{
			columns: []string{"id", "parent", "label"},
			rows:    [][]string{{"1", "10", "A"}, {"2", "10", "A"}, {"3", "11", "B"}, {"4", "11", "B"}, {"5", "10", "A"}},
			schema: &encoding.Schema{Fields: []encoding.Field{
				{Name: "id", Type: encoding.FieldTypeF64, CsvColumnIdx: 0},
				{Name: "parent", Type: encoding.FieldTypeU32, CsvColumnIdx: 1},
				{Name: "label", Type: encoding.FieldTypeCategoricalU16, CsvColumnIdx: 2, Dictionary: encoding.NewDictionary()},
			}},
		}
	}
	decl := []GroupDecl{{Key: []string{"parent"}, Members: []string{"label"}}}
	rep, _, fs, err := runGroupImport(t, src(), decl)
	if err != nil {
		t.Fatal(err)
	}
	_, _, flatFS, err := runGroupImport(t, src(), nil)
	if err != nil {
		t.Fatal(err)
	}
	schema, vals, _ := decodeAll(t, fs, "out.pulse")
	_, flatVals, _ := decodeAll(t, flatFS, "out.pulse")
	if schema.Fields[0].Type != encoding.FieldTypeF64 || len(schema.Groups) != 1 || rep.Groups[0].EntryCount != 2 {
		t.Fatalf("authoritative import: types/groups not honoured (%+v)", rep.Groups)
	}
	if !reflect.DeepEqual(vals, flatVals) {
		t.Fatal("grouped authoritative import decodes differently")
	}
	if _, _, _, err := runGroupImport(t, src(), []GroupDecl{{Members: []string{"nope"}}}); !perrors.HasCode(err, perrors.PULSE_GROUP_FIELD_UNKNOWN) {
		t.Fatalf("unknown field on the authoritative arm: err = %v", err)
	}
}

// TestParseGroupDecl: the CLI declaration syntax.
func TestParseGroupDecl(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want GroupDecl
		ok   bool
	}{
		{"cust_id:cust_name,region", GroupDecl{Key: []string{"cust_id"}, Members: []string{"cust_name", "region"}}, true},
		{"a, b : c ,d", GroupDecl{Key: []string{"a", "b"}, Members: []string{"c", "d"}}, true},
		{"x,y", GroupDecl{Members: []string{"x", "y"}}, true},
		{"x", GroupDecl{Members: []string{"x"}}, true},
		{"", GroupDecl{}, false},
		{"a:", GroupDecl{}, false},
		{":b", GroupDecl{}, false},
		{"a,,b", GroupDecl{}, false},
		{"a:b:c", GroupDecl{}, false},
	} {
		got, err := ParseGroupDecl(tc.in)
		if tc.ok != (err == nil) || (tc.ok && !reflect.DeepEqual(got, tc.want)) {
			t.Fatalf("ParseGroupDecl(%q) = %+v, %v", tc.in, got, err)
		}
		if !tc.ok && !perrors.HasCode(err, perrors.PULSE_GROUP_DECLARATION_INVALID) {
			t.Fatalf("ParseGroupDecl(%q): err %v, want PULSE_GROUP_DECLARATION_INVALID", tc.in, err)
		}
	}
}
