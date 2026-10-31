package synth

import (
	"math"
	"strings"
	"testing"
)

// numericDrawSpec declares `spend` BEFORE `visits` and models spend on
// visits — a numeric predictor declared AFTER its target, the order a
// capture never emits but a hand-authored spec may. visits is itself
// modelled (on region), so the draw is only right if visits' drawer
// runs first: otherwise spend reads visits' marginal draw, which the
// model stage then overwrites.
//
// visits is a u8, drawn from a normal, so its sampler draws are
// non-integers that the file stores rounded; spend's term must read the
// STORED value.
func numericDrawSpec(rows int, spendResidual float64) *Spec {
	return &Spec{
		RowCount: rows,
		Fields: []FieldSpec{
			{
				Name: "region", Type: "categorical_u8",
				Distribution: DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"east", "west"},
					"weights": []any{1.0, 1.0},
				},
			},
			{
				Name: "spend", Type: "f64",
				Distribution: DistNormal,
				Params:       map[string]any{"mean": 100.0, "std": 25.0},
			},
			{
				Name: "visits", Type: "u8",
				Distribution: DistNormal,
				Params:       map[string]any{"mean": 20.0, "std": 5.0},
			},
		},
		Models: []FieldModelSpec{
			{
				Field: "spend", Intercept: 10, ResidualStd: spendResidual,
				Predictors: []ModelPredictorSpec{{
					Kind: ModelPredictorNumeric, Field: "visits", Coefficient: 4,
				}},
			},
			{
				Field: "visits", Intercept: 15, ResidualStd: 3,
				Predictors: []ModelPredictorSpec{{Kind: ModelPredictorCategoricalLevel, Field: "region", Level: "west", Coefficient: 10}},
				Min:        0, Max: 255, HasClamp: true,
			},
		},
	}
}

// TestSynthModel_NumericPredictorReadsTheGeneratedValue asserts the
// composed draw for a numeric term: with no residual, every row's
// spend is exactly intercept + coefficient × the visits value the
// cohort stores. It fails if visits' drawer runs after spend's (spend
// would read the marginal draw the model then overwrote) and if the
// term reads the sampler's unrounded draw instead of the stored u8.
func TestSynthModel_NumericPredictorReadsTheGeneratedValue(t *testing.T) {
	t.Parallel()

	data, _, err := SynthBytes(numericDrawSpec(400, 0), Options{Seed: 3})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	nums, _ := decodeCohort(t, data)
	spend, visits := nums["spend"], nums["visits"]
	if len(spend) != 400 || len(visits) != 400 {
		t.Fatalf("decoded %d spend / %d visits rows, want 400", len(spend), len(visits))
	}
	for i := range spend {
		want := 10 + 4*visits[i]
		if math.Abs(spend[i]-want) > 1e-9*math.Max(1, math.Abs(want)) {
			t.Fatalf("row %d: spend = %v, want 10 + 4×visits(%v) = %v", i, spend[i], visits[i], want)
		}
	}
}

// TestSynthModel_NumericPredictorCycleRefuses pins the refusal for a
// hand-authored cycle: two models each reading the other have no draw
// order at all.
func TestSynthModel_NumericPredictorCycleRefuses(t *testing.T) {
	t.Parallel()

	spec := numericDrawSpec(50, 1)
	spec.Models[1].Predictors = append(spec.Models[1].Predictors, ModelPredictorSpec{
		Kind: ModelPredictorNumeric, Field: "spend", Coefficient: 0.1,
	})
	_, _, err := SynthBytes(spec, Options{Seed: 1})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("err = %v, want a cycle refusal", err)
	}
}

// TestSynthModel_NumericPredictorDeterministic: same spec + same seed,
// same bytes, with the dependency-ordered draw in play.
func TestSynthModel_NumericPredictorDeterministic(t *testing.T) {
	t.Parallel()

	a, _, err := SynthBytes(numericDrawSpec(300, 2), Options{Seed: 8})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	b, _, err := SynthBytes(numericDrawSpec(300, 2), Options{Seed: 8})
	if err != nil {
		t.Fatalf("SynthBytes (repeat): %v", err)
	}
	if string(a) != string(b) {
		t.Fatal("same spec + same seed produced different bytes")
	}
}
