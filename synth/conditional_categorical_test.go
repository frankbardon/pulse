package synth_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// TestProfile_ConditionalCapturesCategoricalPair is E3-S1's basic
// capture assertion: a cohort with two categorical fields, profiled
// with --conditional, produces exactly one
// Profile.Conditional.CategoricalPairs entry whose contingency table
// sums back to the pair's true co-occurrence count and whose cells
// reflect the actual observed combinations — well under
// synth.ContingencyCellCap, so no collapse should occur here.
func TestProfile_ConditionalCapturesCategoricalPair(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	const rowCount = 5000
	srcData := synthCategoricalPair(t, rowCount, 21,
		[]string{"US", "UK", "DE"}, []float64{0.5, 0.3, 0.2},
		[]string{"paid", "free"}, []float64{0.6, 0.4})
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
	if len(prof.Conditional.CategoricalPairs) != 1 {
		t.Fatalf("expected exactly one categorical pair, got %d", len(prof.Conditional.CategoricalPairs))
	}
	pair := prof.Conditional.CategoricalPairs[0]
	if pair.N != rowCount {
		t.Errorf("pair N = %d, want %d (no nulls in this fixture)", pair.N, rowCount)
	}
	// 3 x 2 = 6 possible combinations, all should appear at this row
	// count and none should have been collapsed (well under the cap).
	if len(pair.Cells) != 6 {
		t.Fatalf("expected 6 contingency cells, got %d: %+v", len(pair.Cells), pair.Cells)
	}
	sum := 0
	for _, c := range pair.Cells {
		if c.AValue == "other" || c.BValue == "other" {
			t.Errorf("did not expect an \"other\" cell for a low-cardinality pair, got %+v", c)
		}
		sum += c.Count
	}
	if sum != rowCount {
		t.Errorf("cell counts sum to %d, want %d", sum, rowCount)
	}
}

// TestProfile_WithoutConditional_OmitsCategoricalPairs is the additive
// regression check for the categorical section, mirroring E2-S1's own
// numeric-pairs regression test: no "categorical_pairs" key at all
// unless --conditional was requested.
func TestProfile_WithoutConditional_OmitsCategoricalPairs(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	srcData := synthCategoricalPair(t, 500, 22,
		[]string{"US", "UK"}, []float64{0.5, 0.5},
		[]string{"paid", "free"}, []float64{0.5, 0.5})
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

// TestProfile_ConditionalCategoricalHighCardinality_StaysWithinCap is
// this story's non-negotiable acceptance bar: a fixture with two
// 50+-category fields must not blow up the contingency table. It
// asserts the captured pair's Cells never exceeds
// synth.ContingencyCellCap, and that the long tail is visibly
// collapsed into a single ("other","other") catch-all cell carrying
// the remaining mass — a build-failing assertion on the actual shape,
// not a smoke test that capture merely completes without error.
func TestProfile_ConditionalCategoricalHighCardinality_StaysWithinCap(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	const categoriesPerField = 60
	const rowCount = 30000
	valuesA, weightsA := uniformCategories("a", categoriesPerField)
	valuesB, weightsB := uniformCategories("b", categoriesPerField)
	srcData := synthCategoricalPair(t, rowCount, 23, valuesA, weightsA, valuesB, weightsB)
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
		t.Fatalf("expected exactly one categorical pair, got Conditional=%+v", prof.Conditional)
	}
	pair := prof.Conditional.CategoricalPairs[0]

	// The non-negotiable cap: however many raw combinations exist
	// (up to 60*60 = 3600, or (TopK+1)^2 = 1089 after the per-field
	// top-K collapse), the emitted table must never exceed the cap.
	if len(pair.Cells) > synth.ContingencyCellCap {
		t.Fatalf("contingency cells = %d, must never exceed ContingencyCellCap = %d",
			len(pair.Cells), synth.ContingencyCellCap)
	}
	if len(pair.Cells) != synth.ContingencyCellCap {
		t.Fatalf("expected the raw cardinality (up to 1089 post-top-K combinations) to actually "+
			"exceed the cap and trigger a collapse to exactly %d cells, got %d — fixture may not "+
			"be high-cardinality enough to exercise the cap", synth.ContingencyCellCap, len(pair.Cells))
	}

	// The long tail must be visibly collapsed into a single
	// ("other","other") catch-all cell, not silently dropped.
	foundOther := false
	var otherCount int
	sum := 0
	for _, c := range pair.Cells {
		sum += c.Count
		if c.AValue == "other" && c.BValue == "other" {
			foundOther = true
			otherCount = c.Count
		}
	}
	if !foundOther {
		t.Fatal("expected a visible (\"other\",\"other\") catch-all cell for the collapsed long tail")
	}
	if otherCount <= 0 {
		t.Fatalf("expected the \"other\"/\"other\" cell to carry positive collapsed mass, got %d", otherCount)
	}
	if sum != pair.N {
		t.Errorf("cell counts sum to %d, want pair.N = %d (no observation may be dropped, only collapsed)", sum, pair.N)
	}
}

// TestProfile_ConditionalCategoricalRespectsPerFieldTopK asserts the
// existing per-field categorical top-K cap composes with (is an input
// bound to, not replaced by) the new joint-cell cap: with TopK set
// small, every contingency cell's axis values must be drawn from
// {top-K values, "other"} — never a raw value the per-field cap would
// have excluded from FieldProfile.Categorical.Top.
func TestProfile_ConditionalCategoricalRespectsPerFieldTopK(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	valuesA, weightsA := uniformCategories("a", 20)
	valuesB, weightsB := uniformCategories("b", 20)
	srcData := synthCategoricalPair(t, 20000, 24, valuesA, weightsA, valuesB, weightsB)
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
	if prof.Conditional == nil || len(prof.Conditional.CategoricalPairs) != 1 {
		t.Fatalf("expected exactly one categorical pair, got Conditional=%+v", prof.Conditional)
	}

	var fieldA, fieldB *synth.FieldProfile
	for i := range prof.Fields {
		switch prof.Fields[i].Name {
		case "a":
			fieldA = &prof.Fields[i]
		case "b":
			fieldB = &prof.Fields[i]
		}
	}
	if fieldA == nil || fieldB == nil || fieldA.Categorical == nil || fieldB.Categorical == nil {
		t.Fatal("expected both categorical field profiles to be populated")
	}
	allowedA := map[string]bool{"other": true}
	for _, hit := range fieldA.Categorical.Top {
		allowedA[hit.Value] = true
	}
	allowedB := map[string]bool{"other": true}
	for _, hit := range fieldB.Categorical.Top {
		allowedB[hit.Value] = true
	}
	if len(fieldA.Categorical.Top) != topK {
		t.Fatalf("expected field a's own top-K cap to retain %d values, got %d", topK, len(fieldA.Categorical.Top))
	}

	pair := prof.Conditional.CategoricalPairs[0]
	for _, c := range pair.Cells {
		if !allowedA[c.AValue] {
			t.Errorf("cell a_value %q is not in field a's own top-%d set (or \"other\") — per-field cap not respected", c.AValue, topK)
		}
		if !allowedB[c.BValue] {
			t.Errorf("cell b_value %q is not in field b's own top-%d set (or \"other\") — per-field cap not respected", c.BValue, topK)
		}
	}
}

// TestProfile_ConditionalCategoricalThinCellWarning asserts a
// contingency cell whose count falls below synth.MinPairObservations
// emits the same warning shape E2-S1 introduced for thin numeric
// pairs — no separate/divergent warning mechanism for the categorical
// case.
func TestProfile_ConditionalCategoricalThinCellWarning(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	// A heavily skewed pair: one combination is overwhelmingly common,
	// the other exceedingly rare, guaranteeing at least one thin cell
	// well under MinPairObservations (30) at a modest row count.
	srcData := synthCategoricalPair(t, 400, 25,
		[]string{"common", "rare"}, []float64{0.995, 0.005},
		[]string{"x", "y"}, []float64{0.995, 0.005})
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
		t.Fatalf("expected exactly one categorical pair, got Conditional=%+v", prof.Conditional)
	}
	pair := prof.Conditional.CategoricalPairs[0]

	var thinCell *synth.ContingencyCell
	for i, c := range pair.Cells {
		if c.Count < synth.MinPairObservations {
			thinCell = &pair.Cells[i]
			break
		}
	}
	if thinCell == nil {
		t.Fatalf("expected at least one thin contingency cell (< %d observations), got cells=%+v",
			synth.MinPairObservations, pair.Cells)
	}

	found := false
	for _, w := range prof.Warnings {
		if strings.Contains(w, "thin") && strings.Contains(w, "categorical") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a thin categorical-pair warning matching the E2-S1 warning shape, got warnings=%v", prof.Warnings)
	}
}

// synthCategoricalPair writes a small .pulse cohort with two
// categorical fields "a" and "b", independently weighted-categorical
// distributed per the given value/weight lists, and returns the raw
// bytes.
func synthCategoricalPair(t *testing.T, rowCount int, seed int64, valuesA []string, weightsA []float64, valuesB []string, weightsB []float64) []byte {
	t.Helper()
	toAny := func(ss []string) []any {
		out := make([]any, len(ss))
		for i, s := range ss {
			out[i] = s
		}
		return out
	}
	toAnyF := func(fs []float64) []any {
		out := make([]any, len(fs))
		for i, f := range fs {
			out[i] = f
		}
		return out
	}
	spec := &synth.Spec{
		RowCount: rowCount,
		Fields: []synth.FieldSpec{
			{Name: "a", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": toAny(valuesA), "weights": toAnyF(weightsA)}},
			{Name: "b", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": toAny(valuesB), "weights": toAnyF(weightsB)}},
		},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: seed})
	if err != nil {
		t.Fatalf("synth categorical fixture: %v", err)
	}
	return data
}

// uniformCategories returns n distinct category values (prefixed by
// prefix, to avoid collision when building two independent fields)
// with equal weights — used to build high-cardinality test fixtures.
func uniformCategories(prefix string, n int) ([]string, []float64) {
	values := make([]string, n)
	weights := make([]float64, n)
	w := 1.0 / float64(n)
	for i := 0; i < n; i++ {
		values[i] = fmt.Sprintf("%s%02d", prefix, i)
		weights[i] = w
	}
	return values, weights
}
