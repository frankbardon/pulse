package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// buildDependentCategoricalPairCSV writes CSV text for two categorical
// fields "a" and "b", where b is drawn CONDITIONED on a's value per
// condProbs[aVal] (a probability of "x" vs "y") — a genuinely dependent
// fixture, mirroring synth package's own synthDependentCategoricalPair
// (reimplemented here since this file lives in package pulse, a
// separate test binary). rowsPerA rows are written per value of
// valuesA, in block order; rowsPerA*len(valuesA) is kept comfortably
// under synth's conditionalJointCap (10000) — the categorical-
// categorical joint-capture reservoir is a first-N cap rather than a
// random sample, so a block-ordered fixture exceeding it would have its
// later category's rows truncated out of the captured contingency
// table, skewing the captured table's implied marginal away from the
// true per-field marginal the reconstructed "a" distribution actually
// draws from — a pre-existing capture-time property this test does not
// mean to exercise.
func buildDependentCategoricalPairCSV(rowsPerA int, valuesA []string, condProbsX map[string]float64, seed int64) string {
	rng := rand.New(rand.NewSource(seed))
	var buf strings.Builder
	buf.WriteString("a,b\n")
	for _, av := range valuesA {
		pX := condProbsX[av]
		for r := 0; r < rowsPerA; r++ {
			bv := "y"
			if rng.Float64() < pX {
				bv = "x"
			}
			fmt.Fprintf(&buf, "%s,%s\n", av, bv)
		}
	}
	return buf.String()
}

// buildDependentCategoricalNumericCSV writes CSV text for one
// categorical field "region" and one numeric field "score", where each
// category's score is drawn from Normal(means[i], std) — a genuinely
// dependent fixture, mirroring synth package's own
// synthCategoricalNumericPair.
func buildDependentCategoricalNumericCSV(rowsPerCategory int, categories []string, means []float64, std float64, seed int64) string {
	rng := rand.New(rand.NewSource(seed))
	var buf strings.Builder
	buf.WriteString("region,score\n")
	for i, cat := range categories {
		for r := 0; r < rowsPerCategory; r++ {
			v := rng.NormFloat64()*std + means[i]
			fmt.Fprintf(&buf, "%s,%.8f\n", cat, v)
		}
	}
	return buf.String()
}

// importCSVTextFixture imports csvText into path on fs via p, failing
// the test on any error — the same CSV-auto-inferred-schema import path
// `synth from-profile`'s own source cohorts are built from,
// reimplemented here (rather than imported from synth's test helpers)
// since this file lives in package pulse, a separate test binary from
// synth_test.
func importCSVTextFixture(t *testing.T, p *Pulse, fs afero.Fs, csvText, path string, sampleRows int) {
	t.Helper()
	const csvPath = "/fixture-source.csv"
	if err := afero.WriteFile(fs, csvPath, []byte(csvText), 0o644); err != nil {
		t.Fatalf("write fixture csv: %v", err)
	}
	job := &pio.ImportJob{
		Source:     csv.NewReader(fs, csvPath),
		Target:     path,
		SampleRows: sampleRows,
		FS:         fs,
	}
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("import fixture csv: %v", err)
	}
}

// TestSynth_FidelityReportCategoricalPairwiseWithinTolerance is E3-S4's
// non-negotiable acceptance-bar assertion for the categorical-
// categorical pair kind: a full `profile create --conditional` ->
// `synth from-profile --fidelity-report` round trip (here, the library
// calls p.Profile with IncludeConditional, then p.Synth with
// SourceCohort + FidelityReportPath) on a fixture with a genuinely
// dependent categorical-categorical pair must produce a report whose
// contingency-table delta lands within a defined tolerance — a real
// build-failing assertion on the actual numbers, not a smoke test that
// the report merely renders the new "categorical_pairwise" key.
func TestSynth_FidelityReportCategoricalPairwiseWithinTolerance(t *testing.T) {
	const tolerance = 0.05
	const rowsPerA = 3000
	const newRows = 20000

	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	valuesA := []string{"p", "q"}
	condProbsX := map[string]float64{"p": 0.9, "q": 0.1}
	csvText := buildDependentCategoricalPairCSV(rowsPerA, valuesA, condProbsX, 51)
	importCSVTextFixture(t, p, fs, csvText, "/source.pulse", rowsPerA*len(valuesA))

	// profile create --conditional
	prof, err := p.Profile(context.Background(), "/source.pulse", ProfileOptions{IncludeConditional: true})
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

	// synth from-profile --fidelity-report
	res, err := p.Synth(context.Background(), spec, "/augmented.pulse", SynthOptions{
		Seed:               52,
		SourceCohort:       "/source.pulse",
		FidelityReportPath: "/report.json",
		FidelityWarnings:   prof.Warnings,
	})
	if err != nil {
		t.Fatalf("augment synth: %v", err)
	}
	if res.FidelityReportPath != "/report.json" {
		t.Fatalf("FidelityReportPath = %q, want /report.json", res.FidelityReportPath)
	}

	raw, err := afero.ReadFile(fs, "/report.json")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}

	if len(report.CategoricalPairwise) != 1 {
		t.Fatalf("len(CategoricalPairwise) = %d, want 1", len(report.CategoricalPairwise))
	}
	pair := report.CategoricalPairwise[0]
	if pair.Error != "" {
		t.Fatalf("unexpected Error on categorical pairwise entry: %q", pair.Error)
	}
	if pair.A != "a" || pair.B != "b" {
		t.Errorf("CategoricalPairwise A/B = %s/%s, want a/b", pair.A, pair.B)
	}
	if pair.N == 0 {
		t.Error("CategoricalPairwise N = 0, want > 0")
	}
	if pair.Delta > tolerance {
		t.Fatalf("CategoricalPairwise Delta = %.4f — outside tolerance %.2f", pair.Delta, tolerance)
	}
	if len(report.CategoricalNumericPairwise) != 0 {
		t.Errorf("CategoricalNumericPairwise = %+v, want none (no numeric field in this fixture)", report.CategoricalNumericPairwise)
	}
}

// TestSynth_FidelityReportCategoricalNumericPairwiseWithinTolerance is
// E3-S4's non-negotiable acceptance-bar assertion for the categorical-
// numeric pair kind: the same round trip on a fixture with a genuinely
// dependent categorical-numeric pair must produce a report whose
// per-category conditional-mean/std deltas land within a defined
// tolerance.
func TestSynth_FidelityReportCategoricalNumericPairwiseWithinTolerance(t *testing.T) {
	const meanTolerance = 2.0
	const stdTolerance = 1.0
	const rowsPerCategory = 6000
	const newRows = 20000

	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	categories := []string{"east", "west"}
	means := []float64{80.0, 20.0}
	csvText := buildDependentCategoricalNumericCSV(rowsPerCategory, categories, means, 5.0, 61)
	importCSVTextFixture(t, p, fs, csvText, "/source.pulse", rowsPerCategory*len(categories))

	// profile create --conditional
	prof, err := p.Profile(context.Background(), "/source.pulse", ProfileOptions{IncludeConditional: true})
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

	// synth from-profile --fidelity-report
	res, err := p.Synth(context.Background(), spec, "/augmented.pulse", SynthOptions{
		Seed:               62,
		SourceCohort:       "/source.pulse",
		FidelityReportPath: "/report.json",
		FidelityWarnings:   prof.Warnings,
	})
	if err != nil {
		t.Fatalf("augment synth: %v", err)
	}
	if res.FidelityReportPath != "/report.json" {
		t.Fatalf("FidelityReportPath = %q, want /report.json", res.FidelityReportPath)
	}

	raw, err := afero.ReadFile(fs, "/report.json")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}

	if len(report.CategoricalNumericPairwise) != 1 {
		t.Fatalf("len(CategoricalNumericPairwise) = %d, want 1", len(report.CategoricalNumericPairwise))
	}
	pair := report.CategoricalNumericPairwise[0]
	if pair.A != "region" || pair.B != "score" {
		t.Errorf("CategoricalNumericPairwise A/B = %s/%s, want region/score", pair.A, pair.B)
	}
	if len(pair.Categories) != 2 {
		t.Fatalf("len(Categories) = %d, want 2 (east, west)", len(pair.Categories))
	}
	for _, cat := range pair.Categories {
		if cat.Error != "" {
			t.Fatalf("category %s: unexpected Error: %q", cat.Category, cat.Error)
		}
		if cat.N == 0 {
			t.Errorf("category %s: N = 0, want > 0", cat.Category)
		}
		if math.Abs(cat.MeanDelta) > meanTolerance {
			t.Errorf("category %s: MeanDelta = %.4f, want <= %.2f (SourceMean=%.4f SyntheticMean=%.4f)",
				cat.Category, cat.MeanDelta, meanTolerance, cat.SourceMean, cat.SyntheticMean)
		}
		if cat.StdDelta > stdTolerance {
			t.Errorf("category %s: StdDelta = %.4f, want <= %.2f (SourceStd=%.4f SyntheticStd=%.4f)",
				cat.Category, cat.StdDelta, stdTolerance, cat.SourceStd, cat.SyntheticStd)
		}
	}
	if len(report.CategoricalPairwise) != 0 {
		t.Errorf("CategoricalPairwise = %+v, want none (only one categorical field in this fixture)", report.CategoricalPairwise)
	}
}

// TestSynth_FidelityReportOmitsCategoricalPairwiseKeysWhenEmpty is the
// negative half of the new-section contract: a from-profile run whose
// spec carries no CategoricalPairs/CategoricalNumericPairs (i.e. the
// profile was NOT captured with --conditional) leaves both new wire
// keys entirely absent rather than present-but-empty — mirroring
// TestSynth_FidelityReportOmitsPairwiseKey's own contract for the
// numeric-numeric section.
func TestSynth_FidelityReportOmitsCategoricalPairwiseKeysWhenEmpty(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, spec := setupFidelityFixture(t, fs)

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse",
		SynthOptions{Seed: 2, SourceCohort: "/source.pulse", FidelityReportPath: "/report.json"}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	raw, err := afero.ReadFile(fs, "/report.json")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if _, ok := wire["categorical_pairwise"]; ok {
		t.Error("report JSON carries a \"categorical_pairwise\" key with no captured categorical pairs in the spec")
	}
	if _, ok := wire["categorical_numeric_pairwise"]; ok {
		t.Error("report JSON carries a \"categorical_numeric_pairwise\" key with no captured categorical-numeric pairs in the spec")
	}
}

// TestSynth_FidelityReportSurfacesCategoricalThinPairWarning is
// acceptance criterion 2 (FR-18/FR-19 for the categorical kinds): a
// profile carrying at least one thin-cell warning from E3-S1/E3-S2's
// capture stage must appear in the fidelity report verbatim when the
// caller threads it through SynthOptions.FidelityWarnings — exactly
// what `synth from-profile` does with the profile document it already
// has in hand, and exactly the same shared Warnings channel
// TestSynth_FidelityReportSurfacesThinPairWarning already locks in for
// the numeric-numeric kind (no new mechanism for this pair kind).
func TestSynth_FidelityReportSurfacesCategoricalThinPairWarning(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	valuesA := []string{"p", "q"}
	condProbsX := map[string]float64{"p": 0.9, "q": 0.1}
	csvText := buildDependentCategoricalPairCSV(2000, valuesA, condProbsX, 81)
	importCSVTextFixture(t, p, fs, csvText, "/source.pulse", 4000)

	// SampleLimit caps ingestion well below synth.MinPairObservations
	// (30) so the captured pair's per-cell N is guaranteed thin.
	prof, err := p.Profile(context.Background(), "/source.pulse", ProfileOptions{
		IncludeConditional: true,
		SampleLimit:        20,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if len(prof.Warnings) == 0 {
		t.Fatal("expected at least one thin-pair/thin-cell warning from the profile capture")
	}
	var foundCategoricalWarning bool
	for _, w := range prof.Warnings {
		if strings.Contains(w, "categorical") {
			foundCategoricalWarning = true
			break
		}
	}
	if !foundCategoricalWarning {
		t.Fatalf("expected at least one warning naming a categorical pair kind, got %v", prof.Warnings)
	}

	spec := synth.SpecFromProfile(prof, 4000)
	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse", SynthOptions{
		Seed:               82,
		SourceCohort:       "/source.pulse",
		FidelityReportPath: "/report.json",
		FidelityWarnings:   prof.Warnings,
	}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	raw, err := afero.ReadFile(fs, "/report.json")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}

	if len(report.Warnings) != len(prof.Warnings) {
		t.Fatalf("report.Warnings = %v, want verbatim copy of profile.Warnings = %v", report.Warnings, prof.Warnings)
	}
	for i, w := range prof.Warnings {
		if report.Warnings[i] != w {
			t.Errorf("Warnings[%d] = %q, want %q (verbatim copy of Profile.Warnings)", i, report.Warnings[i], w)
		}
	}
}
