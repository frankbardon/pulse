//go:build heavy

package types

import (
	"math"
	"slices"
	"testing"
)

// TestAutoHACLags_MatchesPowFormExceptBoundaries sweeps n in 1..2e6: the
// exact integer lag equals the former floating-point form
// ⌊4·math.Pow(n/100, 2/9)⌋ everywhere except the exact boundary
// n = 1968300 (= 100·3^9), where Pow rounds 9 down to 8.999999999999998
// and the old form gave 35 for the true lag 36.
func TestAutoHACLags_MatchesPowFormExceptBoundaries(t *testing.T) {
	t.Parallel()

	var exceptions []int
	for n := 1; n <= 2_000_000; n++ {
		pow := int(math.Floor(4 * math.Pow(float64(n)/100, 2.0/9.0)))
		if AutoHACLags(n) != pow {
			exceptions = append(exceptions, n)
		}
	}
	if want := []int{1968300}; !slices.Equal(exceptions, want) {
		t.Errorf("exact lag differs from the Pow form at %v, want exactly %v", exceptions, want)
	}
}
