package embeddersmoke

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// TestDetmathFlow uses the public deterministic kernels the way a model
// pack would: a Weibull lag weight and a log-gamma read back at pinned
// bits (the same on every architecture), and a seeded normal draw that
// repeats.
func TestDetmathFlow(t *testing.T) {
	if b := math.Float64bits(detmath.Pow(2.1447721179624666, 2.1)); b != 0x4013dbf089d512e8 {
		t.Errorf("Pow bits %#016x", b)
	}
	if b := math.Float64bits(detmath.Exp(1)); b != 0x4005bf0a8b145769 {
		t.Errorf("Exp bits %#016x", b)
	}
	lg, sign := detmath.Lgamma(-0.5)
	if math.Float64bits(lg) != 0x3ff43f89a3f0edd6 || sign != -1 {
		t.Errorf("Lgamma(-0.5) = %v, %d", lg, sign)
	}
	a := detmath.NormFloat64(rand.New(rand.NewPCG(7, 9)))
	b := detmath.NormFloat64(rand.New(rand.NewPCG(7, 9)))
	if a != b || math.IsNaN(a) {
		t.Errorf("seeded draws %v, %v", a, b)
	}
}
