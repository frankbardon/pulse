package synth_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/synth"
)

// This file pins the "absent = byte-identical" half of numeric model
// predictors (`profile create --fit-models`, E2-S1 of synth-realism):
// a capture in which NO numeric predictor clears selection must emit
// the same profile document, and the spec it reconstructs must generate
// the same cohort, as the tree did before numeric candidates existed.
//
// The hashes below were computed on the PRE-CHANGE tree (985e654e, the
// commit before numeric candidates entered selection) and are not to be
// refreshed to make a failure go away: a moved hash on a fixture that
// captures no numeric predictor is exactly the regression this file
// exists to catch.
//
// # One hash on every architecture
//
// The OLS engine behind `--fit-models` (internal/processing/regression)
// runs on the FMA-free SPD port since cross-arch-determinism (U61), so a
// captured coefficient, the document bytes and the twin generated from
// them are identical on arm64 and amd64. The pinned values are the
// pre-change amd64 hashes: amd64 did not move under U61, and arm64 now
// reproduces them. A per-architecture split here would be a regression
// of that contract.

// numericPinOptions is the widest capture the pin is taken under —
// every flag that interacts with models, so a stray numeric candidate
// leaking into any section moves a byte.
func numericPinOptions() synth.ProfileOptions {
	return synth.ProfileOptions{
		IncludeStats:            true,
		IncludeCorrelations:     true,
		IncludeConditional:      true,
		FitModels:               true,
		FitResidualCorrelations: true,
		Seed:                    7,
	}
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// profileAndTwinHashes captures data under numericPinOptions and
// returns the document hash and the hash of a 600-row cohort generated
// from the spec it reconstructs, together with the captured profile.
func profileAndTwinHashes(t *testing.T, data []byte) (string, string, *synth.Profile) {
	t.Helper()
	prof, err := synth.ProfileBytes(data, numericPinOptions())
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	doc, err := json.Marshal(prof)
	if err != nil {
		t.Fatalf("json.Marshal(profile): %v", err)
	}
	spec, _ := synth.SpecFromProfile(prof, 600)
	// Generated under first-declared arbitration, the rule the pinned
	// tree had: strength arbitration (synth-realism E2-S2) deliberately
	// moves which competing pair draws a target, which is a change this
	// pin does not exist to catch. Under "declared" every other byte of
	// the twin must still be the pre-change one.
	spec.PairArbitration = synth.PairArbitrationDeclared
	out, _, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	return sha(doc), sha(out), prof
}

// capturedNumericPredictor reports whether any captured model carries a
// numeric predictor.
func capturedNumericPredictor(p *synth.Profile) bool {
	for _, m := range p.Models {
		for _, pr := range m.Predictors {
			if pr.Kind == synth.ModelPredictorNumeric {
				return true
			}
		}
	}
	return false
}

// numericOnlyFixture is a cohort with NO categorical or set_* field
// and two mutually independent numerics. Before numeric candidates
// existed every target here was skipped as having nothing to regress
// on; with them, selection admits nothing, and the capture must still
// say exactly that.
func numericOnlyFixture(t *testing.T, rowCount int, seed int64) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var buf strings.Builder
	buf.WriteString("a,b\n")
	for i := 0; i < rowCount; i++ {
		a := 10.0 + float64(rng.NormFloat64()*2)
		b := 5.0 + rng.NormFloat64()
		fmt.Fprintf(&buf, "%.6f,%.6f\n", a, b)
	}
	return importCSVFixture(t, buf.String(), rowCount)
}

// pinnedHashes is one fixture's pre-change (document, twin) hash pair.
type pinnedHashes [2]string

// TestProfileNumericPredictors_AbsentIsByteIdenticalToPreChange pins
// the pre-change bytes of fixtures whose numerics are mutually
// unrelated (spend depends on region, visits on nothing; a and b on
// nothing), so no numeric candidate can clear the variance floor: the
// document and the generated twin must both be unchanged.
func TestProfileNumericPredictors_AbsentIsByteIdenticalToPreChange(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		data func(*testing.T) []byte
		want pinnedHashes
	}{
		{
			name: "categorical-driven",
			data: func(t *testing.T) []byte { return modelFlagFixture(t, 1200, 7) },
			want: pinnedHashes{"98682b8e61dca16412a6a0dd88c78f100c6b4d948b0358b58eda660121f095de", "ebebeee74d8301a1a02ac144c24e3fbe043839115a207a645eabc46f11bb1ddd"},
		},
		{
			name: "numeric-only",
			data: func(t *testing.T) []byte { return numericOnlyFixture(t, 1000, 3) },
			want: pinnedHashes{"bba91476a767bce34538df804576bd7001c9448f36af499005be4704bcee8028", "f89dd3cdd16cfc3e5ef3875e0711a8257b81adc0685abeb17a21dfe1260c21f3"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, twin, prof := profileAndTwinHashes(t, tc.data(t))
			if capturedNumericPredictor(prof) {
				t.Fatalf("fixture captured a numeric predictor; it no longer tests the absent case: %+v", prof.Models)
			}
			want := tc.want
			if doc != want[0] {
				t.Errorf("profile document moved with no numeric predictor captured:\n got %s\nwant %s", doc, want[0])
			}
			if twin != want[1] {
				t.Errorf("generated twin moved with no numeric predictor captured:\n got %s\nwant %s", twin, want[1])
			}
		})
	}
}

// TestSynthModel_CategoricalOnlyModelsAreByteIdenticalToPreChange pins
// the generation half on a hand-authored spec: two models over
// categorical and set predictors, one pair of correlated residuals. Its
// drawers must keep schema order and their RNG consumption exactly, so
// the bytes match the pre-change tree on every architecture.
func TestSynthModel_CategoricalOnlyModelsAreByteIdenticalToPreChange(t *testing.T) {
	t.Parallel()

	const want = "5e5919dd66fdc9a2a8dedb5d07d56b2d608241b84ef499091ef0a716eb0df193"

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
	spec.Fields = append(spec.Fields, synth.FieldSpec{
		Name: "visits", Type: "f64",
		Distribution: synth.DistNormal,
		Params:       map[string]any{"mean": 12.0, "std": 6.0},
	})
	spec.Models = append(spec.Models, synth.FieldModelSpec{
		Field: "visits", Intercept: 12, ResidualStd: 2,
		Predictors: []synth.ModelPredictorSpec{catLevel("plan", "pro", 4)},
	})
	spec.ResidualCorrelations = []synth.CorrelationSpec{{A: "spend", B: "visits", Rho: 0.4}}

	out, _, err := synth.SynthBytes(spec, synth.Options{Seed: 5})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if got := sha(out); got != want {
		t.Errorf("categorical-only models moved:\n got %s\nwant %s", got, want)
	}
}
