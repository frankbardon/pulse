package pulse

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// groupedRowCount is large enough that eliding two constant columns
// saves more than the constant group's schema-block growth.
const groupedRowCount = 120

// groupedSchema is a denormalised orders × customers schema laid out
// canonically (import writes the layout it is given): cust determines
// name / region / score (region nullable, so a member null bit rides
// the group entry), amount is nullable, source and version are constant
// on every row.
func groupedSchema() encoding.Schema {
	fields := []encoding.Field{
		{Name: "order", Type: encoding.FieldTypeU32},
		{Name: "cust", Type: encoding.FieldTypeU32},
		{Name: "name", Type: encoding.FieldTypeCategoricalU8},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Nullable: true},
		{Name: "score", Type: encoding.FieldTypeF64},
		{Name: "amount", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "source", Type: encoding.FieldTypeCategoricalU8},
		{Name: "version", Type: encoding.FieldTypeU16},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Grouped column " + fields[i].Name + " (builder vs import)."
		off += fields[i].Type.ByteSize()
	}
	return encoding.Schema{Fields: fields}
}

// groupedData returns the same rows as CohortRows and as CSV text.
func groupedData() ([]CohortRow, string) {
	names := []string{"ada", "bo", "cy", "di"}
	regions := []any{"north", nil, "south", "north"}
	var csv strings.Builder
	csv.WriteString("order,cust,name,region,score,amount,source,version\n")
	rows := make([]CohortRow, groupedRowCount)
	for i := range rows {
		c := i % len(names)
		score := 1.5 + float64(c)*0.25
		var amount any = float64(i) * 1.25
		if i%7 == 3 {
			amount = nil
		}
		rows[i] = CohortRow{uint64(i + 1), uint64(100 + c), names[c], regions[c], score, amount, "web", uint64(3)}
		region, amt := "", ""
		if regions[c] != nil {
			region = regions[c].(string)
		}
		if amount != nil {
			amt = strconv.FormatFloat(amount.(float64), 'g', -1, 64)
		}
		fmt.Fprintf(&csv, "%d,%d,%s,%s,%s,%s,web,3\n", i+1, 100+c, names[c], region,
			strconv.FormatFloat(score, 'g', -1, 64), amt)
	}
	return rows, csv.String()
}

var custGroup = pio.GroupDecl{Key: []string{"cust"}, Members: []string{"name", "region", "score"}}

// importGrouped imports groupedData's CSV with the explicit grouped
// schema and the given group / elide options.
func importGrouped(t *testing.T, p *Pulse, path string, groups []pio.GroupDecl, elide bool) []byte {
	t.Helper()
	_, csv := groupedData()
	src, err := pio.NewReaderFromBytes(pio.FormatCSV, []byte(csv), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := groupedSchema()
	job := pio.NewImportJob(src, path)
	job.Schema = &s
	job.Groups = groups
	job.ElideConstants = elide
	rep, err := p.Import(context.Background(), job)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if rep.RowsImported != groupedRowCount || len(rep.RowErrors) != 0 {
		t.Fatalf("import report = %+v", rep)
	}
	return readFile(t, p.fsys, path)
}

// TestCohortBuilder_GroupedByteIdenticalToImport: a grouped build, an
// elided build and a grouped + elided build are each byte-identical to
// `pulse import --group` / `--elide-constants` of the same rows with
// the same explicit schema; each is format 0x02 and reports what it
// formed.
func TestCohortBuilder_GroupedByteIdenticalToImport(t *testing.T) {
	for _, tc := range []struct {
		name       string
		groups     []pio.GroupDecl
		elide      bool
		wantGroups int
		wantElided []string
	}{
		{"group", []pio.GroupDecl{custGroup}, false, 1, nil},
		{"elide", nil, true, 1, []string{"source", "version"}},
		{"group+elide", []pio.GroupDecl{custGroup}, true, 2, []string{"source", "version"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, fsys := memBuilderEngine(t)
			imported := importGrouped(t, p, "imported.pulse", tc.groups, tc.elide)
			rows, _ := groupedData()
			res := buildCohort(t, p, "built.pulse", groupedSchema(),
				CohortBuilderOptions{Groups: tc.groups, ElideConstants: tc.elide}, rows)
			built := readFile(t, fsys, "built.pulse")
			if !bytes.Equal(built, imported) {
				t.Fatalf("built (%d bytes) != imported (%d bytes)", len(built), len(imported))
			}
			if res.FormatVersion != encoding.FormatVersionV2 || len(res.Schema.Groups) != tc.wantGroups {
				t.Fatalf("format %#x with %d groups, want 0x02 with %d", res.FormatVersion, len(res.Schema.Groups), tc.wantGroups)
			}
			if !reflect.DeepEqual(res.ElidedConstants, tc.wantElided) {
				t.Fatalf("elided = %v, want %v", res.ElidedConstants, tc.wantElided)
			}
			if len(tc.groups) > 0 && (len(res.Groups) != 1 || res.Groups[0].Verdict != "admitted") {
				t.Fatalf("group reports = %+v", res.Groups)
			}
			if len(res.Warnings) != 0 {
				t.Fatalf("unexpected warnings: %v", res.Warnings)
			}
			if files := listFiles(t, fsys, "/"); strings.Join(files, ",") != "built.pulse,imported.pulse" {
				t.Fatalf("build left %v", files)
			}
		})
	}
}

// TestCohortBuilder_GroupedReadBack: grouped + elided output reads back
// through CohortReader exactly as the appended rows and as its
// ungrouped twin, and Process returns the twin's result.
func TestCohortBuilder_GroupedReadBack(t *testing.T) {
	p, _ := memBuilderEngine(t)
	rows, _ := groupedData()
	buildCohort(t, p, "flat.pulse", groupedSchema(), CohortBuilderOptions{}, rows)
	buildCohort(t, p, "grouped.pulse", groupedSchema(),
		CohortBuilderOptions{Groups: []pio.GroupDecl{custGroup}, ElideConstants: true}, rows)

	flat, grouped := openReader(t, p, "flat.pulse"), openReader(t, p, "grouped.pulse")
	if grouped.Len() != int64(len(rows)) || flat.Len() != grouped.Len() {
		t.Fatalf("Len grouped %d, flat %d, want %d", grouped.Len(), flat.Len(), len(rows))
	}
	for i := range rows {
		g, err := grouped.RecordAt(int64(i))
		if err != nil {
			t.Fatal(err)
		}
		f, err := flat.RecordAt(int64(i))
		if err != nil {
			t.Fatal(err)
		}
		assertRowEqual(t, fmt.Sprintf("row %d vs input", i), grouped.Schema(), g, rows[i])
		assertRowEqual(t, fmt.Sprintf("row %d vs twin", i), grouped.Schema(), g, f)
	}

	run := func(path string) []map[string]any {
		resp, err := p.Process(context.Background(), &Request{
			Cohort:       &types.Cohort{Filename: path},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "name"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "sum"}, {Type: types.AGG_COUNT, Field: "region", Label: "n"}},
		})
		if err != nil {
			t.Fatalf("Process(%s): %v", path, err)
		}
		return resp.Data
	}
	if g, f := run("grouped.pulse"), run("flat.pulse"); len(g) != 4 || !reflect.DeepEqual(g, f) {
		t.Fatalf("Process over grouped = %v, flat twin = %v", g, f)
	}
}

// TestCohortBuilder_GroupGate: a too-narrow group is dropped with
// PULSE_GROUP_TOO_NARROW and the cohort is the ungrouped 0x01 file
// (byte-identical to the build with no group); a low-ratio group is
// written with PULSE_DEDUP_LOW_RATIO. Under Strict each finding is the
// error — the narrow one at NewCohortBuilder, the ratio one at Close —
// and nothing is left on disk.
func TestCohortBuilder_GroupGate(t *testing.T) {
	rows, _ := groupedData()
	narrow := []pio.GroupDecl{{Members: []string{"region"}}}
	unique := []pio.GroupDecl{{Key: []string{"order"}, Members: []string{"amount"}}}

	t.Run("narrow dropped", func(t *testing.T) {
		p, fsys := memBuilderEngine(t)
		buildCohort(t, p, "plain.pulse", groupedSchema(), CohortBuilderOptions{}, rows)
		res := buildCohort(t, p, "narrow.pulse", groupedSchema(), CohortBuilderOptions{Groups: narrow}, rows)
		if res.FormatVersion != encoding.FormatVersionV1 || len(res.Schema.Groups) != 0 {
			t.Fatalf("format %#x with %d groups, want ungrouped 0x01", res.FormatVersion, len(res.Schema.Groups))
		}
		if len(res.Warnings) != 1 || !errors.HasCode(res.Warnings[0], errors.PULSE_GROUP_TOO_NARROW) {
			t.Fatalf("warnings = %v, want one PULSE_GROUP_TOO_NARROW", res.Warnings)
		}
		if len(res.Groups) != 1 || res.Groups[0].Verdict != "dropped_too_narrow" {
			t.Fatalf("group reports = %+v", res.Groups)
		}
		if !bytes.Equal(readFile(t, fsys, "narrow.pulse"), readFile(t, fsys, "plain.pulse")) {
			t.Fatal("a dropped group changed the cohort bytes")
		}
	})
	t.Run("narrow strict", func(t *testing.T) {
		p, fsys := memBuilderEngine(t)
		_, err := p.NewCohortBuilder(context.Background(), "x.pulse", groupedSchema(), CohortBuilderOptions{Groups: narrow, Strict: true})
		if !errors.HasCode(err, errors.PULSE_GROUP_TOO_NARROW) {
			t.Fatalf("err = %v, want PULSE_GROUP_TOO_NARROW", err)
		}
		if files := listFiles(t, fsys, "/"); len(files) != 0 {
			t.Fatalf("strict refusal left %v", files)
		}
	})
	t.Run("low ratio", func(t *testing.T) {
		p, fsys := memBuilderEngine(t)
		res := buildCohort(t, p, "low.pulse", groupedSchema(), CohortBuilderOptions{Groups: unique}, rows)
		if res.FormatVersion != encoding.FormatVersionV2 {
			t.Fatalf("format %#x, want 0x02 (a low-ratio group is still written)", res.FormatVersion)
		}
		if len(res.Warnings) != 1 || !errors.HasCode(res.Warnings[0], errors.PULSE_DEDUP_LOW_RATIO) {
			t.Fatalf("warnings = %v, want one PULSE_DEDUP_LOW_RATIO", res.Warnings)
		}
		if len(res.Groups) != 1 || res.Groups[0].Verdict != "low_ratio" {
			t.Fatalf("group reports = %+v", res.Groups)
		}
		// Import's numbers and bytes agree.
		imported := importGrouped(t, p, "imp.pulse", unique, false)
		if !bytes.Equal(readFile(t, fsys, "low.pulse"), imported) {
			t.Fatal("low-ratio build differs from the import")
		}
	})
	t.Run("low ratio strict", func(t *testing.T) {
		p, fsys := memBuilderEngine(t)
		b, err := p.NewCohortBuilder(context.Background(), "x.pulse", groupedSchema(), CohortBuilderOptions{Groups: unique, Strict: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if err := b.Append(r); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := b.Close(); !errors.HasCode(err, errors.PULSE_DEDUP_LOW_RATIO) {
			t.Fatalf("Close = %v, want PULSE_DEDUP_LOW_RATIO", err)
		}
		if files := listFiles(t, fsys, "/"); len(files) != 0 {
			t.Fatalf("strict refusal left %v", files)
		}
	})
	t.Run("ratio floor", func(t *testing.T) {
		p, _ := memBuilderEngine(t)
		res := buildCohort(t, p, "f.pulse", groupedSchema(), CohortBuilderOptions{Groups: []pio.GroupDecl{custGroup}, RatioFloor: 1000}, rows)
		if len(res.Warnings) != 1 || !errors.HasCode(res.Warnings[0], errors.PULSE_DEDUP_LOW_RATIO) {
			t.Fatalf("warnings = %v, want PULSE_DEDUP_LOW_RATIO under a 1000 floor", res.Warnings)
		}
	})
}

// TestCohortBuilder_GroupDeclarationErrors: a declaration naming an
// unknown field fails NewCohortBuilder with import's PULSE_GROUP_* code;
// a key that does not determine its members fails Close with
// PULSE_GROUP_MEMBER_NOT_CONSTANT whose source_row is the Append call
// (rejected calls counted) — and leaves nothing on disk.
func TestCohortBuilder_GroupDeclarationErrors(t *testing.T) {
	p, fsys := memBuilderEngine(t)
	_, err := p.NewCohortBuilder(context.Background(), "x.pulse", groupedSchema(),
		CohortBuilderOptions{Groups: []pio.GroupDecl{{Key: []string{"cust"}, Members: []string{"nope"}}}})
	if !errors.HasCode(err, errors.PULSE_GROUP_FIELD_UNKNOWN) {
		t.Fatalf("err = %v, want PULSE_GROUP_FIELD_UNKNOWN", err)
	}

	b, err := p.NewCohortBuilder(context.Background(), "x.pulse", groupedSchema(),
		CohortBuilderOptions{Groups: []pio.GroupDecl{custGroup}})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := groupedData()
	if err := b.Append(CohortRow{"bad"}); err == nil { // call 1: rejected
		t.Fatal("arity row accepted")
	}
	for _, r := range rows[:4] { // calls 2..5
		if err := b.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	clash := append(CohortRow(nil), rows[0]...)
	clash[2] = "bo"                         // cust 100 already named "ada"
	if err := b.Append(clash); err != nil { // call 6
		t.Fatal(err)
	}
	_, err = b.Close()
	if !errors.HasCode(err, errors.PULSE_GROUP_MEMBER_NOT_CONSTANT) {
		t.Fatalf("Close = %v, want PULSE_GROUP_MEMBER_NOT_CONSTANT", err)
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Details["source_row"] != 6 {
		t.Fatalf("details = %v, want source_row 6", ce.Details)
	}
	if files := listFiles(t, fsys, "/"); len(files) != 0 {
		t.Fatalf("failed Close left %v", files)
	}
}

// TestCohortBuilder_GroupedFailedCloseLeavesNothing: a publish failure
// after the physical spool exists removes it with everything else.
func TestCohortBuilder_GroupedFailedCloseLeavesNothing(t *testing.T) {
	mem := afero.NewMemMapFs()
	p, err := New(Options{FS: renameFailFs{mem}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), "out.pulse", groupedSchema(),
		CohortBuilderOptions{Groups: []pio.GroupDecl{custGroup}, ElideConstants: true})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := groupedData()
	for _, r := range rows {
		if err := b.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err == nil {
		t.Fatal("Close succeeded through an injected rename failure")
	}
	if files := listFiles(t, mem, "/"); len(files) != 0 {
		t.Fatalf("failed Close left %v", files)
	}
}
