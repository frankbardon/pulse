package synth_test

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// E1-S4 test pack: the composed model draw in drawRow.
//
// Every assertion on a VALUE here runs with ResidualStd == 0, which
// makes the composed draw deterministic per row: for a `normal` target
// the construction Q(Phi(mu + sigma*z)) collapses to
// prediction + ResidualStd*z (see synth/model_draw.go), so a zero
// residual leaves exactly the linear prediction and the test can assert
// the arithmetic rather than a distribution. The residual half is
// asserted separately and statistically by
// TestSynthModel_NormalTargetReducesToPredictionPlusResidual, which is
// the sanity anchor for that identity.
//
// The "an old profile still generates the same cohort" criterion is NOT
// re-asserted here: TestSpecFromProfile_PreModelsDocumentUnchanged
// already pins testdata/profile_pre_models.json to a fixed Spec.Hash(),
// and generation is a pure function of (Spec, Seed), so an unmoved Spec
// is an unmoved cohort. That was verified directly for THIS change as
// well — `git archive` of the pre-change commit, fed the same fixture at
// seed 11, produced sha256 19162c6d…39e25c, and the tree with the model
// stage in it produces the identical bytes. The byte hash is
// deliberately not pinned in code for the reason recorded on
// preModelsSpecHash: math.Exp / math.Log are architecture-specific in
// the standard library, so a cross-machine byte pin would be a flaky
// gate rather than a contract. TestSynthModel_StageInertWithoutDrawers
// below is the in-tree structural form of the same property.
//
// The composed value is compared with a tolerance rather than exactly
// because it really does make the copula round trip — the standardise
// (prediction-mean)/std, the Phi, the Q — and floating point does not
// promise mean + std*((p-mean)/std) is bit-equal to p. Determinism is
// unaffected: the same computation over the same inputs is bit-stable,
// which is what the byte-identity tests below assert.

const modelValueTolerance = 1e-9

// modelDrawSpec builds a small cohort spec for the model tests:
// `region` (3 levels) and `plan` (2 levels) categoricals, a `features`
// set_* with two options, and the `spend` numeric a model targets.
// clampMin/clampMax are written onto the MODEL (FieldModelSpec), never
// onto the numeric's own params, so the tests exercise the model's own
// carried bounds — the clamp buildModelDrawers prefers.
func modelDrawSpec(rows int, model synth.FieldModelSpec) *synth.Spec {
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
		},
		Models: []synth.FieldModelSpec{model},
	}
}

func catLevel(field, level string, coef float64) synth.ModelPredictorSpec {
	return synth.ModelPredictorSpec{
		Kind: synth.ModelPredictorCategoricalLevel, Field: field, Level: level, Coefficient: coef,
	}
}

func setOption(field, option string, coef float64) synth.ModelPredictorSpec {
	return synth.ModelPredictorSpec{
		Kind: synth.ModelPredictorSetOption, Field: field, Level: option, Coefficient: coef,
	}
}

// TestSynthModel_SinglePredictorAndUnseenLevel covers the two halves of
// the dummy-coded reading in one fixture: a level the model carries a
// coefficient for contributes it, and a level it does NOT carry —
// whether because that level was the fit's dropped reference ("west")
// or because generation drew a level the fit never saw at all
// ("north") — contributes zero and lands on the intercept.
func TestSynthModel_SinglePredictorAndUnseenLevel(t *testing.T) {
	spec := modelDrawSpec(600, synth.FieldModelSpec{
		Field:      "spend",
		Intercept:  100,
		Predictors: []synth.ModelPredictorSpec{catLevel("region", "east", 30)},
	})

	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	regions := readCategoricalField(t, data, "region")
	spend := readField(t, data, "spend")
	if len(regions) != 600 || len(spend) != 600 {
		t.Fatalf("decoded %d regions / %d spend, want 600 each", len(regions), len(spend))
	}

	seen := map[string]int{}
	for i, r := range regions {
		want := 100.0
		if r == "east" {
			want = 130.0
		}
		if math.Abs(spend[i]-want) > modelValueTolerance {
			t.Fatalf("row %d region %q: spend = %v, want %v", i, r, spend[i], want)
		}
		seen[r]++
	}
	for _, level := range []string{"east", "west", "north"} {
		if seen[level] == 0 {
			t.Fatalf("fixture never drew region %q; it cannot have tested that arm", level)
		}
	}
}

// TestSynthModel_PredictorsSum is the "mu sums every selected
// predictor's contribution" criterion: two categorical arms from
// different fields plus a set-option indicator, all live on the same
// row, must add.
func TestSynthModel_PredictorsSum(t *testing.T) {
	spec := modelDrawSpec(800, synth.FieldModelSpec{
		Field:     "spend",
		Intercept: 100,
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", 30),
			catLevel("region", "north", -12),
			catLevel("plan", "pro", 7),
			setOption("features", "premium", 11),
			setOption("features", "trial", -3),
		},
	})

	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 19})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	regions := readCategoricalField(t, data, "region")
	plans := readCategoricalField(t, data, "plan")
	_, features, _ := readSetFieldRows(t, data, "features")
	spend := readField(t, data, "spend")

	combos := map[string]bool{}
	for i := range spend {
		want := 100.0
		switch regions[i] {
		case "east":
			want += 30
		case "north":
			want -= 12
		}
		if plans[i] == "pro" {
			want += 7
		}
		if hasLabel(features[i], "premium") {
			want += 11
		}
		if hasLabel(features[i], "trial") {
			want -= 3
		}
		if math.Abs(spend[i]-want) > modelValueTolerance {
			t.Fatalf("row %d (%s/%s/%v): spend = %v, want %v",
				i, regions[i], plans[i], features[i], spend[i], want)
		}
		combos[regions[i]+"|"+plans[i]] = true
	}
	// Six region x plan combinations exist; a fixture that only drew one
	// would pass the arithmetic above while testing nothing about the sum.
	if len(combos) != 6 {
		t.Fatalf("fixture exercised %d region/plan combinations, want all 6", len(combos))
	}
}

// TestSynthModel_ClampsAtBothBounds is the explicit clamping criterion.
// The model carries bounds and coefficients large enough to drive the
// prediction past both of them, so every value must land inside
// [Min, Max] AND both bounds must actually be hit — a clamp that never
// engages proves nothing.
func TestSynthModel_ClampsAtBothBounds(t *testing.T) {
	spec := modelDrawSpec(600, synth.FieldModelSpec{
		Field:     "spend",
		Intercept: 100,
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", 10_000),
			catLevel("region", "north", -10_000),
		},
		Min: 90, Max: 110, HasClamp: true,
	})

	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 23})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	regions := readCategoricalField(t, data, "region")
	spend := readField(t, data, "spend")

	var hitLow, hitHigh int
	for i, v := range spend {
		if v < 90-modelValueTolerance || v > 110+modelValueTolerance {
			t.Fatalf("row %d region %q: spend = %v escaped [90, 110]", i, regions[i], v)
		}
		switch regions[i] {
		case "east":
			if math.Abs(v-110) > modelValueTolerance {
				t.Fatalf("row %d: east should clamp to the upper bound, got %v", i, v)
			}
			hitHigh++
		case "north":
			if math.Abs(v-90) > modelValueTolerance {
				t.Fatalf("row %d: north should clamp to the lower bound, got %v", i, v)
			}
			hitLow++
		default:
			if math.Abs(v-100) > modelValueTolerance {
				t.Fatalf("row %d: reference level should be the intercept, got %v", i, v)
			}
		}
	}
	if hitLow == 0 || hitHigh == 0 {
		t.Fatalf("clamp never engaged at both ends (low %d, high %d)", hitLow, hitHigh)
	}
}

// TestSynthModel_MarginalClampAppliesWhenModelDeclaresNone pins the
// other half of the clamp decision: with no model bounds, the target's
// OWN reconstructed marginal clamp (normal's min/max params, the one
// fieldMoments reads and correlator.transform applies) is the fallback,
// so a modelled field and a correlated field cannot disagree about the
// field's admissible range.
func TestSynthModel_MarginalClampAppliesWhenModelDeclaresNone(t *testing.T) {
	spec := modelDrawSpec(400, synth.FieldModelSpec{
		Field:      "spend",
		Intercept:  100,
		Predictors: []synth.ModelPredictorSpec{catLevel("region", "east", 10_000)},
	})
	for i := range spec.Fields {
		if spec.Fields[i].Name == "spend" {
			spec.Fields[i].Params["min"] = 0.0
			spec.Fields[i].Params["max"] = 150.0
		}
	}

	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 29})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	regions := readCategoricalField(t, data, "region")
	spend := readField(t, data, "spend")
	var clamped int
	for i, v := range spend {
		if v < 0 || v > 150+modelValueTolerance {
			t.Fatalf("row %d: spend = %v escaped the marginal clamp [0, 150]", i, v)
		}
		if regions[i] == "east" {
			if math.Abs(v-150) > modelValueTolerance {
				t.Fatalf("row %d: east should clamp to the marginal max, got %v", i, v)
			}
			clamped++
		}
	}
	if clamped == 0 {
		t.Fatal("the marginal clamp never engaged")
	}
}

// TestSynthModel_NullPredictorContributesZero pins the listwise-deletion
// reading: the fit never saw a row with a null in this predictor, so its
// coefficient says nothing about such a row and the term does not fire.
func TestSynthModel_NullPredictorContributesZero(t *testing.T) {
	spec := modelDrawSpec(300, synth.FieldModelSpec{
		Field:      "spend",
		Intercept:  100,
		Predictors: []synth.ModelPredictorSpec{catLevel("region", "east", 30)},
	})
	for i := range spec.Fields {
		if spec.Fields[i].Name == "region" {
			spec.Fields[i].Nullable = true
			spec.Fields[i].NullRate = 1
		}
	}

	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 31})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if got := readNullCountForField(t, data, "region"); got != 300 {
		t.Fatalf("fixture produced %d null regions, want 300", got)
	}
	for i, v := range readField(t, data, "spend") {
		if math.Abs(v-100) > modelValueTolerance {
			t.Fatalf("row %d: a null predictor contributed; spend = %v, want the intercept 100", i, v)
		}
	}
}

// TestSynthModel_NormalTargetReducesToPredictionPlusResidual is the
// sanity anchor named in the story: for a plain `normal` target,
// Q(Phi(u)) == mean + std*u exactly by construction, so the whole copula
// round trip must reduce to prediction + ResidualStd*z. Asserted
// statistically because z is a real draw: each level's realised mean
// must sit on its own prediction and each level's realised standard
// deviation on ResidualStd — NOT on the field's own marginal std of 25,
// which is what a construction that forgot to standardise would produce.
func TestSynthModel_NormalTargetReducesToPredictionPlusResidual(t *testing.T) {
	const rows = 60_000
	spec := modelDrawSpec(rows, synth.FieldModelSpec{
		Field:       "spend",
		Intercept:   100,
		Predictors:  []synth.ModelPredictorSpec{catLevel("region", "east", 30)},
		ResidualStd: 5,
	})

	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 41})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	regions := readCategoricalField(t, data, "region")
	spend := readField(t, data, "spend")

	sum := map[string]float64{}
	sumSq := map[string]float64{}
	n := map[string]float64{}
	for i, r := range regions {
		key := "reference"
		if r == "east" {
			key = "east"
		}
		sum[key] += spend[i]
		sumSq[key] += spend[i] * spend[i]
		n[key]++
	}
	for key, want := range map[string]float64{"east": 130, "reference": 100} {
		mean := sum[key] / n[key]
		variance := sumSq[key]/n[key] - mean*mean
		std := math.Sqrt(variance)
		if math.Abs(mean-want) > 0.2 {
			t.Errorf("%s: realised mean %.4f, want the prediction %.1f", key, mean, want)
		}
		if math.Abs(std-5) > 0.2 {
			t.Errorf("%s: realised std %.4f, want ResidualStd 5 (the marginal std 25 means the prediction was not standardised)", key, std)
		}
	}
}

// TestSynthModel_DeterministicByteIdentical is the determinism contract
// for the new stage: same spec + same seed, byte-identical file; a
// different seed, a different file.
func TestSynthModel_DeterministicByteIdentical(t *testing.T) {
	spec := modelDrawSpec(500, synth.FieldModelSpec{
		Field:     "spend",
		Intercept: 100,
		Predictors: []synth.ModelPredictorSpec{
			catLevel("region", "east", 30),
			catLevel("plan", "pro", 7),
			setOption("features", "premium", 11),
		},
		ResidualStd: 4,
		Min:         0, Max: 500, HasClamp: true,
	})

	first, _, err := synth.SynthBytes(spec, synth.Options{Seed: 5})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	second, _, err := synth.SynthBytes(spec, synth.Options{Seed: 5})
	if err != nil {
		t.Fatalf("SynthBytes (repeat): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("same spec + same seed produced different bytes")
	}
	other, _, err := synth.SynthBytes(spec, synth.Options{Seed: 6})
	if err != nil {
		t.Fatalf("SynthBytes (other seed): %v", err)
	}
	if bytes.Equal(first, other) {
		t.Fatal("different seeds produced identical bytes")
	}
}

// TestSynthModel_DrawOrderIsSchemaOrderNotSliceOrder pins the explicit
// ordering rule. Two models drawing two different numerics consume one
// residual draw each per row, in SCHEMA field order — so permuting the
// `models` slice, which is the one ordering a caller controls and the
// one a map-derived construction would leak, must not move a single
// byte.
func TestSynthModel_DrawOrderIsSchemaOrderNotSliceOrder(t *testing.T) {
	build := func(order []int) *synth.Spec {
		models := []synth.FieldModelSpec{
			{
				Field: "spend", Intercept: 100, ResidualStd: 3,
				Predictors: []synth.ModelPredictorSpec{catLevel("region", "east", 30)},
			},
			{
				Field: "visits", Intercept: 12, ResidualStd: 2,
				Predictors: []synth.ModelPredictorSpec{catLevel("plan", "pro", 4)},
			},
		}
		s := modelDrawSpec(400, models[order[0]])
		s.Fields = append(s.Fields, synth.FieldSpec{
			Name: "visits", Type: "f64",
			Distribution: synth.DistNormal,
			Params:       map[string]any{"mean": 12.0, "std": 6.0},
		})
		s.Models = []synth.FieldModelSpec{models[order[0]], models[order[1]]}
		return s
	}

	forward, _, err := synth.SynthBytes(build([]int{0, 1}), synth.Options{Seed: 13})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	reversed, _, err := synth.SynthBytes(build([]int{1, 0}), synth.Options{Seed: 13})
	if err != nil {
		t.Fatalf("SynthBytes (reversed): %v", err)
	}
	if !bytes.Equal(forward, reversed) {
		t.Fatal("permuting Spec.Models moved the output; draw order is not schema-derived")
	}
}

// TestSynthModel_StageInertWithoutDrawers is the models-free
// byte-identity property stated structurally: a model stage that
// compiles no drawers must consume no RNG state at all, so its presence
// is invisible in the output. That is the same reason a spec carrying no
// `models` key reproduces its pre-change bytes exactly.
//
// The zero-drawer state is reached through buildModelDrawers' other
// warn-and-skip: a target whose reconstructed marginal has no finite
// scale to standardise the prediction against. A lognormal with a large
// sigma is the reachable form — its analytic std overflows to +Inf — and
// it is a legal spec, so validateSpec lets it through to the place that
// refuses it by name.
//
// It used to be reached through the captured-shape pre-claim taking the
// model's target; E4-S1 retired that exclusivity, so a shape-fitted
// target now compiles a drawer like any other and is no longer a route
// to zero.
func TestSynthModel_StageInertWithoutDrawers(t *testing.T) {
	build := func(withModel bool) *synth.Spec {
		s := modelDrawSpec(300, synth.FieldModelSpec{
			Field:      "spend",
			Intercept:  100,
			Predictors: []synth.ModelPredictorSpec{catLevel("region", "east", 30)},
		})
		for i := range s.Fields {
			if s.Fields[i].Name == "spend" {
				s.Fields[i].Distribution = synth.DistLogNormal
				s.Fields[i].Params = map[string]any{"mu": 0.0, "sigma": 30.0}
			}
		}
		if !withModel {
			s.Models = nil
		}
		return s
	}

	plain, _, err := synth.SynthBytes(build(false), synth.Options{Seed: 3})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	got, _, err := synth.SynthBytes(build(true), synth.Options{Seed: 3})
	if err != nil {
		t.Fatalf("SynthBytes (dropped model): %v", err)
	}
	if !bytes.Equal(plain, got) {
		t.Fatal("a model that compiled to nothing still perturbed the RNG stream")
	}
}

// TestSynthModel_RetiresNumericTargetPairStages is PRD FR-20: no
// parallel resample survives for a modelled field. Both a
// categorical-numeric and a set-numeric pair name `spend`; both must be
// dropped with a warning, and every row must carry the model's own
// value rather than either pair's conditional draw.
func TestSynthModel_RetiresNumericTargetPairStages(t *testing.T) {
	spec := modelDrawSpec(400, synth.FieldModelSpec{
		Field:      "spend",
		Intercept:  100,
		Predictors: []synth.ModelPredictorSpec{catLevel("region", "east", 30)},
	})
	spec.CategoricalNumericPairs = []synth.CategoricalNumericPairSpec{{
		A: "region", B: "spend",
		Categories: []synth.CategoricalNumericCategorySpec{
			{Category: "east", Mean: -5000, Std: 1},
			{Category: "west", Mean: -5000, Std: 1},
			{Category: "north", Mean: -5000, Std: 1},
		},
	}}
	spec.SetNumericPairs = []synth.SetNumericPairSpec{{
		Set: "features", Option: "premium", Numeric: "spend",
		Categories: []synth.CategoricalNumericCategorySpec{
			{Category: "selected", Mean: 9000, Std: 1},
			{Category: "not_selected", Mean: 9000, Std: 1},
		},
	}}

	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 17})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	regions := readCategoricalField(t, data, "region")
	for i, v := range readField(t, data, "spend") {
		want := 100.0
		if regions[i] == "east" {
			want = 130.0
		}
		if math.Abs(v-want) > modelValueTolerance {
			t.Fatalf("row %d: a retired pair stage still wrote spend = %v, want %v", i, v, want)
		}
	}
	for _, want := range []string{
		"dropping categorical-numeric pair (region -> spend)",
		"dropping set-numeric pair (features.premium -> spend)",
	} {
		if !warningsContain(res.Warnings, want) {
			t.Errorf("missing %q in warnings %v", want, res.Warnings)
		}
	}
}

// TestSynthModel_ValueCorrelationNamingModelledFieldIsRefusedPermanently
// replaces E1-S4's TestSynthModel_CorrelationNamingModelledFieldWarnsAs
// Interim, which pinned the wording of a notice that said the exclusion
// was temporary and correlated residuals had not landed. They have
// (E3-S2, Spec.ResidualCorrelations), and the notice would now be a
// false statement, so it is gone rather than relaxed.
//
// What survives is the half that was never interim: a model OWNS its
// field, so the VALUE-scale copula must not overwrite a modelled draw —
// and the reason it must not is now permanent and stateable. A
// value-scale correlation between two fields sharing predictors already
// contains those predictors' joint effect, so it can be neither applied
// to the value (which would delete the model) nor rerouted into the
// residual (which would apply the shared predictors twice). The warning
// must therefore say the request is NOT APPLIED and name the surface
// that does correlate a modelled field, and it must still not be a
// "conditional relationship conflict" — see
// TestSpecFromProfile_ModelDrivenProfileRaisesNoNumericTargetConflicts,
// which reads that prefix to classify arbitration losses.
func TestSynthModel_ValueCorrelationNamingModelledFieldIsRefusedPermanently(t *testing.T) {
	spec := modelDrawSpec(400, synth.FieldModelSpec{
		Field:      "spend",
		Intercept:  100,
		Predictors: []synth.ModelPredictorSpec{catLevel("region", "east", 30)},
	})
	spec.Fields = append(spec.Fields, synth.FieldSpec{
		Name: "visits", Type: "f64",
		Distribution: synth.DistNormal,
		Params:       map[string]any{"mean": 12.0, "std": 6.0},
	})
	spec.Correlations = []synth.CorrelationSpec{{A: "spend", B: "visits", Correlation: 0.8}}

	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 37})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	regions := readCategoricalField(t, data, "region")
	for i, v := range readField(t, data, "spend") {
		want := 100.0
		if regions[i] == "east" {
			want = 130.0
		}
		if math.Abs(v-want) > modelValueTolerance {
			t.Fatalf("row %d: the copula overwrote a modelled field; spend = %v, want %v", i, v, want)
		}
	}
	if !warningsContain(res.Warnings, "is not applied") {
		t.Fatalf("the refused correlation was silent; warnings = %v", res.Warnings)
	}
	if !warningsContain(res.Warnings, "--residual-correlations") {
		t.Fatalf("the warning does not name the surface that DOES correlate a modelled field; warnings = %v", res.Warnings)
	}
	// The interim wording must not come back under a new coat of paint:
	// a reader told the exclusion is temporary will wait for a release
	// that is never coming.
	for _, stale := range []string{"not yet honoured", "still independent"} {
		if warningsContain(res.Warnings, stale) {
			t.Fatalf("the warning still reads as an interim state (%q); warnings = %v", stale, res.Warnings)
		}
	}
	if warningsContain(res.Warnings, "conditional relationship conflict") {
		t.Fatalf("the refusal was reported as an arbitration conflict; warnings = %v", res.Warnings)
	}
}

// The E4-S1 boundary this file used to pin — TestSynthModel_
// CapturedShapeOutranksModel, "a --fit-shape target displaces its model
// and the drop is reported" — is GONE, not moved: E4-S1 retired the
// exclusivity it asserted. Shape and conditioning now compose, and the
// replacement pack lives in model_draw_shape_test.go, which asserts the
// two halves that exclusivity was standing in for (the fitted marginal
// survives; the conditioning is actually applied) in a single test.

// TestSynthModel_UnsupportedPredictorKindRefuses: generation cannot
// evaluate a numeric predictor term, and contributing zero for it would
// publish a cohort carrying a relationship nothing applied.
func TestSynthModel_UnsupportedPredictorKindRefuses(t *testing.T) {
	spec := modelDrawSpec(50, synth.FieldModelSpec{
		Field:     "spend",
		Intercept: 100,
		Predictors: []synth.ModelPredictorSpec{
			{Kind: synth.ModelPredictorNumeric, Field: "region", Coefficient: 3},
		},
	})
	if _, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1}); err == nil {
		t.Fatal("expected a refusal for an unevaluable predictor kind")
	} else if !strings.Contains(err.Error(), "cannot be evaluated at generation time") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func warningsContain(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
