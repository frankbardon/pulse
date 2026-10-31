package synth_test

import (
	stderrors "errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/synth"
)

// tierFixture is a cohort where first-declared arbitration picks the
// WRONG driver: `region` (declared first) carries no information about
// `tier`, while `plan` decides it 85% of the time. Capture emits
// region→tier before plan→tier, so the old rule kept the noise pair.
func tierFixture(t *testing.T, rows int, seed int64) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	regions := []string{"north", "south", "east", "west"}
	plans := []string{"basic", "plus", "max"}
	tiers := []string{"bronze", "silver", "gold"}
	var buf strings.Builder
	buf.WriteString("region,plan,tier\n")
	for i := 0; i < rows; i++ {
		plan := rng.Intn(3)
		tier := plan
		if rng.Float64() >= 0.85 {
			tier = rng.Intn(3)
		}
		fmt.Fprintf(&buf, "%s,%s,%s\n", regions[rng.Intn(4)], plans[plan], tiers[tier])
	}
	return importCSVFixture(t, buf.String(), rows)
}

// planTierMatch is the share of rows whose tier is its plan's own tier
// (basic→bronze, plus→silver, max→gold), read off a conditional capture
// of data.
func planTierMatch(t *testing.T, data []byte) float64 {
	t.Helper()
	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{IncludeConditional: true, Seed: 3})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	own := map[string]string{"basic": "bronze", "plus": "silver", "max": "gold"}
	for _, p := range prof.Conditional.CategoricalPairs {
		if p.A != "plan" || p.B != "tier" {
			continue
		}
		var match, total int
		for _, c := range p.Cells {
			total += c.Count
			if own[c.AValue] == c.BValue {
				match += c.Count
			}
		}
		return float64(match) / float64(total)
	}
	t.Fatal("no plan x tier pair captured")
	return 0
}

// TestPairArbitration_StrongestAssociationKeepsContestedTarget is the
// FR-13 acceptance: on a cohort where first-declared picks the noise
// pair, the default keeps plan→tier and the twin reproduces it, and
// `"pair_arbitration": "declared"` still gives the author the old rule.
func TestPairArbitration_StrongestAssociationKeepsContestedTarget(t *testing.T) {
	t.Parallel()

	source := tierFixture(t, 3000, 5)
	prof, err := synth.ProfileBytes(source, synth.ProfileOptions{IncludeConditional: true, Seed: 7})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	spec, warnings := synth.SpecFromProfile(prof, 3000)

	kept := func(s *synth.Spec) []string {
		cat, _, _, _, _, _ := synth.ResolveConflicts(s)
		var out []string
		for _, p := range cat {
			out = append(out, p.A+"->"+p.B)
		}
		return out
	}
	if got := strings.Join(kept(spec), ","); !strings.Contains(got, "plan->tier") || strings.Contains(got, "region->tier") {
		t.Fatalf("strength arbitration kept %s, want plan->tier and not region->tier", got)
	}
	if !strings.Contains(strings.Join(warnings, "\n"),
		`field "tier" is already claimed by categorical pair (plan -> tier); dropping categorical pair (region -> tier)`) {
		t.Errorf("the dropped noise pair's warning must name the winner; got:\n  %s", strings.Join(warnings, "\n  "))
	}

	srcMatch := planTierMatch(t, source)
	twin, _, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if got := planTierMatch(t, twin); got < srcMatch-0.05 {
		t.Errorf("twin plan/tier agreement %.3f, source %.3f: the strongest pair did not drive the twin", got, srcMatch)
	}

	declared := *spec
	declared.PairArbitration = synth.PairArbitrationDeclared
	if got := strings.Join(kept(&declared), ","); !strings.Contains(got, "region->tier") || strings.Contains(got, "plan->tier") {
		t.Fatalf("declared arbitration kept %s, want the first-declared region->tier", got)
	}
	old, _, err := synth.SynthBytes(&declared, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes (declared): %v", err)
	}
	if got := planTierMatch(t, old); got > 0.5 {
		t.Errorf("declared twin plan/tier agreement %.3f: the author's first-declared choice was not honoured", got)
	}
}

// numericArbitrationSpec has two categoricals bidding for one numeric:
// `noise` (declared first) whose levels share a mean, and `driver`
// whose levels sit two standard deviations apart.
func numericArbitrationSpec(withCounts bool) *synth.Spec {
	cat := func(name string, values ...any) synth.FieldSpec {
		weights := make([]any, len(values))
		for i := range weights {
			weights[i] = 1.0
		}
		return synth.FieldSpec{
			Name: name, Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
			Params: map[string]any{"values": values, "weights": weights},
		}
	}
	// Without counts, only ONE driver category loses its n: the pair's
	// strength is then unknown, and must not be computed from the rest.
	missing := 500
	if !withCounts {
		missing = 0
	}
	return &synth.Spec{
		RowCount: 50,
		Fields: []synth.FieldSpec{
			cat("noise", "a", "b"), cat("driver", "a", "b", "c"),
			{Name: "y", Type: "f64", Distribution: synth.DistNormal, Params: map[string]any{"mean": 1.0, "std": 1.4}},
		},
		CategoricalNumericPairs: []synth.CategoricalNumericPairSpec{
			{A: "noise", B: "y", Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "a", Mean: 1, Std: 1.4, N: 500}, {Category: "b", Mean: 1.02, Std: 1.4, N: 500},
			}},
			{A: "driver", B: "y", Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "a", Mean: 0, Std: 1, N: 500}, {Category: "b", Mean: 2, Std: 1, N: missing},
				{Category: "c", Mean: 4, Std: 1, N: 500},
			}},
		},
	}
}

// TestPairArbitration_NumericTargetByAdjustedEtaSquared covers the
// numeric half of FR-13, and the fallback when strength is unmeasurable.
func TestPairArbitration_NumericTargetByAdjustedEtaSquared(t *testing.T) {
	t.Parallel()

	winner := func(s *synth.Spec) string {
		_, catNum, _, _, _, _ := synth.ResolveConflicts(s)
		if len(catNum) != 1 {
			t.Fatalf("want exactly one surviving categorical-numeric pair, got %+v", catNum)
		}
		return catNum[0].A
	}
	if got := winner(numericArbitrationSpec(true)); got != "driver" {
		t.Errorf("strength arbitration kept %s->y, want driver->y", got)
	}
	declared := numericArbitrationSpec(true)
	declared.PairArbitration = synth.PairArbitrationDeclared
	if got := winner(declared); got != "noise" {
		t.Errorf("declared arbitration kept %s->y, want the first-declared noise->y", got)
	}
	// A hand-authored pair without per-category counts has no measured
	// strength; the target falls back to first-declared, never a guess.
	if got := winner(numericArbitrationSpec(false)); got != "noise" {
		t.Errorf("uncounted pairs kept %s->y, want the first-declared noise->y", got)
	}
}

// TestPairArbitration_SetOptionTargetAcrossKinds checks the arbitration
// spans pair kinds: a set option named by a weak set-set pair (declared
// in the earlier stage) and a strong set-categorical pair goes to the
// strong one, unless the author asks for declared order.
func TestPairArbitration_SetOptionTargetAcrossKinds(t *testing.T) {
	t.Parallel()

	setField := func(name string) synth.FieldSpec {
		return synth.FieldSpec{
			Name: name, Type: "set_u8", Distribution: synth.DistSetBernoulli,
			Params: map[string]any{"options": []any{"x", "y"}, "frequencies": []any{0.5, 0.5}},
		}
	}
	cell := func(a, b string, n int) synth.CategoricalPairCellSpec {
		return synth.CategoricalPairCellSpec{AValue: a, BValue: b, Count: n}
	}
	spec := &synth.Spec{
		RowCount: 20,
		Fields: []synth.FieldSpec{
			setField("s"),
			{Name: "c", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"p", "q"}, "weights": []any{0.5, 0.5}}},
		},
		SetSetPairs: []synth.SetSetPairSpec{{SetA: "s", OptionA: "x", SetB: "s", OptionB: "y", Cells: []synth.CategoricalPairCellSpec{
			cell("selected", "selected", 260), cell("selected", "not_selected", 240),
			cell("not_selected", "selected", 240), cell("not_selected", "not_selected", 260),
		}}},
		SetCategoricalPairs: []synth.SetCategoricalPairSpec{{Set: "s", Option: "y", Categorical: "c", Cells: []synth.CategoricalPairCellSpec{
			cell("selected", "p", 450), cell("selected", "q", 50),
			cell("not_selected", "p", 50), cell("not_selected", "q", 450),
		}}},
	}
	_, _, setCat, _, setSet, _ := synth.ResolveConflicts(spec)
	if len(setCat) != 1 || len(setSet) != 0 {
		t.Errorf("strength arbitration kept set-categorical %d / set-set %d, want 1 / 0", len(setCat), len(setSet))
	}
	spec.PairArbitration = synth.PairArbitrationDeclared
	_, _, setCat, _, setSet, _ = synth.ResolveConflicts(spec)
	if len(setCat) != 0 || len(setSet) != 1 {
		t.Errorf("declared arbitration kept set-categorical %d / set-set %d, want 0 / 1", len(setCat), len(setSet))
	}
}

// TestPairArbitration_Validation pins the two new spec refusals and the
// one new acceptance.
func TestPairArbitration_Validation(t *testing.T) {
	t.Parallel()

	refused := func(t *testing.T, s *synth.Spec, want string) {
		t.Helper()
		_, _, err := synth.SynthBytes(s, synth.Options{Seed: 1})
		var ce *perrors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perrors.SERVICE_VALIDATION || !strings.Contains(ce.Message, want) {
			t.Errorf("err = %v, want SERVICE_VALIDATION containing %q", err, want)
		}
	}
	bogus := numericArbitrationSpec(true)
	bogus.PairArbitration = "loudest"
	refused(t, bogus, "pair_arbitration")

	set := synth.FieldSpec{
		Name: "s", Type: "set_u8", Distribution: synth.DistSetBernoulli,
		Params: map[string]any{"options": []any{"x", "y"}, "frequencies": []any{0.5, 0.5}},
	}
	cells := []synth.CategoricalPairCellSpec{{AValue: "selected", BValue: "selected", Count: 5}}
	self := &synth.Spec{RowCount: 5, Fields: []synth.FieldSpec{set},
		SetSetPairs: []synth.SetSetPairSpec{{SetA: "s", OptionA: "x", SetB: "s", OptionB: "x", Cells: cells}}}
	refused(t, self, "option_a and option_b must differ")

	within := &synth.Spec{RowCount: 5, Fields: []synth.FieldSpec{set},
		SetSetPairs: []synth.SetSetPairSpec{{SetA: "s", OptionA: "x", SetB: "s", OptionB: "y", Cells: cells}}}
	if _, _, err := synth.SynthBytes(within, synth.Options{Seed: 1}); err != nil {
		t.Errorf("a within-set pair between two different options must be accepted: %v", err)
	}
}

// servicesCohort mirrors a customer cohort's product set: broadband is
// driven by region (strongly), tv is only ever taken WITH broadband, and
// phone is independent of both. Region drives broadband through a
// set-categorical pair, so the twin settles broadband in that stage and
// tv must be drawn after it.
func servicesCohort(t *testing.T) []byte {
	t.Helper()
	opts := []string{"broadband", "tv", "phone"}
	regions := []string{"north", "south"}
	return buildSetCategoricalCohort(t, opts, regions, func(local int, region string, i int) bool {
		bb := local%10 < 8
		if region == "south" {
			bb = local%10 < 2
		}
		switch opts[i] {
		case "broadband":
			return bb
		case "tv":
			return bb && local%3 == 0
		default:
			return local%7 < 3
		}
	}, 4000)
}

// tvWithoutBroadband counts the rows of a cohort selecting tv but not
// broadband, decoded record by record (servicesCohort's dictionary order:
// broadband bit 0, tv bit 1).
func tvWithoutBroadband(t *testing.T, data []byte) (bad, tv int) {
	t.Helper()
	for _, row := range decodeCoherenceRows(t, data) {
		mask := uint64(row.v["features"])
		if mask&2 != 0 {
			tv++
			if mask&1 == 0 {
				bad++
			}
		}
	}
	return bad, tv
}

// withinSetPair returns the captured within-set pair driving option.
func withinSetPair(prof *synth.Profile, option string) (synth.SetSetPairProfile, bool) {
	for _, p := range prof.Conditional.SetSetPairs {
		if p.SetA == "features" && p.SetB == "features" && p.OptionB == option {
			return p, true
		}
	}
	return synth.SetSetPairProfile{}, false
}

// TestWithinSet_ImpossibleCombinationStaysImpossible is the FR-14
// acceptance: a dependency between two options of ONE set field is
// captured, and the twin never selects tv without broadband — while a
// spec without the within-set pair (the old capture) does.
func TestWithinSet_ImpossibleCombinationStaysImpossible(t *testing.T) {
	t.Parallel()

	source := servicesCohort(t)
	if bad, tv := tvWithoutBroadband(t, source); bad != 0 || tv == 0 {
		t.Fatalf("source has %d of %d tv rows without broadband; the fixture is wrong", bad, tv)
	}
	prof, err := synth.ProfileBytes(source, synth.ProfileOptions{IncludeConditional: true, Seed: 7})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if p, ok := withinSetPair(prof, "tv"); !ok || p.OptionA != "broadband" {
		t.Fatalf("no within-set broadband -> tv pair captured: %+v", prof.Conditional.SetSetPairs)
	}
	if p, ok := withinSetPair(prof, "phone"); ok {
		t.Errorf("independent option phone captured a within-set pair %+v; the strength floor must keep it out", p)
	}

	spec, _ := synth.SpecFromProfile(prof, 4000)
	twin, _, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if bad, tv := tvWithoutBroadband(t, twin); bad != 0 || tv == 0 {
		t.Errorf("twin selects tv without broadband on %d of %d tv rows, want exactly 0", bad, tv)
	}

	without := *spec
	without.SetSetPairs = nil
	oldTwin, _, err := synth.SynthBytes(&without, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes (no within-set pair): %v", err)
	}
	if bad, _ := tvWithoutBroadband(t, oldTwin); bad == 0 {
		t.Error("without the within-set pair the twin also never selects tv alone; the test cannot tell the pair did anything")
	}
}
