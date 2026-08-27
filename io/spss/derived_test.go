package spss

// Tests for the derived-column registry and for the multiple-CATEGORY
// arm that deliberately produces no entry in it.
//
// The registry is consumed by the export half, which is not written yet, so
// almost every assertion here is a claim about a document rather than about
// an observable behaviour. That is the point: the registry exists so the fold
// is mechanical, and a fold driven off an under-specified document is exactly
// the name-pattern guessing it was built to replace.

import (
	"context"
	stdjson "encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// The vocabulary
// ---------------------------------------------------------------------------

// TestDerivedRegistry_VocabularyIsClosed is the gate that keeps the registry
// coherent as kinds are added: a kind that reaches Derived.Kind without
// reaching DerivedKinds / DerivedFoldFor is a column an export cannot fold.
func TestDerivedRegistry_VocabularyIsClosed(t *testing.T) {
	kinds := DerivedKinds()
	if len(kinds) == 0 {
		t.Fatal("DerivedKinds is empty")
	}

	// Every kind constant this package declares is in the vocabulary. The
	// source is scanned rather than a hand-kept list restated, so a new
	// `DerivedKind*` constant fails here until it is registered.
	for _, name := range declaredDerivedKindConstants(t) {
		found := false
		for _, k := range kinds {
			if k == name {
				found = true
			}
		}
		if !found {
			t.Errorf("the package declares a derived kind %q that DerivedKinds does not list; an export would not know how to fold it", name)
		}
	}

	// Every listed kind has exactly one fold action, and no listing is a
	// duplicate.
	seen := map[string]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("DerivedKinds lists %q twice", k)
		}
		seen[k] = true
		fold, ok := DerivedFoldFor(k)
		if !ok {
			t.Errorf("kind %q has no fold action", k)
		}
		if fold == DerivedFoldUnknown {
			t.Errorf("kind %q folds to DerivedFoldUnknown, which is never a legal fold", k)
		}
	}

	// The two kinds that exist fold the two different ways, which is the
	// whole reason the vocabulary is not a boolean.
	if fold, _ := DerivedFoldFor(DerivedKindNumericMissing); fold != DerivedFoldRestore {
		t.Errorf("%s folds %s, want %s — its reason dictionary is the only record of the source's missing state",
			DerivedKindNumericMissing, fold, DerivedFoldRestore)
	}
	if fold, _ := DerivedFoldFor(DerivedKindMultipleDichotomy); fold != DerivedFoldDrop {
		t.Errorf("%s folds %s, want %s — its constituents are real columns that carry everything it shows",
			DerivedKindMultipleDichotomy, fold, DerivedFoldDrop)
	}

	// An unrecognised kind is DETECTABLE, not defaulted. A consumer that
	// silently skipped it would drop a column; one that silently emitted it
	// would invent an SPSS variable.
	if fold, ok := DerivedFoldFor("kind_from_a_newer_import"); ok || fold != DerivedFoldUnknown {
		t.Errorf("DerivedFoldFor(unknown) = %v, %v; want DerivedFoldUnknown, false", fold, ok)
	}
	if _, ok := DerivedFoldFor(""); ok {
		t.Error("an empty Kind resolved to a fold action")
	}

	// The returned slice is a copy: a caller mutating it must not reshape
	// the vocabulary for everyone else.
	kinds[0] = "mutated"
	if DerivedKinds()[0] == "mutated" {
		t.Error("DerivedKinds returned the backing slice")
	}
}

// declaredDerivedKindConstants scans this package's own source for
// `DerivedKind* = "..."` declarations, so the closed-vocabulary gate cannot
// be satisfied by editing a list in the test.
func declaredDerivedKindConstants(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, line := range strings.Split(string(src), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "const DerivedKind") {
				continue
			}
			_, rhs, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			value, err := strconv.Unquote(strings.TrimSpace(rhs))
			if err != nil {
				t.Fatalf("%s: cannot read the value of %q", name, line)
			}
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		t.Fatal("found no DerivedKind* constants in the package source")
	}
	return out
}

// TestDerivedFold_String keeps the diagnostic spellings stable — they appear
// in fold-time refusals, where "unknown" is the one a reader must recognise.
func TestDerivedFold_String(t *testing.T) {
	for fold, want := range map[DerivedFold]string{
		DerivedFoldUnknown: "unknown",
		DerivedFoldDrop:    "drop",
		DerivedFoldRestore: "restore",
		DerivedFold(99):    "DerivedFold(99)",
	} {
		if got := fold.String(); got != want {
			t.Errorf("DerivedFold(%d).String() = %q, want %q", int(fold), got, want)
		}
	}
}

// TestDerived_Complete pins what "self-sufficient" means per kind, which is
// the property that lets an export refuse a bad document BEFORE it has
// written half an output file.
func TestDerived_Complete(t *testing.T) {
	code := Float(9)
	full := Derived{
		Name: "income_missing", Kind: DerivedKindNumericMissing,
		Sources: []string{"income"}, Position: 1,
		Reasons: []DerivedReason{{ID: 0, Reason: SysmisReason, Sysmis: true}, {ID: 1, Reason: "Refused", Code: &code}},
	}
	set := Derived{
		Name: "media", Kind: DerivedKindMultipleDichotomy, SetName: "$media",
		Sources: []string{"Q1A", "Q1B"}, Position: 4,
	}

	for _, tc := range []struct {
		name  string
		entry Derived
		want  bool
		why   string
	}{
		{"reason sibling, fully populated", full, true, ""},
		{"set column, fully populated", set, true, ""},
		{
			"reason sibling with no reasons", withoutReasons(full), false,
			"the reason dictionary is the ONLY record of which SPSS state each ID stands for",
		},
		{
			"reason sibling with two sources", withSources(full, "income", "other"), false,
			"a sibling restores into exactly one variable",
		},
		{
			"set column with no set name", withoutSetName(set), false,
			"set_name is the key into multiple_response_sets, where the definition to re-emit lives",
		},
		{
			"set column with no sources", withSources(set), false,
			"sources in bit order is what says what the column was",
		},
		{
			"unknown kind", withKind(full, "kind_from_a_newer_import"), false,
			"an unrecognised kind has no fold action at all",
		},
		{"nameless", withName(full, ""), false, "the fold addresses the column by name"},
		{"negative position", withPosition(full, -1), false, "position is a cohort coordinate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.entry.Complete(); got != tc.want {
				t.Errorf("Complete() = %v, want %v: %s", got, tc.want, tc.why)
			}
		})
	}
}

func withoutReasons(d Derived) Derived           { d.Reasons = nil; return d }
func withoutSetName(d Derived) Derived           { d.SetName = ""; return d }
func withSources(d Derived, s ...string) Derived { d.Sources = s; return d }
func withKind(d Derived, k string) Derived       { d.Kind = k; return d }
func withName(d Derived, n string) Derived       { d.Name = n; return d }
func withPosition(d Derived, p int) Derived      { d.Position = p; return d }

// ---------------------------------------------------------------------------
// The registry as written
// ---------------------------------------------------------------------------

// bothKindsSpec carries one numeric variable declaring user-missing codes and
// one multiple-dichotomy set, so a single import produces both derived kinds.
func bothKindsSpec() spsstest.Spec {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 1}
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{
				Name: "INCOME", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8},
				Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(99)}},
			},
			{Name: "Q1A", Label: "Newspaper", Print: num},
			{Name: "Q1B", Label: "Radio", Print: num},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(30000), spsstest.Num(1), spsstest.Num(0)},
			{spsstest.Num(99), spsstest.Num(0), spsstest.Num(1)},
			{spsstest.SysMis(), spsstest.Num(1), spsstest.Num(1)},
		},
		MultipleResponseSets: []spsstest.MRSet{{
			Name: "$media", Kind: spsstest.MRDichotomy, Label: "Media used",
			CountedValue: "1", Vars: []string{"Q1A", "Q1B"},
			Subtype: spsstest.SubtypeMRSets,
		}},
	}
}

// TestDerivedRegistry_CoversBothKinds is the acceptance criterion in one
// document: one import, both derived kinds, each self-sufficient for its own
// fold.
func TestDerivedRegistry_CoversBothKinds(t *testing.T) {
	_, _, doc := importFixture(t, bothKindsSpec())
	derived := doc.Payload.Derived

	if len(derived) != 2 {
		t.Fatalf("registry has %d entries, want 2 (the reason sibling and the set column): %+v", len(derived), derived)
	}

	byName := map[string]Derived{}
	for _, e := range derived {
		byName[e.Name] = e
		if !e.Complete() {
			t.Errorf("registry entry %+v is not self-sufficient; the fold would have to re-derive something", e)
		}
	}

	sib, ok := byName["INCOME"+MissingSiblingSuffix]
	if !ok {
		t.Fatalf("no reason sibling in the registry: %+v", derived)
	}
	if fold, _ := sib.Fold(); fold != DerivedFoldRestore {
		t.Errorf("%s folds %s, want %s", sib.Name, fold, DerivedFoldRestore)
	}
	if sib.Position != 1 {
		t.Errorf("%s.Position = %d, want 1 — a sibling sits immediately after its source", sib.Name, sib.Position)
	}
	if sib.SetName != "" {
		t.Errorf("%s carries set_name %q; a reason sibling comes from no response set", sib.Name, sib.SetName)
	}

	media, ok := byName["media"]
	if !ok {
		t.Fatalf("no set column in the registry: %+v", derived)
	}
	if fold, _ := media.Fold(); fold != DerivedFoldDrop {
		t.Errorf("media folds %s, want %s", fold, DerivedFoldDrop)
	}
	if len(media.Reasons) != 0 {
		t.Errorf("media carries a reason dictionary; a dropped column needs none")
	}
	// The key into multiple_response_sets resolves — which is what makes
	// "drop the column, re-emit the definition" mechanical.
	found := false
	for _, s := range doc.Payload.MultipleResponseSets {
		if s.Name == media.SetName {
			found = true
			if s.Kind != MRSetKindDichotomy {
				t.Errorf("%s.Kind = %q, want %q", s.Name, s.Kind, MRSetKindDichotomy)
			}
		}
	}
	if !found {
		t.Errorf("media.set_name %q names no entry of multiple_response_sets", media.SetName)
	}
}

// TestDerivedRegistry_EmptyIsAnArrayNotAMissingKey is the criterion that an
// `omitempty` slot would quietly violate.
//
// "No derived columns" and "this document cannot tell me" are different
// answers: an export proceeds on the first and must refuse on the second, so
// they cannot share a spelling on the wire.
func TestDerivedRegistry_EmptyIsAnArrayNotAMissingKey(t *testing.T) {
	// A file with no missing specifications and no response sets derives
	// nothing at all.
	spec := spsstest.Spec{
		Vars:  []spsstest.Var{{Name: "ID", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}}},
		Cases: [][]spsstest.Value{{spsstest.Num(1)}},
	}
	fs, cohort, doc := importFixture(t, spec)

	if doc.Payload.Derived == nil {
		t.Error("payload.derived decoded as nil; a cohort with no derived columns must produce an EMPTY registry, not an absent one")
	}
	if len(doc.Payload.Derived) != 0 {
		t.Errorf("payload.derived = %+v, want empty", doc.Payload.Derived)
	}

	// And on the wire, not just after decoding: encoding/json would omit
	// the key entirely under `omitempty`, and a nil slice would serialise
	// as null, which is a third spelling of the same confusion.
	raw, err := afero.ReadFile(fs, SidecarPath(cohort))
	if err != nil {
		t.Fatalf("reading sidecar: %v", err)
	}
	var generic map[string]any
	if err := stdjson.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decoding sidecar: %v", err)
	}
	payload, ok := generic["payload"].(map[string]any)
	if !ok {
		t.Fatal("sidecar has no payload object")
	}
	slot, present := payload["derived"]
	if !present {
		t.Fatal("the sidecar omits the `derived` key entirely; an export cannot tell \"no derived columns\" from \"unknown\"")
	}
	arr, ok := slot.([]any)
	if !ok {
		t.Fatalf("payload.derived = %v (%T), want a JSON array", slot, slot)
	}
	if len(arr) != 0 {
		t.Errorf("payload.derived = %v, want []", arr)
	}
}

// ---------------------------------------------------------------------------
// Multiple CATEGORY sets
// ---------------------------------------------------------------------------

// mcSpec is a three-slot ranking battery: R1/R2/R3 each hold a code from one
// shared value-label set.
//
// Case 1 deliberately ranks brand 2 in BOTH the first and the second slot.
// That is not corrupt data for an MC set — the flavour permits duplicates —
// and it is one of the two facts a bitmask cannot hold. The other is the slot
// ORDER, which is the difference between a first choice and a third.
func mcSpec() spsstest.Spec {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 1}
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "R1", Label: "First choice", Print: num},
			{Name: "R2", Label: "Second choice", Print: num},
			{Name: "R3", Label: "Third choice", Print: num},
		},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"R1", "R2", "R3"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(1), Label: "Acme"},
				{Value: spsstest.Num(2), Label: "Beta"},
				{Value: spsstest.Num(3), Label: "Gamma"},
			},
		}},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Num(2), spsstest.Num(3)},
			{spsstest.Num(2), spsstest.Num(2), spsstest.Num(1)},
			{spsstest.Num(3), spsstest.Num(1), spsstest.Num(2)},
		},
		MultipleResponseSets: []spsstest.MRSet{{
			Name: "$ranks", Kind: spsstest.MRCategory, Label: "Ranked brands",
			Vars: []string{"R1", "R2", "R3"}, Subtype: spsstest.SubtypeMRSets,
		}},
	}
}

// TestMRSet_CategorySetStaysNCategoricalColumns is the acceptance criterion
// stated as the two facts a `set_*` would destroy.
//
// A multiple-category set is positional and duplicate-tolerant. Collapsing it
// to a bitmask would lose the slot a code arrived in — "first choice" versus
// "third" — and would lose the second occurrence of a repeated code outright,
// because a mask is idempotent. So it maps to exactly what it is: N ordinary
// categorical columns, one per slot.
func TestMRSet_CategorySetStaysNCategoricalColumns(t *testing.T) {
	r := NewReaderFromBytes(buildFixture(t, mcSpec()))
	header, rows := readHeaderAndRows(t, r)

	if !equalStrings(header, []string{"R1", "R2", "R3"}) {
		t.Fatalf("header = %q, want the three slot variables and nothing else", header)
	}

	schema, _ := schemaOf(t, mcSpec())
	if len(schema.Fields) != 3 {
		t.Fatalf("schema has %d fields, want exactly the three source variables: %+v", len(schema.Fields), schema.Fields)
	}
	for _, f := range schema.Fields {
		if f.Type.IsSet() {
			t.Errorf("field %q is a %s; a multiple-CATEGORY set is positional and duplicate-tolerant and must never collapse to a mask", f.Name, f.Type)
		}
		if !f.Type.IsCategorical() {
			t.Errorf("field %q is a %s, want a categorical_* — every slot holds a code from the shared value-label set", f.Name, f.Type)
		}
	}

	// Positional: the slot a code arrived in is the slot it is read from.
	// Duplicate-tolerant: case 1 ranks "2" twice and BOTH occurrences
	// survive, in their own slots.
	for i, want := range [][]string{
		{"1", "2", "3"},
		{"2", "2", "1"},
		{"3", "1", "2"},
	} {
		if !equalStrings(rows[i], want) {
			t.Errorf("row %d = %q, want %q", i, rows[i], want)
		}
	}
}

// TestSidecar_MultipleCategorySetIsRecordedForWriteBack checks the definition
// survives an import that does nothing else with it.
//
// The MC arm produces no cohort column, so the sidecar is the ONLY place its
// existence is written down. An export that lost it would emit a file whose
// data is intact and whose "select up to three" question has silently become
// three unrelated variables.
func TestSidecar_MultipleCategorySetIsRecordedForWriteBack(t *testing.T) {
	_, _, doc := importFixture(t, mcSpec())
	p := doc.Payload

	// It derived nothing — that is the criterion, and it is a claim about
	// absence, so it is asserted over the whole registry.
	if len(p.Derived) != 0 {
		t.Errorf("a multiple-category set derived %+v; it must derive nothing at all", p.Derived)
	}

	var set MRSet
	for _, s := range p.MultipleResponseSets {
		if s.Name == "$ranks" {
			set = s
		}
	}
	if set.Name == "" {
		t.Fatalf("the $ranks definition is absent from the sidecar: %+v", p.MultipleResponseSets)
	}
	if set.Kind != MRSetKindCategory {
		t.Errorf("$ranks kind = %q, want %q", set.Kind, MRSetKindCategory)
	}
	if set.CountedValue != nil {
		t.Errorf("$ranks carries counted_value %q; a category set has none, and a consumer reading it without checking Kind must get nil", *set.CountedValue)
	}
	if set.Label != "Ranked brands" {
		t.Errorf("$ranks label = %q", set.Label)
	}
	if set.Subtype != spsstest.SubtypeMRSets {
		t.Errorf("$ranks subtype = %d, want %d", set.Subtype, spsstest.SubtypeMRSets)
	}
	// Member SHORT names in file order — what a write path re-emits.
	if !equalStrings(set.Variables, []string{"R1", "R2", "R3"}) {
		t.Errorf("$ranks variables = %q, want the members in file order", set.Variables)
	}
	// And the resolution to cohort columns the import already performed,
	// index for index, so the fold never has to redo it.
	if !equalStrings(set.Fields, []string{"R1", "R2", "R3"}) {
		t.Errorf("$ranks fields = %q, want the resolved Pulse field names", set.Fields)
	}
}

// TestSidecar_MRSetFieldsResolveShortNames covers the case the parallel slot
// exists for: members whose cohort column is NOT named what the record says.
//
// Record 7/7 names members by short name; record 7/13 renames them. A
// consumer matching the two up itself is doing the lookup this slot records —
// and doing it against a document where the answer is already known.
func TestSidecar_MRSetFieldsResolveShortNames(t *testing.T) {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 1}
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "R1", LongName: "FirstChoice", Print: num},
			{Name: "R2", LongName: "SecondChoice", Print: num},
		},
		Cases: [][]spsstest.Value{{spsstest.Num(1), spsstest.Num(2)}},
		MultipleResponseSets: []spsstest.MRSet{
			{
				Name: "$ranks", Kind: spsstest.MRCategory,
				// R1 named twice: an MC set may legitimately do that, and
				// the parallel slot must not compact it away.
				Vars: []string{"R1", "R1", "R2"}, Subtype: spsstest.SubtypeMRSets,
			},
		},
	}
	_, _, doc := importFixture(t, spec)

	var set MRSet
	for _, s := range doc.Payload.MultipleResponseSets {
		if s.Name == "$ranks" {
			set = s
		}
	}
	if set.Name == "" {
		t.Fatal("the $ranks definition is absent from the sidecar")
	}
	if len(set.Fields) != len(set.Variables) {
		t.Fatalf("fields = %q and variables = %q have different lengths; the slots are index-for-index", set.Fields, set.Variables)
	}
	// The long name is the cohort column; the record still carries the
	// short one, and both are written down.
	if !equalStrings(set.Variables, []string{"R1", "R1", "R2"}) {
		t.Errorf("variables = %q — the record's own member list, duplicates included", set.Variables)
	}
	if !equalStrings(set.Fields, []string{"FirstChoice", "FirstChoice", "SecondChoice"}) {
		t.Errorf("fields = %q, want the resolved Pulse field names, duplicates intact", set.Fields)
	}
}

// TestSetFieldResolver_UndeclaredMemberResolvesToEmpty covers the one arm the
// fixture builder will not let a `.sav` express: a set naming a variable no
// record type 2 declares.
//
// A real file can carry one — the builder refuses on purpose, because it is
// damage rather than a dialect — and the resolver must say so with "" rather
// than guessing a name or shortening the parallel array.
func TestSetFieldResolver_UndeclaredMemberResolvesToEmpty(t *testing.T) {
	d := &dictionary{vars: []variable{{name: "R1"}, {name: "R2"}}}
	resolve := setFieldResolver(d, nil)

	got := resolve([]string{"R2", "GHOST", "r1"})
	if !equalStrings(got, []string{"R2", "", "R1"}) {
		t.Errorf("fields = %q, want [R2 \"\" R1] — index for index, case-insensitively, with \"\" for the undeclared member", got)
	}
	if resolve(nil) != nil {
		t.Error("a set with no members resolved to a non-nil field list")
	}
}

// ---------------------------------------------------------------------------
// The empty-mask contract note io/io.go now carries
// ---------------------------------------------------------------------------

// TestSetEmptyMask_SurvivesImportEndToEnd is the guard the SchemaAwareReader
// contract note points at.
//
// "Answered the battery and selected nothing" reaches the cohort as a cell of
// one bare delimiter, and that it lands as an EMPTY MASK rather than a null
// rests on two behaviours of the shared import path composing: isNullToken
// does not match "|", and splitSetTokens drops empty tokens. Neither is
// visible from io/spss, and a change to either would collapse the empty-mask
// and null states SILENTLY — both spellings would keep importing and only the
// meaning would change.
//
// So the assertion is made where the distinction physically lives: the stored
// bitmask and the null bitmap bit of the written `.pulse` file.
func TestSetEmptyMask_SurvivesImportEndToEnd(t *testing.T) {
	fs := afero.NewMemMapFs()
	src := NewReaderFromBytes(buildFixture(t, mdSpec()))
	job := pio.NewImportJob(src, "survey.pulse")
	job.FS = fs
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.RowErrors) != 0 {
		t.Fatalf("RowErrors = %v", report.RowErrors)
	}

	field := report.Schema.Field("media")
	if field == nil || !field.Type.IsSet() {
		t.Fatalf("media = %+v, want a set_* column", field)
	}
	if !field.Nullable {
		t.Fatal("media is not nullable, so the null state has nowhere to live and the test cannot distinguish it")
	}
	// Bit i is dictionary entry i is constituent i.
	if got := field.Dictionary.Values(); !equalStrings(got, []string{"Q1A", "Q1B", "Q1C"}) {
		t.Fatalf("media dictionary = %q, want the constituents in bit order", got)
	}

	f, err := fs.Open("survey.pulse")
	if err != nil {
		t.Fatalf("opening cohort: %v", err)
	}
	defer func() { _ = f.Close() }()
	if err := encoding.ReadHeader(f); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	schema, err := encoding.ReadSchema(f)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	rr := encoding.NewRecordReader(f, schema)

	for i, tc := range []struct {
		mask   uint64
		isNull bool
		why    string
	}{
		{mask: 0b101, why: "picked Q1A and Q1C"},
		{mask: 0b100, why: "picked Q1C; Q1B was never asked"},
		{mask: 0, why: "ANSWERED the battery and picked nothing — an empty mask, which is a real \"none of these\""},
		{isNull: true, why: "skipped the whole battery — nothing is known, so this row genuinely is null"},
	} {
		values := map[string]float64{}
		nulls := map[string]bool{}
		wide := map[string]any{}
		if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		if nulls["media"] != tc.isNull {
			t.Errorf("record %d media null = %v, want %v (%s)", i, nulls["media"], tc.isNull, tc.why)
			continue
		}
		if tc.isNull {
			continue
		}
		got, ok := wide["media"].(uint64)
		if !ok {
			t.Fatalf("record %d media mask = %v (%T), want a uint64", i, wide["media"], wide["media"])
		}
		if got != tc.mask {
			t.Errorf("record %d media mask = %b, want %b (%s)", i, got, tc.mask, tc.why)
		}
	}
}
