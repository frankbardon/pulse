package synth_test

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// TestBuildCategoricalPairwise_ComputesTVDAgainstSourceCells is E3-S4's
// unit-level assertion for the categorical-categorical delta: given a
// real AugmentFromProfile output built from a genuinely dependent
// categorical-categorical source (via synthDependentCategoricalPair,
// the same fixture E3-S3's own acceptance test drives) and the
// CategoricalPairSpec that reconstruction targeted,
// BuildCategoricalPairwise's Delta (total variation distance) must land
// close to zero — the generated partition should closely reproduce the
// captured contingency table — N must count exactly the generated
// partition's co-occurring rows, and Error must be unset.
func TestBuildCategoricalPairwise_ComputesTVDAgainstSourceCells(t *testing.T) {
	const tolerance = 0.05
	// rowsPerA is deliberately kept small enough that the fixture's total
	// row count (rowsPerA * len(valuesA)) stays under
	// synth's conditionalJointCap (10000) — the joint-capture reservoir
	// used by --conditional's categorical-categorical capture is a
	// first-N cap, not a random reservoir, so a block-ordered fixture
	// (every "p" row before any "q" row, as synthDependentCategoricalPair
	// writes them) that exceeds the cap would have its LATER category's
	// rows truncated out of the captured contingency table entirely,
	// skewing the captured table's implied marginal away from the true
	// per-field marginal the reconstructed "a" distribution actually
	// draws from — a real, pre-existing capture-time artifact unrelated
	// to this delta metric's own correctness, and not what this test
	// means to exercise.
	const rowsPerA = 3000
	const newRows = 20000

	valuesA := []string{"p", "q"}
	valuesB := []string{"x", "y"}
	condProbs := map[string][]float64{
		"p": {0.9, 0.1},
		"q": {0.1, 0.9},
	}
	srcData := synthDependentCategoricalPair(t, rowsPerA, 41, valuesA, valuesB, condProbs)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prof, err := synth.ProfileBytes(srcData, synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.CategoricalPairs) != 1 {
		t.Fatalf("expected exactly one captured categorical pair, got Conditional=%+v", prof.Conditional)
	}

	spec := synth.SpecFromProfile(prof, newRows)
	if len(spec.CategoricalPairs) != 1 {
		t.Fatalf("expected SpecFromProfile to populate one categorical pair, got %d", len(spec.CategoricalPairs))
	}

	res, err := synth.AugmentFromProfile(fs, spec, "/source.pulse", "/augmented.pulse", synth.Options{Seed: 42})
	if err != nil {
		t.Fatalf("augment: %v", err)
	}
	data, err := afero.ReadFile(fs, res.OutputPath)
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	schema, records := splitCohortForPairwiseTest(t, data)

	report := &synth.FidelityReport{}
	synth.BuildCategoricalPairwise(report, schema, records, spec.CategoricalPairs)

	if len(report.CategoricalPairwise) != 1 {
		t.Fatalf("len(CategoricalPairwise) = %d, want 1", len(report.CategoricalPairwise))
	}
	pair := report.CategoricalPairwise[0]
	if pair.Error != "" {
		t.Fatalf("unexpected Error: %q", pair.Error)
	}
	if pair.A != "a" || pair.B != "b" {
		t.Errorf("A/B = %s/%s, want a/b", pair.A, pair.B)
	}
	if pair.N != newRows {
		t.Errorf("N = %d, want %d (no nulls in this fixture's synthetic partition)", pair.N, newRows)
	}
	if pair.Delta > tolerance {
		t.Fatalf("Delta = %.4f — outside tolerance %.2f (source contingency should be closely reproduced)", pair.Delta, tolerance)
	}
}

// TestBuildCategoricalPairwise_ErrorWhenNoCoOccurrence mirrors
// BuildPairwise's own degenerate-pair contract: pairing a real
// categorical field against the physical _synthetic column (packed_bool,
// no dictionary) can never produce a co-occurring observation, so the
// entry must carry an Error rather than a fabricated Delta — the same
// non-fatal per-entry discipline every Fidelity section follows.
func TestBuildCategoricalPairwise_ErrorWhenNoCoOccurrence(t *testing.T) {
	valuesA := []string{"p", "q"}
	valuesB := []string{"x", "y"}
	condProbs := map[string][]float64{
		"p": {0.9, 0.1},
		"q": {0.1, 0.9},
	}
	srcData := synthDependentCategoricalPair(t, 100, 43, valuesA, valuesB, condProbs)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prof, err := synth.ProfileBytes(srcData, synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	spec := synth.SpecFromProfile(prof, 200)

	res, err := synth.AugmentFromProfile(fs, spec, "/source.pulse", "/augmented.pulse", synth.Options{Seed: 44})
	if err != nil {
		t.Fatalf("augment: %v", err)
	}
	data, err := afero.ReadFile(fs, res.OutputPath)
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	schema, records := splitCohortForPairwiseTest(t, data)

	report := &synth.FidelityReport{}
	degenerate := []synth.CategoricalPairSpec{{
		A: "a", B: synth.SyntheticFieldName,
		Cells: []synth.CategoricalPairCellSpec{{AValue: "p", BValue: "true", Count: 10}},
	}}
	synth.BuildCategoricalPairwise(report, schema, records, degenerate)

	if len(report.CategoricalPairwise) != 1 {
		t.Fatalf("len(CategoricalPairwise) = %d, want 1", len(report.CategoricalPairwise))
	}
	pair := report.CategoricalPairwise[0]
	if pair.Error == "" {
		t.Fatal("expected an Error for a pair against a non-dictionary field")
	}
	if pair.Delta != 0 || pair.N != 0 {
		t.Errorf("Delta/N = %v/%v, want zero values alongside Error", pair.Delta, pair.N)
	}
}

// TestBuildCategoricalNumericPairwise_ComputesMeanStdDeltaAgainstSource
// is E3-S4's unit-level assertion for the categorical-numeric delta:
// given a real AugmentFromProfile output built from
// synthCategoricalNumericPair's genuinely dependent fixture (the same
// one E3-S3's own acceptance test drives) and the
// CategoricalNumericPairSpec that reconstruction targeted, each
// category's realized mean/std must land close to the captured
// mean/std, Error must be unset, and N must be positive.
func TestBuildCategoricalNumericPairwise_ComputesMeanStdDeltaAgainstSource(t *testing.T) {
	const meanTolerance = 2.0
	const stdTolerance = 1.0
	const rowsPerCategory = 6000
	const newRows = 20000

	srcData, truth := synthCategoricalNumericPair(t, rowsPerCategory, 61, []string{"east", "west"}, []float64{80.0, 20.0}, 5.0)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prof, err := synth.ProfileBytes(srcData, synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.CategoricalNumericPairs) != 1 {
		t.Fatalf("expected exactly one captured categorical-numeric pair, got Conditional=%+v", prof.Conditional)
	}

	spec := synth.SpecFromProfile(prof, newRows)
	if len(spec.CategoricalNumericPairs) != 1 {
		t.Fatalf("expected SpecFromProfile to populate one categorical-numeric pair, got %d", len(spec.CategoricalNumericPairs))
	}

	res, err := synth.AugmentFromProfile(fs, spec, "/source.pulse", "/augmented.pulse", synth.Options{Seed: 62})
	if err != nil {
		t.Fatalf("augment: %v", err)
	}
	data, err := afero.ReadFile(fs, res.OutputPath)
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	schema, records := splitCohortForPairwiseTest(t, data)

	report := &synth.FidelityReport{}
	synth.BuildCategoricalNumericPairwise(report, schema, records, spec.CategoricalNumericPairs)

	if len(report.CategoricalNumericPairwise) != 1 {
		t.Fatalf("len(CategoricalNumericPairwise) = %d, want 1", len(report.CategoricalNumericPairwise))
	}
	pair := report.CategoricalNumericPairwise[0]
	if pair.A != "region" || pair.B != "score" {
		t.Errorf("A/B = %s/%s, want region/score", pair.A, pair.B)
	}
	if len(pair.Categories) != 2 {
		t.Fatalf("len(Categories) = %d, want 2 (east, west)", len(pair.Categories))
	}
	for _, cat := range pair.Categories {
		if cat.Error != "" {
			t.Fatalf("category %s: unexpected Error: %q", cat.Category, cat.Error)
		}
		if cat.N == 0 {
			t.Fatalf("category %s: N = 0, want > 0", cat.Category)
		}
		if math.Abs(cat.MeanDelta) > meanTolerance {
			t.Errorf("category %s: MeanDelta = %.4f, want <= %.2f (SourceMean=%.4f SyntheticMean=%.4f)",
				cat.Category, cat.MeanDelta, meanTolerance, cat.SourceMean, cat.SyntheticMean)
		}
		if cat.StdDelta > stdTolerance {
			t.Errorf("category %s: StdDelta = %.4f, want <= %.2f (SourceStd=%.4f SyntheticStd=%.4f)",
				cat.Category, cat.StdDelta, stdTolerance, cat.SourceStd, cat.SyntheticStd)
		}
		if want, ok := truth[cat.Category]; ok {
			if math.Abs(cat.SourceMean-want) > 1e-6 {
				t.Errorf("category %s: SourceMean = %.6f, want %.6f (the profile's own captured mean)", cat.Category, cat.SourceMean, want)
			}
		}
	}
}

// TestBuildCategoricalNumericPairwise_ErrorWhenCategoryUnobserved
// asserts a category the synthetic partition never realizes (because
// it never appeared in the source's own marginal, so the reconstructed
// weighted_categorical sampler never draws it) gets an Error entry
// rather than a fabricated zero-valued delta — mirroring every other
// Fidelity section's non-fatal per-entry contract.
func TestBuildCategoricalNumericPairwise_ErrorWhenCategoryUnobserved(t *testing.T) {
	srcData, _ := synthCategoricalNumericPair(t, 2000, 63, []string{"east", "west"}, []float64{80.0, 20.0}, 5.0)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prof, err := synth.ProfileBytes(srcData, synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	spec := synth.SpecFromProfile(prof, 4000)
	if len(spec.CategoricalNumericPairs) != 1 {
		t.Fatalf("expected one categorical-numeric pair, got %d", len(spec.CategoricalNumericPairs))
	}
	// Inject a category the source never captured, and therefore the
	// reconstructed categorical marginal never draws.
	spec.CategoricalNumericPairs[0].Categories = append(spec.CategoricalNumericPairs[0].Categories,
		synth.CategoricalNumericCategorySpec{Category: "north", Mean: 50.0, Std: 5.0})

	res, err := synth.AugmentFromProfile(fs, spec, "/source.pulse", "/augmented.pulse", synth.Options{Seed: 64})
	if err != nil {
		t.Fatalf("augment: %v", err)
	}
	data, err := afero.ReadFile(fs, res.OutputPath)
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	schema, records := splitCohortForPairwiseTest(t, data)

	report := &synth.FidelityReport{}
	synth.BuildCategoricalNumericPairwise(report, schema, records, spec.CategoricalNumericPairs)

	if len(report.CategoricalNumericPairwise) != 1 {
		t.Fatalf("len(CategoricalNumericPairwise) = %d, want 1", len(report.CategoricalNumericPairwise))
	}
	var foundNorth bool
	for _, cat := range report.CategoricalNumericPairwise[0].Categories {
		if cat.Category != "north" {
			continue
		}
		foundNorth = true
		if cat.Error == "" {
			t.Error("expected an Error for the never-observed 'north' category")
		}
		if cat.SyntheticMean != 0 || cat.N != 0 {
			t.Errorf("SyntheticMean/N = %v/%v, want zero values alongside Error", cat.SyntheticMean, cat.N)
		}
	}
	if !foundNorth {
		t.Fatal("expected a 'north' category entry in the report")
	}
}
