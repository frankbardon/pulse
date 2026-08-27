package spss

import (
	"context"
	"encoding/binary"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// DERIVED-COLUMN FOLD-BACK.
//
// The acceptance criterion is a negative: an emitted `.sav` carries exactly
// the source's own variables and no `_missing` or `set_*` artefacts. So the
// tests below assert the ABSENCE of the derived columns from the emitted
// dictionary, the PRESENCE of what they folded into, and — the criterion that
// matters most — that both decisions come from the registry and not from a
// name.

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// exportedFile imports a spec and writes the cohort straight back out.
func exportedFile(t *testing.T, spec spsstest.Spec, opts WriterOptions) (afero.Fs, string, []byte) {
	t.Helper()
	fs, cohort, _ := importFixture(t, spec)
	return fs, cohort, exportCohort(t, fs, cohort, opts)
}

// emittedNames lists the variables an emitted file declares, in file order.
func emittedNames(t *testing.T, sav []byte) []string {
	t.Helper()
	d, err := parseDictionary(sav)
	if err != nil {
		t.Fatalf("the emitted file's dictionary does not parse: %v", err)
	}
	out := make([]string, 0, len(d.vars))
	for _, v := range d.vars {
		out = append(out, v.fieldName())
	}
	return out
}

// elementDoubles reads one variable's element out of every emitted case.
//
// It goes through the flat case bytes rather than through this package's own
// row reader on purpose: the row reader re-applies the missing specification,
// which is the very thing under test, so it would report a null for a
// restored code and prove nothing.
func elementDoubles(t *testing.T, sav []byte, index int32) []float64 {
	t.Helper()
	d, err := parseDictionary(sav)
	if err != nil {
		t.Fatalf("parsing the emitted dictionary: %v", err)
	}
	dp, err := buildDataPlan(d)
	if err != nil {
		t.Fatalf("buildDataPlan: %v", err)
	}
	flat := flatCases(t, sav)
	at := (int(index) - 1) * elementSize
	out := []float64{}
	for off := 0; off+dp.stride <= len(flat); off += dp.stride {
		out = append(out, math.Float64frombits(
			binary.LittleEndian.Uint64(flat[off+at:off+at+elementSize])))
	}
	return out
}

// exportRequest is the request a real export builds, with the resolution and
// schema exposed so a test can perturb one before emission.
func exportRequest(t *testing.T, spec spsstest.Spec) (DictionaryRequest, *SidecarResolution) {
	t.Helper()
	schema, res := exportFixture(t, spec)
	return DictionaryRequest{Schema: schema, Sidecar: res, Cases: 0, Compression: compressionNone}, res
}

// ---------------------------------------------------------------------------
// The criterion
// ---------------------------------------------------------------------------

// TestFold_EmittedFileCarriesExactlyTheSourcesVariables is the acceptance
// criterion in one assertion. The cohort has five columns; the `.sav` it came
// from had three variables, and the file written back out has those three.
func TestFold_EmittedFileCarriesExactlyTheSourcesVariables(t *testing.T) {
	fs, cohort, sav := exportedFile(t, bothKindsSpec(), WriterOptions{})

	// The cohort really does carry the two derived columns, so the
	// assertion below is about a fold and not about a fixture that never
	// had them.
	schema := cohortSchema(t, fs, cohort)
	if len(schema.Fields) != 5 {
		t.Fatalf("the cohort has %d field(s), want 5 (three variables, a reason sibling and a set column)",
			len(schema.Fields))
	}

	got := emittedNames(t, sav)
	want := []string{"INCOME", "Q1A", "Q1B"}
	if len(got) != len(want) {
		t.Fatalf("the emitted file declares %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("variable %d is %q, want %q", i, got[i], want[i])
		}
	}
	for _, name := range got {
		if name == "INCOME"+MissingSiblingSuffix || name == "media" {
			t.Errorf("the derived column %q was emitted as an SPSS variable", name)
		}
	}
}

// TestFold_UnboundFieldsAreExactlyTheRegistry pins the seam E5-S2 left and
// E5-S5 audits: on a sidecar-driven plan the unbound cohort fields are the
// derived ones, and nothing else.
func TestFold_UnboundFieldsAreExactlyTheRegistry(t *testing.T) {
	req, res := exportRequest(t, bothKindsSpec())
	plan := emit(t, req)

	registry := map[string]bool{}
	for _, d := range res.Document.Payload.Derived {
		registry[d.Name] = true
	}
	if len(registry) != 2 {
		t.Fatalf("the registry has %d entries, want 2", len(registry))
	}
	if len(plan.UnboundFields) != len(registry) {
		t.Fatalf("%d unbound field(s), want %d: %v", len(plan.UnboundFields), len(registry), plan.UnboundFields)
	}
	for _, at := range plan.UnboundFields {
		if name := req.Schema.Fields[at].Name; !registry[name] {
			t.Errorf("cohort field %q is unbound and not in the derived registry; it would be dropped silently", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Restore: the reason sibling folds back into a missing CODE
// ---------------------------------------------------------------------------

// TestFold_MissingCodesAreRestoredIntoTheVariable is the fold that has to
// reconstruct something.
//
// The import nulled INCOME at both missing positions and put the reason in a
// sibling, because the `.pulse` null bitmap is one bit and cannot say why.
// The export has to put the ORIGINAL code back: 99 where the file said 99,
// and the system-missing sentinel where it said sysmis. Writing sysmis for
// both would be a file that no longer distinguishes "refused" from "not
// asked" — the exact loss E4-S2 exists to prevent.
func TestFold_MissingCodesAreRestoredIntoTheVariable(t *testing.T) {
	_, _, sav := exportedFile(t, bothKindsSpec(), WriterOptions{})

	d, err := parseDictionary(sav)
	if err != nil {
		t.Fatalf("parsing the emitted dictionary: %v", err)
	}
	income := readVar(t, d, "INCOME")
	values := elementDoubles(t, sav, income.index)
	if len(values) != 3 {
		t.Fatalf("%d case(s) emitted, want 3", len(values))
	}

	if values[0] != 30000 {
		t.Errorf("case 1 INCOME = %v, want 30000", values[0])
	}
	if values[1] != 99 {
		t.Errorf("case 2 INCOME = %v, want the user-missing code 99 restored from the reason sibling", values[1])
	}
	if !isSysmisBits(values[2], d.sysmis) {
		t.Errorf("case 3 INCOME = %v, want the system-missing sentinel %v", values[2], d.sysmis)
	}
}

// isSysmisBits compares against the sentinel by BITS: -DBL_MAX is a finite
// double and an == comparison on it is exact, but the bit comparison is what
// the encoder actually writes and what a reader actually matches.
func isSysmisBits(v, sysmis float64) bool {
	return math.Float64bits(v) == math.Float64bits(sysmis)
}

// TestFold_RestoredCodesSurviveAReimport closes the loop through an
// independent path: the emitted file is read back by this package's own
// reader, which re-applies the missing specification and regenerates the
// sibling. The reasons that come back must be the reasons that went in.
func TestFold_RestoredCodesSurviveAReimport(t *testing.T) {
	_, _, sav := exportedFile(t, bothKindsSpec(), WriterOptions{})

	head, rows := savRows(t, sav)
	at := -1
	for i, name := range head {
		if name == "INCOME"+MissingSiblingSuffix {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("re-importing the emitted file produced no reason sibling: %v", head)
	}
	want := []string{"", "99", SysmisReason}
	if len(rows) != len(want) {
		t.Fatalf("%d row(s) back, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if rows[i][at] != w {
			t.Errorf("row %d reason = %q, want %q", i, rows[i][at], w)
		}
	}
}

// TestFold_MissingSpecificationIsReemittedBesideTheCodes. Restoring the code
// is only half of it: the emitted file must also DECLARE 99 missing, or the
// value comes back as ordinary data on the next read.
func TestFold_MissingSpecificationIsReemittedBesideTheCodes(t *testing.T) {
	_, _, sav := exportedFile(t, bothKindsSpec(), WriterOptions{})
	d, err := parseDictionary(sav)
	if err != nil {
		t.Fatalf("parsing the emitted dictionary: %v", err)
	}
	income := readVar(t, d, "INCOME")
	if got := income.missing.count(); got != 1 {
		t.Fatalf("INCOME declares %d missing value(s), want 1", got)
	}
	if got := income.missing.numeric[0]; got != 99 {
		t.Errorf("INCOME declares the missing value %v, want 99", got)
	}
}

// ---------------------------------------------------------------------------
// Drop: the set column goes, its constituents stay
// ---------------------------------------------------------------------------

// TestFold_SetColumnIsDroppedAndItsConstituentsRemain. Every bit the derived
// `set_*` column shows is a second reading of a constituent that is still in
// the cohort under its own name, so the fold takes nothing from it — but the
// record 7/7 DEFINITION must still go back out, or the file loses the set it
// came from.
func TestFold_SetColumnIsDroppedAndItsConstituentsRemain(t *testing.T) {
	_, _, sav := exportedFile(t, bothKindsSpec(), WriterOptions{})
	d, err := parseDictionary(sav)
	if err != nil {
		t.Fatalf("parsing the emitted dictionary: %v", err)
	}

	for _, v := range d.vars {
		if v.fieldName() == "media" {
			t.Error("the derived set column was emitted as an SPSS variable")
		}
	}

	if len(d.mrSets) != 1 {
		t.Fatalf("the emitted file declares %d response set(s), want 1", len(d.mrSets))
	}
	set := d.mrSets[0]
	if set.setName() != "$media" {
		t.Errorf("set name = %q, want $media", set.setName())
	}
	if got := set.setVars(); len(got) != 2 || got[0] != "Q1A" || got[1] != "Q1B" {
		t.Errorf("set members = %v, want [Q1A Q1B] in bit order", got)
	}

	// And the constituents carry the values the mask was a second reading
	// of: Q1A selected on cases 1 and 3, Q1B on cases 2 and 3.
	q1a := elementDoubles(t, sav, readVar(t, d, "Q1A").index)
	q1b := elementDoubles(t, sav, readVar(t, d, "Q1B").index)
	for i, want := range [][2]float64{{1, 0}, {0, 1}, {1, 1}} {
		if q1a[i] != want[0] || q1b[i] != want[1] {
			t.Errorf("case %d = (%v, %v), want (%v, %v)", i+1, q1a[i], q1b[i], want[0], want[1])
		}
	}
}

// ---------------------------------------------------------------------------
// Registry-driven, never name-driven
// ---------------------------------------------------------------------------

// TestFold_ARealVariableNamedLikeASiblingIsEmitted is the criterion the whole
// registry exists for.
//
// `INCOME_missing` is a perfectly legal SPSS variable name, and a survey that
// declares one is not hypothetical. A fold that matched on the "_missing"
// suffix would consume it and drop a respondent's answers, in a file that
// still opened cleanly in every reader. The registry does not name it, so it
// is a source variable by construction and is emitted like any other.
func TestFold_ARealVariableNamedLikeASiblingIsEmitted(t *testing.T) {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 8}
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			// No missing specification on INCOME, so no sibling is derived
			// and the name below cannot collide with a generated one.
			{Name: "INCOME", Print: num},
			{Name: "IMISS", LongName: "INCOME_missing", Print: num},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(30000), spsstest.Num(7)},
			{spsstest.Num(40000), spsstest.Num(8)},
		},
	}

	fs, cohort, sav := exportedFile(t, spec, WriterOptions{})

	doc := readSidecar(t, fs, cohort)
	if len(doc.Payload.Derived) != 0 {
		t.Fatalf("the registry is not empty: %+v; the fixture must derive nothing", doc.Payload.Derived)
	}

	names := emittedNames(t, sav)
	if len(names) != 2 || names[1] != "INCOME_missing" {
		t.Fatalf("the emitted file declares %v; the real INCOME_missing variable was consumed by a name match", names)
	}

	d, err := parseDictionary(sav)
	if err != nil {
		t.Fatalf("parsing the emitted dictionary: %v", err)
	}
	got := elementDoubles(t, sav, readVar(t, d, "INCOME_missing").index)
	if len(got) != 2 || got[0] != 7 || got[1] != 8 {
		t.Errorf("INCOME_missing = %v, want [7 8]; its data must survive", got)
	}
}

// TestFold_DroppedColumnNeedsNoRecognisableName is the mirror image: the
// derived multiple-dichotomy column is called `media`, which matches no
// pattern at all, and it is dropped because the registry says so.
func TestFold_DroppedColumnNeedsNoRecognisableName(t *testing.T) {
	req, res := exportRequest(t, bothKindsSpec())
	for _, d := range res.Document.Payload.Derived {
		if d.Kind == DerivedKindMultipleDichotomy && d.Name != "media" {
			t.Fatalf("the derived set column is named %q; the fixture no longer makes the point", d.Name)
		}
	}
	plan := emit(t, req)
	for _, c := range plan.Columns {
		if c.FieldName == "media" {
			t.Error("the set column was bound to an emitted variable")
		}
	}
}

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

// TestFold_UnboundColumnOutsideTheRegistryIsRefused is the check worth more
// than the fold: a cohort column no variable is written from and the registry
// does not account for is data about to leave the export silently.
func TestFold_UnboundColumnOutsideTheRegistryIsRefused(t *testing.T) {
	req, res := exportRequest(t, bothKindsSpec())
	// Forget the reason sibling. The column is still in the cohort and
	// still bound to no variable — it is now simply unexplained.
	kept := res.Document.Payload.Derived[:0]
	for _, d := range res.Document.Payload.Derived {
		if d.Kind != DerivedKindNumericMissing {
			kept = append(kept, d)
		}
	}
	res.Document.Payload.Derived = kept

	ce := emitFails(t, req, perr.PULSE_SPSS_COLUMN_UNMAPPED)
	if got := ce.Details[perr.DetailSPSSField]; got != "INCOME"+MissingSiblingSuffix {
		t.Errorf("details[%s] = %v, want the column about to be dropped", perr.DetailSPSSField, got)
	}
}

// TestFold_UnknownDerivedKindIsRefused. A kind outside this build's
// vocabulary is a document written by a NEWER import: the column's fold-back
// is genuinely unknown, and both guesses — emit it, or drop it — are silent
// data faults. DerivedFoldFor reports false precisely so this is detectable.
func TestFold_UnknownDerivedKindIsRefused(t *testing.T) {
	req, res := exportRequest(t, bothKindsSpec())
	res.Document.Payload.Derived[0].Kind = "something_a_later_import_invented"
	emitFails(t, req, perr.PULSE_SPSS_DERIVED_UNFOLDABLE)
}

// TestFold_IncompleteRegistryEntryIsRefused. E4-S2 was explicit that without
// `reasons` the fold would have to re-derive the mapping from the missing
// specification plus the value labels and hope it matched. It does not hope.
func TestFold_IncompleteRegistryEntryIsRefused(t *testing.T) {
	req, res := exportRequest(t, bothKindsSpec())
	for i := range res.Document.Payload.Derived {
		if res.Document.Payload.Derived[i].Kind == DerivedKindNumericMissing {
			res.Document.Payload.Derived[i].Reasons = nil
		}
	}
	emitFails(t, req, perr.PULSE_SPSS_DERIVED_UNFOLDABLE)
}

// TestFold_RegistryEntryNamingAnEmittedVariableIsRefused: the document
// declaring a column and disowning it at once. It is reported as the
// collision it is — a generated name landing on a real variable.
func TestFold_RegistryEntryNamingAnEmittedVariableIsRefused(t *testing.T) {
	req, res := exportRequest(t, bothKindsSpec())
	res.Document.Payload.Derived = append(res.Document.Payload.Derived, Derived{
		Name: "Q1A", Kind: DerivedKindMultipleDichotomy,
		SetName: "$media", Sources: []string{"Q1B"}, Position: 2,
	})
	ce := emitFails(t, req, perr.PULSE_SPSS_DERIVED_NAME_COLLISION)
	if got := ce.Details[perr.DetailSPSSDerived]; got != "Q1A" {
		t.Errorf("details[%s] = %v, want the derived name", perr.DetailSPSSDerived, got)
	}
}

// TestFold_RestoreIntoAnUnemittedSourceIsRefused. Consuming a reason column
// whose variable is not being written would discard the only record of which
// missing state each row was in.
func TestFold_RestoreIntoAnUnemittedSourceIsRefused(t *testing.T) {
	req, res := exportRequest(t, bothKindsSpec())
	for i := range res.Document.Payload.Derived {
		if d := &res.Document.Payload.Derived[i]; d.Kind == DerivedKindNumericMissing {
			d.Sources = []string{"media"} // a cohort column no variable is written from
		}
	}
	emitFails(t, req, perr.PULSE_SPSS_DERIVED_UNFOLDABLE)
}

// ---------------------------------------------------------------------------
// The synthesised path
// ---------------------------------------------------------------------------

// missingOnlySpec is bothKindsSpec without the response set: one numeric
// variable declaring a user-missing code, and nothing that derives a `set_*`
// column. It is what the synthesised path can be asked about without the set
// expansion getting in the way — see
// TestFold_IgnoreSidecarOnADerivedSetColumnCollides.
func missingOnlySpec() spsstest.Spec {
	spec := bothKindsSpec()
	spec.MultipleResponseSets = nil
	return spec
}

// TestFold_IgnoreSidecarEmitsEveryColumnIncludingTheDerivedOnes.
//
// --ignore-sidecar is not "fold differently", it is "there is no registry":
// the dictionary comes from the `.pulse` schema alone, so a derived column is
// an ordinary column and goes out as its own variable. Nothing is folded and,
// crucially, nothing is dropped — the audit that refuses an unexplained
// unbound column runs on this path too.
func TestFold_IgnoreSidecarEmitsEveryColumnIncludingTheDerivedOnes(t *testing.T) {
	_, _, sav := exportedFile(t, missingOnlySpec(), WriterOptions{IgnoreSidecar: true})
	names := emittedNames(t, sav)

	want := []string{"INCOME", "INCOME" + MissingSiblingSuffix, "Q1A", "Q1B"}
	if len(names) != len(want) {
		t.Fatalf("the synthesised export declares %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("variable %d is %q, want %q", i, names[i], want[i])
		}
	}
}

// TestFold_IgnoreSidecarOnADerivedSetColumnCollides records a real
// interaction rather than a defect, and it is the reason the test above uses
// a set-free fixture.
//
// A derived multiple-dichotomy column's dictionary entries ARE its
// constituents' field names (see planMRSet in mrset.go). Synthesising over
// that cohort expands the set column into one indicator variable per entry,
// NAMED for that entry — so it mints `Q1A` and `Q1B` beside the real `Q1A`
// and `Q1B` columns. Before E5-S5 that emitted a file with two variables of
// each name, of which record 7/13 keeps one. It is now a refusal, which is
// the honest answer: --ignore-sidecar cannot round-trip a cohort whose
// derived set column is still present, and the sidecar-driven path (which
// drops the column) is the one that can.
func TestFold_IgnoreSidecarOnADerivedSetColumnCollides(t *testing.T) {
	fs, cohort, _ := importFixture(t, bothKindsSpec())
	res, err := LoadSidecar(fs, cohort, WriterOptions{IgnoreSidecar: true})
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	emitFails(t, DictionaryRequest{
		Schema: cohortSchema(t, fs, cohort), Sidecar: res, Cases: 0,
	}, perr.PULSE_SPSS_NAME_COLLISION)
}

// TestFold_SynthesisedPlanBindsEveryCohortField. The audit is only as good as
// its input, so the synthesised front-end is held to leaving nothing unbound.
func TestFold_SynthesisedPlanBindsEveryCohortField(t *testing.T) {
	fs, cohort, _ := importFixture(t, missingOnlySpec())
	res, err := LoadSidecar(fs, cohort, WriterOptions{IgnoreSidecar: true})
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	plan := emit(t, DictionaryRequest{
		Schema: cohortSchema(t, fs, cohort), Sidecar: res, Cases: 0,
	})
	if len(plan.UnboundFields) != 0 {
		t.Errorf("the synthesised plan left %v unbound; with no registry, every field must bind",
			plan.UnboundFields)
	}
}

// ---------------------------------------------------------------------------
// The plan's sysmis contract
// ---------------------------------------------------------------------------

// TestPlanSysmis_IsWhatAReaderOfTheEmittedBytesResolves.
//
// A `.sav` may declare its own system-missing sentinel in record 7/4, and a
// conforming reader ADOPTS a coherent declaration. A sidecar-driven
// dictionary re-emits the source's 7/4 verbatim, so a plan that reported the
// spec default over such a file would hand a data encoder a value that reads
// back as ORDINARY DATA in every null it was written into.
//
// The slot is therefore derived from the emitted bytes rather than from the
// front-end's model, which makes the two agree by construction instead of by
// two implementations of one rule staying in step.
func TestPlanSysmis_IsWhatAReaderOfTheEmittedBytesResolves(t *testing.T) {
	declared := -1e300 // coherent with the highest/lowest below, and not -DBL_MAX

	for _, tc := range []struct {
		name string
		mf   *MachineFloat
		want float64
	}{
		{"no record 7/4 — the spec default", nil, defaultSysmis},
		{
			"a coherent declaration is adopted",
			&MachineFloat{Sysmis: Float(declared), Lowest: Float(-1e299), Highest: Float(math.MaxFloat64)},
			declared,
		},
		{
			"an incoherent triple is not adopted",
			&MachineFloat{Sysmis: Float(1), Lowest: Float(2), Highest: Float(0)},
			defaultSysmis,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, res := exportRequest(t, bothKindsSpec())
			res.Document.Payload.Source.MachineFloat = tc.mf
			plan := emit(t, req)

			if plan.Sysmis != tc.want {
				t.Errorf("plan.Sysmis = %v, want %v", plan.Sysmis, tc.want)
			}
			// The load-bearing half: whatever the plan says, a reader of
			// the bytes must say the same.
			d := reparse(t, plan)
			if plan.Sysmis != d.sysmis {
				t.Errorf("plan.Sysmis = %v but a reader of the emitted bytes resolves %v; "+
					"the data section would write a sentinel that reads back as data", plan.Sysmis, d.sysmis)
			}
		})
	}
}

// TestPlanSysmis_IsWhatTheDataEncoderWrites confirms the two have not merely
// been made to agree in the plan: a null really does come out as the declared
// sentinel when a source declares one.
func TestPlanSysmis_IsWhatTheDataEncoderWrites(t *testing.T) {
	req, res := exportRequest(t, bothKindsSpec())
	declared := -1e300
	res.Document.Payload.Source.MachineFloat = &MachineFloat{
		Sysmis: Float(declared), Lowest: Float(-1e299), Highest: Float(math.MaxFloat64),
	}
	req.Cases = -1
	plan := emit(t, req)
	if plan.Sysmis != declared {
		t.Fatalf("plan.Sysmis = %v, want the declared %v", plan.Sysmis, declared)
	}

	// One case, INCOME system-missing: the sibling's ID 0 is the sysmis
	// reason, so the fold resolves to the sentinel rather than to a code.
	c := NewCase(req.Schema)
	for i := range req.Schema.Fields {
		c[i] = CaseValue{}
	}
	byName := schemaIndex(req.Schema)
	c[byName["income"]] = CaseValue{Null: true}
	c[byName["income_missing"]] = CaseValue{Num: 0}

	sav := encodeCases(t, plan, req.Schema, c)
	d, err := parseDictionary(sav)
	if err != nil {
		t.Fatalf("parsing the emitted dictionary: %v", err)
	}
	got := elementDoubles(t, sav, readVar(t, d, "INCOME").index)
	if len(got) != 1 || !isSysmisBits(got[0], declared) {
		t.Errorf("INCOME = %v, want the declared sentinel %v", got, declared)
	}
}

// ---------------------------------------------------------------------------
// The encoder's own bounds
// ---------------------------------------------------------------------------

// TestFold_UnrecordedReasonIDIsRefusedRatherThanGuessed. The encoder resolves
// a null through the sibling's stored dictionary ID; an ID the registry never
// described has no SPSS form, and writing anything for it would be inventing
// a missing state.
func TestFold_UnrecordedReasonIDIsRefusedRatherThanGuessed(t *testing.T) {
	req, _ := exportRequest(t, bothKindsSpec())
	req.Cases = -1
	plan := emit(t, req)

	enc, err := NewDataEncoder(plan, req.Schema)
	if err != nil {
		t.Fatalf("NewDataEncoder: %v", err)
	}
	c := NewCase(req.Schema)
	byName := schemaIndex(req.Schema)
	c[byName["income"]] = CaseValue{Null: true}
	c[byName["income_missing"]] = CaseValue{Num: 99} // no such dictionary ID

	err = enc.WriteCase(c)
	if err == nil {
		t.Fatal("an unrecorded reason ID was written; it has no SPSS form")
	}
	if got := codeOf(t, err); got != perr.PULSE_SPSS_EXPORT_UNSUPPORTED {
		t.Errorf("code = %s, want PULSE_SPSS_EXPORT_UNSUPPORTED", got)
	}
}

// TestFold_ReimportedFileIsCohortIdentical is the round trip stated as one
// question: does a cohort survive a lap through `.sav` unchanged?
//
// It compares the SCHEMA and the ROWS of the re-imported cohort against the
// original, which is the strongest form the fold can be asserted in — a
// derived column that leaked would show up as an extra field, and a missing
// code that did not restore would show up as a changed reason.
func TestFold_ReimportedFileIsCohortIdentical(t *testing.T) {
	fs, cohort, sav := exportedFile(t, bothKindsSpec(), WriterOptions{})

	if err := afero.WriteFile(fs, "round.sav", sav, 0o644); err != nil {
		t.Fatalf("writing the emitted file: %v", err)
	}
	job := pio.NewImportJob(NewReader(fs, "round.sav"), "round.pulse")
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("re-importing the emitted file: %v", err)
	}

	before, after := cohortSchema(t, fs, cohort), cohortSchema(t, fs, "round.pulse")
	if len(before.Fields) != len(after.Fields) {
		t.Fatalf("the cohort had %d field(s) and came back with %d", len(before.Fields), len(after.Fields))
	}
	for i := range before.Fields {
		b, a := &before.Fields[i], &after.Fields[i]
		if b.Name != a.Name || b.Type != a.Type {
			t.Errorf("field %d was %s %s and came back %s %s", i, b.Name, b.Type, a.Name, a.Type)
		}
	}
	if got, want := cohortRows(t, fs, "round.pulse"), cohortRows(t, fs, cohort); !rowsEqual(got, want) {
		t.Errorf("rows came back\n  %v\nwant\n  %v", got, want)
	}
}

// cohortRows renders a cohort's rows through the shared export path.
func cohortRows(t *testing.T, fs afero.Fs, cohort string) [][]string {
	t.Helper()
	f, err := fs.Open(cohort)
	if err != nil {
		t.Fatalf("opening %s: %v", cohort, err)
	}
	defer f.Close()
	if err := encoding.ReadHeader(f); err != nil {
		t.Fatalf("header of %s: %v", cohort, err)
	}
	s, err := encoding.ReadSchema(f)
	if err != nil {
		t.Fatalf("schema of %s: %v", cohort, err)
	}
	var out [][]string
	c := NewCase(s)
	for {
		if err := readCohortCase(f, s, c); err != nil {
			break
		}
		row := make([]string, len(c))
		for i := range c {
			if c[i].Null {
				row[i] = "<null>"
				continue
			}
			row[i] = formatNumericValue(c[i].Num)
		}
		out = append(out, row)
	}
	return out
}

func rowsEqual(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}
