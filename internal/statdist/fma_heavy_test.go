//go:build heavy

package statdist

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/fmagate"
)

// TestStatdist_NoFMAContraction asks the compiler: the package
// cross-compiled for arm64 (which contracts a*b + c into a fused
// multiply-add unless the product is an explicit float64(...)) must
// report no contraction anywhere in it, so every primitive's bits are
// the same on every architecture (doc.go). No file is exempt.
// fmagate's probe first proves the flag still reports one.
func TestStatdist_NoFMAContraction(t *testing.T) {
	t.Parallel()

	if got := fmagate.Build(t, "github.com/frankbardon/pulse/internal/statdist"); len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in statdist; wrap each product in float64(...):\n%s",
			len(got), strings.Join(got, "\n"))
	}
}
