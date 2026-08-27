package spss

// Tests for numeric user-missing values and the `<var>_missing` sibling
// columns that keep the reason.
//
// The claim under test is not "a sibling column appears". It is that
// after an import, `refused` / `don't know` / `not applicable` / `sysmis`
// are still four DISTINGUISHABLE states, and that the analytic column is
// arithmetically clean — so a mean over an income column is the mean of
// the incomes and not of the incomes plus three refusal codes.

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// missingSpec builds the canonical three-shape fixture: one numeric
// variable per missing-value shape the record type 2 field defines, plus
// a control variable that declares none.
//
//	DISCRETE  three discrete codes, two of them labelled
//	RANGED    a lo..hi range, so several distinct values are missing
//	RANGEDSC  a range PLUS one discrete code (n_missing_values -3)
//	PLAIN     no specification at all — the control
func missingFixtureSpec() spsstest.Spec {
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{
				Name: "DISCRETE", LongName: "income", Label: "Annual income",
				Print:   spsstest.Format{Type: spsstest.FormatF, Width: 8},
				Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(97), spsstest.Num(98), spsstest.Num(99)}},
			},
			{
				Name: "RANGED", LongName: "age",
				Print:   spsstest.Format{Type: spsstest.FormatF, Width: 8},
				Missing: &spsstest.MissingValues{Range: &spsstest.MissingRange{Low: 900, High: 999}},
			},
			{
				Name: "RANGEDSC", LongName: "score",
				Print: spsstest.Format{Type: spsstest.FormatF, Width: 8},
				Missing: &spsstest.MissingValues{
					Range:    &spsstest.MissingRange{Low: 90, High: 95},
					Discrete: []spsstest.Value{spsstest.Num(-1)},
				},
			},
			{Name: "PLAIN", LongName: "weight", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
		},
		// Labels on two of the three discrete codes, so both the
		// labelled and the unlabelled reason paths are exercised on one
		// variable. The set names ONLY user-missing codes, which is the
		// near-universal real shape — an income variable whose only value
		// labels sit on 97/98/99 — and is exactly what
		// labelsCodeTheVariable keeps out of the categorical arm.
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"DISCRETE"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(97), Label: "Refused"},
				{Value: spsstest.Num(98), Label: "Don't know"},
			},
		}},
		Cases: [][]spsstest.Value{
			{spsstest.Num(30000), spsstest.Num(41), spsstest.Num(72), spsstest.Num(1.5)},
			{spsstest.Num(97), spsstest.Num(950), spsstest.Num(-1), spsstest.Num(2.5)},
			{spsstest.Num(98), spsstest.Num(999), spsstest.Num(93), spsstest.Num(3.5)},
			{spsstest.Num(99), spsstest.SysMis(), spsstest.Num(91), spsstest.Num(4.5)},
			{spsstest.SysMis(), spsstest.Num(28), spsstest.SysMis(), spsstest.Num(5.5)},
		},
	}
}

// readHeaderAndRows drains a reader and returns its column names beside
// its rows. The two are read together because the whole point of a
// sibling column is that a row is no longer one cell per SPSS variable,
// so a row asserted without the header it belongs to says nothing.
func readHeaderAndRows(t *testing.T, r *Reader) ([]string, [][]string) {
	t.Helper()
	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	return header, readAll(t, r)
}

// TestMissing_AllThreeShapesReadBackDistinguishable is the acceptance
// criterion: a fixture exercising all three missing-spec shapes reads
// back with every reason distinguishable.
func TestMissing_AllThreeShapesReadBackDistinguishable(t *testing.T) {
	r := NewReaderFromBytes(buildFixture(t, missingFixtureSpec()))
	header, rows := readHeaderAndRows(t, r)

	wantHeader := []string{
		"income", "income_missing",
		"age", "age_missing",
		"score", "score_missing",
		"weight",
	}
	if !equalStrings(header, wantHeader) {
		t.Fatalf("header = %q, want %q", header, wantHeader)
	}

	want := [][]string{
		{"30000", "", "41", "", "72", "", "1.5"},
		{"", "Refused", "", "950", "", "-1", "2.5"},
		{"", "Don't know", "", "999", "", "93", "3.5"},
		{"", "99", "", SysmisReason, "", "91", "4.5"},
		{"", SysmisReason, "28", "", "", SysmisReason, "5.5"},
	}
	if len(rows) != len(want) {
		t.Fatalf("read %d row(s), want %d", len(rows), len(want))
	}
	for i := range want {
		if !equalStrings(rows[i], want[i]) {
			t.Errorf("row %d = %q, want %q", i, rows[i], want[i])
		}
	}

	// The four states of the discrete column really are four distinct
	// reasons and not one collapsed null.
	reasons := map[string]bool{}
	for _, row := range rows[1:] {
		reasons[row[1]] = true
	}
	for _, w := range []string{"Refused", "Don't know", "99", SysmisReason} {
		if !reasons[w] {
			t.Errorf("reason %q did not survive the read; the reasons seen were %v", w, reasons)
		}
	}
}

// TestMissing_SiblingSchema checks the generated column's declared shape:
// a categorical whose dictionary IS the reason vocabulary, nullable
// because a present value has no reason.
func TestMissing_SiblingSchema(t *testing.T) {
	r := NewReaderFromBytes(buildFixture(t, missingFixtureSpec()))
	schema, err := r.PulseSchema()
	if err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}

	sib := schema.Field("income_missing")
	if sib == nil {
		t.Fatal("no income_missing field; the sibling was not generated")
	}
	if sib.Type != encoding.FieldTypeCategoricalU8 {
		t.Errorf("income_missing.Type = %s, want categorical_u8", sib.Type)
	}
	if !sib.Nullable {
		t.Error("income_missing is not nullable; a PRESENT value has no reason and renders as null, so every non-missing row would fail")
	}
	want := []string{SysmisReason, "Refused", "Don't know", "99"}
	if got := sib.Dictionary.Values(); !equalStrings(got, want) {
		t.Errorf("income_missing dictionary = %q, want %q", got, want)
	}
	// The empty reason is the null bit, NOT a dead dictionary entry the
	// import path could never reference.
	for _, v := range sib.Dictionary.Values() {
		if v == "" {
			t.Error("the dictionary carries an empty entry; io/import.go reads an empty cell as null before any lookup, so that ID could never appear on the wire")
		}
	}

	// The analytic column is untouched by its sibling: still f64, and
	// now nullable because the scan saw missing values.
	analytic := schema.Field("income")
	if analytic.Type != encoding.FieldTypeF64 {
		t.Errorf("income.Type = %s, want f64", analytic.Type)
	}
	if !analytic.Nullable {
		t.Error("income is not nullable, but four of its five cases are missing")
	}

	// A variable declaring no missing values gets no sibling at all.
	if schema.Field("weight"+MissingSiblingSuffix) != nil {
		t.Error("a variable with no missing-value specification acquired a sibling")
	}
}

// TestMissing_RangeWidensToU16 is the widening criterion. A range is not
// enumerated — only its OBSERVED members get entries — so the width is
// driven by the data, and a range wide enough to be observed at many
// distinct values pushes the sibling past categorical_u8.
func TestMissing_RangeWidensToU16(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{
			Name: "Q", LongName: "q",
			Print:   spsstest.Format{Type: spsstest.FormatF, Width: 8},
			Missing: &spsstest.MissingValues{Range: &spsstest.MissingRange{Low: 1000, High: 9000}},
		}},
	}
	// 256 distinct in-range values plus the sysmis entry is 257, one past
	// what a categorical_u8 dictionary holds.
	for i := 0; i < 256; i++ {
		spec.Cases = append(spec.Cases, []spsstest.Value{spsstest.Num(float64(1000 + i))})
	}
	spec.Cases = append(spec.Cases, []spsstest.Value{spsstest.Num(7)})

	r := NewReaderFromBytes(buildFixture(t, spec))
	schema, err := r.PulseSchema()
	if err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}
	sib := schema.Field("q_missing")
	if sib == nil {
		t.Fatal("no q_missing field")
	}
	if sib.Type != encoding.FieldTypeCategoricalU16 {
		t.Fatalf("q_missing.Type = %s, want categorical_u16 — 257 reasons do not fit a u8 dictionary", sib.Type)
	}
	if got := sib.Dictionary.Count(); got != 257 {
		t.Errorf("q_missing dictionary holds %d entr(ies), want 257 (sysmis plus one per distinct observed missing value)", got)
	}
	// The range is NOT enumerated: 8001 values fall inside 1000..9000
	// and only the 256 observed ones are represented.
	if sib.Dictionary.Count() > 300 {
		t.Error("the range was enumerated rather than observed")
	}
}

// TestMissing_NullModeSuppressesSiblings covers --spss-missing=null: the
// nulls are identical, the sibling columns are gone, and with them the
// reason.
func TestMissing_NullModeSuppressesSiblings(t *testing.T) {
	raw := buildFixture(t, missingFixtureSpec())

	auto := NewReaderFromBytes(raw)
	_, autoRows := readHeaderAndRows(t, auto)

	null := NewReaderFromBytes(raw, WithMissingMode(MissingNull))
	nullHeader, nullRows := readHeaderAndRows(t, null)

	want := []string{"income", "age", "score", "weight"}
	if !equalStrings(nullHeader, want) {
		t.Fatalf("header = %q, want %q — MissingNull must generate no sibling columns", nullHeader, want)
	}
	for _, name := range nullHeader {
		if strings.HasSuffix(name, MissingSiblingSuffix) {
			t.Errorf("column %q survived MissingNull", name)
		}
	}

	// Every analytic cell is byte-identical to the auto mode's: the mode
	// changes only whether the reason is preserved beside the null.
	analyticAt := []int{0, 2, 4, 6}
	for i := range nullRows {
		for j, at := range analyticAt {
			if nullRows[i][j] != autoRows[i][at] {
				t.Errorf("row %d column %d = %q under null mode, %q under auto; the nulls must be identical",
					i, j, nullRows[i][j], autoRows[i][at])
			}
		}
	}

	schema, err := null.PulseSchema()
	if err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}
	if len(schema.Fields) != 4 {
		t.Errorf("schema has %d field(s), want 4", len(schema.Fields))
	}
	if !schema.Field("income").Nullable {
		t.Error("income is not nullable under MissingNull; the values are still missing, only the reason is gone")
	}
}

// TestParseMissingMode covers the flag spellings and the refusal.
func TestParseMissingMode(t *testing.T) {
	cases := []struct {
		in   string
		want MissingMode
		ok   bool
	}{
		{"", MissingAuto, true},
		{"auto", MissingAuto, true},
		{"AUTO", MissingAuto, true},
		{"  null  ", MissingNull, true},
		{"null", MissingNull, true},
		{"nul", MissingAuto, false},
		{"keep", MissingAuto, false},
		{"none", MissingAuto, false},
	}
	for _, c := range cases {
		got, err := ParseMissingMode(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("ParseMissingMode(%q) = %v, want no error", c.in, err)
				continue
			}
			if got != c.want {
				t.Errorf("ParseMissingMode(%q) = %v, want %v", c.in, got, c.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("ParseMissingMode(%q) returned no error; an unrecognised mode must not fall back to the default", c.in)
			continue
		}
		ce, isCoded := err.(*errors.CodedError)
		if !isCoded || ce.Code != errors.PULSE_SPSS_MISSING_MODE_INVALID {
			t.Errorf("ParseMissingMode(%q) error = %v, want PULSE_SPSS_MISSING_MODE_INVALID", c.in, err)
			continue
		}
		if ce.Details[errors.DetailSPSSMissingMode] != c.in {
			t.Errorf("Details[%q] = %v, want %q", errors.DetailSPSSMissingMode, ce.Details[errors.DetailSPSSMissingMode], c.in)
		}
	}
}

// TestMissing_NameCollision is the collision criterion: a generated
// sibling whose name a real variable already holds is a coded error
// naming BOTH sides.
func TestMissing_NameCollision(t *testing.T) {
	base := func(realName string) spsstest.Spec {
		return spsstest.Spec{
			Vars: []spsstest.Var{
				{
					Name: "INCOME", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8},
					Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(99)}},
				},
				{Name: realName, Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			},
			Cases: [][]spsstest.Value{{spsstest.Num(1), spsstest.Num(2)}},
		}
	}

	// The colliding variable is spelled with a DIFFERENT case from the
	// generated name's stem. SPSS variable names are case-insensitive, so
	// the two are one name as far as a re-emitted `.sav` is concerned,
	// and the comparison has to be too.
	spec := base("IM")
	spec.Vars[1].LongName = "INCOME_missing"

	r := NewReaderFromBytes(buildFixture(t, spec))
	_, err := r.PulseSchema()
	if err == nil {
		t.Fatal("PulseSchema succeeded; a generated sibling colliding with a real variable must be a hard error")
	}
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PULSE_SPSS_DERIVED_NAME_COLLISION {
		t.Fatalf("error = %v, want PULSE_SPSS_DERIVED_NAME_COLLISION", err)
	}
	if got := ce.Details[errors.DetailSPSSDerived]; got != "INCOME_missing" {
		t.Errorf("Details[derived] = %v, want the generated name", got)
	}
	if got := ce.Details[errors.DetailSPSSCollidesWith]; got != "INCOME_missing" {
		t.Errorf("Details[collides_with] = %v, want the existing variable", got)
	}
	if got := ce.Details[errors.DetailSPSSVariable]; got != "INCOME" {
		t.Errorf("Details[variable] = %v, want the variable the sibling was derived FROM", got)
	}
	for _, want := range []string{"INCOME_missing", "spss-missing=null"} {
		if !strings.Contains(ce.Message, want) {
			t.Errorf("message does not mention %q:\n%s", want, ce.Message)
		}
	}

	// The documented escape hatch really works.
	relaxed := NewReaderFromBytes(buildFixture(t, spec), WithMissingMode(MissingNull))
	if _, err := relaxed.PulseSchema(); err != nil {
		t.Errorf("MissingNull still failed: %v; suppressing siblings must remove the collision", err)
	}
}

// TestMissing_CollisionIsNotRaisedWithoutASpec guards the false positive:
// a variable named `<other>_missing` is perfectly legal when no sibling
// would be generated for `<other>`.
func TestMissing_CollisionIsNotRaisedWithoutASpec(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "INCOME", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "IM", LongName: "INCOME_missing", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
		},
		Cases: [][]spsstest.Value{{spsstest.Num(1), spsstest.Num(2)}},
	}
	r := NewReaderFromBytes(buildFixture(t, spec))
	if _, err := r.PulseSchema(); err != nil {
		t.Fatalf("PulseSchema: %v; INCOME declares no missing values, so no sibling is generated and nothing collides", err)
	}
}

// TestMissing_CategoricalGetsNoSibling is the scope boundary. A string
// variable and a value-labelled numeric both map to categorical, where
// the missing code IS a dictionary entry — a sibling there would be pure
// redundancy that doubles the width of an all-categorical survey file.
// The categorical arm belongs to E4-S3.
func TestMissing_CategoricalGetsNoSibling(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{
				Name: "CODE", Width: 4,
				Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Text("REF")}},
			},
			{
				Name: "Q1", Print: spsstest.Format{Type: spsstest.FormatF, Width: 1},
				Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(9)}},
			},
		},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"Q1"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(1), Label: "Yes"},
				{Value: spsstest.Num(9), Label: "Refused"},
			},
		}},
		Cases: [][]spsstest.Value{
			{spsstest.Text("AB"), spsstest.Num(1)},
			{spsstest.Text("REF"), spsstest.Num(9)},
		},
	}
	r := NewReaderFromBytes(buildFixture(t, spec))
	header, rows := readHeaderAndRows(t, r)
	if !equalStrings(header, []string{"CODE", "Q1"}) {
		t.Fatalf("header = %q, want the two source variables and no sibling", header)
	}
	// And the missing codes stay in the data, addressable through the
	// main dictionary — nulling them here would lose the value itself,
	// which for a categorical IS the reason.
	if rows[1][0] != "REF" || rows[1][1] != "9" {
		t.Errorf("row 1 = %q, want the categorical missing codes kept as ordinary dictionary values", rows[1])
	}
}

// TestMissing_TemporalUserMissingDoesNotWidenTheColumn is the reason the
// predicate has to be in force during the SCAN and not only at decode
// time. A refusal code of 999 on a DATE variable reads as an instant a
// few minutes after the SPSS epoch — which is pre-1970 — and would widen
// the whole column to datetime for a value that is not a date at all.
func TestMissing_TemporalUserMissingDoesNotWidenTheColumn(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{
			Name: "DOB", LongName: "dob",
			Print:   spsstest.Format{Type: spsstest.FormatDATE, Width: 11},
			Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(999)}},
		}},
		Cases: [][]spsstest.Value{
			{spsstest.Num(spssInstant(1990, 4, 12, 0, 0, 0))},
			{spsstest.Num(999)},
		},
	}
	r := NewReaderFromBytes(buildFixture(t, spec))
	schema, err := r.PulseSchema()
	if err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}
	if got := schema.Field("dob").Type; got != encoding.FieldTypeDate {
		t.Fatalf("dob.Type = %s, want date; the user-missing code was counted as an instant", got)
	}
	if hasCode(r.Warnings(), errors.PULSE_SPSS_DATE_WIDENED) {
		t.Errorf("the column widened on a user-missing code:\n%s", warningText(r.Warnings()))
	}
	_, rows := readHeaderAndRows(t, r)
	if rows[0][0] != "1990-04-12" || rows[0][1] != "" {
		t.Errorf("present row = %q, want the date and no reason", rows[0])
	}
	if rows[1][0] != "" || rows[1][1] != "999" {
		t.Errorf("missing row = %q, want a null date and the reason code", rows[1])
	}
}

// TestMissing_SysmisTestedBeforeAnOpenRange guards the ordering trap.
// SPSS spells an open-ended range with LOWEST, which is -DBL_MAX — the
// SAME double as the default system-missing sentinel. Testing the range
// first reports every sysmis datum as a user-missing one.
func TestMissing_SysmisTestedBeforeAnOpenRange(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{
			Name: "V", LongName: "v",
			Print: spsstest.Format{Type: spsstest.FormatF, Width: 8},
			Missing: &spsstest.MissingValues{
				Range: &spsstest.MissingRange{Low: -math.MaxFloat64, High: 0},
			},
		}},
		Cases: [][]spsstest.Value{
			{spsstest.Num(5)},
			{spsstest.Num(-3)},
			{spsstest.SysMis()},
		},
	}
	r := NewReaderFromBytes(buildFixture(t, spec))
	_, rows := readHeaderAndRows(t, r)
	want := [][]string{
		{"5", ""},
		{"", "-3"},
		{"", SysmisReason},
	}
	for i := range want {
		if !equalStrings(rows[i], want[i]) {
			t.Errorf("row %d = %q, want %q", i, rows[i], want[i])
		}
	}
}

// TestMissing_ReasonCollisionKeepsTheCodesDistinct covers two missing
// codes carrying the same value label. Sharing one dictionary entry
// would collapse them, destroying the exact distinction the sibling
// exists to preserve, so the prettier name gives way and the reader says
// so.
func TestMissing_ReasonCollisionKeepsTheCodesDistinct(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{
			Name: "Q", LongName: "q",
			Print:   spsstest.Format{Type: spsstest.FormatF, Width: 8},
			Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(97), spsstest.Num(98)}},
		}},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"Q"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(97), Label: "No answer"},
				{Value: spsstest.Num(98), Label: "No answer"},
			},
		}},
		Cases: [][]spsstest.Value{
			{spsstest.Num(97)},
			{spsstest.Num(98)},
		},
	}
	r := NewReaderFromBytes(buildFixture(t, spec))
	_, rows := readHeaderAndRows(t, r)
	if rows[0][1] == rows[1][1] {
		t.Fatalf("both codes resolved to the reason %q; two distinct missing codes must stay distinguishable", rows[0][1])
	}
	if rows[0][1] != "No answer" || rows[1][1] != "98" {
		t.Errorf("reasons = %q / %q, want the first label kept and the second code used verbatim", rows[0][1], rows[1][1])
	}
	if !hasCode(r.Warnings(), errors.PULSE_SPSS_VALUE_COLLISION) {
		t.Errorf("the label collision was resolved silently:\n%s", warningText(r.Warnings()))
	}
}

// TestMissing_EndToEndImportKeepsTheArithmetic is the criterion that
// matters most: after a real import the analytic column is null at every
// missing position, so an AGG_SUM / AGG_MEAN over it sees only the real
// values.
//
// It asserts at the cohort level rather than by running the engine —
// io/spss cannot import the pulse facade — but it is the same fact: the
// null bitmap bit is what every aggregator's n_null floor counts, and
// what AGG_SUM and AGG_MEAN skip.
func TestMissing_EndToEndImportKeepsTheArithmetic(t *testing.T) {
	fs := afero.NewMemMapFs()
	src := NewReaderFromBytes(buildFixture(t, missingFixtureSpec()))
	job := pio.NewImportJob(src, "survey.pulse")
	job.FS = fs
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.RowErrors) != 0 {
		t.Fatalf("RowErrors = %v, want none", report.RowErrors)
	}
	if report.RowsImported != 5 {
		t.Fatalf("RowsImported = %d, want 5", report.RowsImported)
	}

	writer := &rowCollector{}
	exp := pio.NewExportJob("survey.pulse", writer)
	exp.FS = fs
	if _, err := exp.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// income: one real value (30000) and four missing positions. Every
	// missing position must read back as the empty cell an aggregator
	// counts as null, NOT as 97 / 98 / 99.
	var sum float64
	var n int
	for i, row := range writer.rows {
		cell := toString(row[0])
		if cell == "" {
			continue
		}
		n++
		v, err := strconv.ParseFloat(cell, 64)
		if err != nil {
			t.Fatalf("row %d income %q is not numeric: %v", i, cell, err)
		}
		sum += v
	}
	if n != 1 || sum != 30000 {
		t.Errorf("income has %d non-null value(s) summing to %v, want exactly 1 summing to 30000; a refusal code reached the analytic column", n, sum)
	}

	// And the reasons are all still there beside them.
	wantReasons := []string{"", "Refused", "Don't know", "99", SysmisReason}
	for i, row := range writer.rows {
		if got := toString(row[1]); got != wantReasons[i] {
			t.Errorf("row %d income_missing = %q, want %q", i, got, wantReasons[i])
		}
	}
}

// TestMissing_SidecarRegistersTheDerivedColumns checks the fold-back
// contract: the sidecar names every synthesised column, its kind, its
// source, its cohort position and the SPSS state behind each reason ID.
func TestMissing_SidecarRegistersTheDerivedColumns(t *testing.T) {
	fs := afero.NewMemMapFs()
	src := NewReaderFromBytes(buildFixture(t, missingFixtureSpec()))
	job := pio.NewImportJob(src, "survey.pulse")
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Import: %v", err)
	}

	raw, err := afero.ReadFile(fs, SidecarPath("survey.pulse"))
	if err != nil {
		t.Fatalf("reading sidecar: %v", err)
	}
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decoding sidecar: %v", err)
	}

	derived := doc.Payload.Derived
	if len(derived) != 3 {
		t.Fatalf("sidecar registers %d derived column(s), want 3", len(derived))
	}
	wantAt := map[string]int{"income_missing": 1, "age_missing": 3, "score_missing": 5}
	for _, e := range derived {
		if e.Kind != DerivedKindNumericMissing {
			t.Errorf("%s.Kind = %q, want %q", e.Name, e.Kind, DerivedKindNumericMissing)
		}
		if len(e.Sources) != 1 || e.Sources[0]+MissingSiblingSuffix != e.Name {
			t.Errorf("%s.Sources = %v, want its one source variable", e.Name, e.Sources)
		}
		if got, ok := wantAt[e.Name]; !ok || e.Position != got {
			t.Errorf("%s.Position = %d, want %d", e.Name, e.Position, got)
		}
	}

	// The source variables' own positions account for the interleaving.
	wantVar := map[string]int{"income": 0, "age": 2, "score": 4, "weight": 6}
	for _, v := range doc.Payload.Variables {
		if got, ok := wantVar[v.Name]; !ok || v.Position != got {
			t.Errorf("variable %s.Position = %d, want %d", v.Name, v.Position, got)
		}
	}

	// The reason dictionary of the discrete column, which is what a fold
	// back to `.sav` reads to recover the original codes.
	var income Derived
	for _, e := range derived {
		if e.Name == "income_missing" {
			income = e
		}
	}
	if len(income.Reasons) != 4 {
		t.Fatalf("income_missing has %d reason(s), want 4", len(income.Reasons))
	}
	if r := income.Reasons[0]; !r.Sysmis || r.ID != 0 || r.Reason != SysmisReason || r.Code != nil {
		t.Errorf("reason 0 = %+v, want the sysmis entry at ID 0 with no code", r)
	}
	for i, want := range []struct {
		reason   string
		code     float64
		label    string
		declared bool
	}{
		{"Refused", 97, "Refused", true},
		{"Don't know", 98, "Don't know", true},
		{"99", 99, "", true},
	} {
		r := income.Reasons[i+1]
		if r.Reason != want.reason || r.Label != want.label || r.Declared != want.declared {
			t.Errorf("reason %d = %+v, want reason %q label %q declared %v", i+1, r, want.reason, want.label, want.declared)
		}
		if r.Code == nil || float64(*r.Code) != want.code {
			t.Errorf("reason %d code = %v, want %v", i+1, r.Code, want.code)
		}
		if !r.Observed {
			t.Errorf("reason %d is not marked observed, but the fixture carries it", i+1)
		}
	}

	// The RANGE column's entries are observed, never declared: a range is
	// not a finite vocabulary and must not be enumerated.
	for _, e := range derived {
		if e.Name != "age_missing" {
			continue
		}
		for _, r := range e.Reasons[1:] {
			if r.Declared {
				t.Errorf("age_missing reason %q is marked declared, but it came from a range", r.Reason)
			}
		}
	}
}

// TestMissing_HeaderIsDictionaryCheap guards the placement of
// planOutputs. ReadHeader must not need the data section: the layout is
// a function of the dictionary alone, and making the names depend on a
// scan would turn a cheap call into a whole-file read.
func TestMissing_HeaderIsDictionaryCheap(t *testing.T) {
	r := NewReaderFromBytes(buildFixture(t, missingFixtureSpec()))
	if _, err := r.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if r.mapped != nil {
		t.Error("ReadHeader resolved the whole schema mapping; the column layout comes from the dictionary alone")
	}
}

// TestMissing_HeaderMatchesSchema is the anti-drift check. ReadHeader and
// PulseSchema derive the layout through one function on purpose; if that
// ever stops being true, a cell lands under the wrong field name.
func TestMissing_HeaderMatchesSchema(t *testing.T) {
	for _, mode := range []MissingMode{MissingAuto, MissingNull} {
		r := NewReaderFromBytes(buildFixture(t, missingFixtureSpec()), WithMissingMode(mode))
		header, err := r.ReadHeader()
		if err != nil {
			t.Fatalf("%v ReadHeader: %v", mode, err)
		}
		schema, err := r.PulseSchema()
		if err != nil {
			t.Fatalf("%v PulseSchema: %v", mode, err)
		}
		if len(schema.Fields) != len(header) {
			t.Fatalf("%v: schema has %d field(s), header has %d", mode, len(schema.Fields), len(header))
		}
		for i, f := range schema.Fields {
			if f.Name != header[i] {
				t.Errorf("%v: field %d is %q but the header calls it %q", mode, i, f.Name, header[i])
			}
			if f.CsvColumnIdx != i {
				t.Errorf("%v: field %q reads row slot %d, want %d", mode, f.Name, f.CsvColumnIdx, i)
			}
		}
	}
}

// TestMissing_PredictSeesTheSameSchema checks the no-write path. Predict
// mirrors Run's schema resolution and then walks every row itself,
// indexing cells by CsvColumnIdx — so a layout the two disagreed about
// would show up here as a row error on a file that imports cleanly.
func TestMissing_PredictSeesTheSameSchema(t *testing.T) {
	src := NewReaderFromBytes(buildFixture(t, missingFixtureSpec()))
	job := pio.NewImportJob(src, "/dev/null")
	job.FS = afero.NewMemMapFs()
	report, err := job.Predict(context.Background())
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if report.EstimatedRows != 5 {
		t.Errorf("EstimatedRows = %d, want 5", report.EstimatedRows)
	}
	if len(report.Schema.Fields) != 7 {
		t.Fatalf("schema has %d field(s), want 7 (four variables plus three siblings)", len(report.Schema.Fields))
	}
	if report.Schema.Field("income_missing") == nil {
		t.Error("predict's schema has no income_missing; it must be the schema Run would write")
	}
}
