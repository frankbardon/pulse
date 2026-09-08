package synth_test

import (
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// meanWhere returns the mean of values[i] for every i where keys[i] ==
// want, plus the matching count.
func meanWhere(values []float64, keys []string, want string) (mean float64, n int) {
	var sum float64
	for i, k := range keys {
		if k != want {
			continue
		}
		sum += values[i]
		n++
	}
	if n == 0 {
		return 0, 0
	}
	return sum / float64(n), n
}

// setRateWhere returns the fraction of rows where keys[i] == want and
// opt is present in labels[i]'s selection, plus the matching count.
func setRateWhere(labels [][]string, keys []string, want, opt string) (rate float64, n int) {
	sel := 0
	for i, k := range keys {
		if k != want {
			continue
		}
		n++
		if hasLabel(labels[i], opt) {
			sel++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return float64(sel) / float64(n), n
}

// warningContainsAll reports whether some warning string in warnings
// contains every one of substrs.
func warningContainsAll(warnings []string, substrs ...string) bool {
	for _, w := range warnings {
		ok := true
		for _, s := range substrs {
			if !strings.Contains(w, s) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// TestSynth_ConflictWarns_CategoricalNumericMultiTarget is E6-S1's first
// confirmed conflict instance: two DIFFERENT categorical fields each
// declare a CategoricalNumericPairSpec conditioning the SAME numeric
// target field. Before this story, drawRow's fixed stage order meant
// whichever pair happened to run last in catNumPairs silently won with
// no trace of the other. resolveConflicts now keeps the FIRST-declared
// pair (region1 -> score) and drops the second (region2 -> score),
// reporting exactly why.
func TestSynth_ConflictWarns_CategoricalNumericMultiTarget(t *testing.T) {
	const rowCount = 20000
	spec := &synth.Spec{
		RowCount: rowCount,
		Fields: []synth.FieldSpec{
			{Name: "region1", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west"}, "weights": []any{0.5, 0.5}}},
			{Name: "region2", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"north", "south"}, "weights": []any{0.5, 0.5}}},
			{Name: "score", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 0.0, "std": 1.0}},
		},
		CategoricalNumericPairs: []synth.CategoricalNumericPairSpec{
			{A: "region1", B: "score", Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "east", Mean: 100, Std: 1},
				{Category: "west", Mean: -100, Std: 1},
			}},
			{A: "region2", B: "score", Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "north", Mean: 500, Std: 1},
				{Category: "south", Mean: -500, Std: 1},
			}},
		},
	}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 101})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}

	score := readF64Field(t, data, "score")
	region1 := readCategoricalField(t, data, "region1")
	region2 := readCategoricalField(t, data, "region2")

	const tol = 5.0
	east, nEast := meanWhere(score, region1, "east")
	west, nWest := meanWhere(score, region1, "west")
	if nEast == 0 || nWest == 0 {
		t.Fatalf("expected rows in both region1 buckets, got east=%d west=%d", nEast, nWest)
	}
	if math.Abs(east-100) > tol {
		t.Errorf("kept relationship: mean(score|region1=east) = %.4f, want ~100 (region1->score must win)", east)
	}
	if math.Abs(west-(-100)) > tol {
		t.Errorf("kept relationship: mean(score|region1=west) = %.4f, want ~-100 (region1->score must win)", west)
	}

	// The dropped relationship's conditioning must NOT show through:
	// grouping by region2 (whose pair was excluded) should look roughly
	// independent, not separated by ~1000.
	north, nNorth := meanWhere(score, region2, "north")
	south, nSouth := meanWhere(score, region2, "south")
	if nNorth == 0 || nSouth == 0 {
		t.Fatalf("expected rows in both region2 buckets, got north=%d south=%d", nNorth, nSouth)
	}
	if diff := math.Abs(north - south); diff > 40 {
		t.Errorf("dropped relationship leaked through: mean(score|region2=north)=%.4f vs south=%.4f differ by %.4f, want small (region2->score must have been excluded)",
			north, south, diff)
	}

	if !warningContainsAll(res.Warnings, `field "score"`, "region1 -> score", "region2 -> score") {
		t.Fatalf("expected a warning naming field %q and both the kept (region1->score) and dropped (region2->score) relationships, got warnings=%v", "score", res.Warnings)
	}
}

// TestSynth_ConflictWarns_SetOptionGranularity is E6-S1's second
// confirmed conflict instance, and simultaneously the granularity proof
// the story calls out as the part most likely to false-positive: a
// set_* field "features" has two options, "a" and "b". Two DIFFERENT
// SetCategoricalPairSpec entries both target option "a" (a genuine
// conflict — the second must be dropped with a warning), while a THIRD
// entry targets option "b" (a DIFFERENT target on the SAME set_* field —
// must NOT be treated as conflicting with anything, and must survive).
func TestSynth_ConflictWarns_SetOptionGranularity(t *testing.T) {
	const rowCount = 20000
	spec := &synth.Spec{
		RowCount: rowCount,
		Fields: []synth.FieldSpec{
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"us", "eu"}, "weights": []any{0.5, 0.5}}},
			{Name: "region2", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"ca", "mx"}, "weights": []any{0.5, 0.5}}},
			{Name: "features", Type: "set_u8", Distribution: synth.DistSetBernoulli,
				Params: map[string]any{"options": []any{"a", "b"}, "frequencies": []any{0.5, 0.5}}},
		},
		SetCategoricalPairs: []synth.SetCategoricalPairSpec{
			{ // kept: claims (features, a)
				Set: "features", Option: "a", Categorical: "region",
				Cells: []synth.CategoricalPairCellSpec{
					{AValue: "selected", BValue: "us", Count: 900},
					{AValue: "not_selected", BValue: "us", Count: 100},
					{AValue: "selected", BValue: "eu", Count: 100},
					{AValue: "not_selected", BValue: "eu", Count: 900},
				},
			},
			{ // dropped: (features, a) already claimed above
				Set: "features", Option: "a", Categorical: "region2",
				Cells: []synth.CategoricalPairCellSpec{
					{AValue: "selected", BValue: "ca", Count: 900},
					{AValue: "not_selected", BValue: "ca", Count: 100},
					{AValue: "selected", BValue: "mx", Count: 100},
					{AValue: "not_selected", BValue: "mx", Count: 900},
				},
			},
			{ // kept: (features, b) is a DIFFERENT target — not a conflict
				Set: "features", Option: "b", Categorical: "region",
				Cells: []synth.CategoricalPairCellSpec{
					{AValue: "selected", BValue: "us", Count: 200},
					{AValue: "not_selected", BValue: "us", Count: 800},
					{AValue: "selected", BValue: "eu", Count: 800},
					{AValue: "not_selected", BValue: "eu", Count: 200},
				},
			},
		},
	}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 103})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}

	_, labels, _ := readSetFieldRows(t, data, "features")
	region := readCategoricalField(t, data, "region")
	region2 := readCategoricalField(t, data, "region2")

	const tol = 0.03
	aGivenUS, nUS := setRateWhere(labels, region, "us", "a")
	aGivenEU, nEU := setRateWhere(labels, region, "eu", "a")
	if nUS == 0 || nEU == 0 {
		t.Fatalf("expected rows for both region buckets, got us=%d eu=%d", nUS, nEU)
	}
	if math.Abs(aGivenUS-0.9) > tol {
		t.Errorf("kept relationship: P(a selected|region=us) = %.4f, want ~0.90", aGivenUS)
	}
	if math.Abs(aGivenEU-0.1) > tol {
		t.Errorf("kept relationship: P(a selected|region=eu) = %.4f, want ~0.10", aGivenEU)
	}

	// The dropped (features.a <- region2) pairing must not show through:
	// option a's selection, grouped by region2, should look independent
	// of region2 (nowhere near the 0.9/0.1 split it declared).
	aGivenCA, nCA := setRateWhere(labels, region2, "ca", "a")
	aGivenMX, nMX := setRateWhere(labels, region2, "mx", "a")
	if nCA == 0 || nMX == 0 {
		t.Fatalf("expected rows for both region2 buckets, got ca=%d mx=%d", nCA, nMX)
	}
	if diff := math.Abs(aGivenCA - aGivenMX); diff > 0.1 {
		t.Errorf("dropped relationship leaked through: P(a|ca)=%.4f vs P(a|mx)=%.4f differ by %.4f, want small (features.a<-region2 must have been excluded)",
			aGivenCA, aGivenMX, diff)
	}

	// Option "b" is a DIFFERENT target on the same set_* field and must
	// have survived untouched by the option-"a" conflict above.
	bGivenUS, nUS2 := setRateWhere(labels, region, "us", "b")
	bGivenEU, nEU2 := setRateWhere(labels, region, "eu", "b")
	if nUS2 == 0 || nEU2 == 0 {
		t.Fatalf("expected rows for both region buckets (option b), got us=%d eu=%d", nUS2, nEU2)
	}
	if math.Abs(bGivenUS-0.2) > tol {
		t.Errorf("different-option relationship: P(b selected|region=us) = %.4f, want ~0.20 (not flagged as conflicting with option a)", bGivenUS)
	}
	if math.Abs(bGivenEU-0.8) > tol {
		t.Errorf("different-option relationship: P(b selected|region=eu) = %.4f, want ~0.80 (not flagged as conflicting with option a)", bGivenEU)
	}

	if !warningContainsAll(res.Warnings, `field "features" option "a"`) {
		t.Fatalf("expected a warning naming field %q option %q, got warnings=%v", "features", "a", res.Warnings)
	}
	// Exactly one conflict — option b must never have been reported.
	conflictCount := 0
	for _, w := range res.Warnings {
		if strings.Contains(w, "conditional relationship conflict") {
			conflictCount++
		}
	}
	if conflictCount != 1 {
		t.Fatalf("expected exactly 1 conflict warning, got %d: %v", conflictCount, res.Warnings)
	}
}

// TestSynth_ConflictWarns_CorrelationVsCategoricalNumeric is E6-S1's
// third confirmed conflict instance: a field ("c") is BOTH the target of
// a categorical-numeric pair AND a participant in Spec.Correlations.
// catNumPairs runs before corr in drawRow's stage order, so the
// categorical-numeric conditioning must win, "c" must be excluded from
// the correlation (not silently overwritten by it at generation time,
// the former bug), and the paired field "d" must fall back to
// independent generation since its only correlation partner was
// excluded.
func TestSynth_ConflictWarns_CorrelationVsCategoricalNumeric(t *testing.T) {
	const rowCount = 20000
	spec := &synth.Spec{
		RowCount: rowCount,
		Fields: []synth.FieldSpec{
			{Name: "z", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"hi", "lo"}, "weights": []any{0.5, 0.5}}},
			{Name: "c", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 0.0, "std": 1.0}},
			{Name: "d", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 0.0, "std": 1.0}},
		},
		CategoricalNumericPairs: []synth.CategoricalNumericPairSpec{
			{A: "z", B: "c", Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "hi", Mean: 1000, Std: 1},
				{Category: "lo", Mean: -1000, Std: 1},
			}},
		},
		Correlations: []synth.CorrelationSpec{{A: "c", B: "d", Correlation: 0.8}},
	}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 107})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}

	c := readF64Field(t, data, "c")
	d := readF64Field(t, data, "d")
	z := readCategoricalField(t, data, "z")

	const tol = 5.0
	hi, nHi := meanWhere(c, z, "hi")
	lo, nLo := meanWhere(c, z, "lo")
	if nHi == 0 || nLo == 0 {
		t.Fatalf("expected rows for both z buckets, got hi=%d lo=%d", nHi, nLo)
	}
	if math.Abs(hi-1000) > tol {
		t.Errorf("kept relationship: mean(c|z=hi) = %.4f, want ~1000 (categorical-numeric pairing must win over correlation)", hi)
	}
	if math.Abs(lo-(-1000)) > tol {
		t.Errorf("kept relationship: mean(c|z=lo) = %.4f, want ~-1000 (categorical-numeric pairing must win over correlation)", lo)
	}

	// The excluded correlation must not have imposed anything on c/d:
	// with c overwhelmingly bimodal at +-1000 (driven by z) and d
	// drawn independently, the realized Pearson correlation must be
	// nowhere near the requested 0.8.
	rho := pearsonOf(c, d)
	if math.Abs(rho) > 0.15 {
		t.Errorf("dropped relationship leaked through: pearson(c,d) = %.4f, want near 0 (correlation must have been excluded once c was claimed by categorical-numeric pairing)", rho)
	}

	if !warningContainsAll(res.Warnings, `field "c"`, "categorical-numeric pair (z -> c)", "pairwise correlation") {
		t.Fatalf("expected a warning naming field %q, the kept categorical-numeric pair, and the excluded correlation, got warnings=%v", "c", res.Warnings)
	}
}

// TestSynth_ConflictWarns_PartialExclusionFromMultiFieldCorrelation is
// E6-S1's fourth confirmed conflict instance and the story's most
// delicate acceptance bar: Spec.Correlations is treated as ONE combined
// claimant across all its participants, so when field "a" (one of three
// mutually-correlated fields) is claimed by an earlier categorical-
// numeric pairing, the correlation must NOT be dropped entirely — only
// "a"'s row/column is excluded from the rebuilt Cholesky matrix, and the
// remaining pair ("b","c") must still correlate at its requested rho.
func TestSynth_ConflictWarns_PartialExclusionFromMultiFieldCorrelation(t *testing.T) {
	const rowCount = 20000
	const rhoBC = 0.7
	spec := &synth.Spec{
		RowCount: rowCount,
		Fields: []synth.FieldSpec{
			{Name: "z", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"hi", "lo"}, "weights": []any{0.5, 0.5}}},
			{Name: "a", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 0.0, "std": 1.0}},
			{Name: "b", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 50.0, "std": 10.0}},
			{Name: "c", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 20.0, "std": 5.0}},
		},
		CategoricalNumericPairs: []synth.CategoricalNumericPairSpec{
			{A: "z", B: "a", Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "hi", Mean: 100, Std: 5},
				{Category: "lo", Mean: -100, Std: 5},
			}},
		},
		Correlations: []synth.CorrelationSpec{
			{A: "a", B: "b", Correlation: 0.6},
			{A: "a", B: "c", Correlation: 0.6},
			{A: "b", B: "c", Correlation: rhoBC},
		},
	}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 109})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}

	a := readF64Field(t, data, "a")
	b := readF64Field(t, data, "b")
	c := readF64Field(t, data, "c")
	z := readCategoricalField(t, data, "z")

	// "a" kept its categorical-numeric conditioning.
	const tolMean = 5.0
	hi, nHi := meanWhere(a, z, "hi")
	lo, nLo := meanWhere(a, z, "lo")
	if nHi == 0 || nLo == 0 {
		t.Fatalf("expected rows for both z buckets, got hi=%d lo=%d", nHi, nLo)
	}
	if math.Abs(hi-100) > tolMean {
		t.Errorf("kept relationship: mean(a|z=hi) = %.4f, want ~100", hi)
	}
	if math.Abs(lo-(-100)) > tolMean {
		t.Errorf("kept relationship: mean(a|z=lo) = %.4f, want ~-100", lo)
	}

	// The OTHER TWO fields (b, c) must still correlate at the requested
	// rho — the non-negotiable partial-exclusion acceptance bar.
	const tol = 0.03
	rhoBCActual := pearsonOf(b, c)
	if math.Abs(rhoBCActual-rhoBC) > tol {
		t.Fatalf("pearson(b,c) = %.4f, want within %.2f of requested %.2f — partial exclusion must not disturb the surviving pair", rhoBCActual, tol, rhoBC)
	}

	// a-b and a-c must NOT show the requested 0.6 correlation — a was
	// excluded from the matrix entirely (it never participates in the
	// Gaussian-copula draw at all once claimed).
	rhoAB := pearsonOf(a, b)
	rhoAC := pearsonOf(a, c)
	if math.Abs(rhoAB) > 0.15 {
		t.Errorf("pearson(a,b) = %.4f, want near 0 (a must be excluded from the correlation matrix)", rhoAB)
	}
	if math.Abs(rhoAC) > 0.15 {
		t.Errorf("pearson(a,c) = %.4f, want near 0 (a must be excluded from the correlation matrix)", rhoAC)
	}

	if !warningContainsAll(res.Warnings, `field "a"`, "categorical-numeric pair (z -> a)", "pairwise correlation") {
		t.Fatalf("expected a warning naming field %q, the kept categorical-numeric pair, and the partial correlation exclusion, got warnings=%v", "a", res.Warnings)
	}
}

// TestSynth_ConflictWarns_ShapeFitExcludedFromCorrelation locks in that
// addCorrelation's former DistMixture special case (synth/profile.go)
// now routes through the SAME resolveConflicts mechanism: a field whose
// distribution is DistMixture (a --fit-shape reconstruction) is
// pre-claimed under "captured shape" before any correlation stage runs,
// so naming it in Spec.Correlations excludes it with an explicit
// warning instead of either silently vanishing (the old profile.go
// behavior) or hard-failing generation via fieldMoments (the old
// hand-authored-spec behavior, since fieldMoments has no closed-form
// moments for a mixture).
func TestSynth_ConflictWarns_ShapeFitExcludedFromCorrelation(t *testing.T) {
	const rowCount = 8000
	const mean1, std1 = -10.0, 1.5
	const mean2, std2 = 10.0, 1.5
	spec := &synth.Spec{
		RowCount: rowCount,
		Fields: []synth.FieldSpec{
			{Name: "shaped", Type: "f64", Distribution: synth.DistMixture,
				Params: map[string]any{
					"means": []any{mean1, mean2}, "stds": []any{std1, std2}, "weights": []any{0.5, 0.5},
				}},
			{Name: "other", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 0.0, "std": 1.0}},
		},
		Correlations: []synth.CorrelationSpec{{A: "shaped", B: "other", Correlation: 0.8}},
	}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 113})
	if err != nil {
		t.Fatalf("synth: %v (must warn-and-exclude, not hard-fail)", err)
	}

	values := readF64Field(t, data, "shaped")
	assertBimodal(t, values, mean1, std1, mean2, std2)

	if !warningContainsAll(res.Warnings, `field "shaped"`, "captured shape (--fit-shape)") {
		t.Fatalf("expected a warning naming field %q excluded by the captured-shape pre-claim, got warnings=%v", "shaped", res.Warnings)
	}
}

// TestSynth_NoConflicts_NoWarnings is a direct regression companion to
// the existing conditional/correlation fixtures: a spec with exactly one
// relationship per target produces zero warnings — resolveConflicts must
// be purely additive for the non-conflicting case.
func TestSynth_NoConflicts_NoWarnings(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 500,
		Fields: []synth.FieldSpec{
			{Name: "a", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 50.0, "std": 10.0}},
			{Name: "b", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 20.0, "std": 5.0}},
		},
		Correlations: []synth.CorrelationSpec{{A: "a", B: "b", Correlation: 0.5}},
	}
	_, res, err := synth.SynthBytes(spec, synth.Options{Seed: 1})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("expected no warnings for a non-conflicting spec, got %v", res.Warnings)
	}
}
