package processing

import (
	"math"
	"testing"
)

// TestTestStat_BitsPinned is the cross-architecture gate on the
// TEST_* distribution helpers in test_stat.go: the normal tails run on
// detmath.Erfc, the chi-square survival and the noncentral chi-square
// mixture on statdist's incomplete gamma, every product barriered, so
// the pinned bits must hold exactly on every GOARCH. Pinned on
// darwin/arm64 and checked identical on linux/amd64 (docker); CI
// re-runs it on linux/amd64. The z inputs are ones where math.Erfc
// differs from detmath.Erfc by an ulp, so a stdlib regression fails.
func TestTestStat_BitsPinned(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  float64
		want uint64
	}{
		{"normalTwoSidedP(0.41)", normalTwoSidedP(0.41), 0x3fe5d15ab4dd8578},
		{"normalTwoSidedP(-0.85)", normalTwoSidedP(-0.85), 0x3fd94d01972d9672},
		{"normalTwoSidedP(1.17)", normalTwoSidedP(1.17), 0x3fcef9e343637034},
		{"standardNormalCDF(-0.67)", standardNormalCDF(-0.67), 0x3fd01769386d0a98},
		{"normalUpperTailP(0.99)", normalUpperTailP(0.99), 0x3fc49e8032343398},
		{"chiSquareSurvival(3.84,1)", chiSquareSurvival(3.84, 1), 0x3fa99f4dee479cfa},
		{"chiSquareSurvival(150,100)", chiSquareSurvival(150, 100), 0x3f4d9ebb47a4ce3b},
		{"noncentralChiSquareCDF(5,3,2.5)", noncentralChiSquareCDF(5, 3, 2.5), 0x3fe149a344ffdd42},
		{"noncentralChiSquareCDF(40,12,18.3)", noncentralChiSquareCDF(40, 12, 18.3), 0x3feae20854a1c17a},
		{"noncentralChiSquareCDF(3,7,0)", noncentralChiSquareCDF(3, 7, 0), 0x3fbd707e6634a595},
	}
	for _, c := range cases {
		if math.Float64bits(c.got) != c.want {
			t.Errorf("%s = %v (%#016x), want %#016x", c.name, c.got, math.Float64bits(c.got), c.want)
		}
	}
}
