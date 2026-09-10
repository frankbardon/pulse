package synth_test

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// E5-S1 test pack: the per-field model-recovery section of the fidelity
// report.
//
// Every fixture here goes through AugmentFromProfile rather than
// SynthBytes, because the section reads the `_synthetic` partition of a
// MERGED output cohort — the same shape the pairwise sections read, and
// the same shape writeSynthFidelityReport hands it in production.
//
// The recovery bar is stated in LATENT units throughout (a captured
// coefficient divided by the target's marginal std), which is the scale
// the section reports on and the scale synth.ModelRecoveryTolerance is
// denominated in. A `normal` target makes that division the only
// difference between the two scales, which is why these fixtures use one:
// the arithmetic stays checkable by hand while the code under test still
// takes the full Q/Phi round trip.

// modelFidelitySpec is the model-recovery fixture cohort: three
// categorical levels on `region`, two on `plan`, a two-option `features`
// set_*, and the `spend` numeric a model targets. Deliberately the same
// shape as model_draw_test.go's own fixture so a reader can compare the
// generation assertions there against the recovery assertions here.
func modelFidelitySpec(rows int, models ...synth.FieldModelSpec) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{
				Name: "region", Type: "categorical_u8",
				Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"east", "west", "north"},
					"weights": []any{1.0, 1.0, 1.0},
				},
			},
			{
				Name: "plan", Type: "categorical_u8",
				Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"basic", "pro"},
					"weights": []any{1.0, 1.0},
				},
			},
			{
				Name: "features", Type: "set_u8",
				Distribution: synth.DistSetBernoulli,
				Params: map[string]any{
					"options":     []any{"premium", "trial"},
					"frequencies": []any{0.5, 0.5},
				},
			},
			{
				Name: "spend", Type: "f64",
				Distribution: synth.DistNormal,
				Params:       map[string]any{"mean": 100.0, "std": 25.0},
			},
			{
				Name: "tenure", Type: "f64",
				Distribution: synth.DistNormal,
				Params:       map[string]any{"mean": 30.0, "std": 6.0},
			},
		},
		Models: models,
	}
}

// augmentForModelFidelity builds the merged output cohort the section
// reads: a small source partition generated from genSpec's fields with
// no models at all, then augmented with newRows rows drawn from genSpec
// itself. Returns the merged schema and record buffer.
//
// genSpec is the spec GENERATION was given. The caller may then hand
// BuildModelFidelity a DIFFERENT spec, which is how the broken-generation
// case below is constructed: a report claiming structure the rows do not
// carry.
func augmentForModelFidelity(t *testing.T, genSpec *synth.Spec, newRows int, seed int64) (*encoding.Schema, []byte) {
	t.Helper()

	sourceSpec := *genSpec
	sourceSpec.Models = nil
	// The residual correlations go with the models: validateSpec refuses
	// a residual correlation naming a field with no model, and the
	// source partition is deliberately generated model-free so the
	// _synthetic rows are the only ones carrying the structure under
	// test.
	sourceSpec.ResidualCorrelations = nil
	sourceSpec.RowCount = 400
	srcData, _, err := synth.SynthBytes(&sourceSpec, synth.Options{Seed: seed})
	if err != nil {
		t.Fatalf("source SynthBytes: %v", err)
	}

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	augSpec := *genSpec
	augSpec.RowCount = newRows
	res, err := synth.AugmentFromProfile(fs, &augSpec, "/source.pulse", "/augmented.pulse", synth.Options{Seed: seed + 1})
	if err != nil {
		t.Fatalf("augment: %v", err)
	}
	data, err := afero.ReadFile(fs, res.OutputPath)
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
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

func findModelFidelity(report *synth.FidelityReport, field string) *synth.ModelFidelity {
	for _, m := range report.Models {
		if m.Field == field {
			return m
		}
	}
	return nil
}

func findPredictorFidelity(m *synth.ModelFidelity, field, level string) *synth.ModelPredictorFidelity {
	for _, p := range m.Predictors {
		if p.Field == field && p.Level == level {
			return p
		}
	}
	return nil
}

// TestBuildModelFidelity_RecoversStrongMultiPredictorModel is the good-
// recovery case: four live terms across three predictor fields and two
// indicator kinds, generated with a real residual, must each come back
// within the flagging band on the latent scale.
//
// The bar is deliberately tighter than synth.ModelRecoveryTolerance
// (0.05 latent sd against the section's own 0.10 flagging floor): a test
// that merely asserted "not flagged" would still pass if recovery were
// twice as noisy as it is, and the point of this fixture is that a
// healthy generation path recovers its structure with room to spare.
func TestBuildModelFidelity_RecoversStrongMultiPredictorModel(t *testing.T) {
	const recoveryBar = 0.05
	const marginalStd = 25.0

	spec := modelFidelitySpec(20000, synth.FieldModelSpec{
		Field:     "spend",
		Intercept: 100,
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", 30),
			catLevel("region", "north", -12),
			catLevel("plan", "pro", 7),
			setOption("features", "premium", 11),
		},
		ResidualStd: 10,
	})
	schema, records := augmentForModelFidelity(t, spec, 20000, 101)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	if len(report.Models) != 1 {
		t.Fatalf("len(Models) = %d, want 1", len(report.Models))
	}
	m := report.Models[0]
	if m.Error != "" {
		t.Fatalf("unexpected Error: %q", m.Error)
	}
	if m.Field != "spend" {
		t.Fatalf("Field = %q, want spend", m.Field)
	}
	if m.Marginal {
		t.Error("Marginal = true for a model carrying four predictors")
	}
	if m.LatentScale != marginalStd {
		t.Errorf("LatentScale = %v, want %v (the target's own marginal std)", m.LatentScale, marginalStd)
	}
	// 20,000 rows were generated but the refit consumes at most
	// modelRecoveryRowCap of them — capture's own snapshot size, so
	// neither side of the comparison is estimated from more rows than
	// the other, and an O(p^2)-per-row accumulator over 105 models stays
	// a footnote to generation rather than the bulk of the run.
	if m.NObs != 10000 {
		t.Errorf("NObs = %d, want 10000 (the recovery row cap; no nulls in this fixture)", m.NObs)
	}
	if math.Abs(m.CapturedIntercept) > 1e-12 {
		t.Errorf("CapturedIntercept = %v, want 0 ((100-100)/25)", m.CapturedIntercept)
	}
	if m.Flagged {
		t.Errorf("Flagged = true on a faithfully generated model (MaxDelta %.4f)", m.MaxDelta)
	}

	want := []struct {
		field, level string
		coef         float64
	}{
		{"region", "east", 30},
		{"region", "north", -12},
		{"plan", "pro", 7},
		{"features", "premium", 11},
	}
	if len(m.Predictors) != len(want) {
		t.Fatalf("len(Predictors) = %d, want %d", len(m.Predictors), len(want))
	}
	for _, w := range want {
		p := findPredictorFidelity(m, w.field, w.level)
		if p == nil {
			t.Fatalf("no predictor entry for %s=%s", w.field, w.level)
		}
		if p.Error != "" {
			t.Fatalf("%s=%s: unexpected Error %q", w.field, w.level, p.Error)
		}
		captured := w.coef / marginalStd
		if math.Abs(p.CapturedCoefficient-captured) > 1e-12 {
			t.Errorf("%s=%s: CapturedCoefficient = %v, want %v (raw %v / std %v)",
				w.field, w.level, p.CapturedCoefficient, captured, w.coef, marginalStd)
		}
		if math.Abs(p.RecoveredCoefficient-captured) > recoveryBar {
			t.Errorf("%s=%s: RecoveredCoefficient = %.4f, captured %.4f — gap %.4f exceeds the %.2f bar",
				w.field, w.level, p.RecoveredCoefficient, captured,
				math.Abs(p.RecoveredCoefficient-captured), recoveryBar)
		}
		if got := math.Abs(p.RecoveredCoefficient - p.CapturedCoefficient); math.Abs(p.Delta-got) > 1e-12 {
			t.Errorf("%s=%s: Delta = %v, want %v", w.field, w.level, p.Delta, got)
		}
		if p.Flagged {
			t.Errorf("%s=%s: Flagged on a %.4f gap", w.field, w.level, p.Delta)
		}
		// A term that never fired would recover a meaningless zero and
		// the assertions above would still pass on a small enough
		// coefficient; NFired is what makes the recovery evidence.
		if p.NFired < 1000 {
			t.Errorf("%s=%s: NFired = %d, too thin for the recovery above to mean anything",
				w.field, w.level, p.NFired)
		}
		if p.StdError <= 0 {
			t.Errorf("%s=%s: StdError = %v, want a positive estimate (it is half the flagging band)",
				w.field, w.level, p.StdError)
		}
	}
}

// TestBuildModelFidelity_ZeroPredictorModelIsMarginal covers the
// intercept-only representation. A model that selection left with no
// predictors is a COMPLETE model — the field's own mean and spread — so
// it must not read as a failure: no Error, no empty predictor
// placeholder, and an intercept comparison that is still a real check.
func TestBuildModelFidelity_ZeroPredictorModelIsMarginal(t *testing.T) {
	spec := modelFidelitySpec(8000, synth.FieldModelSpec{
		Field:       "spend",
		Intercept:   100,
		Predictors:  nil,
		ResidualStd: 25,
	})
	schema, records := augmentForModelFidelity(t, spec, 8000, 111)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	m := findModelFidelity(report, "spend")
	if m == nil {
		t.Fatal("no entry for spend; a zero-predictor model must still be reported")
	}
	if m.Error != "" {
		t.Fatalf("Error = %q; a zero-predictor model is a complete model, not a failure", m.Error)
	}
	if !m.Marginal {
		t.Error("Marginal = false; a zero-predictor model is exactly the marginal")
	}
	if len(m.Predictors) != 0 {
		t.Errorf("len(Predictors) = %d, want 0 — an empty placeholder array reads as failure", len(m.Predictors))
	}
	if m.Flagged {
		t.Error("Flagged = true on a model with nothing to flag")
	}
	if m.NObs != 8000 {
		t.Errorf("NObs = %d, want 8000", m.NObs)
	}
	// Intercept 100 against a marginal mean of 100 is latent zero, and
	// the recovered intercept is the mean latent of the drawn rows. A
	// drifted intercept here would mean the marginal itself did not
	// survive, which is worth catching even with no predictors.
	if math.Abs(m.RecoveredIntercept) > 0.05 {
		t.Errorf("RecoveredIntercept = %.4f, want ~0 (captured %.4f)", m.RecoveredIntercept, m.CapturedIntercept)
	}
	if got := math.Abs(m.RecoveredIntercept - m.CapturedIntercept); math.Abs(m.InterceptDelta-got) > 1e-12 {
		t.Errorf("InterceptDelta = %v, want %v", m.InterceptDelta, got)
	}
}

// TestBuildModelFidelity_UnmodelledFieldIsAbsent pins the two absence
// rules: a numeric carrying no model gets no entry at all (rather than
// an entry with empty values), and a spec carrying no models produces no
// section — which is what keeps a report for a pre-`--fit-models`
// profile byte-identical to what it was.
func TestBuildModelFidelity_UnmodelledFieldIsAbsent(t *testing.T) {
	spec := modelFidelitySpec(4000, synth.FieldModelSpec{
		Field:      "spend",
		Intercept:  100,
		Predictors: []synth.ModelPredictorSpec{catLevel("region", "east", 30)},
	})
	schema, records := augmentForModelFidelity(t, spec, 4000, 121)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)
	if findModelFidelity(report, "tenure") != nil {
		t.Error("tenure carries no model but appears in the recovery section")
	}
	if findModelFidelity(report, "spend") == nil {
		t.Fatal("spend carries a model and must appear")
	}

	modelFree := modelFidelitySpec(4000)
	bare := &synth.FidelityReport{}
	synth.BuildModelFidelity(bare, schema, records, modelFree)
	if len(bare.Models) != 0 {
		t.Fatalf("len(Models) = %d for a spec carrying no models, want 0", len(bare.Models))
	}
	out, err := json.Marshal(bare)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(out, []byte(`"models"`)) {
		t.Errorf("models key present in a report for a models-free spec: %s", out)
	}
}

// TestBuildModelFidelity_BrokenGenerationRecoversBadly is the
// falsification case, and it is the one this section exists for. The
// cohort is generated from a spec whose model carries ZERO coefficients
// — structurally what E2-S5's bug produced, a model that was captured
// and then applied to nothing — and the report is then built against a
// spec claiming a strong relationship.
//
// Every marginal in such a cohort is healthy, and every other section of
// the fidelity report would say so. This section must not.
func TestBuildModelFidelity_BrokenGenerationRecoversBadly(t *testing.T) {
	const marginalStd = 25.0

	inert := modelFidelitySpec(8000, synth.FieldModelSpec{
		Field:     "spend",
		Intercept: 100,
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", 0),
			catLevel("plan", "pro", 0),
		},
		ResidualStd: 10,
	})
	schema, records := augmentForModelFidelity(t, inert, 8000, 131)

	claimed := modelFidelitySpec(8000, synth.FieldModelSpec{
		Field:     "spend",
		Intercept: 100,
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", 30),
			catLevel("plan", "pro", 7),
		},
		ResidualStd: 10,
	})

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, claimed)

	m := findModelFidelity(report, "spend")
	if m == nil {
		t.Fatal("no entry for spend")
	}
	if m.Error != "" {
		t.Fatalf("unexpected Error: %q — the refit itself must succeed; it is the RESULT that is bad", m.Error)
	}
	if !m.Flagged {
		t.Errorf("Flagged = false on a cohort carrying none of the claimed structure (MaxDelta %.4f)", m.MaxDelta)
	}

	east := findPredictorFidelity(m, "region", "east")
	if east == nil {
		t.Fatal("no entry for region=east")
	}
	if !east.Flagged {
		t.Errorf("region=east not flagged: captured %.4f, recovered %.4f, delta %.4f, se %.4f",
			east.CapturedCoefficient, east.RecoveredCoefficient, east.Delta, east.StdError)
	}
	// Recovery must land near ZERO — the rows carry no such relationship
	// — while the captured figure stays 30/25. That gap is the whole
	// signal, and it is invisible to every other section of the report.
	if math.Abs(east.RecoveredCoefficient) > 0.05 {
		t.Errorf("region=east: RecoveredCoefficient = %.4f, want ~0 for a relationship generation never applied",
			east.RecoveredCoefficient)
	}
	if math.Abs(east.Delta-30.0/marginalStd) > 0.05 {
		t.Errorf("region=east: Delta = %.4f, want ~%.4f (the whole captured coefficient)",
			east.Delta, 30.0/marginalStd)
	}
	if east.NFired < 1000 {
		t.Fatalf("region=east: NFired = %d — the term must actually fire, or the zero recovery is trivially explained",
			east.NFired)
	}
}

// TestBuildModelFidelity_UnrealizedLevelReportsNoFabricatedDelta is the
// v0.32.2 criterion at the predictor level: a term whose level the
// generated cohort never carries must be reported as unestimable, not
// as a recovered zero sitting beside a captured coefficient. The second
// reads as a measured failure; the first is the truth.
func TestBuildModelFidelity_UnrealizedLevelReportsNoFabricatedDelta(t *testing.T) {
	spec := modelFidelitySpec(4000, synth.FieldModelSpec{
		Field:     "spend",
		Intercept: 100,
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", 30),
			// "other" is the top-K catch-all's on-wire spelling. It is a
			// real captured term, and the reconstructed `region` marginal
			// has no such level to generate, so no row can ever carry it.
			catLevel("region", "other", 44),
		},
		ResidualStd: 5,
	})
	schema, records := augmentForModelFidelity(t, spec, 4000, 141)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	m := findModelFidelity(report, "spend")
	if m == nil {
		t.Fatal("no entry for spend")
	}
	other := findPredictorFidelity(m, "region", "other")
	if other == nil {
		t.Fatal("the unrealized term must still be listed — a reader has to see it exists")
	}
	if other.Error == "" {
		t.Error("unrealized term carries no Error; it would read as a measured recovery")
	}
	if other.RecoveredCoefficient != 0 || other.Delta != 0 || other.Flagged {
		t.Errorf("unrealized term carries computed-looking figures: recovered=%v delta=%v flagged=%v",
			other.RecoveredCoefficient, other.Delta, other.Flagged)
	}
	if other.NFired != 0 {
		t.Errorf("NFired = %d for a level no row can carry", other.NFired)
	}
	if other.CapturedCoefficient == 0 {
		t.Error("the captured coefficient must still be reported — the term was captured, it just could not be checked")
	}

	// The rest of the model is unaffected: one unestimable term must not
	// cost the others their estimate, which is exactly why a constant
	// column is dropped rather than handed to the solver.
	east := findPredictorFidelity(m, "region", "east")
	if east == nil || east.Error != "" {
		t.Fatalf("region=east lost its estimate to a sibling term's absence: %+v", east)
	}
	if math.Abs(east.RecoveredCoefficient-30.0/25.0) > 0.05 {
		t.Errorf("region=east: RecoveredCoefficient = %.4f, want ~%.4f", east.RecoveredCoefficient, 30.0/25.0)
	}
}

// TestBuildModelFidelity_ReportsOnlyAppliedModels pins that the section
// asks GENERATION's own compiler which models ran rather than reading
// Spec.Models verbatim. A model whose target is not a declared field is
// warned-and-skipped by buildModelDrawers at generation time; reporting a
// recovery for it would be scoring a relationship that was never applied.
func TestBuildModelFidelity_ReportsOnlyAppliedModels(t *testing.T) {
	applied := synth.FieldModelSpec{
		Field:      "spend",
		Intercept:  100,
		Predictors: []synth.ModelPredictorSpec{catLevel("region", "east", 30)},
	}
	schema, records := augmentForModelFidelity(t, modelFidelitySpec(3000, applied), 3000, 151)

	// The ghost model is added only on the REPORT's spec: validateSpec
	// refuses it up front for a hand-authored spec, but SpecFromProfile
	// bypasses that gate, so an undeclared target is reachable in
	// production and is exactly one of buildModelDrawers' two
	// warn-and-skip refusals.
	withGhost := modelFidelitySpec(3000, applied, synth.FieldModelSpec{
		Field:      "ghost",
		Intercept:  5,
		Predictors: []synth.ModelPredictorSpec{catLevel("region", "east", 2)},
	})

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, withGhost)

	if len(report.Models) != 1 {
		t.Fatalf("len(Models) = %d, want 1 — the undeclared target's model was never applied", len(report.Models))
	}
	if report.Models[0].Field != "spend" {
		t.Errorf("Models[0].Field = %q, want spend", report.Models[0].Field)
	}
}

// TestBuildModelFidelity_ShapeFittedTargetRecoversOnLatentScale is the
// like-for-like criterion's own falsification. The target is a captured
// two-component Gaussian mixture (`--fit-shape`), for which the map from
// latent to value is emphatically non-linear, so the term's VALUE-space
// effect and its LATENT coefficient are different numbers.
//
// Generation here is exactly correct, and the section must say so. A
// recovery built by regressing the raw generated values — the obvious
// implementation, and the trap — would compare a value-space contrast
// against a latent coefficient and report a large gap on a healthy path.
// The test asserts both halves: the latent recovery lands, and the raw
// contrast really is far enough away that the distinction is load-bearing
// rather than a theoretical nicety.
func TestBuildModelFidelity_ShapeFittedTargetRecoversOnLatentScale(t *testing.T) {
	const eastCoef = 40.0
	const recoveryBar = 0.05

	spec := modelFidelitySpec(12000, synth.FieldModelSpec{
		Field:     "spend",
		Intercept: 90, // the mixture's own mean, so the latent intercept is 0
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", eastCoef),
		},
		ResidualStd: 30,
	})
	// Replace spend's marginal with a captured shape. Everything else in
	// the fixture, including the model above, is unchanged.
	for i := range spec.Fields {
		if spec.Fields[i].Name != "spend" {
			continue
		}
		spec.Fields[i].Distribution = synth.DistMixture
		spec.Fields[i].Params = map[string]any{
			"means":   []any{50.0, 150.0},
			"stds":    []any{10.0, 20.0},
			"weights": []any{0.6, 0.4},
		}
	}
	schema, records := augmentForModelFidelity(t, spec, 12000, 161)

	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	m := findModelFidelity(report, "spend")
	if m == nil {
		t.Fatal("no entry for spend")
	}
	if m.Error != "" {
		t.Fatalf("unexpected Error: %q", m.Error)
	}
	east := findPredictorFidelity(m, "region", "east")
	if east == nil || east.Error != "" {
		t.Fatalf("region=east missing or unestimated: %+v", east)
	}
	captured := eastCoef / m.LatentScale
	if math.Abs(east.CapturedCoefficient-captured) > 1e-12 {
		t.Errorf("CapturedCoefficient = %v, want %v", east.CapturedCoefficient, captured)
	}
	if math.Abs(east.RecoveredCoefficient-captured) > recoveryBar {
		t.Errorf("RecoveredCoefficient = %.4f, captured %.4f — gap %.4f exceeds %.2f on a correct generation path",
			east.RecoveredCoefficient, captured, east.Delta, recoveryBar)
	}
	if m.Flagged {
		t.Errorf("Flagged on a faithfully generated shape-fitted target (MaxDelta %.4f)", m.MaxDelta)
	}

	// The value-space contrast, measured directly off the generated rows.
	// This is the number a raw-value refit would have recovered.
	eastMean, restMean := syntheticMeanByLevel(t, schema, records, "spend", "region", "east")
	rawContrast := eastMean - restMean
	if math.Abs(rawContrast-eastCoef) < 10 {
		t.Fatalf("value-space contrast %.2f is within 10 of the coefficient %.2f — this fixture no longer distinguishes the two scales, so it cannot falsify a raw-value implementation",
			rawContrast, eastCoef)
	}
}

// syntheticMeanByLevel returns the mean of numeric field `num` over the
// _synthetic partition, split by whether categorical field `cat` holds
// `level`. Used only to demonstrate the value-space/latent gap above.
func syntheticMeanByLevel(t *testing.T, schema *encoding.Schema, records []byte, num, cat, level string) (atLevel, elsewhere float64) {
	t.Helper()
	f := schema.Field(cat)
	if f == nil || f.Dictionary == nil {
		t.Fatalf("field %q has no dictionary", cat)
	}
	id, ok := f.Dictionary.IDFor(level)
	if !ok {
		t.Fatalf("level %q not in %q's dictionary", level, cat)
	}
	var sumA, sumB float64
	var nA, nB int
	rr := encoding.NewRecordReader(bytes.NewReader(records), schema)
	values := make(map[string]float64, len(schema.Fields))
	nulls := make(map[string]bool, len(schema.Fields))
	for {
		if err := rr.ReadRecordWithWide(values, nulls, nil); err != nil {
			break
		}
		if values["_synthetic"] == 0 {
			continue
		}
		if uint32(values[cat]) == id {
			sumA += values[num]
			nA++
		} else {
			sumB += values[num]
			nB++
		}
	}
	if nA == 0 || nB == 0 {
		t.Fatalf("split produced %d/%d rows", nA, nB)
	}
	return sumA / float64(nA), sumB / float64(nB)
}

// TestBuildModelFidelity_IsByteReproducible holds the section to the
// same bar E2-S4 imposed on profile capture: the same inputs must
// produce the same document, to the last bit.
//
// The threat is not the RNG — this section draws nothing — but the
// float folds inside it. An accumulation walked in Go map order drifts
// in its last digits between runs over identical inputs, and a fidelity
// figure that moves when nothing moved is unusable for exactly the
// regression-gating this report exists to support. Everything here folds
// in row order and reads coefficients out of the result map by name, and
// this test is what keeps that true.
func TestBuildModelFidelity_IsByteReproducible(t *testing.T) {
	spec := modelFidelitySpec(6000, synth.FieldModelSpec{
		Field:     "spend",
		Intercept: 100,
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", 30),
			catLevel("plan", "pro", 7),
			setOption("features", "premium", 11),
		},
		ResidualStd: 10,
	})
	schema, records := augmentForModelFidelity(t, spec, 6000, 171)

	var renders [2][]byte
	for i := range renders {
		report := &synth.FidelityReport{}
		synth.BuildModelFidelity(report, schema, records, spec)
		out, err := json.Marshal(report.Models)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		renders[i] = out
	}
	if !bytes.Equal(renders[0], renders[1]) {
		t.Errorf("two builds over identical inputs differ:\n%s\n%s", renders[0], renders[1])
	}
}
