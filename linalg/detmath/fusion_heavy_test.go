//go:build heavy

package detmath

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/fmagate"
)

// TestDetmath_NoFMAContraction is gate 2 (fusion_test.go): fmagate's
// probe first proves a contraction is visible (a scratch module holding
// `a + b*c` must report one — otherwise a renamed compiler flag would
// make this gate silently vacuous), then the detmath build must report
// none.
func TestDetmath_NoFMAContraction(t *testing.T) {
	t.Parallel()

	if got := fmagate.Build(t, "github.com/frankbardon/pulse/linalg/detmath"); len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in detmath; wrap each product in "+
			"float64(...):\n%s", len(got), strings.Join(got, "\n"))
	}
}
