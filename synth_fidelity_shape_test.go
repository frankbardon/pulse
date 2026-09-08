package pulse

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// fidelityBimodalSpec mirrors E4-S1/E4-S2's own bimodal test fixture
// shape (synth_test.go's TestSynth_MixtureReproducesBimodalShape,
// synth/shape_test.go's synthSingleField) — a single f64 field "v"
// drawn from a well-separated two-component mixture, kept as a
// standalone helper here since those helpers live in the external
// synth_test package this file cannot import.
func fidelityBimodalSpec(rows int, mean1, std1, mean2, std2 float64) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "v", Type: "f64", Distribution: synth.DistMixture,
				Params: map[string]any{
					"means":   []any{mean1, mean2},
					"stds":    []any{std1, std2},
					"weights": []any{0.5, 0.5},
				}},
		},
	}
}

// fidelityFieldEntry locates the FieldFidelity entry for name, failing
// the test if absent.
func fidelityFieldEntry(t *testing.T, report *synth.FidelityReport, name string) *synth.FieldFidelity {
	t.Helper()
	for _, f := range report.Fields {
		if f.Field == name {
			return f
		}
	}
	t.Fatalf("report has no field entry for %q", name)
	return nil
}

// TestSynth_FidelityReportCatchesShapeCollapseViaKS is E4-S3's
// non-negotiable acceptance gate. It answers the story's own open
// question — is E1-S2's existing per-field TEST_KS marginal check
// already sufficient to catch a numeric field's shape-fit generation
// collapsing to a plain normal, or does the report need a dedicated
// shape-match indicator? — by proving it with real numbers rather than
// assuming either way.
//
// The correct half: a full `profile create --fit-shape` -> `synth
// from-profile --fidelity-report` round trip (here, the library calls
// those two CLI leaves are thin wrappers over — p.Profile with
// FitShape, then p.Synth with SourceCohort + FidelityReportPath) on
// E4-S1/E4-S2's own bimodal fixture must produce a report whose "v"
// TEST_KS statistic is well within tolerance — a real build-failing
// assertion on the actual numbers, not a smoke test that the report
// merely renders.
//
// The regressed half simulates E4's own bug class directly: a spec
// that ignores the captured Shape and reconstructs "v" as a plain
// DistNormal off the profile's ordinary Mean/Std (exactly what
// SpecFromProfile would emit if its mixture-consumption branch were
// bypassed — see profile.go's DistMixture reconstruction), run through
// the identical from-profile/fidelity-report path and compared against
// the SAME bimodal source. Its "v" TEST_KS statistic must clearly blow
// past the same tolerance, and its p-value must collapse toward zero —
// proving the existing marginal check has teeth against this exact
// failure mode, so no dedicated shape-match metric is needed.
func TestSynth_FidelityReportCatchesShapeCollapseViaKS(t *testing.T) {
	const (
		mean1, std1 = -10.0, 1.5
		mean2, std2 = 10.0, 1.5

		sourceRows = 8000
		newRows    = 20000

		// ksTolerance bounds the correct case's KS D statistic: two
		// samples drawn from the same mixture should differ only by
		// sampling noise, well under the ~0.02 D value the asymptotic
		// KS critical region predicts at alpha=0.05 for these sample
		// sizes (1.36*sqrt(1/sourceRows+1/newRows) ≈ 0.017).
		ksTolerance = 0.05
		// ksRegressedFloor is the minimum KS D statistic the collapsed-
		// to-normal case must clear to count as "clearly fails" — a
		// single normal centered on the mixture's overall mean has
		// essentially no density where the two real modes sit, so the
		// realized statistic is expected far above this floor.
		ksRegressedFloor = 0.2
		pValueCeiling    = 0.01
	)

	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Synth(context.Background(),
		fidelityBimodalSpec(sourceRows, mean1, std1, mean2, std2), "/source.pulse",
		SynthOptions{Seed: 401}); err != nil {
		t.Fatalf("source synth: %v", err)
	}

	// profile create --fit-shape
	prof, err := p.Profile(context.Background(), "/source.pulse", ProfileOptions{FitShape: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if len(prof.Fields) != 1 || prof.Fields[0].Numeric == nil {
		t.Fatalf("expected one numeric field profile, got %+v", prof.Fields)
	}
	shape := prof.Fields[0].Numeric.Shape
	if shape == nil {
		t.Fatal("expected Numeric.Shape to be populated for this clearly bimodal fixture — capture failed upstream of this story")
	}

	// Correct case: synth from-profile, letting SpecFromProfile
	// reconstruct DistMixture from the captured Shape as designed.
	correctSpec, _ := synth.SpecFromProfile(prof, newRows)
	if correctSpec.Fields[0].Distribution != synth.DistMixture {
		t.Fatalf("expected SpecFromProfile to emit DistMixture, got %q", correctSpec.Fields[0].Distribution)
	}
	if _, err := p.Synth(context.Background(), correctSpec, "/augmented_correct.pulse",
		SynthOptions{Seed: 402, SourceCohort: "/source.pulse", FidelityReportPath: "/report_correct.json"}); err != nil {
		t.Fatalf("correct synth from-profile: %v", err)
	}
	correctReport := readFidelityReport(t, fs, "/report_correct.json")
	correctEntry := fidelityFieldEntry(t, correctReport, "v")
	if correctEntry.Error != "" {
		t.Fatalf("correct case: unexpected Error %q", correctEntry.Error)
	}
	if correctEntry.Result == nil {
		t.Fatal("correct case: Result is nil")
	}
	if correctEntry.Result.Statistic > ksTolerance {
		t.Errorf("correct case: KS statistic = %.4f, want <= %.4f (shape preserved within tolerance)",
			correctEntry.Result.Statistic, ksTolerance)
	}

	// Regressed case: simulate the exact bug this epic exists to catch
	// — generation ignores the captured mixture and falls back to a
	// single normal off the field's plain Mean/Std — by constructing
	// that spec by hand and driving it through the identical
	// from-profile/fidelity-report path against the SAME source.
	regressedSpec := &synth.Spec{
		RowCount: newRows,
		Fields: []synth.FieldSpec{
			{Name: "v", Type: "f64", Distribution: synth.DistNormal, Params: map[string]any{
				"mean": prof.Fields[0].Numeric.Mean,
				"std":  prof.Fields[0].Numeric.Std,
			}},
		},
	}
	if _, err := p.Synth(context.Background(), regressedSpec, "/augmented_regressed.pulse",
		SynthOptions{Seed: 403, SourceCohort: "/source.pulse", FidelityReportPath: "/report_regressed.json"}); err != nil {
		t.Fatalf("regressed synth from-profile: %v", err)
	}
	regressedReport := readFidelityReport(t, fs, "/report_regressed.json")
	regressedEntry := fidelityFieldEntry(t, regressedReport, "v")
	if regressedEntry.Error != "" {
		t.Fatalf("regressed case: unexpected Error %q", regressedEntry.Error)
	}
	if regressedEntry.Result == nil {
		t.Fatal("regressed case: Result is nil")
	}
	if regressedEntry.Result.Statistic < ksRegressedFloor {
		t.Errorf("regressed case: KS statistic = %.4f, want >= %.4f — the collapsed-to-normal shape should clearly fail the marginal check",
			regressedEntry.Result.Statistic, ksRegressedFloor)
	}
	if regressedEntry.Result.PValue > pValueCeiling {
		t.Errorf("regressed case: KS p-value = %.6f, want <= %.6f — the collapsed-to-normal shape should be rejected with high confidence",
			regressedEntry.Result.PValue, pValueCeiling)
	}

	// The two cases must be clearly separated, not just individually on
	// the right side of a lenient tolerance — this is what "the check
	// has teeth" means operationally.
	if regressedEntry.Result.Statistic <= correctEntry.Result.Statistic {
		t.Errorf("regressed KS statistic (%.4f) is not greater than correct KS statistic (%.4f) — the check does not distinguish the two cases",
			regressedEntry.Result.Statistic, correctEntry.Result.Statistic)
	}
}

// readFidelityReport reads and decodes the JSON fidelity report at
// path.
func readFidelityReport(t *testing.T, fs afero.Fs, path string) *synth.FidelityReport {
	t.Helper()
	raw, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatalf("read report %s: %v", path, err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal report %s: %v", path, err)
	}
	return &report
}
