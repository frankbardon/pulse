package io

import (
	stderrors "errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// gateFixture is a synthetic 400-row cohort holding four candidate
// parent blocks, each built to land on one side of the viability gate:
//
//	p_id → p_a, p_b          18 B/row, 40 tuples  (ratio 10)   admitted
//	l_id → l_x, l_y, l_z     26 B/row, 320 tuples (ratio 1.25) below the floor, still saves bytes
//	n_id → n_cat              2 B/row                           narrower than the 4-byte index
//	g_id → g_v                5 B/row, 100 tuples (ratio 4)    above the floor, but grows the file
//
// The schema is explicit so the widths are exact, and nothing is
// nullable, so the per-row bitmap plays no part in the byte arithmetic.
func gateFixture() ([]string, [][]string, *encoding.Schema) {
	cols := []string{"row_id", "p_id", "p_a", "p_b", "l_id", "l_x", "l_y", "l_z", "n_id", "n_cat", "g_id", "g_v"}
	types := []encoding.FieldType{
		encoding.FieldTypeU16,
		encoding.FieldTypeU16, encoding.FieldTypeF64, encoding.FieldTypeF64,
		encoding.FieldTypeU16, encoding.FieldTypeF64, encoding.FieldTypeF64, encoding.FieldTypeF64,
		encoding.FieldTypeU8, encoding.FieldTypeCategoricalU8,
		encoding.FieldTypeU8, encoding.FieldTypeU32,
	}
	schema := &encoding.Schema{}
	for i, c := range cols {
		schema.Fields = append(schema.Fields, encoding.Field{Name: c, Type: types[i], CsvColumnIdx: i})
	}
	var rows [][]string
	for i := 0; i < 400; i++ {
		p, l, n, g := i/10, i*4/5, i%7, i%100
		rows = append(rows, []string{
			fmt.Sprint(i),
			fmt.Sprint(p), fmt.Sprintf("%d.5", p), fmt.Sprintf("%d.25", p),
			fmt.Sprint(l), fmt.Sprintf("%d.5", l), fmt.Sprintf("%d.75", l), fmt.Sprintf("%d.125", l),
			fmt.Sprint(n), fmt.Sprintf("kind-%d", n),
			fmt.Sprint(g), fmt.Sprint(100000 + g),
		})
	}
	return cols, rows, schema
}

var (
	gateGood   = GroupDecl{Key: []string{"p_id"}, Members: []string{"p_a", "p_b"}}
	gateLow    = GroupDecl{Key: []string{"l_id"}, Members: []string{"l_x", "l_y", "l_z"}}
	gateNarrow = GroupDecl{Key: []string{"n_id"}, Members: []string{"n_cat"}}
	gateGrows  = GroupDecl{Key: []string{"g_id"}, Members: []string{"g_v"}}
)

// extensionFraming is what a 0x02 schema block adds on top of the group
// descriptors and entries, once per file: u64 extension_length, u16
// section_count, the section header (u16 tag, u8 flags, u64 length) and
// u16 group_count.
const extensionFraming = 8 + 2 + 2 + 1 + 8 + 2

func gateImport(t *testing.T, decls []GroupDecl, mutate ...func(*ImportJob)) (*ImportReport, []byte, afero.Fs, error) {
	t.Helper()
	cols, rows, schema := gateFixture()
	return runGroupImport(t, newMockReader(cols, rows), decls,
		append([]func(*ImportJob){func(j *ImportJob) { j.Schema = schema }}, mutate...)...)
}

// TestImportJob_GroupGate_PerGroup: the gate judges each group on its
// own numbers. On ONE import a good group is admitted, a low-ratio group
// and a file-growing group are written with PULSE_DEDUP_LOW_RATIO, and a
// group no wider than the index is dropped with PULSE_GROUP_TOO_NARROW —
// and the report says which and why. Labels keep their declared
// positions when an earlier group is dropped. The per-group byte deltas
// account for the whole file-size difference against the flat import.
func TestImportJob_GroupGate_PerGroup(t *testing.T) {
	_, flatRaw, flatFS, err := gateImport(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	decls := []GroupDecl{gateGood, gateLow, gateNarrow, gateGrows}
	rep, raw, fs, err := gateImport(t, decls)
	if err != nil {
		t.Fatalf("a low-ratio import must succeed without strict: %v", err)
	}

	type row struct {
		label, verdict, reason string
		entries, memberBytes   int
		ratio                  float64
	}
	want := []row{
		{"group 1 [key: p_id]", encoding.GroupVerdictAdmitted, "", 40, 18, 10},
		{"group 2 [key: l_id]", encoding.GroupVerdictLowRatio, string(perrors.PULSE_DEDUP_LOW_RATIO), 320, 26, 1.25},
		{"group 3 [key: n_id]", encoding.GroupVerdictDroppedTooNarrow, string(perrors.PULSE_GROUP_TOO_NARROW), 0, 2, 0},
		{"group 4 [key: g_id]", encoding.GroupVerdictLowRatio, string(perrors.PULSE_DEDUP_LOW_RATIO), 100, 5, 4},
	}
	if len(rep.Groups) != len(want) {
		t.Fatalf("report has %d groups, want %d", len(rep.Groups), len(want))
	}
	for i, w := range want {
		g := rep.Groups[i]
		got := row{g.Label, g.Verdict, g.Reason, g.EntryCount, g.MemberRowBytes, g.Ratio}
		if got != w {
			t.Errorf("group %d: got %+v\nwant %+v", i+1, got, w)
		}
		if g.IndexWidth != encoding.GroupIndexWidth || g.RatioFloor != encoding.DefaultDedupRatioFloor {
			t.Errorf("group %d: index width %d, floor %v", i+1, g.IndexWidth, g.RatioFloor)
		}
		if g.Verdict != encoding.GroupVerdictDroppedTooNarrow && g.DictionaryBytes != int64(g.EntryCount*g.EntryWidth) {
			t.Errorf("group %d: resident dictionary bytes %d, want %d×%d", i+1, g.DictionaryBytes, g.EntryCount, g.EntryWidth)
		}
	}
	// The low-ratio group still SAVES bytes (above its break-even) — it
	// warns on the floor; the growing group is above the floor but
	// below its break-even.
	if low, grows := rep.Groups[1], rep.Groups[3]; low.ByteDelta >= 0 || low.Ratio <= low.BreakEvenRatio ||
		grows.ByteDelta <= 0 || grows.Ratio >= grows.BreakEvenRatio || grows.BreakEvenRatio != 5 {
		t.Fatalf("low: delta %d ratio %v break-even %v; grows: delta %d ratio %v break-even %v",
			low.ByteDelta, low.Ratio, low.BreakEvenRatio, grows.ByteDelta, grows.Ratio, grows.BreakEvenRatio)
	}

	// Warnings: the width finding (before the row pass), then the ratio
	// findings in group order.
	codes := make([]perrors.Code, len(rep.GroupWarnings))
	for i, w := range rep.GroupWarnings {
		codes[i] = w.Code
	}
	wantCodes := []perrors.Code{perrors.PULSE_GROUP_TOO_NARROW, perrors.PULSE_DEDUP_LOW_RATIO, perrors.PULSE_DEDUP_LOW_RATIO}
	if !reflect.DeepEqual(codes, wantCodes) {
		t.Fatalf("warning codes %v, want %v", codes, wantCodes)
	}
	narrow := rep.GroupWarnings[0].Details
	if narrow["group_label"] != "group 3 [key: n_id]" || narrow["member_row_bytes"] != 2 || narrow["index_width"] != 4 {
		t.Fatalf("narrow details %v", narrow)
	}
	low := rep.GroupWarnings[1].Details
	for k, v := range map[string]any{
		"group_label": "group 2 [key: l_id]", "rows": int64(400), "entry_count": 320, "ratio": 1.25,
		"ratio_floor": 2.0, "grows_file": false, "dictionary_bytes": int64(320 * 26),
		"byte_delta": rep.Groups[1].ByteDelta,
	} {
		if low[k] != v {
			t.Errorf("low-ratio details[%s] = %v (%T), want %v", k, low[k], low[k], v)
		}
	}
	if rep.GroupWarnings[2].Details["grows_file"] != true || rep.GroupWarnings[2].Details["group_label"] != "group 4 [key: g_id]" {
		t.Fatalf("growing group details %v", rep.GroupWarnings[2].Details)
	}

	// The narrow group was not formed: three groups written, its members
	// are row fields, and the cohort decodes to the flat import.
	schema, vals, nulls := decodeAll(t, fs, "out.pulse")
	if len(schema.Groups) != 3 {
		t.Fatalf("written groups = %d, want 3 (narrow group dropped)", len(schema.Groups))
	}
	for g := range schema.Groups {
		for _, m := range schema.Groups[g].Members {
			if n := schema.Fields[m.Field].Name; n == "n_id" || n == "n_cat" {
				t.Fatalf("dropped group's member %s is in written group %d", n, g)
			}
		}
	}
	_, flatVals, flatNulls := decodeAll(t, flatFS, "out.pulse")
	if !reflect.DeepEqual(vals, flatVals) || !reflect.DeepEqual(nulls, flatNulls) {
		t.Fatal("gated cohort decodes differently from the flat import")
	}

	// The per-group deltas are the real byte arithmetic: they sum, with
	// the one-off extension framing, to the actual size difference.
	var sum int64
	for _, g := range rep.Groups {
		sum += g.ByteDelta
	}
	if got := int64(len(raw) - len(flatRaw)); got != sum+extensionFraming {
		t.Fatalf("file grew by %d bytes; per-group deltas sum to %d (+%d framing)", got, sum, extensionFraming)
	}
}

// TestImportJob_GroupGate_Strict: under StrictDedup each finding is a
// fatal error carrying its own code (reachable with errors.As) and no
// cohort is written; a clean group still imports under strict.
func TestImportJob_GroupGate_Strict(t *testing.T) {
	strict := func(j *ImportJob) { j.StrictDedup = true }
	for _, tc := range []struct {
		name  string
		decls []GroupDecl
		code  perrors.Code
	}{
		{"narrow", []GroupDecl{gateGood, gateNarrow}, perrors.PULSE_GROUP_TOO_NARROW},
		{"low_ratio", []GroupDecl{gateGood, gateLow}, perrors.PULSE_DEDUP_LOW_RATIO},
		{"grows_file", []GroupDecl{gateGrows}, perrors.PULSE_DEDUP_LOW_RATIO},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, fs, err := gateImport(t, tc.decls, strict)
			var ce *perrors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			if ce.Details["group_label"] == nil {
				t.Fatalf("strict error does not name the group: %v", ce.Details)
			}
			if ok, _ := afero.Exists(fs, "out.pulse"); ok {
				t.Fatal("a strict refusal left a cohort behind")
			}
		})
	}
	rep, _, _, err := gateImport(t, []GroupDecl{gateGood}, strict)
	if err != nil || rep.Groups[0].Verdict != encoding.GroupVerdictAdmitted {
		t.Fatalf("clean group under strict: err %v", err)
	}
}

// TestImportJob_GroupGate_RatioFloor: the floor is overridable. Below
// the measured ratio the low-ratio group is admitted; zero selects the
// default; a floor of 1 silences the floor but never the grows-the-file
// check.
func TestImportJob_GroupGate_RatioFloor(t *testing.T) {
	floor := func(f float64) func(*ImportJob) { return func(j *ImportJob) { j.DedupRatioFloor = f } }
	for _, tc := range []struct {
		name    string
		decl    GroupDecl
		floor   float64
		verdict string
	}{
		{"below_measured", gateLow, 1.2, encoding.GroupVerdictAdmitted},
		{"above_measured", gateLow, 1.3, encoding.GroupVerdictLowRatio},
		{"zero_is_default", gateLow, 0, encoding.GroupVerdictLowRatio},
		{"one_disables_floor", gateLow, 1, encoding.GroupVerdictAdmitted},
		{"one_keeps_grows_check", gateGrows, 1, encoding.GroupVerdictLowRatio},
		{"high_floor_flags_good", gateGood, 12, encoding.GroupVerdictLowRatio},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep, _, _, err := gateImport(t, []GroupDecl{tc.decl}, floor(tc.floor))
			if err != nil {
				t.Fatal(err)
			}
			if g := rep.Groups[0]; g.Verdict != tc.verdict {
				t.Fatalf("verdict %s (ratio %v, floor %v), want %s", g.Verdict, g.Ratio, g.RatioFloor, tc.verdict)
			}
			if (tc.verdict == encoding.GroupVerdictAdmitted) != (len(rep.GroupWarnings) == 0) {
				t.Fatalf("verdict %s with warnings %v", tc.verdict, rep.GroupWarnings)
			}
		})
	}
}

// TestImportJob_GroupGate_DroppedFreesElision: a dropped group reserves
// nothing, so a constant member of it is elided like any row field.
func TestImportJob_GroupGate_DroppedFreesElision(t *testing.T) {
	cols, rows, schema := gateFixture()
	cols = append(cols, "src")
	schema.Fields = append(schema.Fields, encoding.Field{Name: "src", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: len(cols) - 1})
	for i := range rows {
		rows[i] = append(rows[i], "feed")
	}
	rep, _, _, err := runGroupImport(t, newMockReader(cols, rows),
		[]GroupDecl{{Key: []string{"n_id"}, Members: []string{"src"}}},
		func(j *ImportJob) { j.Schema = schema; j.ElideConstants = true })
	if err != nil {
		t.Fatal(err)
	}
	if rep.Groups[0].Verdict != encoding.GroupVerdictDroppedTooNarrow || !reflect.DeepEqual(rep.ElidedConstants, []string{"src"}) {
		t.Fatalf("verdict %s, elided %v; want dropped and [src]", rep.Groups[0].Verdict, rep.ElidedConstants)
	}
}
