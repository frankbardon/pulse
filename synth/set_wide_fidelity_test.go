package synth_test

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// E5-S2 fidelity coverage for wide set columns.
//
// A wide-set column silently OMITTED from the report reads as "nothing
// wrong", and a wide-set column present but read through a uint64
// assertion is worse: both partitions come back all-zero, so every
// per-option delta is exactly 0.0 and the section actively certifies a
// generation it never measured.

// TestBuildFidelityReport_WideSetFieldCoverage asserts the per-field
// marginal section covers a 206-member set_u256 end to end: one entry,
// one SetOptionFidelity per member in bit order, source frequencies
// echoing the fixture's exact known rates ABOVE bit 64 as well as
// below, and deltas that are small because they were measured rather
// than because both sides were zero.
func TestBuildFidelityReport_WideSetFieldCoverage(t *testing.T) {
	const members = 206
	const sourceRows = 1000
	const newRows = 20000
	const tolerance = 0.03

	options := wideSetOptionNames(members)
	counts := wideSetMemberCounts(members, sourceRows)
	srcData := buildWideSetFieldCohort(t, encoding.FieldTypeSetU256, options, counts, sourceRows)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prof, err := synth.ProfileBytes(srcData, synth.ProfileOptions{})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	spec, _ := synth.SpecFromProfile(prof, newRows)

	res, err := synth.AugmentFromProfile(fs, spec, "/source.pulse", "/augmented.pulse", synth.Options{Seed: 83})
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
	report := synth.BuildFidelityReport(schema, records, sourceRows, newRows, run)

	if len(report.SetFields) != 1 {
		t.Fatalf("len(SetFields) = %d, want 1 — a wide set column omitted from the "+
			"report reads as nothing wrong", len(report.SetFields))
	}
	entry := report.SetFields[0]
	if entry.Field != "features" {
		t.Errorf("Field = %q, want features", entry.Field)
	}
	if len(entry.Options) != members {
		t.Fatalf("len(Options) = %d, want %d — the report must cover every member, "+
			"not the first 64", len(entry.Options), members)
	}
	var highChecked int
	for i, o := range entry.Options {
		if o.Value != options[i] {
			t.Fatalf("Options[%d].Value = %q, want %q", i, o.Value, options[i])
		}
		if o.Error != "" {
			t.Fatalf("Options[%d] (%q): Error %q", i, o.Value, o.Error)
		}
		wantSrc := float64(counts[i]) / float64(sourceRows)
		if math.Abs(o.SourceFrequency-wantSrc) > 1e-12 {
			t.Errorf("member %q (bit %d): SourceFrequency = %v, want %v",
				o.Value, i, o.SourceFrequency, wantSrc)
		}
		if o.Delta > tolerance {
			t.Errorf("member %q (bit %d): Delta = %.4f exceeds %.2f (source %.4f, synthetic %.4f)",
				o.Value, i, o.Delta, tolerance, o.SourceFrequency, o.SyntheticFrequency)
		}
		if i >= 64 {
			// A zero-vs-zero comparison also yields Delta 0, so the
			// high half needs its synthetic side asserted NON-zero
			// against a source rate that is non-zero too.
			if o.SyntheticFrequency == 0 {
				t.Errorf("member %q (bit %d): SyntheticFrequency = 0 against a source rate "+
					"of %.4f — the report compared two empty masks", o.Value, i, wantSrc)
			}
			highChecked++
		}
	}
	if highChecked != members-64 {
		t.Fatalf("checked %d members above bit 63, want %d", highChecked, members-64)
	}
}

// wideSetModelFidelitySpec is the model-recovery fixture with a WIDE
// set predictor: a set_u128 whose 80 options put the coefficient-
// carrying members on both sides of the word boundary at 64.
func wideSetModelFidelitySpec(rows int, models ...synth.FieldModelSpec) *synth.Spec {
	names := wideSetOptionNames(80)
	optionsAny := make([]any, len(names))
	freqsAny := make([]any, len(names))
	for i := range names {
		optionsAny[i] = names[i]
		freqsAny[i] = 0.5
	}
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{
				Name: "features", Type: "set_u128",
				Distribution: synth.DistSetBernoulli,
				Params:       map[string]any{"options": optionsAny, "frequencies": freqsAny},
			},
			{
				Name: "spend", Type: "f64",
				Distribution: synth.DistNormal,
				Params:       map[string]any{"mean": 100.0, "std": 25.0},
			},
		},
		Models: models,
	}
}

// TestBuildModelFidelity_WideSetPredictorRecovers covers the recovery
// refit's own set-column reader. A term whose indicator is always 0 is
// reported as "constant over the admitted synthetic rows" — a message
// that describes a degenerate GENERATION, not a broken reader — so a
// uint64 assertion here misattributes its own defect to the cohort.
func TestBuildModelFidelity_WideSetPredictorRecovers(t *testing.T) {
	const recoveryBar = 0.05
	const marginalStd = 25.0
	const lowMember = "opt003"
	const highMember = "opt070"

	spec := wideSetModelFidelitySpec(20000, synth.FieldModelSpec{
		Field:     "spend",
		Intercept: 100,
		Predictors: []synth.ModelPredictorSpec{
			setOption("features", lowMember, 14),
			setOption("features", highMember, -18),
		},
		ResidualStd: 10,
	})
	schema, records := augmentForModelFidelity(t, spec, 20000, 211)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	m := findModelFidelity(report, "spend")
	if m == nil {
		t.Fatalf("no model fidelity entry for spend: %+v", report.Models)
	}
	if m.Error != "" {
		t.Fatalf("unexpected Error: %q", m.Error)
	}
	for _, w := range []struct {
		level string
		coef  float64
	}{{lowMember, 14}, {highMember, -18}} {
		p := findPredictorFidelity(m, "features", w.level)
		if p == nil {
			t.Fatalf("no predictor entry for features=%s", w.level)
		}
		if p.Error != "" {
			t.Fatalf("features=%s: unexpected Error %q — a wide member read as never "+
				"selected produces a constant column and exactly this message", w.level, p.Error)
		}
		if p.NFired < 1000 {
			t.Errorf("features=%s: NFired = %d, too thin for recovery to mean anything",
				w.level, p.NFired)
		}
		captured := w.coef / marginalStd
		if math.Abs(p.CapturedCoefficient-captured) > 1e-12 {
			t.Errorf("features=%s: CapturedCoefficient = %v, want %v",
				w.level, p.CapturedCoefficient, captured)
		}
		if math.Abs(p.RecoveredCoefficient-captured) > recoveryBar {
			t.Errorf("features=%s: RecoveredCoefficient = %.4f, captured %.4f — gap %.4f exceeds %.2f",
				w.level, p.RecoveredCoefficient, captured,
				math.Abs(p.RecoveredCoefficient-captured), recoveryBar)
		}
	}
}

// TestBuildPairwise_WideSetOptionsAreMeasured covers the three
// set-involving PAIRWISE sections of the report against a wide set
// field. Each reads the generated cohort's own masks back out of the
// decoded row cache, so each carries its own copy of the
// narrow-assertion hazard: a member above bit 63 read as never selected
// puts the whole synthetic partition in the not_selected arm, which
// produces a confident, fully-populated entry whose delta is simply
// wrong.
//
// Each kind gets its OWN generation carrying exactly one pair. The
// fixture's 66 members would otherwise put thousands of captured pairs
// through arbitration, where a relationship under test can be dropped
// on its merits — and a test that cannot tell arbitration from a broken
// mask reader is not a test of the mask reader.
func TestBuildPairwise_WideSetOptionsAreMeasured(t *testing.T) {
	const rowCount = 2400
	const newRows = 20000
	srcData, gt := buildWideSetPairCohort(t, rowCount, true)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prof, err := synth.ProfileBytes(srcData, synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	base, _ := synth.SpecFromProfile(prof, newRows)

	// generate builds a merged cohort from a copy of the profile-derived
	// spec carrying only the pairs `keep` leaves in place.
	generate := func(t *testing.T, name string, seed int64, keep func(*synth.Spec)) (*encoding.Schema, []byte, *synth.Spec) {
		t.Helper()
		spec := *base
		spec.SetCategoricalPairs = nil
		spec.SetNumericPairs = nil
		spec.SetSetPairs = nil
		spec.CategoricalPairs = nil
		spec.CategoricalNumericPairs = nil
		keep(&spec)
		out := "/augmented_" + name + ".pulse"
		res, err := synth.AugmentFromProfile(fs, &spec, "/source.pulse", out, synth.Options{Seed: seed})
		if err != nil {
			t.Fatalf("augment(%s): %v", name, err)
		}
		data, err := afero.ReadFile(fs, res.OutputPath)
		if err != nil {
			t.Fatalf("read augmented(%s): %v", name, err)
		}
		schema, records := splitCohortForPairwiseTest(t, data)
		return schema, records, &spec
	}

	t.Run("set_categorical", func(t *testing.T) {
		var keep []synth.SetCategoricalPairSpec
		for _, p := range base.SetCategoricalPairs {
			if p.Option == gt.hiBitOption && p.Categorical == "region" {
				keep = append(keep, p)
			}
		}
		if len(keep) != 1 {
			t.Fatalf("expected one captured set-categorical pair on the high-bit member, got %d", len(keep))
		}
		schema, records, spec := generate(t, "setcat", 57, func(s *synth.Spec) { s.SetCategoricalPairs = keep })
		report := &synth.FidelityReport{}
		synth.BuildSetCategoricalPairwise(report, schema, records, spec.SetCategoricalPairs)
		if len(report.SetCategoricalPairwise) != 1 {
			t.Fatalf("len(SetCategoricalPairwise) = %d, want 1", len(report.SetCategoricalPairwise))
		}
		e := report.SetCategoricalPairwise[0]
		if e.Error != "" {
			t.Fatalf("Error %q", e.Error)
		}
		// Total variation distance. With the high word dropped the whole
		// synthetic partition lands in the not_selected arm while a
		// third of the source sits in the selected one.
		if e.Delta > 0.05 {
			t.Errorf("features[%s] x region: Delta = %.4f, want <=0.05", gt.hiBitOption, e.Delta)
		}
	})

	t.Run("set_numeric", func(t *testing.T) {
		var keep []synth.SetNumericPairSpec
		for _, p := range base.SetNumericPairs {
			if p.Option == gt.spendOption && p.Numeric == "spend" {
				keep = append(keep, p)
			}
		}
		if len(keep) != 1 {
			t.Fatalf("expected one captured set-numeric pair on the high-bit member, got %d", len(keep))
		}
		schema, records, spec := generate(t, "setnum", 58, func(s *synth.Spec) { s.SetNumericPairs = keep })
		report := &synth.FidelityReport{}
		synth.BuildSetNumericPairwise(report, schema, records, spec.SetNumericPairs)
		if len(report.SetNumericPairwise) != 1 {
			t.Fatalf("len(SetNumericPairwise) = %d, want 1", len(report.SetNumericPairwise))
		}
		e := report.SetNumericPairwise[0]
		if len(e.Categories) != 2 {
			t.Fatalf("%d bucket(s), want 2", len(e.Categories))
		}
		for _, c := range e.Categories {
			if c.Error != "" {
				t.Fatalf("bucket %q: Error %q — a member read as never selected leaves the "+
					"selected bucket with no synthetic observation at all", c.Category, c.Error)
			}
			if c.N < 500 {
				t.Errorf("bucket %q: N = %d, too thin to be evidence", c.Category, c.N)
			}
			if c.MeanDelta > 2.0 {
				t.Errorf("bucket %q: MeanDelta = %.4f (source %.2f, synthetic %.2f)",
					c.Category, c.MeanDelta, c.SourceMean, c.SyntheticMean)
			}
		}
	})

	t.Run("set_set", func(t *testing.T) {
		var keep []synth.SetSetPairSpec
		for _, p := range base.SetSetPairs {
			if p.OptionA == gt.hiBitOption && p.OptionB == "c0" {
				keep = append(keep, p)
			}
		}
		if len(keep) != 1 {
			t.Fatalf("expected one captured set-set pair on the high-bit member, got %d", len(keep))
		}
		schema, records, spec := generate(t, "setset", 59, func(s *synth.Spec) { s.SetSetPairs = keep })
		report := &synth.FidelityReport{}
		synth.BuildSetSetPairwise(report, schema, records, spec.SetSetPairs)
		if len(report.SetSetPairwise) != 1 {
			t.Fatalf("len(SetSetPairwise) = %d, want 1", len(report.SetSetPairwise))
		}
		e := report.SetSetPairwise[0]
		if e.Error != "" {
			t.Fatalf("Error %q", e.Error)
		}
		if e.Delta > 0.05 {
			t.Errorf("features[%s] x channels[c0]: Delta = %.4f, want <=0.05", gt.hiBitOption, e.Delta)
		}
	})
}
