package io

import (
	"context"
	stderrors "errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
)

// orderLinesFixture is a synthetic denormalised order-line export: every
// line repeats its customer's attributes and its product's attributes.
//
//	line_id                     unique per row
//	cust_id → cust_name, cust_tier, cust_score   ~12 lines per customer, rows sorted by customer
//	cust_flag                   constant within cust_id for the first 11,000 rows, varies after
//	prod_id → prod_cat, prod_price               50 products scattered across rows
//	qty                         child-level, depends on nothing
//	channel                     one value on every row (an elision candidate, never a member)
//
// cust_score is blank (null) for customers ≥ 100 — well past the
// 500-row inference sample — so an inferred import promotes it to
// nullable mid-pass. The row count clears the 10,000-row nomination
// window so the late cust_flag break lands on the full pass only.
// cust_name outgrows the categorical_u8 the 500-row sample infers (the
// 257th customer is row 3,073), so an inferred import promotes it to
// categorical_u16 mid-pass (PULSE_IMPORT_WIDTH_PROMOTED).
func orderLinesFixture(n int) ([]string, [][]string) {
	cols := []string{"line_id", "cust_id", "cust_name", "cust_tier", "cust_score", "cust_flag", "prod_id", "prod_cat", "prod_price", "qty", "channel"}
	var rows [][]string
	for i := 0; i < n; i++ {
		c := i / 12
		p := (i * 7) % 50
		score := fmt.Sprintf("%d.5", 1000+c)
		if c >= 100 && c%5 == 0 {
			score = ""
		}
		flag := c % 2
		if i >= 11000 {
			flag = (c + i) % 2
		}
		rows = append(rows, []string{
			fmt.Sprint(i),
			fmt.Sprint(1000 + c), fmt.Sprintf("customer-%04d", c), fmt.Sprintf("tier-%d", c%3), score, fmt.Sprint(flag),
			fmt.Sprint(p), fmt.Sprintf("cat-%02d", p%9), fmt.Sprintf("%d.25", 10+p),
			fmt.Sprint(1 + (i*31)%9),
			"web",
		})
	}
	return cols, rows
}

func predictJob(t *testing.T, src Reader, mutate ...func(*ImportJob)) (*PredictReport, error) {
	t.Helper()
	job := NewImportJob(src, "unused.pulse")
	for _, m := range mutate {
		m(job)
	}
	return job.Predict(context.Background())
}

func suggest(j *ImportJob) { j.SuggestGroups = true }

func candidateByKey(t *testing.T, d *GroupDetection, key string) GroupCandidate {
	t.Helper()
	for _, c := range d.Candidates {
		if len(c.Key) == 1 && c.Key[0] == key {
			return c
		}
	}
	t.Fatalf("no candidate keyed on %q in %+v", key, d.Candidates)
	return GroupCandidate{}
}

// TestImportPredict_SuggestGroups_FindsJoinParents: detection finds both
// independent parents of a join-shaped source, with exactly the fields
// each key determines; drops the member that holds across the window but
// breaks later; never nominates the global constant or a child-level
// column; and suggests both as a non-overlapping, ready-to-paste set.
func TestImportPredict_SuggestGroups_FindsJoinParents(t *testing.T) {
	cols, rows := orderLinesFixture(14000)
	rep, err := predictJob(t, newMockReader(cols, rows), suggest)
	if err != nil {
		t.Fatal(err)
	}
	d := rep.GroupCandidates
	if d == nil {
		t.Fatal("GroupCandidates is nil with SuggestGroups set")
	}
	if d.WindowRows != detectWindowMaxRows || d.Rows != 14000 {
		t.Fatalf("window %d rows %d, want %d / 14000", d.WindowRows, d.Rows, detectWindowMaxRows)
	}

	cust := candidateByKey(t, d, "cust_id")
	if want := []string{"cust_name", "cust_tier", "cust_score"}; !reflect.DeepEqual(cust.Members, want) {
		t.Errorf("cust_id members = %v, want %v", cust.Members, want)
	}
	if want := []string{"cust_flag"}; !reflect.DeepEqual(cust.RejectedMembers, want) {
		t.Errorf("cust_id rejected = %v, want %v (constant in the window, varies after)", cust.RejectedMembers, want)
	}
	if cust.Declaration != "cust_id:cust_name,cust_tier,cust_score" || !cust.Suggested {
		t.Errorf("cust_id declaration %q suggested %v", cust.Declaration, cust.Suggested)
	}
	prod := candidateByKey(t, d, "prod_id")
	if want := []string{"prod_cat", "prod_price"}; !reflect.DeepEqual(prod.Members, want) || !prod.Suggested {
		t.Errorf("prod_id members = %v suggested %v", prod.Members, prod.Suggested)
	}
	if prod.EntryCount != 50 || prod.Ratio != 280 {
		t.Errorf("prod_id entry_count %d ratio %v, want 50 / 280", prod.EntryCount, prod.Ratio)
	}
	if len(d.Suggested) != 2 {
		t.Fatalf("suggested = %v, want the two parents", d.Suggested)
	}
	for _, c := range d.Candidates {
		for _, f := range append(append([]string(nil), c.Key...), c.Members...) {
			if f == "channel" || f == "line_id" {
				t.Errorf("%s names %q: a constant / unique column is never a key or member", c.Label, f)
			}
		}
		if c.Suggested && c.Verdict != encoding.GroupVerdictAdmitted {
			t.Errorf("%s suggested with verdict %s", c.Label, c.Verdict)
		}
	}
	// The equivalent key (cust_name is 1:1 with cust_id) is one
	// candidate, not two.
	for _, c := range d.Candidates {
		if c.Key[0] == "cust_name" {
			t.Errorf("equivalent key cust_name nominated separately: %+v", c)
		}
	}
}

// TestImportPredict_SuggestedMatchesImport: every figure a suggested
// candidate reports is what importing with that declaration produces —
// the gate's numbers, the exact file size — and the projection's flat
// size is the flat import's. Covers a member promoted to nullable past
// the inference sample, and one (cust_name) promoted past its inferred
// categorical_u8 width — which makes predict re-measure from the top.
func TestImportPredict_SuggestedMatchesImport(t *testing.T) {
	cols, rows := orderLinesFixture(14000)
	rep, err := predictJob(t, newMockReader(cols, rows), suggest)
	if err != nil {
		t.Fatal(err)
	}
	flatRep, flat, _, err := runGroupImport(t, newMockReader(cols, rows), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Projection.FlatFileBytes; got != int64(len(flat)) {
		t.Errorf("flat_file_bytes %d, flat import wrote %d", got, len(flat))
	}
	// The width promotion the sample could not foresee: predict reports
	// the promoted schema and the same warning the import raises.
	if len(rep.WidthWarnings) != 1 || !reflect.DeepEqual(rep.WidthWarnings[0].Details, map[string]any{
		"field": "cust_name", "from": "categorical_u8", "to": "categorical_u16", "source_row": 3073,
	}) {
		t.Fatalf("predict width warnings = %v", rep.WidthWarnings)
	}
	if !reflect.DeepEqual(rep.WidthWarnings, flatRep.WidthWarnings) {
		t.Errorf("predict width warnings %v, import %v", rep.WidthWarnings, flatRep.WidthWarnings)
	}
	for i := range rep.Schema.Fields {
		if p, w := rep.Schema.Fields[i], flatRep.Schema.Fields[i]; p.Type != w.Type || p.Nullable != w.Nullable || p.ByteOffset != w.ByteOffset {
			t.Errorf("field %s: predict %s/%v/@%d, import %s/%v/@%d", p.Name, p.Type, p.Nullable, p.ByteOffset, w.Type, w.Nullable, w.ByteOffset)
		}
	}
	promoted := false
	for _, f := range rep.Schema.Fields {
		promoted = promoted || (f.Name == "cust_score" && f.Nullable)
	}
	if !promoted {
		t.Fatal("fixture did not promote cust_score past the inference sample")
	}
	nullWarns := 0
	for _, w := range rep.Warnings {
		if w.Column == "cust_score" {
			nullWarns++
		}
	}
	if nullWarns != 1 {
		t.Errorf("cust_score null-promotion warnings = %d, want exactly 1 across the re-measure", nullWarns)
	}
	if rep.Projection.RowsImported != 14000 || rep.EstimatedRows != 14000 {
		t.Errorf("rows imported %d estimated %d", rep.Projection.RowsImported, rep.EstimatedRows)
	}
	for _, s := range rep.GroupCandidates.Suggested {
		decl, err := ParseGroupDecl(s)
		if err != nil {
			t.Fatal(err)
		}
		c := candidateByKey(t, rep.GroupCandidates, decl.Key[0])
		ir, raw, _, err := runGroupImport(t, newMockReader(cols, rows), []GroupDecl{decl})
		if err != nil {
			t.Fatalf("import with suggested %q: %v", s, err)
		}
		g := ir.Groups[0]
		got := [...]any{c.Verdict, c.EntryCount, c.EntryWidth, c.MemberRowBytes, c.IndexWidth, c.DictionaryBytes, c.Ratio, c.BreakEvenRatio, c.RatioFloor, c.ByteDelta}
		want := [...]any{g.Verdict, g.EntryCount, g.EntryWidth, g.MemberRowBytes, g.IndexWidth, g.DictionaryBytes, g.Ratio, g.BreakEvenRatio, g.RatioFloor, g.ByteDelta}
		if got != want {
			t.Errorf("%s: predicted %v, import reported %v", s, got, want)
		}
		if c.ProjectedFileBytes != int64(len(raw)) {
			t.Errorf("%s: projected %d bytes, import wrote %d", s, c.ProjectedFileBytes, len(raw))
		}
	}
}

// TestImportPredict_DeclaredGroupsMatchImport: predict with declared
// groups reports what Run reports — per-group verdicts and numbers,
// warning codes, and the exact size of the file Run writes — including
// a dropped, a low-ratio and a file-growing group, with and without
// constant elision.
func TestImportPredict_DeclaredGroupsMatchImport(t *testing.T) {
	decls := []GroupDecl{gateGood, gateLow, gateNarrow, gateGrows}
	for _, elide := range []bool{false, true} {
		t.Run(fmt.Sprintf("elide=%v", elide), func(t *testing.T) {
			cols, rows, schema := gateFixture()
			// A constant column so elision has something to plan.
			cols = append(cols, "const_c")
			schema.Fields = append(schema.Fields, encoding.Field{Name: "const_c", Type: encoding.FieldTypeU32, CsvColumnIdx: len(cols) - 1})
			for i := range rows {
				rows[i] = append(rows[i], "77")
			}
			set := func(j *ImportJob) { j.Schema = schema; j.Groups = decls; j.ElideConstants = elide }
			rep, err := predictJob(t, newMockReader(cols, rows), set)
			if err != nil {
				t.Fatal(err)
			}
			_, _, schema2 := gateFixture()
			schema2.Fields = append(schema2.Fields, encoding.Field{Name: "const_c", Type: encoding.FieldTypeU32, CsvColumnIdx: len(cols) - 1})
			ir, raw, _, err := runGroupImport(t, newMockReader(cols, rows), decls, func(j *ImportJob) { j.Schema = schema2; j.ElideConstants = elide })
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rep.Groups, ir.Groups) {
				t.Errorf("predict groups\n%+v\nimport groups\n%+v", rep.Groups, ir.Groups)
			}
			if codes(rep.GroupWarnings) != codes(ir.GroupWarnings) {
				t.Errorf("predict warnings %s, import %s", codes(rep.GroupWarnings), codes(ir.GroupWarnings))
			}
			if !reflect.DeepEqual(rep.ElidedConstants, ir.ElidedConstants) {
				t.Errorf("predict elides %v, import elided %v", rep.ElidedConstants, ir.ElidedConstants)
			}
			if rep.Projection.ProjectedFileBytes != int64(len(raw)) {
				t.Errorf("projected %d bytes, import wrote %d", rep.Projection.ProjectedFileBytes, len(raw))
			}
			if rep.GroupCandidates != nil {
				t.Error("GroupCandidates set without SuggestGroups")
			}
		})
	}
}

func codes(ws []*perrors.CodedError) string {
	s := ""
	for _, w := range ws {
		s += string(w.Code) + ";"
	}
	return s
}

// TestImportPredict_DeclaredKeyViolation: a declared key whose member
// varies fails predict with the error Run fails with — same code and
// the same group / field / record / source-row details.
func TestImportPredict_DeclaredKeyViolation(t *testing.T) {
	cols, rows := orderLinesFixture(14000)
	bad := []GroupDecl{{Key: []string{"cust_id"}, Members: []string{"cust_name", "cust_flag"}}}
	_, perr := predictJob(t, newMockReader(cols, rows), func(j *ImportJob) { j.Groups = bad })
	_, _, _, rerr := runGroupImport(t, newMockReader(cols, rows), bad)
	var pce, rce *perrors.CodedError
	if !stderrors.As(perr, &pce) || !stderrors.As(rerr, &rce) {
		t.Fatalf("want coded errors, predict %v, import %v", perr, rerr)
	}
	if pce.Code != perrors.PULSE_GROUP_MEMBER_NOT_CONSTANT || pce.Message != rce.Message || !reflect.DeepEqual(pce.Details, rce.Details) {
		t.Errorf("predict error %s %q %v\nimport error %s %q %v", pce.Code, pce.Message, pce.Details, rce.Code, rce.Message, rce.Details)
	}
}

// TestImportPredict_Strict: StrictDedup fails predict exactly where it
// fails the import.
func TestImportPredict_Strict(t *testing.T) {
	cols, rows, schema := gateFixture()
	_, err := predictJob(t, newMockReader(cols, rows), func(j *ImportJob) {
		j.Schema = schema
		j.Groups = []GroupDecl{gateGood, gateLow}
		j.StrictDedup = true
	})
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_DEDUP_LOW_RATIO {
		t.Fatalf("want PULSE_DEDUP_LOW_RATIO, got %v", err)
	}
}

// TestImportPredict_NoViableGroup: a source whose only dependencies are
// below the floors reports them WITH the verdict and reason, and
// suggests none — never promotes a marginal group.
func TestImportPredict_NoViableGroup(t *testing.T) {
	cols, rows, schema := gateFixture()
	// Keep only the three non-viable parents (low ratio, narrow,
	// grows-the-file) and the unique row id.
	keep := []int{0, 4, 5, 6, 7, 8, 9, 10, 11}
	var kc []string
	var ks encoding.Schema
	for n, i := range keep {
		kc = append(kc, cols[i])
		f := schema.Fields[i]
		f.CsvColumnIdx = n
		ks.Fields = append(ks.Fields, f)
	}
	var kr [][]string
	for _, r := range rows {
		var nr []string
		for _, i := range keep {
			nr = append(nr, r[i])
		}
		kr = append(kr, nr)
	}
	rep, err := predictJob(t, newMockReader(kc, kr), suggest, func(j *ImportJob) { j.Schema = &ks })
	if err != nil {
		t.Fatal(err)
	}
	d := rep.GroupCandidates
	if len(d.Suggested) != 0 {
		t.Fatalf("suggested %v from a source with no viable group", d.Suggested)
	}
	want := map[string]string{
		"l_id": string(perrors.PULSE_DEDUP_LOW_RATIO),
		"n_id": string(perrors.PULSE_GROUP_TOO_NARROW),
		"g_id": string(perrors.PULSE_DEDUP_LOW_RATIO),
	}
	for key, reason := range want {
		c := candidateByKey(t, d, key)
		if c.Reason != reason || c.Suggested {
			t.Errorf("%s: verdict %s reason %q suggested %v, want reason %s", key, c.Verdict, c.Reason, c.Suggested, reason)
		}
	}
	if g := candidateByKey(t, d, "g_id"); !g.GrowsFile {
		t.Errorf("g_id grows the file but grows_file=false")
	}
}

// TestImportPredict_PlainUnchanged: without Groups, ElideConstants or
// SuggestGroups, predict runs the plain pass and the measured fields
// stay nil — the report (and its JSON) is unchanged.
func TestImportPredict_PlainUnchanged(t *testing.T) {
	cols, rows := orderLinesFixture(600)
	rep, err := predictJob(t, newMockReader(cols, rows))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Projection != nil || rep.GroupCandidates != nil || rep.Groups != nil || rep.ElidedConstants != nil {
		t.Errorf("plain predict carries measured fields: %+v", rep)
	}
	// The measured pass reports the same schema and row count.
	mrep, err := predictJob(t, newMockReader(cols, rows), suggest)
	if err != nil {
		t.Fatal(err)
	}
	if mrep.EstimatedRows != rep.EstimatedRows || !reflect.DeepEqual(mrep.Schema, rep.Schema) || !reflect.DeepEqual(mrep.Warnings, rep.Warnings) {
		t.Errorf("measured predict schema/rows/warnings differ from plain predict")
	}
}

// TestImportPredict_ShortSource: a source shorter than the window is
// nominated over all of it.
func TestImportPredict_ShortSource(t *testing.T) {
	cols, rows := orderLinesFixture(600)
	rep, err := predictJob(t, newMockReader(cols, rows), suggest)
	if err != nil {
		t.Fatal(err)
	}
	d := rep.GroupCandidates
	if d.WindowRows != 600 || d.WindowBound != detectWindowMaxRows {
		t.Errorf("window %d bound %d", d.WindowRows, d.WindowBound)
	}
	if c := candidateByKey(t, d, "prod_id"); !c.Suggested {
		t.Errorf("prod_id not suggested over a short source: %+v", c)
	}
}

// TestDetectWindow: the nomination window shrinks for a wide or
// long-stride schema and never below the floor.
func TestDetectWindow(t *testing.T) {
	cases := []struct{ fields, stride, want int }{
		{10, 80, detectWindowMaxRows},
		{1000, 8000, detectWindowCells / 1000},
		{100, 128 << 10, detectWindowMinRows},
	}
	for _, c := range cases {
		if got := detectWindow(c.fields, c.stride); got != c.want {
			t.Errorf("detectWindow(%d, %d) = %d, want %d", c.fields, c.stride, got, c.want)
		}
	}
}
