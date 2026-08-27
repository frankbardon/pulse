package spss

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// spssInstant renders a calendar instant as the SPSS datum for it: seconds
// since 1582-10-14, which is what every date and time format stores.
func spssInstant(y int, mo time.Month, d, h, mi, sec int) float64 {
	t := time.Date(y, mo, d, h, mi, sec, 0, time.UTC)
	return float64(t.Unix() + spssEpochOffsetSeconds)
}

func mustSchema(t *testing.T, r *Reader) *encoding.Schema {
	t.Helper()
	s, err := r.PulseSchema()
	if err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}
	return s
}

func schemaOf(t *testing.T, spec spsstest.Spec, opts ...Option) (*encoding.Schema, *Reader) {
	t.Helper()
	r := NewReaderFromBytes(build(t, spec), opts...)
	return mustSchema(t, r), r
}

// fieldOf returns the named field or fails.
func fieldOf(t *testing.T, s *encoding.Schema, name string) encoding.Field {
	t.Helper()
	f := s.Field(name)
	if f == nil {
		t.Fatalf("schema has no field %q", name)
	}
	return *f
}

// warningsOf collects the reader's warnings carrying the given code.
func warningsOf(r *Reader, code perr.Code) []*perr.CodedError {
	var out []*perr.CodedError
	for _, w := range r.Warnings() {
		if w.Code == code {
			out = append(out, w)
		}
	}
	return out
}

// columnOf returns the resolved mapping column for a variable.
func columnOf(t *testing.T, r *Reader, name string) columnMapping {
	t.Helper()
	m, err := r.loadMapping()
	if err != nil {
		t.Fatalf("loadMapping: %v", err)
	}
	for _, c := range m.cols {
		if c.name == name {
			return c
		}
	}
	t.Fatalf("mapping has no column %q", name)
	return columnMapping{}
}

// ---------------------------------------------------------------------------
// Numeric: f64 and nothing narrower
// ---------------------------------------------------------------------------

// TestMapping_NumericIsAlwaysF64 is the effort's headline rule. Every one
// of these columns would tempt a range probe into a narrower integer, and
// the last would tempt decimal128; the source declared a double, so a
// double is what the mapping produces.
func TestMapping_NumericIsAlwaysF64(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "TINY"},   // 0..3, would fit u4
			{Name: "SMALL"},  // 0..200, would fit u8
			{Name: "BIG"},    // would fit u32
			{Name: "MONEY"},  // two decimal places, would tempt decimal128
			{Name: "SIGNED"}, // negative, would fit no unsigned type at all
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(0), spsstest.Num(0), spsstest.Num(70000), spsstest.Num(12.34), spsstest.Num(-1)},
			{spsstest.Num(3), spsstest.Num(200), spsstest.Num(4000000), spsstest.Num(0.05), spsstest.Num(-99)},
		},
	}
	s, _ := schemaOf(t, spec)
	for _, name := range []string{"TINY", "SMALL", "BIG", "MONEY", "SIGNED"} {
		if got := fieldOf(t, s, name).Type; got != encoding.FieldTypeF64 {
			t.Errorf("%s.Type = %s, want f64", name, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Print-format dispatch
// ---------------------------------------------------------------------------

// TestMapping_PrintFormatDispatch is the acceptance table: day-resolution
// formats are dates, DATETIME is an instant, and TIME / DTIME are
// DURATIONS — which is why they are f64 seconds and not a temporal type.
//
// The dispatch was corroborated against R's haven (ReadStat), which shares
// no code with anything here. Handed the same fixture it reports DATE,
// ADATE, EDATE, SDATE and JDATE as R `Date` (day resolution), DATETIME as
// `POSIXct` (an instant, 2024-03-04 10:11:12), and TIME and DTIME as
// `hms`/`difftime` — durations of 01:01:01 and 25:01:01, i.e. the 3661 and
// 90061 raw seconds below. That is the split this table encodes. Repeat
// with:
//
//	Rscript -e 'x <- haven::read_sav("temporal.sav"); str(x)'
func TestMapping_PrintFormatDispatch(t *testing.T) {
	// Every value is midnight-aligned and after the Unix epoch so the
	// dispatch alone decides; the widening rules get their own test.
	midnight := spssInstant(2024, time.March, 4, 0, 0, 0)

	cases := []struct {
		name   string
		format spsstest.FormatType
		datum  float64
		want   encoding.FieldType
		kind   columnKind
	}{
		{"DATEV", spsstest.FormatDATE, midnight, encoding.FieldTypeDate, kindDate},
		{"ADATEV", spsstest.FormatADATE, midnight, encoding.FieldTypeDate, kindDate},
		{"EDATEV", spsstest.FormatEDATE, midnight, encoding.FieldTypeDate, kindDate},
		{"SDATEV", spsstest.FormatSDATE, midnight, encoding.FieldTypeDate, kindDate},
		{"JDATEV", spsstest.FormatJDATE, midnight, encoding.FieldTypeDate, kindDate},
		{"STAMP", spsstest.FormatDATETIME, spssInstant(2024, time.March, 4, 10, 11, 12),
			encoding.FieldTypeDateTime, kindDateTime},
		{"ELAPSED", spsstest.FormatTIME, 3661, encoding.FieldTypeF64, kindDuration},
		{"SPAN", spsstest.FormatDTIME, 90061, encoding.FieldTypeF64, kindDuration},
		{"PLAIN", spsstest.FormatF, 42, encoding.FieldTypeF64, kindNumeric},
	}

	spec := spsstest.Spec{}
	row := make([]spsstest.Value, 0, len(cases))
	for _, c := range cases {
		spec.Vars = append(spec.Vars, spsstest.Var{
			Name:  c.name,
			Print: spsstest.Format{Type: c.format, Width: 20},
		})
		row = append(row, spsstest.Num(c.datum))
	}
	spec.Cases = [][]spsstest.Value{row}

	s, r := schemaOf(t, spec)
	for _, c := range cases {
		if got := fieldOf(t, s, c.name).Type; got != c.want {
			t.Errorf("%s (format %d): Type = %s, want %s", c.name, c.format, got, c.want)
		}
		if got := columnOf(t, r, c.name).kind; got != c.kind {
			t.Errorf("%s (format %d): kind = %s, want %s", c.name, c.format, got, c.kind)
		}
	}

	// A duration keeps its format code so an export can reconstruct the
	// display form — the raw seconds alone would not say what they mean.
	if got := columnOf(t, r, "ELAPSED").printFormat.code; got != fmtTIME {
		t.Errorf("ELAPSED print format code = %d, want %d", got, fmtTIME)
	}
	if got := columnOf(t, r, "SPAN").printFormat.code; got != fmtDTIME {
		t.Errorf("SPAN print format code = %d, want %d", got, fmtDTIME)
	}
}

// TestMapping_DateWidensRatherThanCorrupt covers the two values an
// unsigned epoch-day `date` cannot hold. Both widen to datetime, which
// holds them exactly, and both say so.
func TestMapping_DateWidensRatherThanCorrupt(t *testing.T) {
	cases := []struct {
		name   string
		datum  float64
		reason string
	}{
		{"pre-1970 birth date", spssInstant(1955, time.June, 2, 0, 0, 0), "before 1970-01-01"},
		{"a time of day on a DATE column", spssInstant(2024, time.March, 4, 13, 30, 0), "time of day"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := spsstest.Spec{
				Vars: []spsstest.Var{{
					Name:  "DOB",
					Print: spsstest.Format{Type: spsstest.FormatDATE, Width: 11},
				}},
				Cases: [][]spsstest.Value{{spsstest.Num(c.datum)}},
			}
			s, r := schemaOf(t, spec)
			if got := fieldOf(t, s, "DOB").Type; got != encoding.FieldTypeDateTime {
				t.Fatalf("DOB.Type = %s, want datetime", got)
			}
			w := warningsOf(r, perr.PULSE_SPSS_DATE_WIDENED)
			if len(w) != 1 {
				t.Fatalf("got %d widening warning(s), want 1: %v", len(w), r.Warnings())
			}
			if !strings.Contains(w[0].Message, c.reason) {
				t.Errorf("message = %q, want it to name %q", w[0].Message, c.reason)
			}
			if got := w[0].Details[perr.DetailSPSSVariable]; got != "DOB" {
				t.Errorf("Details[%q] = %v, want DOB", perr.DetailSPSSVariable, got)
			}
			if got := w[0].Details[perr.DetailSPSSFormat]; got != fmtDATE {
				t.Errorf("Details[%q] = %v, want %d", perr.DetailSPSSFormat, got, fmtDATE)
			}
			// And the instant really does survive. The 1955 case is the
			// one that matters: encoding.ParseDate would have wrapped it
			// into a uint32 in the 4.29-billion range.
			rows := readAll(t, r)
			back, err := encoding.ParseDateTime(rows[0][0])
			if err != nil {
				t.Fatalf("ParseDateTime(%q): %v", rows[0][0], err)
			}
			want := uint64(int64(c.datum) - spssEpochOffsetSeconds)
			if back != want {
				t.Errorf("cell %q round-tripped to %d, want %d", rows[0][0], back, want)
			}
		})
	}
}

// TestMapping_TemporalPrecisionDropsToF64 is the recorded position on
// fractional seconds: E1's datetime wire form is second resolution, so a
// fractional value routes to lossless raw seconds instead of truncating.
// The same rule catches a non-finite datum, which no temporal type holds.
func TestMapping_TemporalPrecisionDropsToF64(t *testing.T) {
	cases := []struct {
		name   string
		format spsstest.FormatType
		datum  float64
	}{
		{"a fractional DATETIME", spsstest.FormatDATETIME, spssInstant(2024, time.March, 4, 10, 11, 12) + 0.5},
		{"a fractional DATE", spsstest.FormatDATE, spssInstant(2024, time.March, 4, 0, 0, 0) + 0.25},
		{"an infinite DATETIME", spsstest.FormatDATETIME, math.Inf(1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := spsstest.Spec{
				Vars: []spsstest.Var{{
					Name:  "WHEN",
					Print: spsstest.Format{Type: c.format, Width: 20},
				}},
				Cases: [][]spsstest.Value{{spsstest.Num(c.datum)}},
			}
			s, r := schemaOf(t, spec)
			if got := fieldOf(t, s, "WHEN").Type; got != encoding.FieldTypeF64 {
				t.Fatalf("WHEN.Type = %s, want f64", got)
			}
			if w := warningsOf(r, perr.PULSE_SPSS_TEMPORAL_PRECISION); len(w) != 1 {
				t.Fatalf("got %d precision warning(s), want 1: %v", len(w), r.Warnings())
			}
			// The raw seconds are still there — that is the point of the
			// downcast — so the cell round-trips through strconv.
			rows := readAll(t, r)
			back, err := strconv.ParseFloat(rows[0][0], 64)
			if err != nil {
				t.Fatalf("ParseFloat(%q): %v", rows[0][0], err)
			}
			if back != c.datum && !(math.IsInf(back, 1) && math.IsInf(c.datum, 1)) {
				t.Errorf("cell = %q, which is %v, want %v", rows[0][0], back, c.datum)
			}
		})
	}
}

// TestReadRows_TemporalRendering pins the cell text a temporal column
// emits. The literals must be exactly the ones encoding.ParseDate and
// encoding.ParseDateTime read back, because the shared import path parses
// them with nothing else.
func TestReadRows_TemporalRendering(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "DAY", Print: spsstest.Format{Type: spsstest.FormatDATE, Width: 11}},
			{Name: "STAMP", Print: spsstest.Format{Type: spsstest.FormatDATETIME, Width: 20}},
			{Name: "ELAPSED", Print: spsstest.Format{Type: spsstest.FormatTIME, Width: 10}},
		},
		Cases: [][]spsstest.Value{
			{
				spsstest.Num(spssInstant(2024, time.March, 4, 0, 0, 0)),
				spsstest.Num(spssInstant(2024, time.March, 4, 10, 11, 12)),
				spsstest.Num(3661),
			},
			{spsstest.SysMis(), spsstest.SysMis(), spsstest.SysMis()},
		},
	}
	r := NewReaderFromBytes(build(t, spec))
	assertRows(t, readAll(t, r), [][]string{
		{"2024-03-04", "2024-03-04T10:11:12Z", "3661"},
		{"", "", ""},
	})

	// And the literals really do convert back to the intended on-wire
	// values, which is the only property that matters downstream.
	day, err := encoding.ParseDate("2024-03-04")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	if want := uint32(time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC).Unix() / 86400); day != want {
		t.Errorf("epoch day = %d, want %d", day, want)
	}
	stamp, err := encoding.ParseDateTime("2024-03-04T10:11:12Z")
	if err != nil {
		t.Fatalf("ParseDateTime: %v", err)
	}
	if want := uint64(time.Date(2024, 3, 4, 10, 11, 12, 0, time.UTC).Unix()); stamp != want {
		t.Errorf("epoch seconds = %d, want %d", stamp, want)
	}
}

// ---------------------------------------------------------------------------
// Categoricals, and the code ↔ label ↔ ID triple
// ---------------------------------------------------------------------------

// TestMapping_ValueLabelledNumericIsCategorical is the second acceptance
// criterion, and with it the story's centrepiece: the dictionary holds the
// SPSS CODES in the file's declared order, and the triple carries the
// labels alongside the IDs.
func TestMapping_ValueLabelledNumericIsCategorical(t *testing.T) {
	spec := spsstest.Spec{
		Vars:  []spsstest.Var{{Name: "Q1", Label: "Satisfaction"}},
		Cases: [][]spsstest.Value{{spsstest.Num(1)}, {spsstest.Num(5)}, {spsstest.Num(9)}},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"Q1"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(1), Label: "Very satisfied"},
				{Value: spsstest.Num(2), Label: "Satisfied"},
				{Value: spsstest.Num(5), Label: "Dissatisfied"},
			},
		}},
	}
	s, r := schemaOf(t, spec)

	q1 := fieldOf(t, s, "Q1")
	if q1.Type != encoding.FieldTypeCategoricalU8 {
		t.Fatalf("Q1.Type = %s, want categorical_u8", q1.Type)
	}
	if q1.Dictionary == nil {
		t.Fatal("Q1 has no dictionary; an authoritative schema must pre-seed one")
	}
	// Declared codes in record order first, then the code the data
	// carried that no label declared.
	want := []string{"1", "2", "5", "9"}
	if got := q1.Dictionary.Values(); !equalStrings(got, want) {
		t.Fatalf("dictionary = %q, want %q (SPSS CODES in declared order, then appended)", got, want)
	}

	col := columnOf(t, r, "Q1")
	if len(col.categories) != 4 {
		t.Fatalf("got %d triple entr(ies), want 4: %+v", len(col.categories), col.categories)
	}
	type triple struct {
		id       uint32
		code     float64
		label    string
		labelled bool
		observed bool
	}
	wantTriples := []triple{
		{0, 1, "Very satisfied", true, true},
		{1, 2, "Satisfied", true, false}, // declared, never used
		{2, 5, "Dissatisfied", true, true},
		{3, 9, "", false, true}, // used, never declared
	}
	for i, w := range wantTriples {
		got := col.categories[i]
		if got.id != w.id || got.code != w.code || got.label != w.label ||
			got.labelled != w.labelled || got.observed != w.observed {
			t.Errorf("categories[%d] = %+v, want %+v", i, got, w)
		}
		if !got.numeric {
			t.Errorf("categories[%d].numeric = false, want true for a numeric variable", i)
		}
		if got.value != strconv.FormatFloat(w.code, 'g', -1, 64) {
			t.Errorf("categories[%d].value = %q, want the code rendering", i, got.value)
		}
	}

	// The cell text and the dictionary entry are the same string, which is
	// what makes the pre-seeded IDs the ones the import actually uses.
	assertRows(t, readAll(t, r), [][]string{{"1"}, {"5"}, {"9"}})
}

// TestMapping_StringIsCategorical covers the string half: values in
// first-seen order, the declared width retained for re-padding, and the
// trailing spaces SPSS pads with gone.
func TestMapping_StringIsCategorical(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{Name: "CITY", Width: 12, Label: "City of residence"}},
		Cases: [][]spsstest.Value{
			{spsstest.Text("Leeds")},
			{spsstest.Text("York")},
			{spsstest.Text("Leeds")},
		},
	}
	s, r := schemaOf(t, spec)

	city := fieldOf(t, s, "CITY")
	if city.Type != encoding.FieldTypeCategoricalU8 {
		t.Fatalf("CITY.Type = %s, want categorical_u8", city.Type)
	}
	if got := city.Dictionary.Values(); !equalStrings(got, []string{"Leeds", "York"}) {
		t.Errorf("dictionary = %q, want first-seen order with no padding", got)
	}
	if got := city.Description; got != "City of residence" {
		t.Errorf("Description = %q, want the SPSS variable label", got)
	}

	col := columnOf(t, r, "CITY")
	if col.declaredWidth != 12 {
		t.Errorf("declaredWidth = %d, want 12 — an export has to re-pad to it", col.declaredWidth)
	}
	for i, c := range col.categories {
		if c.numeric {
			t.Errorf("categories[%d].numeric = true for a string variable", i)
		}
		if c.text != c.value {
			t.Errorf("categories[%d]: text %q and value %q disagree", i, c.text, c.value)
		}
		if c.labelled {
			t.Errorf("categories[%d].labelled = true, but the file declared no labels", i)
		}
	}
}

// TestMapping_StringValueLabelsSeedTheOrder proves a short-string value
// label set drives the dictionary order the same way a numeric one does.
func TestMapping_StringValueLabelsSeedTheOrder(t *testing.T) {
	spec := spsstest.Spec{
		Vars:  []spsstest.Var{{Name: "GRADE", Width: 2}},
		Cases: [][]spsstest.Value{{spsstest.Text("C")}, {spsstest.Text("A")}},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"GRADE"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Text("A"), Label: "Distinction"},
				{Value: spsstest.Text("B"), Label: "Merit"},
			},
		}},
	}
	s, r := schemaOf(t, spec)
	if got := fieldOf(t, s, "GRADE").Dictionary.Values(); !equalStrings(got, []string{"A", "B", "C"}) {
		t.Errorf("dictionary = %q, want the declared keys first then the appended one", got)
	}
	col := columnOf(t, r, "GRADE")
	if !col.categories[0].labelled || col.categories[0].label != "Distinction" {
		t.Errorf("categories[0] = %+v, want the declared label", col.categories[0])
	}
	if col.categories[2].labelled {
		t.Errorf("categories[2] = %+v, want an appended entry with no label", col.categories[2])
	}
}

// TestMapping_CategoricalWidthByCardinality walks the u8 → u16 boundary
// with real data on both sides of it.
func TestMapping_CategoricalWidthByCardinality(t *testing.T) {
	cases := []struct {
		distinct int
		want     encoding.FieldType
	}{
		{1, encoding.FieldTypeCategoricalU8},
		{256, encoding.FieldTypeCategoricalU8},
		{257, encoding.FieldTypeCategoricalU16},
	}
	for _, c := range cases {
		t.Run(strconv.Itoa(c.distinct), func(t *testing.T) {
			spec := spsstest.Spec{Vars: []spsstest.Var{{Name: "CODE", Width: 8}}}
			for i := 0; i < c.distinct; i++ {
				spec.Cases = append(spec.Cases,
					[]spsstest.Value{spsstest.Text("v" + strconv.Itoa(i))})
			}
			// The fixtures are deliberately all-distinct, which is the
			// bloat signature; the warning is asserted elsewhere.
			s, _ := schemaOf(t, spec, WithCardinalityWarnFraction(2))
			if got := fieldOf(t, s, "CODE").Type; got != c.want {
				t.Errorf("%d distinct: Type = %s, want %s", c.distinct, got, c.want)
			}
		})
	}
}

// TestCategoricalTypeFor covers the width ladder directly, including the
// u32 backstop no fixture can reach.
func TestCategoricalTypeFor(t *testing.T) {
	cases := []struct {
		distinct int64
		want     encoding.FieldType
		ok       bool
	}{
		{0, encoding.FieldTypeCategoricalU8, true},
		{256, encoding.FieldTypeCategoricalU8, true},
		{257, encoding.FieldTypeCategoricalU16, true},
		{65536, encoding.FieldTypeCategoricalU16, true},
		{65537, encoding.FieldTypeCategoricalU32, true},
		{4294967295, encoding.FieldTypeCategoricalU32, true},
		{4294967296, 0, false},
		{-1, 0, false},
	}
	for _, c := range cases {
		if c.distinct > math.MaxInt32 && strconv.IntSize == 32 {
			continue
		}
		got, ok := categoricalTypeFor(int(c.distinct))
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("categoricalTypeFor(%d) = (%s, %v), want (%s, %v)",
				c.distinct, got, ok, c.want, c.ok)
		}
	}
}

// TestCategoricalOverflow_IsAHardError covers the u32 backstop's refusal.
// It is asserted at the error rather than through a fixture for the
// obvious reason: the trigger is four billion distinct values, so the
// ladder (TestCategoricalTypeFor) and the refusal it produces are tested
// separately and the two-line wiring between them is read, not run.
func TestCategoricalOverflow_IsAHardError(t *testing.T) {
	v := variable{name: "OTHER", longName: "OtherPleaseSpecify", offset: 0x1C0}
	const distinct = 5_000_000_000

	ce := categoricalOverflowError(v, distinct)
	if ce.Code != perr.PULSE_SPSS_CATEGORICAL_OVERFLOW {
		t.Errorf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_CATEGORICAL_OVERFLOW)
	}
	if got := ce.Details[perr.DetailSPSSVariable]; got != "OtherPleaseSpecify" {
		t.Errorf("Details[%q] = %v, want the long name", perr.DetailSPSSVariable, got)
	}
	if got := ce.Details[perr.DetailSPSSDistinct]; got != distinct {
		t.Errorf("Details[%q] = %v, want %d", perr.DetailSPSSDistinct, got, distinct)
	}
	if got := ce.Details[perr.DetailSPSSOffset]; got != 0x1C0 {
		t.Errorf("Details[%q] = %v, want the record type 2 offset", perr.DetailSPSSOffset, got)
	}
	if !strings.Contains(ce.Message, "OtherPleaseSpecify") {
		t.Errorf("message = %q, want it to name the variable", ce.Message)
	}
}

// TestMapping_CardinalityWarning is the schema-bloat signal: loud, but
// never a refusal, because the mapping is lossless and the cost is
// performance.
func TestMapping_CardinalityWarning(t *testing.T) {
	unique := func(n int) spsstest.Spec {
		spec := spsstest.Spec{Vars: []spsstest.Var{{Name: "OTHER", Width: 8}}}
		for i := 0; i < n; i++ {
			spec.Cases = append(spec.Cases,
				[]spsstest.Value{spsstest.Text("t" + strconv.Itoa(i))})
		}
		return spec
	}

	t.Run("one entry per case warns", func(t *testing.T) {
		s, r := schemaOf(t, unique(150))
		if fieldOf(t, s, "OTHER").Type == 0 {
			t.Fatal("no type resolved")
		}
		w := warningsOf(r, perr.PULSE_SPSS_CARDINALITY_HIGH)
		if len(w) != 1 {
			t.Fatalf("got %d cardinality warning(s), want 1: %v", len(w), r.Warnings())
		}
		if got := w[0].Details[perr.DetailSPSSDistinct]; got != 150 {
			t.Errorf("Details[%q] = %v, want 150", perr.DetailSPSSDistinct, got)
		}
		if got := w[0].Details[perr.DetailSPSSActualCases]; got != 150 {
			t.Errorf("Details[%q] = %v, want 150", perr.DetailSPSSActualCases, got)
		}
		if len(readAll(t, r)) != 150 {
			t.Error("the warning blocked the read; it must not")
		}
	})

	t.Run("a small fixture is below the floor", func(t *testing.T) {
		_, r := schemaOf(t, unique(4))
		if w := warningsOf(r, perr.PULSE_SPSS_CARDINALITY_HIGH); len(w) != 0 {
			t.Errorf("four all-distinct cases warned: %v", w)
		}
	})

	t.Run("the fraction is configurable", func(t *testing.T) {
		_, r := schemaOf(t, unique(150), WithCardinalityWarnFraction(2))
		if w := warningsOf(r, perr.PULSE_SPSS_CARDINALITY_HIGH); len(w) != 0 {
			t.Errorf("a disabled check still warned: %v", w)
		}
	})

	t.Run("a coded question does not warn", func(t *testing.T) {
		spec := spsstest.Spec{Vars: []spsstest.Var{{Name: "Q1", Width: 8}}}
		for i := 0; i < 150; i++ {
			spec.Cases = append(spec.Cases,
				[]spsstest.Value{spsstest.Text("v" + strconv.Itoa(i%5))})
		}
		_, r := schemaOf(t, spec)
		if w := warningsOf(r, perr.PULSE_SPSS_CARDINALITY_HIGH); len(w) != 0 {
			t.Errorf("five categories over 150 cases warned: %v", w)
		}
	})
}

// TestMapping_NullTokenCollision covers both halves of guess (1): an
// all-blank string is SPSS's missing-string convention and passes
// silently, but a literal "NA" is real data the shared import path
// collapses to null, and that must be visible.
func TestMapping_NullTokenCollision(t *testing.T) {
	t.Run("an all-blank string is silently null", func(t *testing.T) {
		spec := spsstest.Spec{
			Vars: []spsstest.Var{{Name: "NOTE", Width: 4}},
			Cases: [][]spsstest.Value{
				{spsstest.Text("ok")},
				{spsstest.Text("")},
			},
		}
		s, r := schemaOf(t, spec)
		if w := warningsOf(r, perr.PULSE_SPSS_NULL_TOKEN_COLLISION); len(w) != 0 {
			t.Errorf("a blank string warned: %v", w)
		}
		if !fieldOf(t, s, "NOTE").Nullable {
			t.Error("Nullable = false, but a blank string reads as null")
		}
		if got := fieldOf(t, s, "NOTE").Dictionary.Values(); !equalStrings(got, []string{"ok"}) {
			t.Errorf("dictionary = %q, want the blank excluded", got)
		}
	})

	t.Run("a literal NA is reported", func(t *testing.T) {
		spec := spsstest.Spec{
			Vars: []spsstest.Var{{Name: "NOTE", Width: 4}},
			Cases: [][]spsstest.Value{
				{spsstest.Text("ok")},
				{spsstest.Text("NA")},
			},
		}
		_, r := schemaOf(t, spec)
		w := warningsOf(r, perr.PULSE_SPSS_NULL_TOKEN_COLLISION)
		if len(w) != 1 {
			t.Fatalf("got %d warning(s), want 1: %v", len(w), r.Warnings())
		}
		if !strings.Contains(w[0].Message, `"NA"`) {
			t.Errorf("message = %q, want it to name the value", w[0].Message)
		}
	})
}

// TestMapping_ValueCollision is the one reachable way two distinct SPSS
// values become one Pulse dictionary entry: io/import.go trims every cell,
// so leading whitespace is not distinguishing. Both source values stay in
// the triple against the shared id so the ambiguity is visible.
func TestMapping_ValueCollision(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{Name: "CODE", Width: 4}},
		Cases: [][]spsstest.Value{
			{spsstest.Text("X")},
			{spsstest.Text(" X")},
		},
	}
	s, r := schemaOf(t, spec)
	if got := fieldOf(t, s, "CODE").Dictionary.Values(); !equalStrings(got, []string{"X"}) {
		t.Fatalf("dictionary = %q, want one entry", got)
	}
	if w := warningsOf(r, perr.PULSE_SPSS_VALUE_COLLISION); len(w) != 1 {
		t.Fatalf("got %d collision warning(s), want 1: %v", len(w), r.Warnings())
	}
	col := columnOf(t, r, "CODE")
	if len(col.categories) != 2 {
		t.Fatalf("got %d triple entr(ies), want both source values: %+v", len(col.categories), col.categories)
	}
	if col.categories[0].id != col.categories[1].id {
		t.Errorf("ids %d and %d differ; a collision shares one id",
			col.categories[0].id, col.categories[1].id)
	}
	if col.categories[1].text != " X" {
		t.Errorf("categories[1].text = %q, want the untrimmed source value", col.categories[1].text)
	}
}

// TestMapping_LabelOnSysmisIsNotAnEntry: a label bound to the
// system-missing sentinel can never match a datum, so it must not occupy
// an ID that would shift every later code.
func TestMapping_LabelOnSysmisIsNotAnEntry(t *testing.T) {
	spec := spsstest.Spec{
		Vars:  []spsstest.Var{{Name: "Q1"}},
		Cases: [][]spsstest.Value{{spsstest.Num(1)}},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"Q1"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(spsstest.SysMisDouble), Label: "System missing"},
				{Value: spsstest.Num(1), Label: "Yes"},
			},
		}},
	}
	s, r := schemaOf(t, spec)
	if got := fieldOf(t, s, "Q1").Dictionary.Values(); !equalStrings(got, []string{"1"}) {
		t.Errorf("dictionary = %q, want only the reachable code", got)
	}
	if w := warningsOf(r, perr.PULSE_SPSS_NULL_TOKEN_COLLISION); len(w) != 1 {
		t.Errorf("got %d warning(s), want the unreachable label reported: %v", len(w), r.Warnings())
	}
}

// ---------------------------------------------------------------------------
// Nullability, descriptions, offsets
// ---------------------------------------------------------------------------

// TestMapping_NullabilityIsAFactNotASample: the mapping walks every case,
// so a field is nullable exactly when one of them is null. The
// SchemaAwareReader contract forbids out-of-sample promotion, which is
// only safe because of this.
func TestMapping_NullabilityIsAFactNotASample(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{Name: "ALWAYS"}, {Name: "SOMETIME"}},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Num(1)},
			{spsstest.Num(2), spsstest.SysMis()},
		},
	}
	s, _ := schemaOf(t, spec)
	if fieldOf(t, s, "ALWAYS").Nullable {
		t.Error("ALWAYS.Nullable = true, but no case is missing")
	}
	if !fieldOf(t, s, "SOMETIME").Nullable {
		t.Error("SOMETIMES.Nullable = false, but a case is system-missing")
	}
}

// TestMapping_VariableLabelBecomesDescription is the acceptance criterion
// for the label → description mapping, including the absent case.
func TestMapping_VariableLabelBecomesDescription(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "ID", Label: "Respondent identifier"},
			{Name: "PLAIN"},
		},
		Cases: [][]spsstest.Value{{spsstest.Num(1), spsstest.Num(2)}},
	}
	s, _ := schemaOf(t, spec)
	if got := fieldOf(t, s, "ID").Description; got != "Respondent identifier" {
		t.Errorf("ID.Description = %q, want the variable label", got)
	}
	if got := fieldOf(t, s, "PLAIN").Description; got != "" {
		t.Errorf("PLAIN.Description = %q, want empty when the file declares no label", got)
	}
}

// TestMapping_SchemaGeometry pins the two slots the SchemaAwareReader
// contract calls load-bearing: CsvColumnIdx is the row position, and the
// byte offsets are contiguous in the declared widths.
func TestMapping_SchemaGeometry(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "A"},
			{Name: "B", Width: 20},
			{Name: "C", Print: spsstest.Format{Type: spsstest.FormatDATE, Width: 11}},
		},
		Cases: [][]spsstest.Value{{
			spsstest.Num(1), spsstest.Text("x"),
			spsstest.Num(spssInstant(2024, time.March, 4, 0, 0, 0)),
		}},
	}
	s, _ := schemaOf(t, spec)
	offset := 0
	for i, f := range s.Fields {
		if f.CsvColumnIdx != i {
			t.Errorf("field %d CsvColumnIdx = %d, want %d", i, f.CsvColumnIdx, i)
		}
		if f.ByteOffset != offset {
			t.Errorf("field %d ByteOffset = %d, want %d", i, f.ByteOffset, offset)
		}
		offset += f.Type.ByteSize()
	}
	if got := s.RecordByteSize(); got != offset {
		t.Errorf("RecordByteSize = %d, want %d (no field is nullable here)", got, offset)
	}
}

// TestPulseSchema_FreshDictionaryPerCall: encoding.Dictionary is mutable
// and the import path appends to it, so two imports off one reader must
// not share one.
func TestPulseSchema_FreshDictionaryPerCall(t *testing.T) {
	spec := spsstest.Spec{
		Vars:  []spsstest.Var{{Name: "CITY", Width: 8}},
		Cases: [][]spsstest.Value{{spsstest.Text("Leeds")}},
	}
	r := NewReaderFromBytes(build(t, spec))
	first := mustSchema(t, r)
	second := mustSchema(t, r)
	if first == second {
		t.Fatal("PulseSchema returned the same schema pointer twice")
	}
	if first.Fields[0].Dictionary == second.Fields[0].Dictionary {
		t.Fatal("PulseSchema shared one mutable dictionary between two calls")
	}
	if _, err := first.Fields[0].Dictionary.Add("York"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if second.Fields[0].Dictionary.Count() != 1 {
		t.Error("appending to one schema's dictionary leaked into the other's")
	}
}

// TestPulseSchema_FailsRatherThanDeclines: a `.sav` always has a
// dictionary, so PulseSchema never returns (nil, nil) — the interface's
// decline path. A source it cannot read through must fail the import with
// the coded error rather than quietly fall back to inference and produce
// a differently-typed cohort.
func TestPulseSchema_FailsRatherThanDeclines(t *testing.T) {
	raw := build(t, spsstest.ReferenceSpec())
	r := NewReaderFromBytes(raw[:len(raw)-3])
	s, err := r.PulseSchema()
	if err == nil {
		t.Fatalf("a truncated file yielded a schema: %+v", s)
	}
	if got := codedError(t, err).Code; got != perr.PULSE_SPSS_DATA_TRUNCATED {
		t.Errorf("code = %s, want %s", got, perr.PULSE_SPSS_DATA_TRUNCATED)
	}
}

// ---------------------------------------------------------------------------
// Measure level
// ---------------------------------------------------------------------------

// TestMapping_MeasureLevelHints is the smart-default criterion. The level
// sets the hint where the file declares one; the mapped type supplies it
// otherwise. It never selects a type — an unlabelled nominal numeric is
// still f64, because narrowing on optional metadata would make two
// otherwise identical files map differently.
func TestMapping_MeasureLevelHints(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "NOM", Measure: spsstest.MeasureNominal},
			{Name: "ORD", Measure: spsstest.MeasureOrdinal},
			{Name: "SCA", Measure: spsstest.MeasureScale},
			{Name: "UNSET"},
			{Name: "UNSETCAT", Width: 4},
		},
		Cases: [][]spsstest.Value{{
			spsstest.Num(1), spsstest.Num(1), spsstest.Num(1), spsstest.Num(1),
			spsstest.Text("a"),
		}},
		DisplayParams: true,
	}
	_, r := schemaOf(t, spec)

	cases := []struct {
		name  string
		agg   types.AggregationType
		group types.GroupType
		ft    encoding.FieldType
	}{
		{"NOM", types.AGG_FREQUENCY, types.GROUP_CATEGORY, encoding.FieldTypeF64},
		{"ORD", types.AGG_FREQUENCY, types.GROUP_CATEGORY, encoding.FieldTypeF64},
		{"SCA", types.AGG_SUM, types.GROUP_RANGE, encoding.FieldTypeF64},
		{"UNSET", types.AGG_SUM, types.GROUP_RANGE, encoding.FieldTypeF64},
		{"UNSETCAT", types.AGG_FREQUENCY, types.GROUP_CATEGORY, encoding.FieldTypeCategoricalU8},
	}
	for _, c := range cases {
		col := columnOf(t, r, c.name)
		if col.defaultAgg != c.agg || col.defaultGroup != c.group {
			t.Errorf("%s hints = %s / %s, want %s / %s",
				c.name, col.defaultAgg, col.defaultGroup, c.agg, c.group)
		}
		if col.fieldType != c.ft {
			t.Errorf("%s.fieldType = %s, want %s — measure level must not select a type",
				c.name, col.fieldType, c.ft)
		}
	}

	// A date's hint has no aggregator: summing instants is never the intent.
	if agg, group := defaultHints(encoding.FieldTypeDate, measureUnset); agg != "" || group != types.GROUP_DATE {
		t.Errorf("date hints = %s / %s, want none / %s", agg, group, types.GROUP_DATE)
	}
}

// TestMapping_MeasureLevelMismatch: a scale variable that carries value
// labels maps categorical, which changes the defaults Pulse will apply.
// The mapping reports the disagreement instead of resolving it by guess.
func TestMapping_MeasureLevelMismatch(t *testing.T) {
	spec := spsstest.Spec{
		Vars:          []spsstest.Var{{Name: "AGE", Measure: spsstest.MeasureScale}},
		Cases:         [][]spsstest.Value{{spsstest.Num(41)}, {spsstest.Num(99)}},
		DisplayParams: true,
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars:   []string{"AGE"},
			Labels: []spsstest.ValueLabel{{Value: spsstest.Num(99), Label: "Refused"}},
		}},
	}
	s, r := schemaOf(t, spec)
	if got := fieldOf(t, s, "AGE").Type; got != encoding.FieldTypeCategoricalU8 {
		t.Fatalf("AGE.Type = %s, want categorical_u8", got)
	}
	w := warningsOf(r, perr.PULSE_SPSS_MEASURE_LEVEL_MISMATCH)
	if len(w) != 1 {
		t.Fatalf("got %d mismatch warning(s), want 1: %v", len(w), r.Warnings())
	}
	if got := w[0].Details[perr.DetailSPSSVariable]; got != "AGE" {
		t.Errorf("Details[%q] = %v, want AGE", perr.DetailSPSSVariable, got)
	}

	// The nominal direction is the mandate's own no-narrowing rule, so it
	// must stay silent — warning there would train callers to ignore the
	// channel.
	quiet := spsstest.Spec{
		Vars:          []spsstest.Var{{Name: "Q1", Measure: spsstest.MeasureNominal}},
		Cases:         [][]spsstest.Value{{spsstest.Num(1)}},
		DisplayParams: true,
	}
	_, qr := schemaOf(t, quiet)
	if w := warningsOf(qr, perr.PULSE_SPSS_MEASURE_LEVEL_MISMATCH); len(w) != 0 {
		t.Errorf("an unlabelled nominal numeric warned: %v", w)
	}
}

// ---------------------------------------------------------------------------
// End to end
// ---------------------------------------------------------------------------

// TestPulseSchema_ImportRoundTrip is the property that matters more than
// any single mapping rule: the schema PulseSchema hands over and the cells
// ReadRows emits must agree, or every row of a real import fails. It runs
// the shared io.ImportJob against a `.sav` and reads the cohort back out.
func TestPulseSchema_ImportRoundTrip(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "ID"},
			{Name: "Q1", Label: "Satisfaction"},
			{Name: "CITY", Width: 12},
			{Name: "DOB", Print: spsstest.Format{Type: spsstest.FormatDATE, Width: 11}},
			{Name: "SEEN", Print: spsstest.Format{Type: spsstest.FormatDATETIME, Width: 20}},
			{Name: "ELAPSED", Print: spsstest.Format{Type: spsstest.FormatTIME, Width: 10}},
		},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"Q1"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(1), Label: "Very satisfied"},
				{Value: spsstest.Num(5), Label: "Dissatisfied"},
			},
		}},
		Cases: [][]spsstest.Value{
			{
				spsstest.Num(1), spsstest.Num(1), spsstest.Text("Leeds"),
				spsstest.Num(spssInstant(1990, time.April, 12, 0, 0, 0)),
				spsstest.Num(spssInstant(2024, time.March, 4, 10, 11, 12)),
				spsstest.Num(3661),
			},
			{
				spsstest.Num(2), spsstest.Num(9), spsstest.Text("York"),
				spsstest.Num(spssInstant(2001, time.January, 2, 0, 0, 0)),
				spsstest.SysMis(),
				spsstest.Num(59),
			},
		},
	}

	fs := afero.NewMemMapFs()
	src := NewReaderFromBytes(build(t, spec))
	job := pio.NewImportJob(src, "survey.pulse")
	job.FS = fs
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.RowErrors) != 0 {
		t.Fatalf("RowErrors = %v, want none — the schema and the cells disagree", report.RowErrors)
	}
	if len(report.PromotedFields) != 0 {
		t.Errorf("PromotedFields = %v, want none: an authoritative schema never promotes",
			report.PromotedFields)
	}
	if report.RowsImported != 2 {
		t.Fatalf("RowsImported = %d, want 2", report.RowsImported)
	}

	// The import consumed the authoritative types, not inferred ones.
	want := map[string]encoding.FieldType{
		"ID":      encoding.FieldTypeF64,
		"Q1":      encoding.FieldTypeCategoricalU8,
		"CITY":    encoding.FieldTypeCategoricalU8,
		"DOB":     encoding.FieldTypeDate,
		"SEEN":    encoding.FieldTypeDateTime,
		"ELAPSED": encoding.FieldTypeF64,
	}
	for name, ft := range want {
		if got := report.Schema.Field(name).Type; got != ft {
			t.Errorf("%s.Type = %s, want %s", name, got, ft)
		}
	}
	// The pre-seeded dictionary was USED, not rebuilt: the appended code 9
	// sits after the two declared codes, so an ID still means its code.
	if got := report.Schema.Field("Q1").Dictionary.Values(); !equalStrings(got, []string{"1", "5", "9"}) {
		t.Errorf("Q1 dictionary after import = %q, want the declared order preserved", got)
	}

	// And the cohort reads back as the values the file carried.
	writer := &rowCollector{}
	exp := pio.NewExportJob("survey.pulse", writer)
	exp.FS = fs
	if _, err := exp.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	wantRows := [][]string{
		{"1", "1", "Leeds", "1990-04-12", "2024-03-04T10:11:12Z", "3661"},
		{"2", "9", "York", "2001-01-02", "", "59"},
	}
	if len(writer.rows) != len(wantRows) {
		t.Fatalf("exported %d row(s), want %d", len(writer.rows), len(wantRows))
	}
	for i, row := range writer.rows {
		for j, cell := range row {
			if got := toString(cell); got != wantRows[i][j] {
				t.Errorf("row %d column %d = %q, want %q", i, j, got, wantRows[i][j])
			}
		}
	}
}

// rowCollector is a pio.Writer that keeps what it is given.
type rowCollector struct {
	columns []string
	rows    [][]any
}

func (w *rowCollector) WriteHeader(columns []string) error {
	w.columns = append([]string(nil), columns...)
	return nil
}

func (w *rowCollector) WriteRow(values []any) error {
	w.rows = append(w.rows, append([]any(nil), values...))
	return nil
}

func (w *rowCollector) Close() error { return nil }

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
