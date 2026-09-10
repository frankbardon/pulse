package synth_test

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// TestProfile_ConditionalCapturesCategoricalNumericPair is this story's
// (E3-S2) non-negotiable acceptance bar: a fixture where the numeric
// field's mean genuinely differs by category (region "east" centered
// meaningfully higher than "west") must have that difference captured
// correctly per category. The assertion compares the captured
// conditional means against the FIXTURE'S OWN measured per-category
// mean (ground truth computed independently in the test, from the
// exact same generated values, not the abstract target) — a
// build-failing assertion on the actual numbers, not a smoke test that
// capture merely completes without error.
func TestProfile_ConditionalCapturesCategoricalNumericPair(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	const rowsPerCategory = 3000
	srcData, truth := synthCategoricalNumericPair(t,
		rowsPerCategory, 31,
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
	if prof.Conditional == nil {
		t.Fatal("expected Conditional section to be populated")
	}
	if len(prof.Conditional.CategoricalNumericPairs) != 1 {
		t.Fatalf("expected exactly one categorical-numeric pair, got %d", len(prof.Conditional.CategoricalNumericPairs))
	}
	pair := prof.Conditional.CategoricalNumericPairs[0]
	if pair.A != "region" || pair.B != "score" {
		t.Fatalf("pair = %+v, want A=region B=score", pair)
	}
	if pair.N != rowsPerCategory*2 {
		t.Errorf("pair N = %d, want %d (no nulls in this fixture)", pair.N, rowsPerCategory*2)
	}
	if len(pair.Categories) != 2 {
		t.Fatalf("expected 2 categories, got %d: %+v", len(pair.Categories), pair.Categories)
	}

	byCategory := make(map[string]synth.CategoricalNumericCategoryStat, len(pair.Categories))
	for _, c := range pair.Categories {
		byCategory[c.Category] = c
	}
	east, ok := byCategory["east"]
	if !ok {
		t.Fatalf("expected an \"east\" category entry, got %+v", pair.Categories)
	}
	west, ok := byCategory["west"]
	if !ok {
		t.Fatalf("expected a \"west\" category entry, got %+v", pair.Categories)
	}

	// The captured conditional mean must match the fixture's own
	// measured ground truth for that category — not merely be
	// "somewhere higher" or "somewhere lower".
	const tol = 1e-6
	if math.Abs(east.Mean-truth["east"]) > tol {
		t.Errorf("east conditional mean = %.8f, want %.8f (fixture ground truth)", east.Mean, truth["east"])
	}
	if math.Abs(west.Mean-truth["west"]) > tol {
		t.Errorf("west conditional mean = %.8f, want %.8f (fixture ground truth)", west.Mean, truth["west"])
	}
	if east.N != rowsPerCategory || west.N != rowsPerCategory {
		t.Errorf("category N = east:%d west:%d, want %d each", east.N, west.N, rowsPerCategory)
	}

	// The whole point of conditional capture: the two categories' means
	// must be genuinely, meaningfully different — not collapsed toward
	// one shared marginal mean.
	if diff := east.Mean - west.Mean; diff < 40 {
		t.Errorf("east/west conditional means too close (diff=%.4f); expected a meaningful (~60) separation", diff)
	}
}

// TestProfile_WithoutConditional_OmitsCategoricalNumericPairs is the
// additive regression check for this story's section, mirroring
// E2-S1/E3-S1's own regression tests: no "categorical_numeric_pairs"
// key at all unless --conditional was requested.
func TestProfile_WithoutConditional_OmitsCategoricalNumericPairs(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	srcData, _ := synthCategoricalNumericPair(t, 500, 32,
		[]string{"east", "west"}, []float64{80.0, 20.0}, 5.0)
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional != nil {
		t.Fatal("expected Conditional to be nil when --conditional was not requested")
	}
}

// TestProfile_ConditionalCategoricalNumericRespectsPerFieldTopK asserts
// the existing per-field categorical top-K cap composes with this
// story's capture: with TopK set small, every retained category must be
// drawn from {top-K values, "other"} — never a raw value the per-field
// cap would have excluded from FieldProfile.Categorical.Top — and the
// number of Categories entries must never exceed TopK+1 (the "other"
// bucket).
func TestProfile_ConditionalCategoricalNumericRespectsPerFieldTopK(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	const categories = 20
	values := make([]string, categories)
	means := make([]float64, categories)
	for i := 0; i < categories; i++ {
		values[i] = fmt.Sprintf("region%02d", i)
		means[i] = float64(i) * 5.0
	}
	srcData, _ := synthCategoricalNumericPair(t, 1000, 33, values, means, 1.0)
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	const topK = 5
	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{
		IncludeConditional: true,
		TopK:               topK,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.CategoricalNumericPairs) != 1 {
		t.Fatalf("expected exactly one categorical-numeric pair, got Conditional=%+v", prof.Conditional)
	}
	pair := prof.Conditional.CategoricalNumericPairs[0]

	var fieldRegion *synth.FieldProfile
	for i := range prof.Fields {
		if prof.Fields[i].Name == "region" {
			fieldRegion = &prof.Fields[i]
		}
	}
	if fieldRegion == nil || fieldRegion.Categorical == nil {
		t.Fatal("expected the region field profile to be populated")
	}
	allowed := map[string]bool{"other": true}
	for _, hit := range fieldRegion.Categorical.Top {
		allowed[hit.Value] = true
	}
	if len(fieldRegion.Categorical.Top) != topK {
		t.Fatalf("expected the region field's own top-K cap to retain %d values, got %d", topK, len(fieldRegion.Categorical.Top))
	}

	if len(pair.Categories) > topK+1 {
		t.Fatalf("expected at most %d categories (top-%d + other), got %d: %+v", topK+1, topK, len(pair.Categories), pair.Categories)
	}
	for _, c := range pair.Categories {
		if !allowed[c.Category] {
			t.Errorf("category %q is not in the region field's own top-%d set (or \"other\") — per-field cap not respected", c.Category, topK)
		}
	}
}

// TestProfile_ConditionalCategoricalNumericThinCellWarning asserts a
// category whose conditional observation count falls below
// synth.MinPairObservations emits the same warning shape E2-S1/E3-S1
// already use for thin numeric/categorical pairs — no separate,
// divergent warning mechanism for this third pair kind.
func TestProfile_ConditionalCategoricalNumericThinCellWarning(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	// A heavily skewed category split: "common" gets almost all rows,
	// "rare" gets only a handful — guaranteeing the "rare" category's
	// conditional N falls well under MinPairObservations (30).
	srcData := synthSkewedCategoricalNumeric(t, 500, 34, "common", "rare", 0.98)
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
		t.Fatalf("expected exactly one categorical-numeric pair, got Conditional=%+v", prof.Conditional)
	}
	pair := prof.Conditional.CategoricalNumericPairs[0]

	var thin *synth.CategoricalNumericCategoryStat
	for i, c := range pair.Categories {
		if c.N < synth.MinPairObservations {
			thin = &pair.Categories[i]
			break
		}
	}
	if thin == nil {
		t.Fatalf("expected at least one thin category (< %d observations), got categories=%+v",
			synth.MinPairObservations, pair.Categories)
	}

	found := false
	for _, w := range prof.Warnings {
		if strings.Contains(w, "thin") && strings.Contains(w, "categorical-numeric") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a thin categorical-numeric warning matching the E2-S1/E3-S1 warning shape, got warnings=%v", prof.Warnings)
	}
}

// synthCategoricalNumericPair writes a small cohort with one
// categorical field "region" and one numeric field "score", where each
// category's score is drawn from a normal distribution centered on its
// own mean (means[i], shared std) — producing a fixture with a known,
// genuine per-category mean difference. It returns the raw .pulse bytes
// plus the FIXTURE'S OWN measured per-category mean (ground truth
// computed from the exact generated values, not the abstract target
// mean, mirroring synthCorrelatedPair's convention in conditional_test.go).
func synthCategoricalNumericPair(t *testing.T, rowsPerCategory int, seed int64, categories []string, means []float64, std float64) ([]byte, map[string]float64) {
	t.Helper()
	if len(categories) != len(means) {
		t.Fatalf("categories/means length mismatch: %d vs %d", len(categories), len(means))
	}
	rng := rand.New(rand.NewSource(seed))

	var buf strings.Builder
	buf.WriteString("region,score\n")
	sums := make(map[string]float64, len(categories))
	counts := make(map[string]int, len(categories))
	for i, cat := range categories {
		for r := 0; r < rowsPerCategory; r++ {
			v := float64(rng.NormFloat64()*std) + means[i]
			fmt.Fprintf(&buf, "%s,%.8f\n", cat, v)
			sums[cat] += v
			counts[cat]++
		}
	}

	truth := make(map[string]float64, len(categories))
	for _, cat := range categories {
		truth[cat] = sums[cat] / float64(counts[cat])
	}

	data := importCSVFixture(t, buf.String(), rowsPerCategory*len(categories))
	return data, truth
}

// synthSkewedCategoricalNumeric writes a small cohort with one
// categorical field "region" (two values, commonValue overwhelmingly
// frequent per commonFrac) and one numeric field "score", used to
// exercise the thin-cell warning path for categorical-numeric capture.
func synthSkewedCategoricalNumeric(t *testing.T, rowCount int, seed int64, commonValue, rareValue string, commonFrac float64) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var buf strings.Builder
	buf.WriteString("region,score\n")
	for i := 0; i < rowCount; i++ {
		cat := commonValue
		if rng.Float64() >= commonFrac {
			cat = rareValue
		}
		v := float64(rng.NormFloat64()*5.0) + 50.0
		fmt.Fprintf(&buf, "%s,%.8f\n", cat, v)
	}
	return importCSVFixture(t, buf.String(), rowCount)
}

// importCSVFixture imports CSV text (auto-inferring the schema) into a
// throwaway in-memory filesystem and returns the resulting .pulse
// file's raw bytes.
func importCSVFixture(t *testing.T, csvText string, sampleRows int) []byte {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	const csvPath = "/fixture.csv"
	const outPath = "/fixture.pulse"
	if err := afero.WriteFile(fs, csvPath, []byte(csvText), 0o644); err != nil {
		t.Fatalf("write fixture csv: %v", err)
	}
	job := &pio.ImportJob{
		Source:     csv.NewReader(fs, csvPath),
		Target:     outPath,
		SampleRows: sampleRows,
		FS:         fs,
	}
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("import fixture csv: %v", err)
	}
	data, err := afero.ReadFile(fs, outPath)
	if err != nil {
		t.Fatalf("read imported fixture: %v", err)
	}
	return data
}
