package spss

// Tests for the CATEGORICAL arm of the user-missing mapping.
//
// The claim under test is not "a flag appears". It is that a coded
// question's refusal code survives the import as an ordinary, addressable
// dictionary entry — losslessly, with no sibling column — AND that an
// analyst can find out which entry it is without reading the source
// `.sav`. A flag nobody can see is not a resolution; that is why the
// discoverability tests below are the substantive ones.

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	"github.com/spf13/afero"
)

// categoricalMissingSpec is the canonical fixture: a coded question whose
// missing code is one label among several (so it classifies categorical),
// a string variable with a text missing code, and a control that declares
// no missing values at all.
func categoricalMissingSpec() spsstest.Spec {
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{
				Name: "Q1", LongName: "q1", Label: "Satisfied?",
				Print:   spsstest.Format{Type: spsstest.FormatF, Width: 1},
				Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(9)}},
			},
			{
				Name: "CODE", LongName: "code", Width: 4,
				Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Text("REF")}},
			},
			{
				Name: "Q2", LongName: "q2",
				Print: spsstest.Format{Type: spsstest.FormatF, Width: 1},
			},
		},
		ValueLabels: []spsstest.ValueLabelSet{
			{
				Vars: []string{"Q1"},
				Labels: []spsstest.ValueLabel{
					{Value: spsstest.Num(1), Label: "Yes"},
					{Value: spsstest.Num(2), Label: "No"},
					{Value: spsstest.Num(9), Label: "Refused"},
				},
			},
			{
				Vars: []string{"Q2"},
				Labels: []spsstest.ValueLabel{
					{Value: spsstest.Num(1), Label: "Yes"},
					{Value: spsstest.Num(2), Label: "No"},
				},
			},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Text("AB"), spsstest.Num(1)},
			{spsstest.Num(9), spsstest.Text("REF"), spsstest.Num(2)},
			{spsstest.Num(2), spsstest.Text("CD"), spsstest.Num(1)},
		},
	}
}

// ---------------------------------------------------------------------------
// The codes stay in the dictionary, and no sibling appears
// ---------------------------------------------------------------------------

// TestMissingCategorical_CodesStayOrdinaryDictionaryEntries is the first
// acceptance criterion. A categorical variable's user-missing code is an
// ordinary labelled dictionary entry — same ID space, same file order —
// and the case that carried it still says so in the data.
//
// This is the whole reason the categorical arm needs no sibling: nothing
// was removed, so nothing has to be reconstructed.
func TestMissingCategorical_CodesStayOrdinaryDictionaryEntries(t *testing.T) {
	r := NewReaderFromBytes(buildFixture(t, categoricalMissingSpec()))
	schema, err := r.PulseSchema()
	if err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}

	q1 := schema.Field("q1")
	if q1 == nil || q1.Dictionary == nil {
		t.Fatalf("q1 has no dictionary; fields = %+v", schema.Fields)
	}
	// File order, and 9 among them: the missing specification does not
	// reorder, skip or re-ID anything.
	if want := []string{"1", "2", "9"}; !equalStrings(q1.Dictionary.Values(), want) {
		t.Errorf("q1 dictionary = %q, want %q — the missing code must be an ordinary entry in file order", q1.Dictionary.Values(), want)
	}

	code := schema.Field("code")
	if code == nil || code.Dictionary == nil {
		t.Fatalf("code has no dictionary")
	}
	if !containsString(code.Dictionary.Values(), "REF") {
		t.Errorf("code dictionary = %q, want it to contain the string missing code %q", code.Dictionary.Values(), "REF")
	}

	_, rows := readHeaderAndRows(t, r)
	if rows[1][0] != "9" {
		t.Errorf("q1 row 1 = %q, want %q — nulling a categorical missing code would lose the value, which for a categorical IS the reason", rows[1][0], "9")
	}
	if rows[1][1] != "REF" {
		t.Errorf("code row 1 = %q, want %q", rows[1][1], "REF")
	}
}

// TestMissingCategorical_AllCategoricalSurveyHasNoSiblings is the
// criterion that pins the width argument. Every variable in this fixture
// is categorical and every one declares a user-missing code; the cohort
// must be exactly as wide as the source dictionary.
//
// TestMissing_CategoricalGetsNoSibling (E4-S2) covers the two-variable
// boundary; this one covers the shape the decision was actually made
// about — a whole questionnaire, where per-variable siblings would double
// the schema to carry nothing new.
func TestMissingCategorical_AllCategoricalSurveyHasNoSiblings(t *testing.T) {
	spec := allCategoricalSurveySpec(12)
	r := NewReaderFromBytes(buildFixture(t, spec))
	header, rows := readHeaderAndRows(t, r)

	if len(header) != len(spec.Vars) {
		t.Fatalf("header has %d column(s) for %d variable(s): %q — an all-categorical survey must produce no sibling columns at all",
			len(header), len(spec.Vars), header)
	}
	for _, name := range header {
		if strings.HasSuffix(name, MissingSiblingSuffix) {
			t.Errorf("column %q is a generated sibling; the categorical arm generates none", name)
		}
	}
	// And the refusal codes are still in the data.
	for i := range spec.Vars {
		if rows[1][i] != "9" {
			t.Errorf("row 1 column %d = %q, want the refusal code %q", i, rows[1][i], "9")
		}
	}
}

// allCategoricalSurveySpec builds n coded questions, every one of them
// value-labelled on 1/2/9 with 9 declared user-missing.
func allCategoricalSurveySpec(n int) spsstest.Spec {
	spec := spsstest.Spec{}
	present := make([]spsstest.Value, 0, n)
	refused := make([]spsstest.Value, 0, n)
	for i := 0; i < n; i++ {
		name := "Q" + string(rune('A'+i))
		spec.Vars = append(spec.Vars, spsstest.Var{
			Name:    name,
			Print:   spsstest.Format{Type: spsstest.FormatF, Width: 1},
			Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(9)}},
		})
		spec.ValueLabels = append(spec.ValueLabels, spsstest.ValueLabelSet{
			Vars: []string{name},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(1), Label: "Yes"},
				{Value: spsstest.Num(2), Label: "No"},
				{Value: spsstest.Num(9), Label: "Refused"},
			},
		})
		present = append(present, spsstest.Num(1))
		refused = append(refused, spsstest.Num(9))
	}
	spec.Cases = [][]spsstest.Value{present, refused}
	return spec
}

// ---------------------------------------------------------------------------
// The sidecar flag
// ---------------------------------------------------------------------------

// TestMissingCategorical_SidecarFlagsTheMissingEntries is the persistent
// half of the discoverability answer: which dictionary entries are
// missing-coded, per variable, exactly.
func TestMissingCategorical_SidecarFlagsTheMissingEntries(t *testing.T) {
	_, _, doc := importFixture(t, categoricalMissingSpec())

	for _, tc := range []struct {
		field       string
		wantMissing []string
		wantPresent []string
	}{
		{field: "q1", wantMissing: []string{"9"}, wantPresent: []string{"1", "2"}},
		{field: "code", wantMissing: []string{"REF"}, wantPresent: []string{"AB", "CD"}},
		{field: "q2", wantMissing: nil, wantPresent: []string{"1", "2"}},
	} {
		t.Run(tc.field, func(t *testing.T) {
			v := varOf(t, doc, tc.field)
			var got []string
			for _, c := range v.Categories {
				if c.Missing {
					got = append(got, c.Value)
				}
			}
			if !equalStrings(got, tc.wantMissing) {
				t.Errorf("flagged entries = %q, want %q", got, tc.wantMissing)
			}
			for _, want := range tc.wantPresent {
				for _, c := range v.Categories {
					if c.Value == want && c.Missing {
						t.Errorf("entry %q is flagged missing but is an ordinary answer", want)
					}
				}
			}
		})
	}
}

// TestMissingCategorical_FlagRidesAlongsideTheLabel proves the flag does
// not displace the triple: the flagged entry keeps its SPSS code, its
// label and its Pulse ID, so a consumer can render "Refused" while
// excluding "9".
func TestMissingCategorical_FlagRidesAlongsideTheLabel(t *testing.T) {
	_, _, doc := importFixture(t, categoricalMissingSpec())
	v := varOf(t, doc, "q1")

	var found bool
	for _, c := range v.Categories {
		if !c.Missing {
			continue
		}
		found = true
		if c.Value != "9" {
			t.Errorf("flagged value = %q, want %q — the cohort stores CODES", c.Value, "9")
		}
		if c.Label != "Refused" {
			t.Errorf("flagged label = %q, want %q; the flag must not displace the triple", c.Label, "Refused")
		}
		if c.Code == nil || float64(*c.Code) != 9 {
			t.Errorf("flagged code = %v, want 9", c.Code)
		}
		if !c.Labelled || !c.Observed {
			t.Errorf("flagged entry labelled=%v observed=%v, want both true", c.Labelled, c.Observed)
		}
	}
	if !found {
		t.Fatal("no flagged entry on q1")
	}

	// The full missing-value specification still rides its own slot: the
	// flag is a projection onto the dictionary, never a replacement for
	// the declaration.
	if v.Missing == nil || len(v.Missing.Discrete) != 1 || float64(v.Missing.Discrete[0]) != 9 {
		t.Errorf("missing spec = %+v, want the declared discrete code 9 retained", v.Missing)
	}
}

// TestMissingCategorical_NoFlagIsWireIdentical pins the additive
// contract. A file declaring no categorical user-missing codes produces a
// document byte-identical to the pre-flag shape, because Missing is
// omitempty — which is why SidecarFormatVersion does not move.
func TestMissingCategorical_NoFlagIsWireIdentical(t *testing.T) {
	fs, cohort := importFixtureNoSidecar(t, spsstest.ReferenceSpec())
	raw, err := afero.ReadFile(fs, SidecarPath(cohort))
	if err != nil {
		t.Fatalf("reading sidecar: %v", err)
	}
	if strings.Contains(string(raw), `"missing":`) {
		t.Errorf("a file with no categorical user-missing codes emitted a \"missing\" key:\n%s", raw)
	}
	doc := readSidecar(t, fs, cohort)
	if doc.FormatVersion != SidecarFormatVersion {
		t.Errorf("format_version = %d, want %d — an additive omitempty slot does not bump it", doc.FormatVersion, SidecarFormatVersion)
	}
}

// ---------------------------------------------------------------------------
// Long strings: record 7/22 lands on the same slot
// ---------------------------------------------------------------------------

// TestMissing_LongStringCodesAreFlagged makes the routing explicit. E4-S2
// deferred STRING user-missing to this arm; a record 7/22 long-string
// specification binds to the same variable.missing slot a record type 2
// one does, so it is covered by the same call with no branch.
//
// If that binding ever changes, this test is what says so.
func TestMissing_LongStringCodesAreFlagged(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{Name: "COMMENT", LongName: "comment", Width: 40}},
		Cases: [][]spsstest.Value{
			{spsstest.Text("all good")},
			{spsstest.Text("REFUSED")},
			{spsstest.Text("DK")},
		},
		LongStringMissingValues: []spsstest.LongStringMissingValues{
			{Var: "COMMENT", Values: []string{"REFUSED", "DK"}},
		},
	}

	// No sibling: a string variable is categorical, so the same rule
	// applies as to a value-labelled numeric.
	r := NewReaderFromBytes(buildFixture(t, spec))
	header, _ := readHeaderAndRows(t, r)
	if !equalStrings(header, []string{"comment"}) {
		t.Fatalf("header = %q, want the one source variable and no sibling", header)
	}

	_, _, doc := importFixture(t, spec)
	v := varOf(t, doc, "comment")
	var flagged []string
	for _, c := range v.Categories {
		if c.Missing {
			flagged = append(flagged, c.Value)
		}
	}
	// Dictionary ID order, which for an unlabelled string is first-seen
	// order in the data section.
	if want := []string{"REFUSED", "DK"}; !equalStrings(flagged, want) {
		t.Errorf("flagged entries = %q, want %q — record 7/22 values must flag through the same call as a record type 2 spec", flagged, want)
	}
	if !containsString(flagged, "REFUSED") {
		t.Errorf("the 7/22 code %q is not flagged", "REFUSED")
	}
}

// ---------------------------------------------------------------------------
// Import-time discoverability
// ---------------------------------------------------------------------------

// TestMissingCategorical_ImportWarnsOnce is the import-time half of the
// discoverability answer, and the noise budget that goes with it. A
// twelve-variable all-categorical survey raises exactly ONE diagnostic,
// not twelve.
func TestMissingCategorical_ImportWarnsOnce(t *testing.T) {
	r := NewReaderFromBytes(buildFixture(t, allCategoricalSurveySpec(12)))
	if _, err := r.PulseSchema(); err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}

	var found []*errors.CodedError
	for _, w := range r.Warnings() {
		if w.Code == errors.PULSE_SPSS_CATEGORICAL_USER_MISSING {
			found = append(found, w)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d PULSE_SPSS_CATEGORICAL_USER_MISSING diagnostics, want exactly 1:\n%s",
			len(found), warningText(r.Warnings()))
	}

	w := found[0]
	detail, ok := w.Details[errors.DetailSPSSMissingCategories].(map[string][]string)
	if !ok {
		t.Fatalf("details[%s] = %T, want map[string][]string", errors.DetailSPSSMissingCategories, w.Details[errors.DetailSPSSMissingCategories])
	}
	if len(detail) != 12 {
		t.Errorf("details cover %d variable(s), want 12 — the prose is capped, the details are not", len(detail))
	}
	for name, values := range detail {
		if !equalStrings(values, []string{"9"}) {
			t.Errorf("%s flagged %q, want [\"9\"]", name, values)
		}
	}
	if got := w.Details[errors.DetailSPSSDistinct]; got != 12 {
		t.Errorf("details[distinct] = %v, want 12", got)
	}

	// The prose caps the list rather than printing twelve names, and says
	// so, because this line is what a person at a CLI actually reads.
	if !strings.Contains(w.Message, "more") {
		t.Errorf("message does not cap its list:\n%s", w.Message)
	}
	if !strings.Contains(w.Message, "FILTER_EXCLUDE") {
		t.Errorf("message does not name the exclusion idiom:\n%s", w.Message)
	}
}

// TestMissingCategorical_WarningNamesTheEntryNotTheLabel guards the trap
// the story called out. The cohort's dictionary holds SPSS CODES, so the
// value an analyst types into FILTER_EXCLUDE is "9". A message that said
// "Refused" would send them to a value that is not in the cohort.
func TestMissingCategorical_WarningNamesTheEntryNotTheLabel(t *testing.T) {
	r := NewReaderFromBytes(buildFixture(t, categoricalMissingSpec()))
	if _, err := r.PulseSchema(); err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}
	w := warningWithCode(t, r.Warnings(), errors.PULSE_SPSS_CATEGORICAL_USER_MISSING)

	if !strings.Contains(w.Message, `"9"`) {
		t.Errorf("message does not name the dictionary entry %q:\n%s", "9", w.Message)
	}
	if strings.Contains(w.Message, "Refused") {
		t.Errorf("message names the LABEL, which is not in the cohort dictionary:\n%s", w.Message)
	}
	if !strings.Contains(w.Message, `"REF"`) {
		t.Errorf("message does not name the string missing code %q:\n%s", "REF", w.Message)
	}
}

// TestMissingCategorical_CleanFileIsSilent keeps the channel honest. A
// file with no categorical user-missing codes must raise nothing, or the
// diagnostic becomes background noise and stops being read.
func TestMissingCategorical_CleanFileIsSilent(t *testing.T) {
	r := NewReaderFromBytes(buildFixture(t, spsstest.ReferenceSpec()))
	if _, err := r.PulseSchema(); err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}
	if hasCode(r.Warnings(), errors.PULSE_SPSS_CATEGORICAL_USER_MISSING) {
		t.Errorf("a file with no categorical user-missing codes warned:\n%s", warningText(r.Warnings()))
	}
}

// TestMissingCategorical_NumericSiblingArmDoesNotWarn pins the boundary
// from the other side. INCOME labelled only on its missing codes goes to
// the NUMERIC arm and gets a sibling; it is not a categorical
// missing-coded dictionary and must not appear in this summary.
//
// This is also the confirmation of E4-S2's labelsCodeTheVariable rule,
// which that story flagged for this one to confirm or override. Confirmed
// unchanged: the rule is the line between "a sibling is the only home for
// the reason" and "the dictionary already is".
func TestMissingCategorical_NumericSiblingArmDoesNotWarn(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{
			Name: "INCOME", LongName: "income",
			Print:   spsstest.Format{Type: spsstest.FormatF, Width: 8},
			Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(97), spsstest.Num(98), spsstest.Num(99)}},
		}},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"INCOME"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(97), Label: "Refused"},
				{Value: spsstest.Num(98), Label: "Don't know"},
			},
		}},
		Cases: [][]spsstest.Value{
			{spsstest.Num(42000)},
			{spsstest.Num(97)},
		},
	}
	r := NewReaderFromBytes(buildFixture(t, spec))
	header, _ := readHeaderAndRows(t, r)
	if !equalStrings(header, []string{"income", "income" + MissingSiblingSuffix}) {
		t.Fatalf("header = %q, want the analytic column and its sibling — labels naming ONLY missing codes must not make the variable categorical", header)
	}
	if hasCode(r.Warnings(), errors.PULSE_SPSS_CATEGORICAL_USER_MISSING) {
		t.Errorf("the numeric-sibling arm raised the categorical summary:\n%s", warningText(r.Warnings()))
	}
}

// TestMissingCategorical_MissingModeDoesNotChangeTheCategoricalArm
// confirms E4-S2's statement from this side. --spss-missing=null
// suppresses SIBLINGS; a categorical column has none, so its codes, its
// dictionary, its flags and its summary are all identical under both
// modes. Two imports of one file must not disagree about what a field IS.
func TestMissingCategorical_MissingModeDoesNotChangeTheCategoricalArm(t *testing.T) {
	raw := buildFixture(t, categoricalMissingSpec())

	for _, mode := range []MissingMode{MissingAuto, MissingNull} {
		t.Run(mode.String(), func(t *testing.T) {
			r := NewReaderFromBytes(raw, WithMissingMode(mode))
			header, rows := readHeaderAndRows(t, r)
			if !equalStrings(header, []string{"q1", "code", "q2"}) {
				t.Fatalf("header = %q, want the three source columns", header)
			}
			if rows[1][0] != "9" || rows[1][1] != "REF" {
				t.Errorf("row 1 = %q, want the missing codes kept under %s", rows[1], mode)
			}
			if !hasCode(r.Warnings(), errors.PULSE_SPSS_CATEGORICAL_USER_MISSING) {
				t.Errorf("no categorical summary under %s; the mode governs siblings, not the categorical arm", mode)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func warningWithCode(t *testing.T, warnings []*errors.CodedError, code errors.Code) *errors.CodedError {
	t.Helper()
	for _, w := range warnings {
		if w.Code == code {
			return w
		}
	}
	t.Fatalf("no %s warning:\n%s", code, warningText(warnings))
	return nil
}
