//go:build heavy

package mcmc_test

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/fmagate"
)

// TestMCMC_NoFMAContraction asks the compiler: the package
// cross-compiled for arm64 (which contracts a*b + c into a fused
// multiply-add unless the product is an explicit float64(...)) must
// report no contraction, so the package's bits are the same on every
// architecture. fmagate's probe first proves the flag still reports one.
func TestMCMC_NoFMAContraction(t *testing.T) {
	t.Parallel()

	if got := fmagate.Build(t, "github.com/frankbardon/pulse/mcmc"); len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in mcmc; wrap each product in float64(...):\n%s",
			len(got), strings.Join(got, "\n"))
	}
}

// TestNUTS_TestModelsNoFMAContraction holds the test targets (and every
// other test file) to the package's FMA-free rule: TestNUTS_GoldenHash
// pins draws computed through them, so a contraction in a test model
// would make the golden hash arm64-only.
func TestNUTS_TestModelsNoFMAContraction(t *testing.T) {
	t.Parallel()

	if got := fmagate.TestBinary(t, "github.com/frankbardon/pulse/mcmc"); len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in mcmc or its tests; wrap each product in float64(...):\n%s",
			len(got), strings.Join(got, "\n"))
	}
}
