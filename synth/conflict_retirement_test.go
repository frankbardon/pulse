package synth_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// The numeric-target relationship arms retire as resolveConflicts
// claimants because SpecFromProfile stops populating them for a field
// that landed a model — NOT because conflict.go stopped arbitrating
// them. conflict.go still holds every one of those code paths, fully
// reachable, and a hand-authored spec that declares both a model and a
// numeric-target pair still exercises them (see
// TestSynthModel_RetiresNumericTargetPairStages in model_draw_test.go).
//
// That makes the retirement an EMERGENT property of two files agreeing,
// which is exactly the kind of property that regresses without anything
// failing: put one `if modelled[...]` guard back on the wrong side of a
// condition and generation still succeeds, the cohort still writes, and
// the only symptom is thousands of warnings reappearing to say the
// engine threw most of the measured structure away. These tests are the
// lock on that agreement.
//
// Conflict classes are matched by SUBSTRING against the warning text
// rather than by counting the warning slice. Total warning volume is not
// a stable quantity across this effort — model-drop warnings ("not
// applied" / "skipped") ride the same channel, and predictor selection
// changes how often they fire — so a count assertion over the whole
// slice would break for reasons unrelated to what is being locked here.

// conflictClass is one arm of resolveConflicts' arbitration, identified
// by the fragment its warning text carries.
//
// The fragment always keys off the "dropping ..." clause, never the
// "already claimed by ..." clause, for two reasons. It is the DROPPED
// relationship whose class the arbitration decided, and — the mechanical
// reason — the claimant spellings nest as substrings of one another:
// "set-categorical pair (" ends in "categorical pair (", so classifying
// on the bare spelling would file every set-categorical conflict under
// the categorical-categorical arm. Prefixing with "dropping " makes all
// six mutually exclusive; TestConflictClassFragments_Disjoint is the
// standing check on that.
type conflictClass struct {
	name     string
	fragment string
}

var (
	classCategoricalPair    = conflictClass{"categorical pair", "dropping categorical pair ("}
	classCategoricalNumeric = conflictClass{"categorical-numeric pair", "dropping categorical-numeric pair ("}
	classSetNumeric         = conflictClass{"set-numeric pair", "dropping set-numeric pair ("}
	classSetCategorical     = conflictClass{"set-categorical pair", "dropping set-categorical pair ("}
	classSetSet             = conflictClass{"set-set pair", "dropping set-set pair ("}
	// The correlation arm excludes a claimed participant rather than
	// dropping the whole matrix, so its clause names the exclusion
	// instead of a pair spelling.
	classCorrelationExclusion = conflictClass{"pairwise-correlation participant exclusion", "dropping pairwise correlation"}

	// numericTargetConflictClasses are the three arbitration outcomes
	// that must fall silent once a target is model-driven.
	numericTargetConflictClasses = []conflictClass{
		classCategoricalNumeric, classSetNumeric, classCorrelationExclusion,
	}
	// nonNumericTargetConflictClasses are the three that must not move
	// at all: a model predicts a numeric FROM categorical and set
	// structure and says nothing about how that structure co-varies with
	// itself.
	nonNumericTargetConflictClasses = []conflictClass{
		classCategoricalPair, classSetCategorical, classSetSet,
	}
	allConflictClasses = append(append([]conflictClass{}, numericTargetConflictClasses...), nonNumericTargetConflictClasses...)
)

// isConflictWarning reports whether w is one of resolveConflicts' own
// arbitration warnings, as opposed to a model-drop warning or the
// interim unhonoured-correlation notice, both of which share the
// channel.
func isConflictWarning(w string) bool {
	return strings.Contains(w, "conditional relationship conflict")
}

// conflictsOfClass returns every conflict warning belonging to class.
func conflictsOfClass(warnings []string, class conflictClass) []string {
	var out []string
	for _, w := range warnings {
		if isConflictWarning(w) && strings.Contains(w, class.fragment) {
			out = append(out, w)
		}
	}
	return out
}

// classesPresent names every conflict class appearing in warnings.
func classesPresent(warnings []string) map[string]bool {
	present := make(map[string]bool, len(allConflictClasses))
	for _, class := range allConflictClasses {
		if len(conflictsOfClass(warnings, class)) > 0 {
			present[class.name] = true
		}
	}
	return present
}

// TestConflictClassFragments_Disjoint guards the classifier the rest of
// this file leans on. If a set-categorical warning ever started
// matching the categorical-categorical class, the numeric-arm
// assertions below would keep passing while measuring the wrong thing.
//
// The samples are verbatim resolveConflicts output, not paraphrases —
// a paraphrase would test the test.
func TestConflictClassFragments_Disjoint(t *testing.T) {
	samples := []struct {
		want   conflictClass
		sample string
	}{
		{classCategoricalNumeric, `conditional relationship conflict: field "spend" is already claimed by categorical-numeric pair (region -> spend); dropping categorical-numeric pair (tier -> spend)`},
		{classCategoricalPair, `conditional relationship conflict: field "brand" is already claimed by categorical pair (region -> brand); dropping categorical pair (tier -> brand)`},
		{classSetNumeric, `conditional relationship conflict: field "spend" is already claimed by categorical-numeric pair (region -> spend); dropping set-numeric pair (features.premium -> spend)`},
		{classSetCategorical, `conditional relationship conflict: field "channels" option "email" is already claimed by set-set pair (features.premium -> channels.email); dropping set-categorical pair (channels.email <- region)`},
		{classSetSet, `conditional relationship conflict: field "channels" option "email" is already claimed by set-set pair (features.premium -> channels.email); dropping set-set pair (features.trial -> channels.email)`},
		{classCorrelationExclusion, `conditional relationship conflict: field "spend" is already claimed by categorical-numeric pair (region -> spend); dropping pairwise correlation (excluding this field; remaining participants still correlate)`},
	}
	for _, tc := range samples {
		var matched []string
		for _, class := range allConflictClasses {
			if strings.Contains(tc.sample, class.fragment) {
				matched = append(matched, class.name)
			}
		}
		if len(matched) != 1 || matched[0] != tc.want.name {
			t.Errorf("sample for %s classified as %v, want exactly [%s]\n  %s",
				tc.want.name, matched, tc.want.name, tc.sample)
		}
	}
}

// modelDrivenProfiles profiles the same cohort twice — once with
// --conditional alone, once with --conditional --fit-models — and
// returns both resulting Specs and their SpecFromProfile warnings.
//
// Profiling the SAME bytes both ways is what makes every comparison in
// this file like-for-like: any difference in the warning set is
// attributable to the flag and to nothing else.
func modelDrivenProfiles(t *testing.T) (condSpec, bothSpec *synth.Spec, condWarn, bothWarn []string) {
	t.Helper()
	data := modelsCohort(t)
	condOpts := synth.ProfileOptions{IncludeStats: true, IncludeConditional: true, CorrelationTopK: 8, Seed: 5}
	condOnly, err := synth.ProfileBytes(data, condOpts)
	if err != nil {
		t.Fatalf("conditional profile: %v", err)
	}
	bothOpts := condOpts
	bothOpts.FitModels = true
	both, err := synth.ProfileBytes(data, bothOpts)
	if err != nil {
		t.Fatalf("conditional+models profile: %v", err)
	}
	condSpec, condWarn = synth.SpecFromProfile(condOnly, 500)
	bothSpec, bothWarn = synth.SpecFromProfile(both, 500)
	return condSpec, bothSpec, condWarn, bothWarn
}

// TestSpecFromProfile_ModelDrivenProfileRaisesNoNumericTargetConflicts
// is this story's headline assertion, stated per CLASS rather than as a
// total: a profile whose numerics landed models must produce not one
// arbitration warning about a categorical-numeric pair, a set-numeric
// pair, or a correlation participant excluded because a pair claimed it.
//
// The conditional-only arm of the same cohort is asserted to produce
// each of those classes first, so a fixture that quietly stopped
// exercising an arm cannot make the retirement look proven.
//
// The correlation class needs its own sentence. Under models the
// participants ARE still excluded from the copula — but the exclusion is
// no longer an arbitration outcome, because the two are meant to compose
// (the correlation becomes the model's residual structure) and simply
// have not been wired together yet. conflict.go says so in a
// deliberately differently-worded interim notice that is not a conflict
// at all; TestSynthModel_CorrelationNamingModelledFieldWarnsAsInterim
// (model_draw_test.go) is where that wording is pinned, and E3-S2 is
// where it goes away. What this test locks is only that the
// CONFLICT-class exclusion is gone: a modelled field must never be
// reported as having lost its correlation to a pair that claimed it
// first, because no pair claims it any more.
func TestSpecFromProfile_ModelDrivenProfileRaisesNoNumericTargetConflicts(t *testing.T) {
	condSpec, bothSpec, condWarn, bothWarn := modelDrivenProfiles(t)

	if len(bothSpec.Models) == 0 {
		t.Fatal("the --fit-models profile produced no spec models; nothing below proves anything")
	}
	if len(condSpec.Models) != 0 {
		t.Fatalf("the conditional-only profile produced %d spec models", len(condSpec.Models))
	}

	for _, class := range numericTargetConflictClasses {
		if got := conflictsOfClass(condWarn, class); len(got) == 0 {
			t.Fatalf("fixture no longer exercises the %s conflict class without --fit-models; the retirement assertion below would be vacuous", class.name)
		}
		if got := conflictsOfClass(bothWarn, class); len(got) != 0 {
			t.Errorf("%s conflicts survived --fit-models (%d):\n  %s",
				class.name, len(got), strings.Join(got, "\n  "))
		}
	}

	// The mechanism, asserted directly beside its symptom: the arms are
	// empty for a modelled target, so the claims are never made.
	if n := len(bothSpec.CategoricalNumericPairs); n != 0 {
		t.Errorf("CategoricalNumericPairs = %d, want 0 — the empty arm, not a change in arbitration, is what retires the warnings", n)
	}
	if n := len(bothSpec.SetNumericPairs); n != 0 {
		t.Errorf("SetNumericPairs = %d, want 0 — the empty arm, not a change in arbitration, is what retires the warnings", n)
	}
}

// TestSpecFromProfile_NonNumericConflictArbitrationSurvivesModels is the
// other half of the same claim, and the half that makes this a targeted
// retirement rather than a blunt suppression: the three non-numeric arms
// must arbitrate IDENTICALLY with and without --fit-models — same
// warnings, same wording, same order.
//
// Ordered string equality rather than a count, because pick-one
// arbitration is order-dependent (first claimant wins) and a count would
// pass if the kept and dropped relationships swapped places.
//
// The fixture exercises the set-categorical and set-set arms but not the
// categorical-categorical one — modelsCohort has no two categoricals
// contending for the same categorical target. That third arm is covered
// against the exported surface by
// TestResolveConflicts_SurvivingArmsUnchangedByModels below, which can
// hand-author the contention a profile capture does not produce here.
func TestSpecFromProfile_NonNumericConflictArbitrationSurvivesModels(t *testing.T) {
	_, _, condWarn, bothWarn := modelDrivenProfiles(t)

	collect := func(warnings []string) []string {
		var out []string
		for _, w := range warnings {
			if !isConflictWarning(w) {
				continue
			}
			for _, class := range nonNumericTargetConflictClasses {
				if strings.Contains(w, class.fragment) {
					out = append(out, w)
					break
				}
			}
		}
		return out
	}
	condNonNumeric := collect(condWarn)
	bothNonNumeric := collect(bothWarn)

	if len(condNonNumeric) == 0 {
		t.Fatal("fixture produced no non-numeric-target conflicts; the parity check below would be vacuous")
	}
	if len(condNonNumeric) != len(bothNonNumeric) {
		t.Fatalf("non-numeric conflict count moved under --fit-models: %d -> %d\n without:\n  %s\n with:\n  %s",
			len(condNonNumeric), len(bothNonNumeric),
			strings.Join(condNonNumeric, "\n  "), strings.Join(bothNonNumeric, "\n  "))
	}
	for i := range condNonNumeric {
		if condNonNumeric[i] != bothNonNumeric[i] {
			t.Errorf("non-numeric conflict %d moved under --fit-models\n without: %s\n    with: %s",
				i, condNonNumeric[i], bothNonNumeric[i])
		}
	}

	// Equality alone would also hold if an arm had been suppressed on
	// BOTH sides, so name the arms this fixture is actually able to see.
	for _, class := range []conflictClass{classSetCategorical, classSetSet} {
		if len(conflictsOfClass(bothNonNumeric, class)) == 0 {
			t.Errorf("no %s conflict in the --fit-models run; the parity check cannot see that arm", class.name)
		}
	}
}

// TestResolveConflicts_SurvivingArmsUnchangedByModels drives the
// EXPORTED surface — the one synth_fidelity.go calls to decide which
// relationships a fidelity report is allowed to score — against a
// hand-authored spec declaring a duplicate claim on each of the three
// surviving arms, with and without a model on an unrelated numeric.
//
// Hand-authored rather than profile-derived on purpose. A
// profile-derived spec cannot express "a model AND a contended
// categorical-categorical target" on this fixture, and it is precisely
// the arms a capture happens not to contend that a suppression bug would
// slip through. Here the model is present, the numeric arms are empty
// exactly as SpecFromProfile leaves them for a modelled target, and all
// three non-numeric arms must come back pruned pick-one either way.
func TestResolveConflicts_SurvivingArmsUnchangedByModels(t *testing.T) {
	base := func() *synth.Spec {
		return &synth.Spec{
			RowCount: 100,
			Fields: []synth.FieldSpec{
				{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
					Params: map[string]any{"values": []any{"east", "west"}, "weights": []any{0.5, 0.5}}},
				{Name: "tier", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
					Params: map[string]any{"values": []any{"gold", "silver"}, "weights": []any{0.5, 0.5}}},
				{Name: "brand", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
					Params: map[string]any{"values": []any{"acme", "zeta"}, "weights": []any{0.5, 0.5}}},
				{Name: "features", Type: "set_u8", Distribution: synth.DistSetBernoulli,
					Params: map[string]any{"options": []any{"premium", "trial"}, "frequencies": []any{0.5, 0.5}}},
				{Name: "channels", Type: "set_u8", Distribution: synth.DistSetBernoulli,
					Params: map[string]any{"options": []any{"email", "sms"}, "frequencies": []any{0.5, 0.5}}},
				{Name: "spend", Type: "f64", Distribution: synth.DistNormal,
					Params: map[string]any{"mean": 50.0, "std": 10.0}},
			},
			CategoricalPairs: []synth.CategoricalPairSpec{
				{A: "region", B: "brand", Cells: []synth.CategoricalPairCellSpec{
					{AValue: "east", BValue: "acme", Count: 90}, {AValue: "west", BValue: "zeta", Count: 90},
				}},
				{A: "tier", B: "brand", Cells: []synth.CategoricalPairCellSpec{
					{AValue: "gold", BValue: "acme", Count: 90}, {AValue: "silver", BValue: "zeta", Count: 90},
				}},
			},
			SetCategoricalPairs: []synth.SetCategoricalPairSpec{
				{Set: "features", Option: "premium", Categorical: "region", Cells: []synth.CategoricalPairCellSpec{
					{AValue: "selected", BValue: "east", Count: 90}, {AValue: "not_selected", BValue: "west", Count: 90},
				}},
				{Set: "features", Option: "premium", Categorical: "tier", Cells: []synth.CategoricalPairCellSpec{
					{AValue: "selected", BValue: "gold", Count: 90}, {AValue: "not_selected", BValue: "silver", Count: 90},
				}},
			},
			SetSetPairs: []synth.SetSetPairSpec{
				{SetA: "features", OptionA: "premium", SetB: "channels", OptionB: "email",
					Cells: []synth.CategoricalPairCellSpec{
						{AValue: "selected", BValue: "selected", Count: 90},
						{AValue: "not_selected", BValue: "not_selected", Count: 90},
					}},
				{SetA: "features", OptionA: "trial", SetB: "channels", OptionB: "email",
					Cells: []synth.CategoricalPairCellSpec{
						{AValue: "selected", BValue: "selected", Count: 90},
						{AValue: "not_selected", BValue: "not_selected", Count: 90},
					}},
			},
		}
	}

	withoutModel := base()
	withModel := base()
	withModel.Models = []synth.FieldModelSpec{{
		Field:     "spend",
		Intercept: 40,
		Predictors: []synth.ModelPredictorSpec{
			{Kind: synth.ModelPredictorCategoricalLevel, Field: "region", Level: "west", Coefficient: 20},
		},
		ResidualStd: 5,
	}}

	type resolved struct {
		CatPairs     []synth.CategoricalPairSpec
		CatNumPairs  []synth.CategoricalNumericPairSpec
		SetCatPairs  []synth.SetCategoricalPairSpec
		SetNumPairs  []synth.SetNumericPairSpec
		SetSetPairs  []synth.SetSetPairSpec
		Correlations []synth.CorrelationSpec
	}
	resolve := func(s *synth.Spec) resolved {
		var r resolved
		r.CatPairs, r.CatNumPairs, r.SetCatPairs, r.SetNumPairs, r.SetSetPairs, r.Correlations = synth.ResolveConflicts(s)
		return r
	}

	plain := resolve(withoutModel)
	modelled := resolve(withModel)

	for _, tc := range []struct {
		name string
		got  resolved
	}{{"without a model", plain}, {"with a model", modelled}} {
		if n := len(tc.got.CatPairs); n != 1 {
			t.Errorf("%s: CategoricalPairs survived = %d, want 1 (pick-one)", tc.name, n)
		}
		if n := len(tc.got.SetCatPairs); n != 1 {
			t.Errorf("%s: SetCategoricalPairs survived = %d, want 1 (pick-one)", tc.name, n)
		}
		if n := len(tc.got.SetSetPairs); n != 1 {
			t.Errorf("%s: SetSetPairs survived = %d, want 1 (pick-one)", tc.name, n)
		}
	}

	// And the model changed nothing about WHICH one won.
	plainJSON, err := json.Marshal(plain)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	modelledJSON, err := json.Marshal(modelled)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(plainJSON) != string(modelledJSON) {
		t.Errorf("ResolveConflicts' surviving arms moved when a model was added\n without: %s\n    with: %s",
			plainJSON, modelledJSON)
	}
}

// TestSpecFromProfile_PreModelsDocumentKeepsFullConflictSet is the
// "retirement is conditional on models, not global" gate.
//
// testdata/profile_pre_models.json is a static document captured by the
// pre-change tree, so it keeps describing the old world however far the
// capture path moves. It must still produce the FULL pre-change conflict
// set — the exact 20 warnings the fixture's provenance comment records
// (see preModelsSpecHash) — with every class that document can raise
// still represented. A count is justified here in a way it is not
// elsewhere in this file: the input is a frozen file, so its warning set
// is a fixed quantity, and the whole point is that it did not shrink.
func TestSpecFromProfile_PreModelsDocumentKeepsFullConflictSet(t *testing.T) {
	raw, err := os.ReadFile("testdata/profile_pre_models.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var prof synth.Profile
	if err := json.Unmarshal(raw, &prof); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(prof.Models) != 0 {
		t.Fatal("fixture is not a pre-models document")
	}

	_, warnings := synth.SpecFromProfile(&prof, 500)

	// The 20 the pre-change tree produced, verified against it when the
	// fixture was pinned.
	const wantConflicts = 20
	got := countWarnings(warnings, "conditional relationship conflict")
	if got != wantConflicts {
		t.Errorf("an old document no longer arbitrates as it did: %d conflict warnings, want %d\n  %s",
			got, wantConflicts, strings.Join(warnings, "\n  "))
	}
	// A document with no models section must raise no model warning
	// either — the whole slice is conflicts.
	if len(warnings) != got {
		t.Errorf("a pre-models document produced %d warnings of which only %d are conflicts\n  %s",
			len(warnings), got, strings.Join(warnings, "\n  "))
	}

	// classCategoricalPair is deliberately absent from this list: the
	// document contends no categorical-categorical target, so requiring
	// it would assert something about the fixture rather than about the
	// retirement.
	for _, class := range []conflictClass{
		classCategoricalNumeric, classSetNumeric, classCorrelationExclusion,
		classSetCategorical, classSetSet,
	} {
		if len(conflictsOfClass(warnings, class)) == 0 {
			t.Errorf("the %s conflict class vanished from the pre-models document's warning set", class.name)
		}
	}
	if present := classesPresent(warnings); present[classCategoricalPair.name] {
		t.Errorf("the fixture grew a %s conflict; the class list above needs updating", classCategoricalPair.name)
	}
}
