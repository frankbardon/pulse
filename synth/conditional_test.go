package synth_test

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// TestSynth_CorrelationReconstructionWithinTolerance is this story's own
// fidelity-gate assertion (E2-S1): the reconstructed Pearson correlation
// of two numeric fields declared with a target rho of 0.8 must land
// within a defined tolerance of that target. This directly exercises
// synth/copula.go's conditional-Gaussian replacement for the removed
// ±5%·std blend — a build-failing assertion on the actual number, not a
// smoke test that generation merely runs without error.
func TestSynth_CorrelationReconstructionWithinTolerance(t *testing.T) {
	const targetRho = 0.8
	const tolerance = 0.03

	spec := &synth.Spec{
		RowCount: 20000,
		Fields: []synth.FieldSpec{
			{Name: "a", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 50.0, "std": 10.0}},
			{Name: "b", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 20.0, "std": 5.0}},
		},
		Correlations: []synth.CorrelationSpec{{A: "a", B: "b", Correlation: targetRho}},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 42})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	a := readF64Field(t, data, "a")
	b := readF64Field(t, data, "b")
	rho := pearsonOf(a, b)
	if math.Abs(rho-targetRho) > tolerance {
		t.Fatalf("reconstructed rho = %.4f, want within %.2f of target %.2f", rho, tolerance, targetRho)
	}
}

// TestProfile_ConditionalCapturesNumericPairs profiles a strongly
// correlated cohort with --conditional and asserts the new
// Profile.Conditional.NumericPairs section actually captures the pair,
// with a correlation matching the source's own measured correlation and
// an accurate co-occurrence N (no nulls here, so N == the row count).
func TestProfile_ConditionalCapturesNumericPairs(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	srcData, sourceRho := synthCorrelatedPair(t, 0.8, 5000, 1)
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
	if len(prof.Conditional.NumericPairs) != 1 {
		t.Fatalf("expected exactly one numeric pair, got %d", len(prof.Conditional.NumericPairs))
	}
	pair := prof.Conditional.NumericPairs[0]
	if pair.N != 5000 {
		t.Errorf("pair N = %d, want 5000 (no nulls in this fixture)", pair.N)
	}
	if math.Abs(pair.Rho-sourceRho) > 1e-6 {
		t.Errorf("captured rho = %.6f, want %.6f (the source's own measured correlation)", pair.Rho, sourceRho)
	}
}

// TestProfile_WithoutConditional_OmitsSection asserts the new section is
// additive: it is entirely absent — not present-but-empty — unless
// ProfileOptions.IncludeConditional was set, so a document captured
// without --conditional is byte-for-byte the pre-existing shape.
func TestProfile_WithoutConditional_OmitsSection(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	srcData, _ := synthCorrelatedPair(t, 0.8, 500, 2)
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{
		IncludeCorrelations: true,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional != nil {
		t.Fatal("expected Conditional to be nil when --conditional was not requested")
	}
	raw, err := json.Marshal(prof)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"conditional"`) {
		t.Fatal("expected no \"conditional\" key in profile JSON when the flag is off")
	}
}

// TestProfile_ConditionalThinPairWarning asserts a pair whose supporting
// observation count falls below synth.MinPairObservations is still
// shipped (never refused) but carries a warning naming the pair — the
// generic thin-pair mechanism this story introduces for reuse by later
// categorical / set_* pair captures.
func TestProfile_ConditionalThinPairWarning(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	srcData, _ := synthCorrelatedPair(t, 0.8, 5000, 3)
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// SampleLimit caps ingestion well below MinPairObservations (30) so
	// the captured pair's N is guaranteed thin.
	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{
		IncludeConditional: true,
		SampleLimit:        20,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.NumericPairs) != 1 {
		t.Fatalf("expected exactly one numeric pair, got Conditional=%+v", prof.Conditional)
	}
	if n := prof.Conditional.NumericPairs[0].N; n >= synth.MinPairObservations {
		t.Fatalf("expected a thin pair (N < %d), got N=%d", synth.MinPairObservations, n)
	}
	found := false
	for _, w := range prof.Warnings {
		if strings.Contains(w, "thin") && strings.Contains(w, "a") && strings.Contains(w, "b") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a thin-pair warning, got warnings=%v", prof.Warnings)
	}
}

// TestProfile_ConditionalThenSynth_ReconstructsCorrelationWithinTolerance
// is the end-to-end acceptance path: profile a cohort with a known
// strong correlation using --conditional, rebuild a Spec from that
// profile, regenerate, and assert the regenerated cohort's own measured
// correlation lands within tolerance of the SOURCE's measured
// correlation (not the abstract 0.8 target — this is what the story's
// acceptance criteria asks for: reconstruction fidelity relative to the
// captured source, end to end through profile -> SpecFromProfile ->
// generation).
func TestProfile_ConditionalThenSynth_ReconstructsCorrelationWithinTolerance(t *testing.T) {
	const tolerance = 0.03
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	srcData, sourceRho := synthCorrelatedPair(t, 0.8, 8000, 4)
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{
		IncludeConditional: true,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}

	specFromProfile, _ := synth.SpecFromProfile(prof, 20000)
	if _, err := p.Synth(context.Background(), specFromProfile, "/regen.pulse",
		pulse.SynthOptions{Seed: 99}); err != nil {
		t.Fatalf("synth from profile: %v", err)
	}

	regen, err := afero.ReadFile(fs, "/regen.pulse")
	if err != nil {
		t.Fatalf("read regenerated cohort: %v", err)
	}
	a := readF64Field(t, regen, "a")
	b := readF64Field(t, regen, "b")
	reconstructedRho := pearsonOf(a, b)

	if math.Abs(reconstructedRho-sourceRho) > tolerance {
		t.Fatalf("reconstructed rho = %.4f, source rho = %.4f — outside tolerance %.2f",
			reconstructedRho, sourceRho, tolerance)
	}
}

// TestProfile_LegacyDocumentWithoutConditional_StillValidInput asserts a
// profile document with no "conditional" key at all — the shape every
// pre-existing profile document has — still unmarshals and still
// produces a runnable Spec via SpecFromProfile. This is the
// backward-compatibility half of the acceptance bar: adding the new
// section must not be a breaking change to the JSON shape.
func TestProfile_LegacyDocumentWithoutConditional_StillValidInput(t *testing.T) {
	const legacyJSON = `{
		"row_count": 100,
		"fields": [
			{"name": "a", "type": "f64", "numeric": {"min": 0, "max": 100, "mean": 50, "std": 10}},
			{"name": "b", "type": "f64", "numeric": {"min": 0, "max": 100, "mean": 20, "std": 5}}
		],
		"pairwise": [{"a": "a", "b": "b", "rho": 0.8}]
	}`
	var prof synth.Profile
	if err := json.Unmarshal([]byte(legacyJSON), &prof); err != nil {
		t.Fatalf("unmarshal legacy profile: %v", err)
	}
	if prof.Conditional != nil {
		t.Fatal("expected Conditional to be nil for a document with no such key")
	}

	spec, _ := synth.SpecFromProfile(&prof, 500)
	if len(spec.Fields) != 2 {
		t.Fatalf("expected 2 fields in reconstructed spec, got %d", len(spec.Fields))
	}
	if len(spec.Correlations) != 1 {
		t.Fatalf("expected the legacy Pairwise entry to still populate Correlations, got %d", len(spec.Correlations))
	}

	fsMap := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsMap})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	if _, err := p.Synth(context.Background(), spec, "/legacy.pulse", pulse.SynthOptions{Seed: 1}); err != nil {
		t.Fatalf("synth from legacy profile: %v", err)
	}
}

// synthCorrelatedPair writes a small .pulse cohort with two normally
// distributed f64 fields "a" and "b" declared with the given target
// correlation, then returns the raw bytes plus the SOURCE's own
// measured Pearson correlation (which is what a real profile/reconstruct
// pipeline is actually held to — not the abstract target, since the
// target itself is only approached asymptotically).
func synthCorrelatedPair(t *testing.T, targetRho float64, rowCount int, seed int64) ([]byte, float64) {
	t.Helper()
	spec := &synth.Spec{
		RowCount: rowCount,
		Fields: []synth.FieldSpec{
			{Name: "a", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 50.0, "std": 10.0}},
			{Name: "b", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 20.0, "std": 5.0}},
		},
		Correlations: []synth.CorrelationSpec{{A: "a", B: "b", Correlation: targetRho}},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: seed})
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}
	a := readF64Field(t, data, "a")
	b := readF64Field(t, data, "b")
	return data, pearsonOf(a, b)
}

// pearsonOf computes the Pearson correlation of two equal-length
// samples read back from generated/regenerated .pulse output — a
// standalone test helper distinct from the package-internal `pearson`
// used by profile capture, so the test suite verifies the on-wire
// numbers independently of the implementation it is checking.
func pearsonOf(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var sumA, sumB float64
	for i := 0; i < n; i++ {
		sumA += a[i]
		sumB += b[i]
	}
	meanA := sumA / float64(n)
	meanB := sumB / float64(n)
	var num, dA, dB float64
	for i := 0; i < n; i++ {
		da := a[i] - meanA
		db := b[i] - meanB
		num += da * db
		dA += da * da
		dB += db * db
	}
	return num / math.Sqrt(dA*dB)
}
