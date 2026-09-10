package synth_test

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// TestAugmentFromProfile_ReconstructsCategoricalContingencyWithinTolerance
// is E3-S3's non-negotiable acceptance bar for the categorical-categorical
// pair kind: a full profile -> generate round-trip through the ACTUAL
// production path behind `synth from-profile` (AugmentFromProfile, reached
// via p.Synth with SourceCohort set) must reproduce the source's captured
// contingency table in the newly GENERATED partition — a genuinely
// dependent fixture (field "b" is drawn conditioned on field "a", not
// independently), not the independent fixture conditional_categorical_test.go
// uses for its own capture-only assertions.
func TestAugmentFromProfile_ReconstructsCategoricalContingencyWithinTolerance(t *testing.T) {
	const rowsPerA = 6000
	const newRows = 20000
	const tolerance = 0.03

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	valuesA := []string{"p", "q"}
	valuesB := []string{"x", "y"}
	condProbs := map[string][]float64{
		"p": {0.9, 0.1},
		"q": {0.1, 0.9},
	}
	srcData := synthDependentCategoricalPair(t, rowsPerA, 41, valuesA, valuesB, condProbs)
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{
		IncludeConditional: true,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.CategoricalPairs) != 1 {
		t.Fatalf("expected exactly one captured categorical pair, got Conditional=%+v", prof.Conditional)
	}

	spec, _ := synth.SpecFromProfile(prof, newRows)
	if len(spec.CategoricalPairs) != 1 {
		t.Fatalf("expected SpecFromProfile to populate one categorical pair from Conditional.CategoricalPairs, got %d", len(spec.CategoricalPairs))
	}

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse", pulse.SynthOptions{
		Seed:         99,
		SourceCohort: "/source.pulse",
	}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	augmented, err := afero.ReadFile(fs, "/augmented.pulse")
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}

	sourceRows := rowsPerA * len(valuesA)
	a := readCategoricalField(t, augmented, "a")
	b := readCategoricalField(t, augmented, "b")
	if len(a) != sourceRows+newRows {
		t.Fatalf("augmented row count = %d, want %d", len(a), sourceRows+newRows)
	}
	genA, genB := a[sourceRows:], b[sourceRows:]

	for _, av := range valuesA {
		rate, n := conditionalCategoryRate(genA, genB, av, "x")
		if n == 0 {
			t.Fatalf("no generated rows observed for a=%q", av)
		}
		want := condProbs[av][0]
		if math.Abs(rate-want) > tolerance {
			t.Errorf("generated P(b=x|a=%s) = %.4f (n=%d), want within %.2f of source's %.2f",
				av, rate, n, tolerance, want)
		}
	}
}

// TestAugmentFromProfile_ReconstructsCategoricalNumericMeanWithinTolerance
// is E3-S3's non-negotiable acceptance bar for the categorical-numeric
// pair kind: a full profile -> generate round-trip through
// AugmentFromProfile must reproduce the source's captured per-category
// numeric mean/std shift in the newly GENERATED partition.
func TestAugmentFromProfile_ReconstructsCategoricalNumericMeanWithinTolerance(t *testing.T) {
	const rowsPerCategory = 6000
	const newRows = 20000
	const tolerance = 2.0 // well under the fixture's ~60-unit east/west separation

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	srcData, truth := synthCategoricalNumericPair(t, rowsPerCategory, 61,
		[]string{"east", "west"}, []float64{80.0, 20.0}, 5.0)
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{
		IncludeConditional: true,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.CategoricalNumericPairs) != 1 {
		t.Fatalf("expected exactly one captured categorical-numeric pair, got Conditional=%+v", prof.Conditional)
	}

	spec, _ := synth.SpecFromProfile(prof, newRows)
	if len(spec.CategoricalNumericPairs) != 1 {
		t.Fatalf("expected SpecFromProfile to populate one categorical-numeric pair from Conditional.CategoricalNumericPairs, got %d", len(spec.CategoricalNumericPairs))
	}

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse", pulse.SynthOptions{
		Seed:         99,
		SourceCohort: "/source.pulse",
	}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	augmented, err := afero.ReadFile(fs, "/augmented.pulse")
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}

	sourceRows := rowsPerCategory * 2
	region := readCategoricalField(t, augmented, "region")
	score := readF64Field(t, augmented, "score")
	if len(region) != sourceRows+newRows {
		t.Fatalf("augmented row count = %d, want %d", len(region), sourceRows+newRows)
	}
	genRegion, genScore := region[sourceRows:], score[sourceRows:]

	for _, cat := range []string{"east", "west"} {
		mean, n := conditionalMean(genRegion, genScore, cat)
		if n == 0 {
			t.Fatalf("no generated rows observed for region=%q", cat)
		}
		if math.Abs(mean-truth[cat]) > tolerance {
			t.Errorf("generated mean(score|region=%s) = %.4f (n=%d), want within %.2f of source's %.4f",
				cat, mean, n, tolerance, truth[cat])
		}
	}
}

// TestAugmentFromProfile_WithoutCategoricalConditional_GeneratesIndependentMarginals
// is E3-S3's regression test: a source cohort with GENUINE
// categorical-categorical and categorical-numeric dependence, profiled
// WITHOUT --conditional, must generate a partition where both
// dependencies have vanished — independent-marginal sampling, exactly
// today's pre-E3-S3 behavior — because Conditional is nil and
// SpecFromProfile therefore populates neither CategoricalPairs nor
// CategoricalNumericPairs. This locks in FR-10/FR-11: no new generate-time
// flag activates the feature, and its absence from the profile document
// is what keeps behavior unchanged.
func TestAugmentFromProfile_WithoutCategoricalConditional_GeneratesIndependentMarginals(t *testing.T) {
	const rowsPerA = 6000
	const newRows = 20000
	// The source's own dependence is large (0.8 rate split, 60-unit mean
	// split) — independenceBound just needs to be comfortably smaller
	// than that gap while allowing for ordinary sampling noise at
	// n ~ 10000 per category.
	const independenceBound = 0.1
	const meanIndependenceBound = 5.0

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	srcData := synthDependentCategoricalAndNumeric(t, rowsPerA, 51)
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// Neither IncludeConditional nor IncludeCorrelations — the plain
	// default capture every pre-existing caller uses.
	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{
		IncludeStats: true,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional != nil {
		t.Fatal("expected Conditional to be nil when --conditional was not requested")
	}

	spec, _ := synth.SpecFromProfile(prof, newRows)
	if len(spec.CategoricalPairs) != 0 {
		t.Fatalf("expected no categorical pairs reconstructed from a profile with no conditional section, got %d", len(spec.CategoricalPairs))
	}
	if len(spec.CategoricalNumericPairs) != 0 {
		t.Fatalf("expected no categorical-numeric pairs reconstructed from a profile with no conditional section, got %d", len(spec.CategoricalNumericPairs))
	}

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse", pulse.SynthOptions{
		Seed:         99,
		SourceCohort: "/source.pulse",
	}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	augmented, err := afero.ReadFile(fs, "/augmented.pulse")
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}

	sourceRows := rowsPerA * 2
	a := readCategoricalField(t, augmented, "a")
	b := readCategoricalField(t, augmented, "b")
	c := readF64Field(t, augmented, "c")
	if len(a) != sourceRows+newRows {
		t.Fatalf("augmented row count = %d, want %d", len(a), sourceRows+newRows)
	}
	genA, genB, genC := a[sourceRows:], b[sourceRows:], c[sourceRows:]

	rateP, nP := conditionalCategoryRate(genA, genB, "p", "x")
	rateQ, nQ := conditionalCategoryRate(genA, genB, "q", "x")
	if nP == 0 || nQ == 0 {
		t.Fatalf("expected generated rows for both a=p (n=%d) and a=q (n=%d)", nP, nQ)
	}
	if diff := math.Abs(rateP - rateQ); diff > independenceBound {
		t.Errorf("generated P(b=x|a=p)=%.4f vs P(b=x|a=q)=%.4f differ by %.4f, want within %.2f (source's own gap is ~0.8 — this must have vanished)",
			rateP, rateQ, diff, independenceBound)
	}

	meanP, nMP := conditionalMean(genA, genC, "p")
	meanQ, nMQ := conditionalMean(genA, genC, "q")
	if nMP == 0 || nMQ == 0 {
		t.Fatalf("expected generated rows for both a=p (n=%d) and a=q (n=%d)", nMP, nMQ)
	}
	if diff := math.Abs(meanP - meanQ); diff > meanIndependenceBound {
		t.Errorf("generated mean(c|a=p)=%.4f vs mean(c|a=q)=%.4f differ by %.4f, want within %.2f (source's own gap is ~60 — this must have vanished)",
			meanP, meanQ, diff, meanIndependenceBound)
	}
}

// synthDependentCategoricalPair writes a small .pulse cohort with two
// categorical fields "a" and "b" where b is drawn CONDITIONED on a's
// value per condProbs[aValue] (a probability vector aligned with
// valuesB) — unlike synthCategoricalPair (conditional_categorical_test.go),
// which draws a and b independently and so captures a contingency table
// that is just the product of the two marginals. rowsPerA rows are
// written per value of valuesA, in block order (row order does not affect
// profile capture, which is a streaming per-row accumulation).
func synthDependentCategoricalPair(t *testing.T, rowsPerA int, seed int64, valuesA, valuesB []string, condProbs map[string][]float64) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var buf strings.Builder
	buf.WriteString("a,b\n")
	for _, av := range valuesA {
		probs, ok := condProbs[av]
		if !ok || len(probs) != len(valuesB) {
			t.Fatalf("condProbs[%q] must have %d entries, got %+v", av, len(valuesB), probs)
		}
		for r := 0; r < rowsPerA; r++ {
			x := rng.Float64()
			cum := 0.0
			bv := valuesB[len(valuesB)-1]
			for i, pr := range probs {
				cum += pr
				if x < cum {
					bv = valuesB[i]
					break
				}
			}
			fmt.Fprintf(&buf, "%s,%s\n", av, bv)
		}
	}
	return importCSVFixture(t, buf.String(), rowsPerA*len(valuesA))
}

// synthDependentCategoricalAndNumeric writes a small .pulse cohort with
// three fields: categorical "a" (values "p"/"q"), categorical "b"
// (strongly dependent on "a" — P(b=x|a=p)=0.9, P(b=x|a=q)=0.1), and
// numeric "c" (mean strongly dependent on "a" — 80 for "p", 20 for "q",
// shared std 5). Used by the regression test to prove BOTH dependency
// kinds vanish in generated output when the profile was captured without
// --conditional.
func synthDependentCategoricalAndNumeric(t *testing.T, rowsPerA int, seed int64) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	valuesA := []string{"p", "q"}
	condProbsB := map[string]float64{"p": 0.9, "q": 0.1}
	meansC := map[string]float64{"p": 80.0, "q": 20.0}
	var buf strings.Builder
	buf.WriteString("a,b,c\n")
	for _, av := range valuesA {
		pX := condProbsB[av]
		meanC := meansC[av]
		for r := 0; r < rowsPerA; r++ {
			bv := "y"
			if rng.Float64() < pX {
				bv = "x"
			}
			cv := float64(rng.NormFloat64()*5.0) + meanC
			fmt.Fprintf(&buf, "%s,%s,%.8f\n", av, bv, cv)
		}
	}
	return importCSVFixture(t, buf.String(), rowsPerA*len(valuesA))
}

// conditionalCategoryRate returns the empirical rate at which bs[i] ==
// bVal among the positions where as[i] == aVal, plus the number of such
// positions (n). Returns (0, 0) when aVal was never observed.
func conditionalCategoryRate(as, bs []string, aVal, bVal string) (rate float64, n int) {
	count, match := 0, 0
	for i := range as {
		if as[i] != aVal {
			continue
		}
		count++
		if bs[i] == bVal {
			match++
		}
	}
	if count == 0 {
		return 0, 0
	}
	return float64(match) / float64(count), count
}

// conditionalMean returns the mean of cs[i] among the positions where
// as[i] == aVal, plus the number of such positions (n). Returns (0, 0)
// when aVal was never observed.
func conditionalMean(as []string, cs []float64, aVal string) (mean float64, n int) {
	sum := 0.0
	count := 0
	for i := range as {
		if as[i] != aVal {
			continue
		}
		sum += cs[i]
		count++
	}
	if count == 0 {
		return 0, 0
	}
	return sum / float64(count), count
}
