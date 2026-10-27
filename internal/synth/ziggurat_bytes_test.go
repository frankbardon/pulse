package synth_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/frankbardon/pulse/internal/synth"
)

// variateSpec draws every variate-consuming path: plain normal /
// lognormal / exponential / mixture fields, a correlated (copula) block,
// a modelled field with a residual, and a vector field with a correlated
// pair and an exponential element.
func variateSpec() *synth.Spec {
	return &synth.Spec{
		RowCount: 20_000,
		Fields: []synth.FieldSpec{
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west", "north"}, "weights": []any{0.5, 0.3, 0.2}}},
			{Name: "score", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 50.0, "std": 10.0}},
			{Name: "revenue", Type: "f64", Distribution: synth.DistLogNormal,
				Params: map[string]any{"mu": 4.0, "sigma": 0.8}},
			{Name: "wait", Type: "f64", Distribution: synth.DistExponential,
				Params: map[string]any{"lambda": 2.0}},
			{Name: "gap", Type: "f32", Distribution: synth.DistExponential,
				Params: map[string]any{"lambda": 0.5}},
			{Name: "mix", Type: "f64", Distribution: synth.DistMixture,
				Params: map[string]any{"means": []any{10.0, 40.0}, "stds": []any{3.0, 8.0}, "weights": []any{0.6, 0.4}}},
			{Name: "spend", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 100.0, "std": 25.0}},
			{Name: "v", Type: "vec_f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 0.0, "std": 1.0},
				Vector: &synth.VectorSpec{Dim: 3, Kind: "measure",
					Elements:     []synth.VectorElementSpec{{}, {}, {Distribution: synth.DistExponential, Params: map[string]any{"lambda": 1.0}}},
					Correlations: []synth.VectorCorrelationSpec{{A: 1, B: 2, Rho: 0.5}}}},
		},
		Correlations: []synth.CorrelationSpec{
			{A: "score", B: "revenue", Rho: 0.4},
			{A: "score", B: "wait", Rho: -0.3},
		},
		Models: []synth.FieldModelSpec{
			{Field: "spend", Intercept: 100, ResidualStd: 20, Predictors: []synth.ModelPredictorSpec{
				{Kind: "categorical_level", Field: "region", Level: "west", Coefficient: 30},
			}},
		},
	}
}

// variateSpecHash is the sha256 of variateSpec at seed 4242. Every
// variate, transcendental and multiply-add on synth's generation path is
// portable (the detmath ziggurat, Exp / Log / Pow / Erf / Erfinv, float64
// barriers), so — unlike the specs this package used to pin, which had
// to avoid normal / exponential draws and every transcendental — this
// pin must hold on every architecture and CPU. Taken on darwin/arm64 and
// checked identical on linux/amd64 (docker); CI re-runs it on amd64.
const variateSpecHash = "234b32db8a16c236cdfa897b5dbc0c3f4e3809059b3e8952e05c2e5979f0f5cb"

// TestSynthZiggurat_GenerationBytesPortable pins variateSpec's generated
// bytes.
func TestSynthZiggurat_GenerationBytesPortable(t *testing.T) {
	t.Parallel()

	data, _, err := synth.SynthBytes(variateSpec(), synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != variateSpecHash {
		t.Fatalf("variateSpec at seed 4242 no longer generates the pinned bytes\n got %s (%d bytes)\nwant %s",
			got, len(data), variateSpecHash)
	}
}
