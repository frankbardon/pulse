package pulse

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// fidelityCorrelatedSpec mirrors synth_test's own synthCorrelatedPair
// fixture (synth/conditional_test.go) — two normally distributed f64
// fields "a"/"b" declared with a target Pearson correlation — kept as a
// standalone helper in this package since that helper lives in an
// external test package (synth_test) this file cannot import.
func fidelityCorrelatedSpec(rows int, targetRho float64) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "a", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 50.0, "std": 10.0}},
			{Name: "b", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 20.0, "std": 5.0}},
		},
		Correlations: []synth.CorrelationSpec{{A: "a", B: "b", Correlation: targetRho}},
	}
}

// TestSynth_FidelityReportPairwiseWithinTolerance is E2-S3's
// non-negotiable acceptance-bar assertion: a full `profile create
// --conditional` -> `synth from-profile --fidelity-report` round trip
// (here, the library calls those two CLI leaves are thin wrappers over
// — p.Profile with IncludeConditional, then p.Synth with SourceCohort +
// FidelityReportPath) on a known-correlation fixture must produce a
// report whose pairwise correlation-delta figure is within E2-S1's own
// 0.03 tolerance, checked as a real build-failing assertion on the
// actual numbers rather than a smoke test that the report merely
// renders a "pairwise" key.
func TestSynth_FidelityReportPairwiseWithinTolerance(t *testing.T) {
	const tolerance = 0.03
	const sourceRows = 8000
	const newRows = 20000

	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Synth(context.Background(), fidelityCorrelatedSpec(sourceRows, 0.8), "/source.pulse",
		SynthOptions{Seed: 71}); err != nil {
		t.Fatalf("source synth: %v", err)
	}

	// profile create --conditional
	prof, err := p.Profile(context.Background(), "/source.pulse", ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.NumericPairs) != 1 {
		t.Fatalf("expected exactly one captured numeric pair, got Conditional=%+v", prof.Conditional)
	}
	sourceRho := prof.Conditional.NumericPairs[0].Rho

	spec, _ := synth.SpecFromProfile(prof, newRows)
	if len(spec.Correlations) != 1 {
		t.Fatalf("expected SpecFromProfile to populate one correlation, got %d", len(spec.Correlations))
	}

	// synth from-profile --fidelity-report
	res, err := p.Synth(context.Background(), spec, "/augmented.pulse", SynthOptions{
		Seed:               72,
		SourceCohort:       "/source.pulse",
		FidelityReportPath: "/report.json",
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

	if len(report.Pairwise) != 1 {
		t.Fatalf("len(Pairwise) = %d, want 1", len(report.Pairwise))
	}
	pair := report.Pairwise[0]
	if pair.Error != "" {
		t.Fatalf("unexpected Error on pairwise entry: %q", pair.Error)
	}
	if pair.A != "a" || pair.B != "b" {
		t.Errorf("pair A/B = %s/%s, want a/b", pair.A, pair.B)
	}
	if math.Abs(pair.SourceRho-sourceRho) > 1e-9 {
		t.Errorf("SourceRho = %.6f, want %.6f (the profile's own captured rho)", pair.SourceRho, sourceRho)
	}

	// This is the acceptance-gate figure: the synthetic partition's
	// realized correlation must land within tolerance of the source's
	// own measured correlation.
	if pair.Delta > tolerance {
		t.Fatalf("Delta = %.4f — outside E2-S1's own tolerance %.2f (SourceRho=%.4f SyntheticRho=%.4f)",
			pair.Delta, tolerance, pair.SourceRho, pair.SyntheticRho)
	}
	if want := math.Abs(pair.SyntheticRho - pair.SourceRho); math.Abs(pair.Delta-want) > 1e-9 {
		t.Errorf("Delta = %.6f, want %.6f (abs(SourceRho-SyntheticRho))", pair.Delta, want)
	}
}

// TestSynth_FidelityReportSurfacesThinPairWarning locks in acceptance
// criterion 2 (FR-18): a profile carrying at least one thin-pair
// warning from E2-S1's capture stage (Profile.Warnings) must appear in
// the fidelity report verbatim when the caller threads it through
// SynthOptions.FidelityWarnings — exactly what `synth from-profile`
// does with the profile document it already has in hand.
func TestSynth_FidelityReportSurfacesThinPairWarning(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Synth(context.Background(), fidelityCorrelatedSpec(5000, 0.8), "/source.pulse",
		SynthOptions{Seed: 81}); err != nil {
		t.Fatalf("source synth: %v", err)
	}

	// SampleLimit caps ingestion well below synth.MinPairObservations
	// (30) so the captured pair's N is guaranteed thin.
	prof, err := p.Profile(context.Background(), "/source.pulse", ProfileOptions{
		IncludeConditional: true,
		SampleLimit:        20,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if len(prof.Warnings) == 0 {
		t.Fatal("expected at least one thin-pair warning from the profile capture")
	}

	spec, _ := synth.SpecFromProfile(prof, 5000)
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

// TestSynth_FidelityReportOmitsWarningsKeyWhenEmpty is the negative
// half of the thin-pair-warning surfacing contract: leaving
// FidelityWarnings unset (the pre-E2-S3 shape, and every from-schema
// caller) leaves the "warnings" key entirely absent from the wire JSON
// rather than present-but-empty.
func TestSynth_FidelityReportOmitsWarningsKeyWhenEmpty(t *testing.T) {
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
	if _, ok := wire["warnings"]; ok {
		t.Error("report JSON carries a \"warnings\" key with no FidelityWarnings supplied")
	}
	if _, ok := wire["pairwise"]; ok {
		t.Error("report JSON carries a \"pairwise\" key with no correlations in the spec")
	}
}
