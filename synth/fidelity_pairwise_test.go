package synth_test

import (
	"bytes"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// TestBuildPairwise_ComputesDeltaAgainstSourceRho is E2-S3's own
// unit-level assertion on the exported building block the fidelity
// report bridge (synth_fidelity.go at the module root) drives: given a
// real AugmentFromProfile output and the CorrelationSpec that produced
// it, BuildPairwise's SourceRho must echo the spec verbatim, its
// SyntheticRho must land within E2-S1's own 0.03 tolerance of the
// source cohort's actual measured correlation (the same bar
// TestSynth_CorrelationReconstructionWithinTolerance already holds
// generation to, never a second, looser number invented for this
// section), Delta must equal abs(SourceRho-SyntheticRho), N must count
// exactly the generated partition (no nulls in this fixture), and the
// supplied warnings must be copied onto the report verbatim.
func TestBuildPairwise_ComputesDeltaAgainstSourceRho(t *testing.T) {
	const tolerance = 0.03
	const sourceRows = 8000
	const newRows = 20000

	srcData, sourceRho := synthCorrelatedPair(t, 0.8, sourceRows, 61)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	spec := &synth.Spec{
		RowCount: newRows,
		Fields: []synth.FieldSpec{
			{Name: "a", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 50.0, "std": 10.0}},
			{Name: "b", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 20.0, "std": 5.0}},
		},
		Correlations: []synth.CorrelationSpec{{A: "a", B: "b", Correlation: sourceRho}},
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
	synth.BuildPairwise(report, schema, records, spec.Correlations, []string{"warn-a", "warn-b"})

	if len(report.Pairwise) != 1 {
		t.Fatalf("len(Pairwise) = %d, want 1", len(report.Pairwise))
	}
	pair := report.Pairwise[0]
	if pair.Error != "" {
		t.Fatalf("unexpected Error: %q", pair.Error)
	}
	if pair.A != "a" || pair.B != "b" {
		t.Errorf("A/B = %s/%s, want a/b", pair.A, pair.B)
	}
	if pair.SourceRho != sourceRho {
		t.Errorf("SourceRho = %.6f, want %.6f (verbatim CorrelationSpec.Correlation)", pair.SourceRho, sourceRho)
	}
	if math.Abs(pair.SyntheticRho-sourceRho) > tolerance {
		t.Fatalf("SyntheticRho = %.4f, source rho = %.4f — outside tolerance %.2f", pair.SyntheticRho, sourceRho, tolerance)
	}
	if want := math.Abs(pair.SyntheticRho - pair.SourceRho); math.Abs(pair.Delta-want) > 1e-9 {
		t.Errorf("Delta = %.6f, want %.6f (abs(SourceRho-SyntheticRho))", pair.Delta, want)
	}
	if pair.N != newRows {
		t.Errorf("N = %d, want %d (no nulls in this fixture's synthetic partition)", pair.N, newRows)
	}
	if len(report.Warnings) != 2 || report.Warnings[0] != "warn-a" || report.Warnings[1] != "warn-b" {
		t.Errorf("Warnings = %v, want verbatim [warn-a warn-b]", report.Warnings)
	}
}

// TestBuildPairwise_ErrorWhenSyntheticPartitionDegenerate asserts a pair
// whose synthetic-partition sample is degenerate (zero variance on one
// side — pearson's own NaN contract) gets an Error entry rather than a
// fabricated SyntheticRho/Delta, mirroring BuildFidelityReport's own
// non-fatal per-entry contract (one pair's failure never withholds the
// rest of the report). "_synthetic" itself is a constant 1 across every
// row this scan admits (by construction — the admission filter is
// values[_synthetic]!=0), so pairing a real field against it is a
// reliable, deterministic way to hit the degenerate case without
// needing a hand-crafted record buffer.
func TestBuildPairwise_ErrorWhenSyntheticPartitionDegenerate(t *testing.T) {
	srcData, sourceRho := synthCorrelatedPair(t, 0.8, 100, 63)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	spec := &synth.Spec{
		RowCount: 200,
		Fields: []synth.FieldSpec{
			{Name: "a", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 50.0, "std": 10.0}},
			{Name: "b", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 20.0, "std": 5.0}},
		},
		Correlations: []synth.CorrelationSpec{{A: "a", B: "b", Correlation: sourceRho}},
	}
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
	degenerate := []synth.CorrelationSpec{{A: "a", B: synth.SyntheticFieldName, Correlation: 0.5}}
	synth.BuildPairwise(report, schema, records, degenerate, nil)

	if len(report.Pairwise) != 1 {
		t.Fatalf("len(Pairwise) = %d, want 1", len(report.Pairwise))
	}
	pair := report.Pairwise[0]
	if pair.Error == "" {
		t.Fatal("expected an Error for a degenerate (zero-variance) pair")
	}
	if pair.SyntheticRho != 0 || pair.Delta != 0 {
		t.Errorf("SyntheticRho/Delta = %v/%v, want zero values alongside Error", pair.SyntheticRho, pair.Delta)
	}
	if pair.N != 200 {
		t.Errorf("N = %d, want 200 (every generated row admits — _synthetic is constant-true, not null)", pair.N)
	}
	if len(report.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none (nil supplied)", report.Warnings)
	}
}

// splitCohortForPairwiseTest splits a .pulse file's bytes into its
// physical schema and raw record bytes — the same shape
// synth_fidelity.go's writeSynthFidelityReport uses at the module root,
// reimplemented here since this package cannot import the pulse
// facade (pulse imports synth).
func splitCohortForPairwiseTest(t *testing.T, data []byte) (*encoding.Schema, []byte) {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	return schema, data[len(data)-r.Len():]
}
