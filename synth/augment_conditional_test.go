package synth_test

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// TestAugmentFromProfile_ReconstructsCorrelationWithinTolerance is E2-S2's
// own acceptance-bar assertion: a full profile -> generate round-trip
// through the ACTUAL production path behind `synth from-profile` —
// AugmentFromProfile, reached here via p.Synth with SourceCohort set,
// exactly as internal/cli/synth.go's synthFromProfileCmd calls it — must
// reproduce the source's numeric-numeric correlation in the newly
// GENERATED partition, not merely in a bare SpecFromProfile+Synth call
// with no source cohort (that path is already covered by E2-S1's
// TestProfile_ConditionalThenSynth_ReconstructsCorrelationWithinTolerance).
// This is deliberately a different call path: AugmentFromProfile builds a
// merged schema, tags rows, and generates against genSchema/wfs
// internally — it must consult spec.Correlations exactly as the bare path
// does, and this test is what actually proves that rather than assuming
// it from the bare-path test alone.
func TestAugmentFromProfile_ReconstructsCorrelationWithinTolerance(t *testing.T) {
	const tolerance = 0.03
	const sourceRows = 8000
	const newRows = 20000

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	srcData, sourceRho := synthCorrelatedPair(t, 0.8, sourceRows, 11)
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{
		IncludeConditional: true,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.NumericPairs) != 1 {
		t.Fatalf("expected exactly one captured numeric pair, got Conditional=%+v", prof.Conditional)
	}

	spec, _ := synth.SpecFromProfile(prof, newRows)
	if len(spec.Correlations) != 1 {
		t.Fatalf("expected SpecFromProfile to populate one correlation from Conditional.NumericPairs, got %d", len(spec.Correlations))
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

	// Isolate the newly-generated partition (rows sourceRows..end) —
	// AugmentFromProfile writes source rows first, generated rows after,
	// per its own doc comment.
	a := readF64Field(t, augmented, "a")
	b := readF64Field(t, augmented, "b")
	if len(a) != sourceRows+newRows {
		t.Fatalf("augmented row count = %d, want %d", len(a), sourceRows+newRows)
	}
	genA, genB := a[sourceRows:], b[sourceRows:]
	reconstructedRho := pearsonOf(genA, genB)

	if math.Abs(reconstructedRho-sourceRho) > tolerance {
		t.Fatalf("generated-partition rho = %.4f, source rho = %.4f — outside tolerance %.2f",
			reconstructedRho, sourceRho, tolerance)
	}
}

// TestAugmentFromProfile_WithoutConditional_GeneratesIndependentMarginals
// is E2-S2's regression test: a profile captured without --conditional
// (and without --include-correlations) carries no correlation structure
// at all, so SpecFromProfile.Correlations stays empty and the
// AugmentFromProfile-generated partition must show no reconstructed
// correlation — independent-marginal sampling, byte-for-byte the same
// code path as before this effort touched synth/copula.go. This locks in
// FR-11: no new generate-time flag activates the feature, and its absence
// from the profile document is what keeps behavior unchanged.
func TestAugmentFromProfile_WithoutConditional_GeneratesIndependentMarginals(t *testing.T) {
	const sourceRows = 8000
	const newRows = 20000

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	srcData, _ := synthCorrelatedPair(t, 0.8, sourceRows, 12)
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
	if len(spec.Correlations) != 0 {
		t.Fatalf("expected no correlations reconstructed from a profile with no conditional/pairwise section, got %d", len(spec.Correlations))
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

	a := readF64Field(t, augmented, "a")
	b := readF64Field(t, augmented, "b")
	if len(a) != sourceRows+newRows {
		t.Fatalf("augmented row count = %d, want %d", len(a), sourceRows+newRows)
	}
	genA, genB := a[sourceRows:], b[sourceRows:]
	reconstructedRho := pearsonOf(genA, genB)

	// The source carries rho ~0.8; independent-marginal sampling should
	// land the generated partition's measured rho near zero. 0.1 gives
	// ample headroom above sampling noise at n=20000 while staying far
	// below any plausible partial-correlation leakage.
	const independenceBound = 0.1
	if math.Abs(reconstructedRho) > independenceBound {
		t.Fatalf("generated-partition rho = %.4f, want within %.2f of 0 (independent marginals, no conditional data present)",
			reconstructedRho, independenceBound)
	}
}
