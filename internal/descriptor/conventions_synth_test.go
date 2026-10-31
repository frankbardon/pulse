package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/internal/synth"
)

// TestSynthFidelityEffectBandsMatchConventions pins the fidelity
// report's effect-size bands (internal/synth/fidelity_effect.go) to this
// registry. synth cannot import descriptor (descriptor imports synth),
// so the thresholds are restated there; this is what keeps the copy from
// drifting.
func TestSynthFidelityEffectBandsMatchConventions(t *testing.T) {
	t.Parallel()

	for _, id := range []string{ConventionCohenD, ConventionCohenW} {
		thresholds, labels, ok := synth.EffectBandThresholds(id)
		if !ok {
			t.Errorf("%s: the fidelity report does not band it", id)
			continue
		}
		want := builtinConventions[id]
		if !slices.Equal(thresholds, want.Thresholds) {
			t.Errorf("%s thresholds = %v, registry %v", id, thresholds, want.Thresholds)
		}
		if !slices.Equal(labels, want.Labels) {
			t.Errorf("%s labels = %v, registry %v", id, labels, want.Labels)
		}
	}
}
