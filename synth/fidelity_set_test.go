package synth_test

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestBuildFidelityReport_SetFieldMarginalFrequencyDelta is E5-S4's
// unit-level assertion for the set_* per-field marginal section: given
// a real AugmentFromProfile output built from buildSetFieldCohort's
// known per-option frequencies (the same fixture E5-S1's own acceptance
// test drives) and the resulting merged cohort, BuildFidelityReport
// must append one SetFieldFidelity entry (not a Fields/TEST_KS/
// TEST_CHISQ entry — this section never calls the supplied TestRunner)
// with one SetOptionFidelity per dictionary entry, in bit order,
// SourceFrequency echoing the fixture's exact known rate, and Delta
// landing close to zero.
func TestBuildFidelityReport_SetFieldMarginalFrequencyDelta(t *testing.T) {
	const tolerance = 0.03
	options := []string{"email", "sms", "push", "chat"}
	const rowCount = 4000
	// Exact frequencies: 1.0, 0.5, 0.2, 0.025.
	counts := []int{4000, 2000, 800, 100}
	srcData := buildSetFieldCohort(t, options, counts, rowCount)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prof, err := synth.ProfileBytes(srcData, synth.ProfileOptions{})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}

	const newRows = 20000
	spec := synth.SpecFromProfile(prof, newRows)

	res, err := synth.AugmentFromProfile(fs, spec, "/source.pulse", "/augmented.pulse", synth.Options{Seed: 91})
	if err != nil {
		t.Fatalf("augment: %v", err)
	}
	data, err := afero.ReadFile(fs, res.OutputPath)
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	schema, records := splitCohortForPairwiseTest(t, data)

	run := func(physical, presented *encoding.Schema, records []byte, test *types.Test) (*types.TestResult, error) {
		t.Fatalf("run should not be called for a set_* field, got Test=%+v", test)
		return nil, nil
	}
	report := synth.BuildFidelityReport(schema, records, rowCount, newRows, run)

	if len(report.Fields) != 0 {
		t.Errorf("Fields = %+v, want none (only a set_* field in this fixture)", report.Fields)
	}
	if len(report.SetFields) != 1 {
		t.Fatalf("len(SetFields) = %d, want 1", len(report.SetFields))
	}
	sf := report.SetFields[0]
	if sf.Field != "channels" {
		t.Errorf("Field = %q, want channels", sf.Field)
	}
	if len(sf.Options) != len(options) {
		t.Fatalf("len(Options) = %d, want %d", len(sf.Options), len(options))
	}
	for i, opt := range sf.Options {
		if opt.Value != options[i] {
			t.Errorf("Options[%d].Value = %q, want %q (bit-order contract)", i, opt.Value, options[i])
		}
		if opt.Error != "" {
			t.Fatalf("option %s: unexpected Error: %q", opt.Value, opt.Error)
		}
		wantSrc := float64(counts[i]) / float64(rowCount)
		if math.Abs(opt.SourceFrequency-wantSrc) > 1e-9 {
			t.Errorf("option %s: SourceFrequency = %.6f, want %.6f (exact fixture rate)", opt.Value, opt.SourceFrequency, wantSrc)
		}
		if opt.N == 0 {
			t.Errorf("option %s: N = 0, want > 0", opt.Value)
		}
		if opt.Delta > tolerance {
			t.Errorf("option %s: Delta = %.4f, want <= %.2f (SourceFrequency=%.4f SyntheticFrequency=%.4f)",
				opt.Value, opt.Delta, tolerance, opt.SourceFrequency, opt.SyntheticFrequency)
		}
	}
}

// TestBuildFidelityReport_SetFieldErrorWhenNoSyntheticObservations
// mirrors BuildFidelityReport's own non-fatal per-entry contract for
// the set_* section: a schema whose synthetic partition never appears
// in records (nil records — no rows at all) must produce an Error per
// option rather than a fabricated SyntheticFrequency/Delta.
func TestBuildFidelityReport_SetFieldErrorWhenNoSyntheticObservations(t *testing.T) {
	dict := encoding.NewDictionary()
	for _, v := range []string{"a", "b"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add(%q): %v", v, err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "channels", Type: encoding.FieldTypeSetU8, Dictionary: dict},
		{Name: synth.SyntheticFieldName, Type: encoding.FieldTypePackedBool},
	}}
	run := func(physical, presented *encoding.Schema, records []byte, test *types.Test) (*types.TestResult, error) {
		t.Fatal("run should not be called for a set_* field")
		return nil, nil
	}
	report := synth.BuildFidelityReport(schema, nil, 0, 0, run)

	if len(report.SetFields) != 1 {
		t.Fatalf("len(SetFields) = %d, want 1", len(report.SetFields))
	}
	sf := report.SetFields[0]
	if len(sf.Options) != 2 {
		t.Fatalf("len(Options) = %d, want 2", len(sf.Options))
	}
	for _, opt := range sf.Options {
		if opt.Error == "" {
			t.Errorf("option %s: expected an Error with no synthetic observations", opt.Value)
		}
		if opt.SyntheticFrequency != 0 || opt.Delta != 0 || opt.N != 0 {
			t.Errorf("option %s: SyntheticFrequency/Delta/N = %v/%v/%v, want zero values alongside Error",
				opt.Value, opt.SyntheticFrequency, opt.Delta, opt.N)
		}
	}
}

// TestBuildSetCategoricalPairwise_ComputesTVDAgainstSourceCells is
// E5-S4's unit-level assertion for the set-categorical delta: given a
// real AugmentFromProfile output built from buildSetCategoricalCohort's
// genuine regional association (the same fixture E5-S2/E5-S3's own
// acceptance tests drive) and the SetCategoricalPairSpec that
// reconstruction targeted, BuildSetCategoricalPairwise's Delta (total
// variation distance) must land close to zero for every captured
// option, Error must be unset, and N must be positive.
func TestBuildSetCategoricalPairwise_ComputesTVDAgainstSourceCells(t *testing.T) {
	const tolerance = 0.05
	featureOpts := []string{"darkmode", "export", "api", "sso"}
	regionOpts := []string{"us", "eu"}
	const rowsPerRegion = 3000
	const rowCount = rowsPerRegion * 2
	const newRows = 20000

	featureSelected := func(row int, region string, featureIdx int) bool {
		switch featureIdx {
		case 1: // export
			if region == "eu" {
				return row%10 != 0 // 90% selected
			}
			return row%10 == 0 // 10% selected
		default:
			return row%2 == 0 // flat 50%, no association
		}
	}
	srcData := buildSetCategoricalCohort(t, featureOpts, regionOpts, featureSelected, rowCount)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prof, err := synth.ProfileBytes(srcData, synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.SetCategoricalPairs) != len(featureOpts) {
		t.Fatalf("expected %d captured set-categorical pairs, got Conditional=%+v", len(featureOpts), prof.Conditional)
	}

	spec := synth.SpecFromProfile(prof, newRows)
	if len(spec.SetCategoricalPairs) != len(featureOpts) {
		t.Fatalf("expected SpecFromProfile to populate %d set-categorical pairs, got %d",
			len(featureOpts), len(spec.SetCategoricalPairs))
	}

	res, err := synth.AugmentFromProfile(fs, spec, "/source.pulse", "/augmented.pulse", synth.Options{Seed: 92})
	if err != nil {
		t.Fatalf("augment: %v", err)
	}
	data, err := afero.ReadFile(fs, res.OutputPath)
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	schema, records := splitCohortForPairwiseTest(t, data)

	report := &synth.FidelityReport{}
	synth.BuildSetCategoricalPairwise(report, schema, records, spec.SetCategoricalPairs)

	if len(report.SetCategoricalPairwise) != len(featureOpts) {
		t.Fatalf("len(SetCategoricalPairwise) = %d, want %d", len(report.SetCategoricalPairwise), len(featureOpts))
	}
	for _, pair := range report.SetCategoricalPairwise {
		if pair.Set != "features" || pair.Categorical != "region" {
			t.Errorf("Set/Categorical = %s/%s, want features/region", pair.Set, pair.Categorical)
		}
		if pair.Error != "" {
			t.Fatalf("option %s: unexpected Error: %q", pair.Option, pair.Error)
		}
		if pair.N == 0 {
			t.Errorf("option %s: N = 0, want > 0", pair.Option)
		}
		if pair.Delta > tolerance {
			t.Errorf("option %s: Delta = %.4f, want <= %.2f", pair.Option, pair.Delta, tolerance)
		}
	}
}

// TestBuildSetCategoricalPairwise_ErrorWhenOptionUnknown mirrors
// BuildCategoricalPairwise's own degenerate-pair contract: a
// SetCategoricalPairSpec naming an Option the set field's dictionary
// does not carry can never produce a co-occurring observation, so the
// entry must carry an Error rather than a fabricated Delta.
func TestBuildSetCategoricalPairwise_ErrorWhenOptionUnknown(t *testing.T) {
	featureOpts := []string{"export"}
	regionOpts := []string{"us", "eu"}
	const rowCount = 2000
	featureSelected := func(row int, region string, featureIdx int) bool { return row%2 == 0 }
	srcData := buildSetCategoricalCohort(t, featureOpts, regionOpts, featureSelected, rowCount)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prof, err := synth.ProfileBytes(srcData, synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	spec := synth.SpecFromProfile(prof, 4000)

	res, err := synth.AugmentFromProfile(fs, spec, "/source.pulse", "/augmented.pulse", synth.Options{Seed: 93})
	if err != nil {
		t.Fatalf("augment: %v", err)
	}
	data, err := afero.ReadFile(fs, res.OutputPath)
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	schema, records := splitCohortForPairwiseTest(t, data)

	report := &synth.FidelityReport{}
	bogus := []synth.SetCategoricalPairSpec{{
		Set: "features", Option: "does-not-exist", Categorical: "region",
		Cells: []synth.CategoricalPairCellSpec{{AValue: "selected", BValue: "us", Count: 1}},
	}}
	synth.BuildSetCategoricalPairwise(report, schema, records, bogus)

	if len(report.SetCategoricalPairwise) != 1 {
		t.Fatalf("len(SetCategoricalPairwise) = %d, want 1", len(report.SetCategoricalPairwise))
	}
	pair := report.SetCategoricalPairwise[0]
	if pair.Error == "" {
		t.Fatal("expected an Error for an unknown option")
	}
	if pair.Delta != 0 || pair.N != 0 {
		t.Errorf("Delta/N = %v/%v, want zero values alongside Error", pair.Delta, pair.N)
	}
}

// TestBuildSetNumericPairwise_ComputesMeanStdDeltaAgainstSource is
// E5-S4's unit-level assertion for the set-numeric delta: given a real
// AugmentFromProfile output built from synthSetNumericPair's genuine
// dependency (the same fixture E5-S3's own acceptance test drives),
// each bucket's realized mean/std must land close to the captured
// mean/std, Error must be unset, and N must be positive.
func TestBuildSetNumericPairwise_ComputesMeanStdDeltaAgainstSource(t *testing.T) {
	const meanTolerance = 2.0
	const stdTolerance = 1.0
	const rowCount = 8000
	const newRows = 20000
	srcData := synthSetNumericPair(t, rowCount, 94, 100.0, 10.0, 5.0, 0.5)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prof, err := synth.ProfileBytes(srcData, synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.SetNumericPairs) != 1 {
		t.Fatalf("expected exactly one captured set-numeric pair, got Conditional=%+v", prof.Conditional)
	}

	spec := synth.SpecFromProfile(prof, newRows)
	if len(spec.SetNumericPairs) != 1 {
		t.Fatalf("expected SpecFromProfile to populate one set-numeric pair, got %d", len(spec.SetNumericPairs))
	}

	res, err := synth.AugmentFromProfile(fs, spec, "/source.pulse", "/augmented.pulse", synth.Options{Seed: 95})
	if err != nil {
		t.Fatalf("augment: %v", err)
	}
	data, err := afero.ReadFile(fs, res.OutputPath)
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	schema, records := splitCohortForPairwiseTest(t, data)

	report := &synth.FidelityReport{}
	synth.BuildSetNumericPairwise(report, schema, records, spec.SetNumericPairs)

	if len(report.SetNumericPairwise) != 1 {
		t.Fatalf("len(SetNumericPairwise) = %d, want 1", len(report.SetNumericPairwise))
	}
	pair := report.SetNumericPairwise[0]
	if pair.Set != "tier" || pair.Option != "premium" || pair.Numeric != "spend" {
		t.Errorf("Set/Option/Numeric = %s/%s/%s, want tier/premium/spend", pair.Set, pair.Option, pair.Numeric)
	}
	if len(pair.Categories) != 2 {
		t.Fatalf("len(Categories) = %d, want 2 (selected, not_selected)", len(pair.Categories))
	}
	for _, cat := range pair.Categories {
		if cat.Error != "" {
			t.Fatalf("bucket %s: unexpected Error: %q", cat.Category, cat.Error)
		}
		if cat.N == 0 {
			t.Errorf("bucket %s: N = 0, want > 0", cat.Category)
		}
		if math.Abs(cat.MeanDelta) > meanTolerance {
			t.Errorf("bucket %s: MeanDelta = %.4f, want <= %.2f (SourceMean=%.4f SyntheticMean=%.4f)",
				cat.Category, cat.MeanDelta, meanTolerance, cat.SourceMean, cat.SyntheticMean)
		}
		if cat.StdDelta > stdTolerance {
			t.Errorf("bucket %s: StdDelta = %.4f, want <= %.2f", cat.Category, cat.StdDelta, stdTolerance)
		}
	}
}

// TestBuildSetSetPairwise_ComputesTVDAgainstSourceCells is E5-S4's
// unit-level assertion for the set-set delta: given a real
// AugmentFromProfile output built from synthSetSetPair's genuine
// cross-field option association (the same fixture E5-S3's own
// acceptance test drives), Delta (total variation distance) must land
// close to zero, Error must be unset, and N must be positive.
func TestBuildSetSetPairwise_ComputesTVDAgainstSourceCells(t *testing.T) {
	const tolerance = 0.05
	const rowCount = 8000
	const newRows = 20000
	srcData := synthSetSetPair(t, rowCount, 10)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prof, err := synth.ProfileBytes(srcData, synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.SetSetPairs) != 1 {
		t.Fatalf("expected exactly one captured set-set pair, got Conditional=%+v", prof.Conditional)
	}

	spec := synth.SpecFromProfile(prof, newRows)
	if len(spec.SetSetPairs) != 1 {
		t.Fatalf("expected SpecFromProfile to populate one set-set pair, got %d", len(spec.SetSetPairs))
	}

	res, err := synth.AugmentFromProfile(fs, spec, "/source.pulse", "/augmented.pulse", synth.Options{Seed: 96})
	if err != nil {
		t.Fatalf("augment: %v", err)
	}
	data, err := afero.ReadFile(fs, res.OutputPath)
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	schema, records := splitCohortForPairwiseTest(t, data)

	report := &synth.FidelityReport{}
	synth.BuildSetSetPairwise(report, schema, records, spec.SetSetPairs)

	if len(report.SetSetPairwise) != 1 {
		t.Fatalf("len(SetSetPairwise) = %d, want 1", len(report.SetSetPairwise))
	}
	pair := report.SetSetPairwise[0]
	if pair.SetA != "setA" || pair.OptionA != "x" || pair.SetB != "setB" || pair.OptionB != "y" {
		t.Errorf("SetA/OptionA/SetB/OptionB = %s/%s/%s/%s, want setA/x/setB/y", pair.SetA, pair.OptionA, pair.SetB, pair.OptionB)
	}
	if pair.Error != "" {
		t.Fatalf("unexpected Error: %q", pair.Error)
	}
	if pair.N == 0 {
		t.Error("N = 0, want > 0")
	}
	if pair.Delta > tolerance {
		t.Errorf("Delta = %.4f, want <= %.2f", pair.Delta, tolerance)
	}
}
