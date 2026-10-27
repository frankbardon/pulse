//go:build heavy

package optimize_test

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/fmagate"
)

// TestOptimize_NoFMAContraction asks the compiler: the package
// cross-compiled for arm64 (which contracts a*b + c into a fused
// multiply-add unless the product is an explicit float64(...)) must
// report no contraction, so the solver's bits are the same on every
// architecture. fmagate's probe first proves the flag still reports one.
func TestOptimize_NoFMAContraction(t *testing.T) {
	t.Parallel()

	if got := fmagate.Build(t, "github.com/frankbardon/pulse/optimize"); len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in optimize; wrap each product in float64(...):\n%s",
			len(got), strings.Join(got, "\n"))
	}
}
