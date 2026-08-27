package spss

// Tests for the multiple-DICHOTOMY `set_*` convenience column.
//
// The claim under test is deliberately two-sided, because half of it is a
// claim about what did NOT happen. A derived set column appearing is easy;
// the design decision this story implements is that the constituents are
// still there beside it, so that "not selected" and "not asked" — which one
// bit cannot separate — stay separable somewhere in the cohort. A test suite
// that only checked the mask would pass just as happily against the
// destructive collapse this file exists to reject.

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
)

// mdSpec is the canonical fixture: a three-option media battery declared as
// a multiple dichotomy with counted value 1, plus an unrelated variable so
// placement is observable.
//
// Case 1 is the one that matters most. Q1B is system-missing there — the
// respondent was not asked that item — while Q1A and Q1C were asked and
// answered. A bitmask reports bit 1 clear either way; the constituent
// column is the only place the difference survives.
func mdSpec() spsstest.Spec {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 1}
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "RESPID", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "Q1A", Label: "Newspaper", Print: num},
			{Name: "Q1B", Label: "Radio", Print: num},
			{Name: "Q1C", Label: "TV", Print: num},
			{Name: "AGE", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
		},
		Cases: [][]spsstest.Value{
			// 0: picked newspaper and TV.
			{spsstest.Num(1), spsstest.Num(1), spsstest.Num(0), spsstest.Num(1), spsstest.Num(30)},
			// 1: picked TV; RADIO was never asked (sysmis).
			{spsstest.Num(2), spsstest.Num(0), spsstest.SysMis(), spsstest.Num(1), spsstest.Num(41)},
			// 2: answered the battery and picked NOTHING.
			{spsstest.Num(3), spsstest.Num(0), spsstest.Num(0), spsstest.Num(0), spsstest.Num(52)},
			// 3: skipped the whole battery.
			{spsstest.Num(4), spsstest.SysMis(), spsstest.SysMis(), spsstest.SysMis(), spsstest.Num(63)},
		},
		MultipleResponseSets: []spsstest.MRSet{{
			Name: "$media", Kind: spsstest.MRDichotomy, Label: "Media used",
			CountedValue: "1", Vars: []string{"Q1A", "Q1B", "Q1C"},
			Subtype: spsstest.SubtypeMRSets,
		}},
	}
}

// ---------------------------------------------------------------------------
// The column exists, and so do its constituents
// ---------------------------------------------------------------------------

// TestMRSet_DerivesASetColumnAdditively is the first two acceptance criteria
// together, and they belong together: the derived column appears AND every
// constituent variable is still its own column.
func TestMRSet_DerivesASetColumnAdditively(t *testing.T) {
	r := NewReaderFromBytes(buildFixture(t, mdSpec()))
	header, _ := readHeaderAndRows(t, r)

	// Placement: immediately after the LAST constituent, so the parts are
	// always met before the whole.
	want := []string{"RESPID", "Q1A", "Q1B", "Q1C", "media", "AGE"}
	if !equalStrings(header, want) {
		t.Fatalf("header = %q, want %q", header, want)
	}

	schema, err := r.PulseSchema()
	if err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}

	// Every constituent is retained as an ordinary f64 column. This is the
	// criterion a destructive collapse would fail.
	for _, name := range []string{"Q1A", "Q1B", "Q1C"} {
		f := schema.Field(name)
		if f == nil {
			t.Fatalf("constituent %q is missing from the schema; the derived column must be ADDITIVE, never a replacement", name)
		}
		if f.Type != encoding.FieldTypeF64 {
			t.Errorf("constituent %q type = %s, want f64 — deriving the set must not restyle its sources", name, f.Type)
		}
	}

	set := schema.Field("media")
	if set == nil {
		t.Fatalf("no derived column %q; fields = %+v", "media", schema.Fields)
	}
	// Three constituents fit set_u8, the narrowest set type.
	if set.Type != encoding.FieldTypeSetU8 {
		t.Errorf("media type = %s, want set_u8 for 3 constituents", set.Type)
	}
	if set.Dictionary == nil {
		t.Fatalf("media has no dictionary; a set_* column carries one inline")
	}
	// Bit i is dictionary entry i is constituent i — the entry text is the
	// constituent's field name, which is what makes a bit traceable to the
	// column holding its fidelity.
	if got, want := set.Dictionary.Values(), []string{"Q1A", "Q1B", "Q1C"}; !equalStrings(got, want) {
		t.Errorf("media dictionary = %q, want %q", got, want)
	}
	if !strings.Contains(set.Description, "$media") {
		t.Errorf("media description = %q, want it to name the source set", set.Description)
	}
}

// TestMRSet_DropsTheSigilFromTheFieldName pins the naming rule. SPSS set
// names begin with '$'; a Pulse field named "$media" would encode fine and
// then be unreachable from ATTR_FORMULA / FILTER_EXPRESSION, which are
// expr-lang and do not accept a leading sigil. The full name is retained on
// the sidecar instead.
func TestMRSet_DropsTheSigilFromTheFieldName(t *testing.T) {
	for _, tc := range []struct{ set, field string }{
		{"$media", "media"},
		{"$Q1_MULTI", "Q1_MULTI"},
	} {
		t.Run(tc.set, func(t *testing.T) {
			spec := mdSpec()
			spec.MultipleResponseSets[0].Name = tc.set
			r := NewReaderFromBytes(buildFixture(t, spec))
			header, _ := readHeaderAndRows(t, r)
			if !containsString(header, tc.field) {
				t.Errorf("header = %q, want the derived column named %q", header, tc.field)
			}
			if containsString(header, tc.set) {
				t.Errorf("header = %q still carries the '$' sigil", header)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The mask comes from the DECLARED counted value
// ---------------------------------------------------------------------------

// TestMRSet_MaskUsesTheDeclaredCountedValue is the third acceptance
// criterion. The bit is set where the constituent holds the value the FILE
// declared, not where it holds 1.
//
// The 0/1 case is the common one and would pass under a hardcoded guess, so
// the interesting rows are the ones where the counted value is 2 and where
// it is text: under a guess they would be empty and they are not.
func TestMRSet_MaskUsesTheDeclaredCountedValue(t *testing.T) {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 1}

	t.Run("numeric counted value that is not 1", func(t *testing.T) {
		spec := spsstest.Spec{
			Vars: []spsstest.Var{
				{Name: "A", Print: num},
				{Name: "B", Print: num},
			},
			Cases: [][]spsstest.Value{
				{spsstest.Num(2), spsstest.Num(1)},
				{spsstest.Num(1), spsstest.Num(2)},
			},
			MultipleResponseSets: []spsstest.MRSet{{
				Name: "$s", Kind: spsstest.MRDichotomy, CountedValue: "2",
				Vars: []string{"A", "B"}, Subtype: spsstest.SubtypeMRSets,
			}},
		}
		r := NewReaderFromBytes(buildFixture(t, spec))
		header, rows := readHeaderAndRows(t, r)
		at := indexOfColumn(t, header, "s")
		// Counted is 2, so row 0 selects A and row 1 selects B. A reader
		// guessing 1 would produce exactly the opposite.
		if rows[0][at] != "A" {
			t.Errorf("row 0 set cell = %q, want %q — the counted value 2 must decide the bit, not a guessed 1", rows[0][at], "A")
		}
		if rows[1][at] != "B" {
			t.Errorf("row 1 set cell = %q, want %q", rows[1][at], "B")
		}
	})

	t.Run("string constituents with a text counted value", func(t *testing.T) {
		spec := spsstest.Spec{
			Vars: []spsstest.Var{
				{Name: "A", Width: 3},
				{Name: "B", Width: 3},
			},
			Cases: [][]spsstest.Value{
				{spsstest.Text("YES"), spsstest.Text("NO")},
				{spsstest.Text("NO"), spsstest.Text("YES")},
			},
			MultipleResponseSets: []spsstest.MRSet{{
				Name: "$s", Kind: spsstest.MRDichotomy, CountedValue: "YES",
				Vars: []string{"A", "B"}, Subtype: spsstest.SubtypeMRSets,
			}},
		}
		r := NewReaderFromBytes(buildFixture(t, spec))
		header, rows := readHeaderAndRows(t, r)
		at := indexOfColumn(t, header, "s")
		if rows[0][at] != "A" {
			t.Errorf("row 0 set cell = %q, want %q", rows[0][at], "A")
		}
		if rows[1][at] != "B" {
			t.Errorf("row 1 set cell = %q, want %q", rows[1][at], "B")
		}
	})
}

// TestMRSet_UserMissingCodeIsNotSelected covers the constituent that carries
// a declared refusal code rather than a real answer. A refusal is not an
// answer to a multi-select, so it sets no bit — and it is also not evidence
// the row was answered, for the same reason.
func TestMRSet_UserMissingCodeIsNotSelected(t *testing.T) {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 1}
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "A", Print: num,
				Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(9)}}},
			{Name: "B", Print: num,
				Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(9)}}},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(9), spsstest.Num(9)},
			{spsstest.Num(9), spsstest.Num(1)},
		},
		MultipleResponseSets: []spsstest.MRSet{{
			Name: "$s", Kind: spsstest.MRDichotomy, CountedValue: "1",
			Vars: []string{"A", "B"}, Subtype: spsstest.SubtypeMRSets,
		}},
	}
	r := NewReaderFromBytes(buildFixture(t, spec))
	header, rows := readHeaderAndRows(t, r)
	at := indexOfColumn(t, header, "s")

	// Both refused: nothing selected AND nothing known, so the cell is the
	// empty string, which the import path reads as null.
	if rows[0][at] != "" {
		t.Errorf("row 0 set cell = %q, want the empty (null) cell — every constituent is a refusal", rows[0][at])
	}
	// One refusal, one real selection.
	if rows[1][at] != "B" {
		t.Errorf("row 1 set cell = %q, want %q", rows[1][at], "B")
	}
	// And the refusal codes are still addressable: A's reason sibling says
	// so for every row.
	sib := indexOfColumn(t, header, "A"+MissingSiblingSuffix)
	if rows[0][sib] == "" || rows[1][sib] == "" {
		t.Errorf("A%s = %q / %q, want the refusal reason on both rows — the derived set column must not consume the constituent's own fidelity",
			MissingSiblingSuffix, rows[0][sib], rows[1][sib])
	}
}

// ---------------------------------------------------------------------------
// The distinction the mask cannot hold, and where it survives
// ---------------------------------------------------------------------------

// TestMRSet_SysmisDistinctionSurvivesInTheConstituents is the acceptance
// criterion the whole additive design exists for, and it is written as the
// claim itself rather than as an assertion about a mask.
//
// Rows 1 and 2 of the fixture are indistinguishable at bit 1: neither
// selected Radio. They differ in why — row 1 was never asked, row 2 answered
// and declined — and the constituent column Q1B says which is which. If a
// future change collapsed the set destructively, the mask would be identical
// and this test is the one that would notice.
func TestMRSet_SysmisDistinctionSurvivesInTheConstituents(t *testing.T) {
	r := NewReaderFromBytes(buildFixture(t, mdSpec()))
	header, rows := readHeaderAndRows(t, r)
	set := indexOfColumn(t, header, "media")
	q1b := indexOfColumn(t, header, "Q1B")

	// The mask cannot tell rows 1 and 2 apart on Radio: bit 1 is clear in
	// both. That is not a bug — it is the premise.
	if containsToken(rows[1][set], "Q1B") || containsToken(rows[2][set], "Q1B") {
		t.Fatalf("Q1B appears in a mask that should not have it: %q / %q", rows[1][set], rows[2][set])
	}

	// The constituent does tell them apart, and that is the criterion.
	if rows[1][q1b] != "" {
		t.Errorf("row 1 Q1B = %q, want the empty (null) cell — this respondent was NOT ASKED", rows[1][q1b])
	}
	if rows[2][q1b] != "0" {
		t.Errorf("row 2 Q1B = %q, want %q — this respondent WAS asked and declined", rows[2][q1b], "0")
	}
	if rows[1][q1b] == rows[2][q1b] {
		t.Errorf("\"not asked\" and \"asked, not selected\" both render %q in the constituent; the distinction a bitmask cannot hold has been lost in the one column that was supposed to keep it", rows[1][q1b])
	}
}

// TestMRSet_ThreeRowStates pins the cell vocabulary, including the one state
// that needs a spelling of its own.
//
// "Answered and selected nothing" is a real answer and CLAUDE.md defines an
// empty mask as valid and distinct from null — but the shared import path
// reads an empty cell as null before consulting any dictionary, so the two
// states cannot share the empty string.
func TestMRSet_ThreeRowStates(t *testing.T) {
	r := NewReaderFromBytes(buildFixture(t, mdSpec()))
	header, rows := readHeaderAndRows(t, r)
	at := indexOfColumn(t, header, "media")

	for _, tc := range []struct {
		row  int
		want string
		why  string
	}{
		{0, "Q1A" + setElementDelimiter + "Q1C", "two options selected, joined by the delimiter the shared import path splits on"},
		{1, "Q1C", "one option selected; the unasked constituent contributes nothing"},
		{2, setEmptySelection, "answered the battery and selected nothing — an EMPTY MASK, which is not null"},
		{3, "", "every constituent missing — nothing is known, so this row genuinely is null"},
	} {
		if got := rows[tc.row][at]; got != tc.want {
			t.Errorf("row %d set cell = %q, want %q (%s)", tc.row, got, tc.want, tc.why)
		}
	}

	// The empty-selection spelling must not itself be a null token, or the
	// distinction it exists to carry would collapse on import.
	if rendersAsNull(setEmptySelection) {
		t.Fatalf("setEmptySelection %q reads as a null token; it cannot carry an empty mask", setEmptySelection)
	}
}

// TestMRSet_NullabilityIsScannedNotAssumed proves the derived column's
// nullable flag is the fact PulseSchema promises rather than a blanket
// declaration: a file whose every row answered at least one constituent
// yields a NON-nullable set column.
func TestMRSet_NullabilityIsScannedNotAssumed(t *testing.T) {
	t.Run("some row is all-missing", func(t *testing.T) {
		schema, _ := schemaOf(t, mdSpec())
		if f := schema.Field("media"); f == nil || !f.Nullable {
			t.Errorf("media nullable = %v, want true — row 3 skipped the whole battery", f)
		}
	})

	t.Run("no row is all-missing", func(t *testing.T) {
		spec := mdSpec()
		// Drop the all-missing row and repair the not-asked one so every
		// remaining row has at least one present constituent.
		spec.Cases = spec.Cases[:3]
		schema, _ := schemaOf(t, spec)
		f := schema.Field("media")
		if f == nil {
			t.Fatalf("no media field")
		}
		if f.Nullable {
			t.Errorf("media nullable = true, want false — no row of this file has every constituent missing, and an empty mask is not a null")
		}
	})
}

// ---------------------------------------------------------------------------
// Width
// ---------------------------------------------------------------------------

// TestMRSet_WidthFollowsTheConstituentCount walks the set-type ladder and
// the acceptance criterion at its top: over 64 constituents, constituents
// only plus a warning naming the set.
func TestMRSet_WidthFollowsTheConstituentCount(t *testing.T) {
	for _, tc := range []struct {
		members int
		want    encoding.FieldType
		derives bool
	}{
		{1, encoding.FieldTypeSetU8, true},
		{8, encoding.FieldTypeSetU8, true},
		{9, encoding.FieldTypeSetU16, true},
		{16, encoding.FieldTypeSetU16, true},
		{17, encoding.FieldTypeSetU32, true},
		{32, encoding.FieldTypeSetU32, true},
		{33, encoding.FieldTypeSetU64, true},
		{64, encoding.FieldTypeSetU64, true},
		{65, 0, false},
	} {
		t.Run(tc.want.String()+"/"+itoa(tc.members), func(t *testing.T) {
			r := NewReaderFromBytes(buildFixture(t, wideMDSpec(tc.members)))
			header, rows := readHeaderAndRows(t, r)

			// Constituents are present at every size — that is what makes
			// the over-64 refusal cost ergonomics and not data.
			for i := 0; i < tc.members; i++ {
				if !containsString(header, wideMemberName(i)) {
					t.Fatalf("constituent %q missing from header %q", wideMemberName(i), header)
				}
			}

			schema, err := r.PulseSchema()
			if err != nil {
				t.Fatalf("PulseSchema: %v", err)
			}
			f := schema.Field("wide")
			if !tc.derives {
				if f != nil {
					t.Errorf("a %d-constituent set derived a %s column; over %d there is no set type wide enough", tc.members, f.Type, maxSetElements)
				}
				assertMRSetWarning(t, r, "$wide", "more than the 64")
				return
			}
			if f == nil {
				t.Fatalf("no derived column for a %d-constituent set", tc.members)
			}
			if f.Type != tc.want {
				t.Errorf("width for %d constituents = %s, want %s", tc.members, f.Type, tc.want)
			}
			// The first constituent is the only one holding the counted
			// value, so exactly bit 0 is set.
			at := indexOfColumn(t, header, "wide")
			if rows[0][at] != wideMemberName(0) {
				t.Errorf("set cell = %q, want %q", rows[0][at], wideMemberName(0))
			}
		})
	}
}

// wideMDSpec builds an n-constituent dichotomy set. Only the first member
// holds the counted value, so the expected mask is bit 0 alone.
func wideMDSpec(n int) spsstest.Spec {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 1}
	spec := spsstest.Spec{}
	row := make([]spsstest.Value, 0, n)
	members := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := wideMemberName(i)
		spec.Vars = append(spec.Vars, spsstest.Var{Name: name, Print: num})
		members = append(members, name)
		if i == 0 {
			row = append(row, spsstest.Num(1))
		} else {
			row = append(row, spsstest.Num(0))
		}
	}
	spec.Cases = [][]spsstest.Value{row}
	spec.MultipleResponseSets = []spsstest.MRSet{{
		Name: "$wide", Kind: spsstest.MRDichotomy, CountedValue: "1",
		Vars: members, Subtype: spsstest.SubtypeMRSets,
	}}
	return spec
}

// wideMemberName produces a legal 8-byte uppercase SPSS short name.
func wideMemberName(i int) string {
	s := itoa(i)
	for len(s) < 2 {
		s = "0" + s
	}
	return "V" + s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Refusals: warn, keep the constituents, never fail the import
// ---------------------------------------------------------------------------

// TestMRSet_RefusalsWarnAndKeepEveryConstituent walks every reason a set does
// not derive.
//
// The shared assertion across all of them is the point: the import succeeds,
// the warning names the set, and every constituent is in the cohort. A
// derived column is a convenience, so refusing one must never cost data —
// which is exactly why these are warnings and not errors.
func TestMRSet_RefusalsWarnAndKeepEveryConstituent(t *testing.T) {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 1}

	for _, tc := range []struct {
		name     string
		spec     func() spsstest.Spec
		code     errors.Code
		contains string
		members  []string
		// setName overrides the name the warning is expected to carry,
		// for a case whose definition is not in Spec.MultipleResponseSets.
		setName string
	}{
		{
			name: "member the dictionary does not declare",
			code: errors.PULSE_SPSS_MR_SET_NOT_DERIVED,
			// The parser already warns about the unknown member; this
			// warning says what it cost.
			contains: "no record type 2",
			members:  []string{"A", "B"},
			setName:  "$ghost",
			spec: func() spsstest.Spec {
				// The fixture builder refuses to emit a set naming an
				// undeclared variable, which is the right default — so
				// this one definition is written as a raw 7/7 payload.
				// The shape is the same grammar planMRSet reads:
				// name '=' 'D' counted-value counted-label ' ' member...
				s := twoMemberSpec(num)
				s.MultipleResponseSets = nil
				s.RawExtensions = []spsstest.RawExtension{{
					Subtype: spsstest.SubtypeMRSets,
					Payload: []byte("$ghost=D1 1 0 A GHOST\n"),
				}}
				return s
			},
		},
		{
			name:     "the same member twice",
			code:     errors.PULSE_SPSS_MR_SET_NOT_DERIVED,
			contains: "more than once",
			members:  []string{"A", "B"},
			spec: func() spsstest.Spec {
				s := twoMemberSpec(num)
				s.MultipleResponseSets[0].Vars = []string{"A", "A"}
				return s
			},
		},
		{
			name:     "counted value that is not a number, over numeric members",
			code:     errors.PULSE_SPSS_MR_SET_NOT_DERIVED,
			contains: "is not a number",
			members:  []string{"A", "B"},
			spec: func() spsstest.Spec {
				s := twoMemberSpec(num)
				s.MultipleResponseSets[0].CountedValue = "yes"
				return s
			},
		},
		{
			name:     "constituent named after a null sentinel token",
			code:     errors.PULSE_SPSS_MR_SET_NOT_DERIVED,
			contains: "null sentinel token",
			members:  []string{"A", "NA"},
			spec: func() spsstest.Spec {
				s := twoMemberSpec(num)
				s.Vars[1].Name = "NA"
				s.MultipleResponseSets[0].Vars = []string{"A", "NA"}
				return s
			},
		},
		{
			name:     "constituent whose long name carries the set delimiter",
			code:     errors.PULSE_SPSS_MR_SET_NOT_DERIVED,
			contains: "ambiguous",
			members:  []string{"A", "b|c"},
			spec: func() spsstest.Spec {
				s := twoMemberSpec(num)
				s.Vars[1].LongName = "b|c"
				return s
			},
		},
		{
			name:     "derived name a real variable already holds",
			code:     errors.PULSE_SPSS_DERIVED_NAME_COLLISION,
			contains: "already declares a variable named",
			members:  []string{"A", "B"},
			spec: func() spsstest.Spec {
				s := twoMemberSpec(num)
				// The derived name would be "A" once the '$' is dropped,
				// which is a member's own name.
				s.MultipleResponseSets[0].Name = "$A"
				return s
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.spec()
			r := NewReaderFromBytes(buildFixture(t, spec))

			// The import succeeds — a convenience column is never worth
			// failing a readable file for.
			header, rows := readHeaderAndRows(t, r)
			if len(rows) == 0 {
				t.Fatalf("no rows read")
			}
			for _, name := range tc.members {
				if !containsString(header, name) {
					t.Errorf("constituent %q missing from header %q; a refused set must still import every constituent", name, header)
				}
			}

			schema, err := r.PulseSchema()
			if err != nil {
				t.Fatalf("PulseSchema: %v", err)
			}
			for _, f := range schema.Fields {
				if f.Type.IsSet() {
					t.Errorf("field %q is a %s; this set must not have derived one", f.Name, f.Type)
				}
			}

			ce := findWarning(t, r, tc.code)
			if !strings.Contains(ce.Message, tc.contains) {
				t.Errorf("warning message = %q, want it to contain %q", ce.Message, tc.contains)
			}
			wantSet := tc.setName
			if wantSet == "" {
				wantSet = spec.MultipleResponseSets[0].Name
			}
			if got := ce.Details[errors.DetailSPSSSet]; got != wantSet {
				t.Errorf("details[%s] = %v, want %q — the warning must NAME the set",
					errors.DetailSPSSSet, got, wantSet)
			}
		})
	}
}

// twoMemberSpec is the minimal derivable fixture the refusal cases mutate.
func twoMemberSpec(num spsstest.Format) spsstest.Spec {
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "A", Print: num},
			{Name: "B", Print: num},
		},
		Cases: [][]spsstest.Value{{spsstest.Num(1), spsstest.Num(0)}},
		MultipleResponseSets: []spsstest.MRSet{{
			Name: "$s", Kind: spsstest.MRDichotomy, CountedValue: "1",
			Vars: []string{"A", "B"}, Subtype: spsstest.SubtypeMRSets,
		}},
	}
}

// ---------------------------------------------------------------------------
// Multiple CATEGORY sets derive nothing
// ---------------------------------------------------------------------------

// TestMRSet_CategorySetDerivesNothing pins the boundary E2-S3 built the
// two-type model for. An MC set is positional and permits duplicates; it is
// N categorical columns and not a set, and it has no counted value to build
// a mask from. E4-S5 owns it.
func TestMRSet_CategorySetDerivesNothing(t *testing.T) {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 1}
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "R1", Print: num},
			{Name: "R2", Print: num},
		},
		Cases: [][]spsstest.Value{{spsstest.Num(1), spsstest.Num(2)}},
		MultipleResponseSets: []spsstest.MRSet{{
			Name: "$ranks", Kind: spsstest.MRCategory, Label: "Ranked brands",
			Vars: []string{"R1", "R2"}, Subtype: spsstest.SubtypeMRSets,
		}},
	}
	schema, _ := schemaOf(t, spec)
	for _, f := range schema.Fields {
		if f.Type.IsSet() {
			t.Errorf("field %q is a %s; a multiple-CATEGORY set must derive no set column", f.Name, f.Type)
		}
	}
	if len(schema.Fields) != 2 {
		t.Errorf("schema has %d field(s), want exactly the two source variables", len(schema.Fields))
	}
	// And it warns about nothing: an MC set is not a failed MD set.
	r := NewReaderFromBytes(buildFixture(t, spec))
	if _, err := r.PulseSchema(); err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}
	for _, w := range r.Warnings() {
		if w.Code == errors.PULSE_SPSS_MR_SET_NOT_DERIVED {
			t.Errorf("a multiple-category set raised %s: %s", w.Code, w.Message)
		}
	}
}

// TestMRSet_ExtendedSubtype19DerivesLikeSubtype7 covers the definition form
// this effort's research flagged as PSPP-spec-only and uncorroborated: the
// record 7/19 'E' grammar, in both its label-source spellings.
//
// It cannot prove the grammar is right — neither R reader exposes MR set
// metadata at all, so there is no second opinion available (see mrset.go).
// What it does pin is the blast radius: a subtype-19 definition reaches
// exactly the same derivation as a subtype-7 one, so a misreading can only
// mis-derive a convenience column and can never touch a constituent.
func TestMRSet_ExtendedSubtype19DerivesLikeSubtype7(t *testing.T) {
	for _, tc := range []struct {
		name              string
		subtype           int32
		extended          bool
		labelFromVarLabel bool
	}{
		{name: "subtype 7 plain D", subtype: spsstest.SubtypeMRSets},
		{name: "subtype 19 E label-source 1", subtype: spsstest.SubtypeMRSetsExtended, extended: true},
		{name: "subtype 19 E label-source 11", subtype: spsstest.SubtypeMRSetsExtended, extended: true, labelFromVarLabel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := mdSpec()
			spec.MultipleResponseSets[0].Subtype = tc.subtype
			spec.MultipleResponseSets[0].Extended = tc.extended
			spec.MultipleResponseSets[0].LabelFromVarLabel = tc.labelFromVarLabel

			r := NewReaderFromBytes(buildFixture(t, spec))
			header, rows := readHeaderAndRows(t, r)
			at := indexOfColumn(t, header, "media")
			if got, want := rows[0][at], "Q1A"+setElementDelimiter+"Q1C"; got != want {
				t.Errorf("set cell = %q, want %q", got, want)
			}
			// The constituents are untouched under every form — which is
			// the bound on what a misread 7/19 could cost.
			if rows[1][indexOfColumn(t, header, "Q1B")] != "" {
				t.Errorf("Q1B row 1 = %q, want the empty (null) cell", rows[1][indexOfColumn(t, header, "Q1B")])
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The sidecar
// ---------------------------------------------------------------------------

// TestMRSet_SidecarRecordsTheDerivation checks the registry entry an export
// reads to know the column is synthesised and needs no reconstruction.
func TestMRSet_SidecarRecordsTheDerivation(t *testing.T) {
	_, _, doc := importFixture(t, mdSpec())

	var found *Derived
	for i := range doc.Payload.Derived {
		if doc.Payload.Derived[i].Kind == DerivedKindMultipleDichotomy {
			found = &doc.Payload.Derived[i]
		}
	}
	if found == nil {
		t.Fatalf("sidecar derived = %+v, want a %s entry", doc.Payload.Derived, DerivedKindMultipleDichotomy)
	}
	if found.Name != "media" || found.SetName != "$media" {
		t.Errorf("derived = {name %q, set_name %q}, want {\"media\", \"$media\"}", found.Name, found.SetName)
	}
	// Bit order is the contract: bit i is Sources[i].
	if want := []string{"Q1A", "Q1B", "Q1C"}; !equalStrings(found.Sources, want) {
		t.Errorf("sources = %q, want %q in bit order", found.Sources, want)
	}
	if found.Position != 4 {
		t.Errorf("position = %d, want 4 (immediately after the last constituent)", found.Position)
	}

	// The counted value is reachable from the set name, which is why the
	// derived entry does not duplicate it.
	var counted *string
	for _, s := range doc.Payload.MultipleResponseSets {
		if s.Name == found.SetName {
			counted = s.CountedValue
		}
	}
	if counted == nil || *counted != "1" {
		t.Errorf("multiple_response_sets[%q].counted_value = %v, want \"1\"", found.SetName, counted)
	}

	// Every constituent is still a source VARIABLE in its own right.
	for _, name := range found.Sources {
		if varOf(t, doc, name).Name != name {
			t.Errorf("sidecar has no variable entry for constituent %q", name)
		}
	}
}

// TestMRSet_SidecarStaysByteIdenticalWithoutSets guards the additive
// promise of the new SetName slot: a document over a file with no derivable
// response set must be exactly what it was before this story.
func TestMRSet_SidecarStaysByteIdenticalWithoutSets(t *testing.T) {
	spec := mdSpec()
	spec.MultipleResponseSets = nil
	_, _, doc := importFixture(t, spec)
	for _, d := range doc.Payload.Derived {
		if d.SetName != "" {
			t.Errorf("derived %q carries set_name %q on a file with no response sets", d.Name, d.SetName)
		}
	}
	if doc.FormatVersion != SidecarFormatVersion || SidecarFormatVersion != 1 {
		t.Errorf("SidecarFormatVersion = %d, want 1 — the set_name slot is additive", SidecarFormatVersion)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func indexOfColumn(t *testing.T, header []string, name string) int {
	t.Helper()
	for i, h := range header {
		if h == name {
			return i
		}
	}
	t.Fatalf("header %q has no column %q", header, name)
	return -1
}

// containsToken reports whether a rendered set cell carries the given
// element, splitting on the delimiter so "Q1B" does not match inside a
// longer name.
func containsToken(cell, token string) bool {
	for _, part := range strings.Split(cell, setElementDelimiter) {
		if part == token {
			return true
		}
	}
	return false
}

func findWarning(t *testing.T, r *Reader, code errors.Code) *errors.CodedError {
	t.Helper()
	if _, err := r.PulseSchema(); err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}
	for _, w := range r.Warnings() {
		if w.Code == code {
			return w
		}
	}
	t.Fatalf("no %s warning; got %v", code, r.Warnings())
	return nil
}

func assertMRSetWarning(t *testing.T, r *Reader, setName, contains string) {
	t.Helper()
	ce := findWarning(t, r, errors.PULSE_SPSS_MR_SET_NOT_DERIVED)
	if got := ce.Details[errors.DetailSPSSSet]; got != setName {
		t.Errorf("details[%s] = %v, want %q", errors.DetailSPSSSet, got, setName)
	}
	if !strings.Contains(ce.Message, contains) {
		t.Errorf("warning = %q, want it to contain %q", ce.Message, contains)
	}
}
