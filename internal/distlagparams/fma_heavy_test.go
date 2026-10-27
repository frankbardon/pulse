//go:build heavy

package distlagparams

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/fmagate"
)

// TestDistLag_NoFMAContraction asks the compiler: the package
// cross-compiled for arm64 (which contracts a*b + c into a fused
// multiply-add unless the product is an explicit float64(...)) must
// report no contraction, so the kernel's bits are the same on every
// architecture. fmagate's probe first proves the flag still reports one.
func TestDistLag_NoFMAContraction(t *testing.T) {
	t.Parallel()

	if got := fmagate.Build(t, "github.com/frankbardon/pulse/internal/distlagparams"); len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in distlagparams; wrap each product in float64(...):\n%s",
			len(got), strings.Join(got, "\n"))
	}
}
