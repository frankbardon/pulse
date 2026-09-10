package pulse

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// E5-S1's facade-level wiring test: writeSynthFidelityReport must reach
// the model-recovery section, and the written JSON must carry it only
// when the spec carried models.
//
// The section's own behaviour is covered exhaustively in
// synth/fidelity_models_test.go; what this file proves is that the
// bridge calls it, that the section survives the JSON round trip, and
// that a report for a models-free spec is byte-identical to what it was
// before this section existed.

// modelReportSpec is a two-categorical/one-numeric cohort whose `spend`
// field carries a strong single-predictor model.
func modelReportSpec(rows int, models ...synth.FieldModelSpec) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"east", "west"},
					"weights": []any{1.0, 1.0},
				}},
			{Name: "spend", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 100.0, "std": 20.0}},
		},
		Models: models,
	}
}

// writeModelFidelityReport generates a source cohort from spec's fields
// (models stripped), augments it with spec, and returns the parsed
// fidelity report plus its raw bytes.
func writeModelFidelityReport(t *testing.T, spec *synth.Spec) (*synth.FidelityReport, []byte) {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	source := *spec
	source.Models = nil
	source.RowCount = 400
	if _, err := p.Synth(context.Background(), &source, "/source.pulse", SynthOptions{Seed: 3}); err != nil {
		t.Fatalf("source synth: %v", err)
	}
	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse", SynthOptions{
		Seed: 4, SourceCohort: "/source.pulse", FidelityReportPath: "/report.json",
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
	return &report, raw
}

// TestSynth_FidelityReportCarriesModelRecovery asserts the written
// report carries the section, with the captured coefficient on the
// latent scale beside a recovery that lands on it.
func TestSynth_FidelityReportCarriesModelRecovery(t *testing.T) {
	report, raw := writeModelFidelityReport(t, modelReportSpec(6000, synth.FieldModelSpec{
		Field:       "spend",
		Intercept:   100,
		Predictors:  []synth.ModelPredictorSpec{{Kind: synth.ModelPredictorCategoricalLevel, Field: "region", Level: "east", Coefficient: 24}},
		ResidualStd: 8,
	}))

	if len(report.Models) != 1 {
		t.Fatalf("len(Models) = %d, want 1; raw report: %s", len(report.Models), raw)
	}
	m := report.Models[0]
	if m.Field != "spend" || m.Error != "" {
		t.Fatalf("Models[0] = %+v", m)
	}
	if len(m.Predictors) != 1 {
		t.Fatalf("len(Predictors) = %d, want 1", len(m.Predictors))
	}
	p := m.Predictors[0]
	// 24 raw over a marginal std of 20 is 1.2 latent standard deviations.
	if math.Abs(p.CapturedCoefficient-1.2) > 1e-9 {
		t.Errorf("CapturedCoefficient = %v, want 1.2 (24/20)", p.CapturedCoefficient)
	}
	if math.Abs(p.RecoveredCoefficient-1.2) > 0.08 {
		t.Errorf("RecoveredCoefficient = %.4f, want ~1.2 (delta %.4f)", p.RecoveredCoefficient, p.Delta)
	}
	if m.Flagged || p.Flagged {
		t.Errorf("flagged a faithfully generated model: delta %.4f, se %.4f", p.Delta, p.StdError)
	}
}

// TestSynth_FidelityReportOmitsModelsKey is the old-format criterion: a
// spec with no models produces a report whose JSON carries no "models"
// key at all, so every pre-`--fit-models` profile's report is unchanged.
func TestSynth_FidelityReportOmitsModelsKey(t *testing.T) {
	_, raw := writeModelFidelityReport(t, modelReportSpec(600))

	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if _, ok := wire["models"]; ok {
		t.Errorf("report JSON carries a \"models\" key for a spec with no models: %s", raw)
	}
}
