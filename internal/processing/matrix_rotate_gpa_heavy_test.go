//go:build heavy

package processing

import (
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/internal/vectors"
)

// TestGPFoblq_ObliminBitIdenticalToReference: oblimin on the generic
// oblique driver reproduces the pre-driver loop bit for bit — the raw
// fit (loadings, rotation, Φ, iterations, convergence) and the
// canonicalised result rotateLoadings returns — on every oracle input
// and the synthetic ones, at γ = 0 (quartimin), 0.5 (biquartimin),
// −0.5, 1 (covarimin) and 2 (which stops at the iteration cap on
// most inputs). At least one run must stop at its cap so that path is
// covered too.
func TestGPFoblq_ObliminBitIdenticalToReference(t *testing.T) {
	capped := 0
	for name, a := range gpaRotationInputs(t) {
		for _, gamma := range []float64{0, 0.5, -0.5, 1, 2} {
			t.Run(fmt.Sprintf("%s/g%g", name, gamma), func(t *testing.T) {
				want, err := obliminRotateReference(a, gamma)
				if err != nil {
					t.Fatal(err)
				}
				got, err := obliminRotate(a, gamma)
				if err != nil {
					t.Fatal(err)
				}
				sameBitRotation(t, got, want)
				if !want.Converged {
					capped++
				}
				canonicalizeRotation(&want)
				gotPublic, err := rotateLoadings(a, rotationSpec{Method: vectors.RotationOblimin, Gamma: gamma})
				if err != nil {
					t.Fatal(err)
				}
				sameBitRotation(t, gotPublic, want)
			})
		}
	}
	if capped == 0 {
		t.Error("no run stopped at its cap; the non-converged path is uncovered")
	}
}
