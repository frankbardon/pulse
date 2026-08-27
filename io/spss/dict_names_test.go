package spss

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
)

// The NAME BOUNDARY.
//
// Pulse validates nothing about a field name and SPSS validates a great deal,
// so this is a genuinely new boundary rather than a second opinion about an
// existing one — there is no earlier pass these tests could be duplicating.
// The rule itself is exercised directly through nameFault, and every
// front-end is then checked to be standing behind it.

// numField is a plain numeric cohort field of the given name.
func numField(name string) encoding.Field {
	return encoding.Field{Name: name, Type: encoding.FieldTypeF64}
}

// synthNames emits a synthesised dictionary from a list of field names.
func synthNames(names ...string) (*DictionaryPlan, error) {
	fields := make([]encoding.Field, 0, len(names))
	for _, n := range names {
		fields = append(fields, numField(n))
	}
	return BuildDictionary(DictionaryRequest{
		Schema: &encoding.Schema{Fields: fields},
		Cases:  0,
	})
}

// TestNameFault_Rule is the rule itself, stated once.
//
// The wire length is passed separately from the name because the two halves
// of the rule are defined on different things: the 64-byte ceiling counts
// EMITTED bytes and the character set is a question about characters. Every
// row here passes the UTF-8 length as the wire length, which is what an
// ASCII or UTF-8 file emits; TestNameLength_IsMeasuredInEmittedBytes covers
// the case where the two differ.
func TestNameFault_Rule(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		ok   bool
		why  string
	}{
		{"a plain identifier", "income", true, ""},
		{"digits after the first character", "q1a", true, ""},
		{"the punctuation SPSS allows", "a._$#@1", true, ""},
		{"an interior period", "household.income", true, ""},
		{"a leading '@'", "@sysvar", true, ""},
		{"a leading '#' (a scratch variable)", "#temp", true, ""},
		{"a leading '$' (a set name, and a system variable)", "$media", true, ""},
		{"a non-ASCII letter", "Identität", true, "SPSS in UTF-8 mode accepts one, and our own fixtures carry it"},
		{"a trailing underscore", "income_", true, "SPSS discourages it and does not forbid it"},
		{"a reserved syntax keyword", "TO", true, "the keyword list restricts the command language, not the file format"},
		{"exactly 64 bytes", strings.Repeat("a", 64), true, ""},

		{"empty", "", false, "every variable must be named"},
		{"65 bytes", strings.Repeat("a", 65), false, "past the ceiling"},
		{"a space", "household income", false, "record 7/7 is space-separated over the same namespace"},
		{"an '='", "gross=net", false, "record 7/13 is a list of SHORT=LONG pairs with no escape"},
		{"a tab", "gross\tnet", false, "record 7/13 separates its pairs with tabs"},
		{"a newline", "gross\nnet", false, "a line break in a name is a name in two records"},
		{"a leading digit", "2024_total", false, "an SPSS name opens with a letter"},
		{"a leading period", ".total", false, "an SPSS name opens with a letter"},
		{"a trailing period", "total.", false, "SPSS reads a trailing period as a command terminator"},
		{"a bracket", "income (gross)", false, "outside the SPSS character set"},
		{"a hyphen", "gross-net", false, "outside the SPSS character set"},
		{"a NUL", "gross\x00net", false, "a counted string is not NUL-terminated, but a name is not a place for one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why := nameFault(tc.in, len(tc.in))
			if tc.ok && why != "" {
				t.Errorf("nameFault(%q) = %q, want it accepted: %s", tc.in, why, tc.why)
			}
			if !tc.ok && why == "" {
				t.Errorf("nameFault(%q) accepted the name; it must be refused: %s", tc.in, tc.why)
			}
		})
	}
}

// TestNameLength_IsMeasuredInEmittedBytes pins the half of the rule that is
// easiest to get wrong.
//
// SPSS name lengths are BYTE counts. A 40-character name is 40 bytes as
// ASCII and more as UTF-8, so a validator measuring the UTF-8 form would
// pass names that overflow the file and refuse names that do not.
func TestNameLength_IsMeasuredInEmittedBytes(t *testing.T) {
	// 33 two-byte characters: 33 runes, 66 UTF-8 bytes, 33 windows-1252
	// bytes. The same name overflows in one charset and fits in the other.
	name := strings.Repeat("ä", 33)
	if len(name) != 66 {
		t.Fatalf("fixture is %d bytes, want 66", len(name))
	}

	if why := nameFault(name, len(name)); why == "" {
		t.Error("66 emitted bytes was accepted; the ceiling is 64")
	}
	if why := nameFault(name, 33); why != "" {
		t.Errorf("33 emitted bytes was refused (%s); the ceiling is 64", why)
	}

	// And the whole pipeline agrees: the name is refused as UTF-8 and
	// accepted as windows-1252, purely because of the charset.
	if _, err := synthNames(name); err == nil {
		t.Error("a UTF-8 export accepted the 66-byte name")
	} else if got := codeOf(t, err); got != perr.PULSE_SPSS_NAME_INVALID {
		t.Errorf("code = %s, want PULSE_SPSS_NAME_INVALID", got)
	}
	synthIn(t, "windows-1252", numField(name))
}

// TestNames_InvalidIsRefusedNamingTheField checks the diagnostic, not just
// the refusal. A caller has to be told which cohort column to rename, and on
// a set expansion the SPSS variable name and the cohort field name are
// different strings.
func TestNames_InvalidIsRefusedNamingTheField(t *testing.T) {
	ce := emitFails(t, DictionaryRequest{
		Schema: &encoding.Schema{Fields: []encoding.Field{numField("household income")}},
		Cases:  0,
	}, perr.PULSE_SPSS_NAME_INVALID)

	if got := ce.Details[perr.DetailSPSSVariable]; got != "household income" {
		t.Errorf("details[%s] = %v, want the offending name", perr.DetailSPSSVariable, got)
	}
	if got := ce.Details[perr.DetailSPSSField]; got != "household income" {
		t.Errorf("details[%s] = %v, want the cohort field", perr.DetailSPSSField, got)
	}
	if !strings.Contains(ce.Error(), "' '") {
		t.Errorf("the message does not name the offending character: %v", ce)
	}
}

// TestNames_SetMemberNamesTheCohortFieldItCameFrom is the case the two detail
// keys exist for: a `set_*` column expands into one indicator variable per
// dictionary ENTRY, so the illegal name is an entry and the column to rename
// is the set.
func TestNames_SetMemberNamesTheCohortFieldItCameFrom(t *testing.T) {
	ce := emitFails(t, DictionaryRequest{
		Schema: &encoding.Schema{Fields: []encoding.Field{{
			Name: "media", Type: encoding.FieldTypeSetU8,
			Dictionary: dictOf(t, "tv", "web radio"),
		}}},
		Cases: 0,
	}, perr.PULSE_SPSS_NAME_INVALID)

	if got := ce.Details[perr.DetailSPSSVariable]; got != "web radio" {
		t.Errorf("details[%s] = %v, want the dictionary entry that became the variable name",
			perr.DetailSPSSVariable, got)
	}
	if got := ce.Details[perr.DetailSPSSField]; got != "media" {
		t.Errorf("details[%s] = %v, want the set_* cohort column", perr.DetailSPSSField, got)
	}
}

// TestNames_AreCaseInsensitivelyUnique: SPSS folds case, Pulse does not, so
// two cohort fields that Pulse considers distinct are one SPSS variable.
//
// It is a refusal because the failure is silent: record 7/13 keeps only the
// first mapping for a name, so the file holds a column no name reaches.
func TestNames_AreCaseInsensitivelyUnique(t *testing.T) {
	ce := emitFails(t, DictionaryRequest{
		Schema: &encoding.Schema{Fields: []encoding.Field{numField("Region"), numField("REGION")}},
		Cases:  0,
	}, perr.PULSE_SPSS_NAME_COLLISION)

	if got := ce.Details[perr.DetailSPSSCollidesWith]; got != "Region" {
		t.Errorf("details[%s] = %v, want the variable that claimed the name first",
			perr.DetailSPSSCollidesWith, got)
	}
}

// TestNames_SetMemberCollidingWithARealColumnIsRefused is the collision this
// package can CREATE rather than merely receive: a set member is named for
// its dictionary entry, and the entry can be a name some other column has.
func TestNames_SetMemberCollidingWithARealColumnIsRefused(t *testing.T) {
	emitFails(t, DictionaryRequest{
		Schema: &encoding.Schema{Fields: []encoding.Field{
			numField("tv"),
			{Name: "media", Type: encoding.FieldTypeSetU8, Dictionary: dictOf(t, "tv", "radio")},
		}},
		Cases: 0,
	}, perr.PULSE_SPSS_NAME_COLLISION)
}

// TestNames_SidecarPathIsValidatedToo: the validation pass sits behind BOTH
// front-ends, so a hand-edited sidecar cannot smuggle a name past it.
//
// A real `.sav` cannot carry one — SPSS wrote the names — which is precisely
// why the check has to be on the shared path rather than in dict_synth.go: a
// document is an editable JSON file and the cohort it names is not.
func TestNames_SidecarPathIsValidatedToo(t *testing.T) {
	fs, cohort, _ := importFixture(t, bothKindsSpec())
	res, err := LoadSidecar(fs, cohort, WriterOptions{})
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	schema := cohortSchema(t, fs, cohort)

	// Rename the recorded variable AND the cohort field together, so the
	// document still describes the cohort and the only fault left is the
	// name itself.
	for i := range res.Document.Payload.Variables {
		if res.Document.Payload.Variables[i].Name == "INCOME" {
			res.Document.Payload.Variables[i].Name = "INCOME BAND"
		}
	}
	for i := range res.Document.Payload.Derived {
		if d := &res.Document.Payload.Derived[i]; len(d.Sources) == 1 && d.Sources[0] == "INCOME" {
			d.Sources[0] = "INCOME BAND"
		}
	}
	for i := range schema.Fields {
		if schema.Fields[i].Name == "INCOME" {
			schema.Fields[i].Name = "INCOME BAND"
		}
	}

	emitFails(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 0}, perr.PULSE_SPSS_NAME_INVALID)
}

// TestNames_MultipleResponseSetNamesAreValidated covers the name that is not
// a variable's.
//
// A synthesised set name is "$" plus the cohort column's name, so an illegal
// column name produces an illegal set name — and record 7/7's payload is
// SPACE-separated, so a space in one would split the definition into members
// that do not exist. The '$' itself is legal and must stay legal: the rule
// admits it as an opening character precisely so set names go through the
// same validator.
func TestNames_MultipleResponseSetNamesAreValidated(t *testing.T) {
	// A legal set column: the set name "$media" passes.
	plan := emit(t, DictionaryRequest{
		Schema: &encoding.Schema{Fields: []encoding.Field{{
			Name: "media", Type: encoding.FieldTypeSetU8, Dictionary: dictOf(t, "tv", "web"),
		}}},
		Cases: 0,
	})
	d := reparse(t, plan)
	if len(d.mrSets) != 1 || d.mrSets[0].setName() != "$media" {
		t.Fatalf("re-parsed sets = %+v, want one named $media", d.mrSets)
	}

	// nameFault is the shared rule, and it is what a set name is held to.
	if why := nameFault("$media", len("$media")); why != "" {
		t.Errorf("the set name $media was refused (%s); a leading '$' is what a set name IS", why)
	}
	if why := nameFault("$media used", len("$media used")); why == "" {
		t.Error("a set name carrying a space was accepted; record 7/7 is space-separated")
	}
}

// TestNames_ShortNameCollisionIsRefused covers the same fault one level down.
// Records 7/5, 7/7, 7/14 and 7/19 key by the eight-byte record type 2 name,
// so two variables sharing one leaves each of those records naming only one.
//
// The synthesised minter cannot produce this — it hands out unique short
// names by construction — so the check is exercised on the model directly,
// which is also the only shape a sidecar could deliver it in.
func TestNames_ShortNameCollisionIsRefused(t *testing.T) {
	f := &outFile{vars: []*outVar{
		{name: "alpha", utf8Name: "alpha", shortName: "DUP", fieldName: "alpha"},
		{name: "beta", utf8Name: "beta", shortName: "dup", fieldName: "beta"},
	}}
	err := validateNames(f)
	if err == nil {
		t.Fatal("two variables sharing a short name were accepted")
	}
	if got := codeOf(t, err); got != perr.PULSE_SPSS_NAME_COLLISION {
		t.Errorf("code = %s, want PULSE_SPSS_NAME_COLLISION", got)
	}
}

// TestNames_RefusalHappensBeforeAnythingIsEmitted. The pass runs between the
// transcode and emission, so a refusal leaves no half-written file and no
// plan a caller could mistake for a usable one.
func TestNames_RefusalHappensBeforeAnythingIsEmitted(t *testing.T) {
	plan, err := synthNames("ok_one", "bad name", "ok_two")
	if err == nil {
		t.Fatal("BuildDictionary succeeded on an illegal name")
	}
	if plan != nil {
		t.Errorf("a plan came back alongside the refusal: %d column(s)", len(plan.Columns))
	}
}
