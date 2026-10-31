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
// report no contraction in the Student-t / incomplete-beta /
// chi-square / NormalQuantileDet files, so
// their bits are the same on every architecture. normal.go
// (NormalQuantile) is exempt by name: it is not on the bit contract
// (doc.go). fmagate's probe first proves the flag still reports one.
func TestStatdist_NoFMAContraction(t *testing.T) {
	t.Parallel()

	var got []string
	for _, l := range fmagate.Build(t, "github.com/frankbardon/pulse/internal/statdist") {
		if !strings.Contains(l, "normal.go:") {
			got = append(got, l)
		}
	}
	if len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in statdist's bit-contract files; wrap each product in float64(...):\n%s",
			len(got), strings.Join(got, "\n"))
	}
}
