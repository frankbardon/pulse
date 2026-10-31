//go:build heavy

package sem

import (
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/fmagate"
)

// TestPolychoric_NoFMAContraction asks the compiler: internal/sem
// cross-compiled for arm64 with -d=fmahash=vy prints one line per
// multiply-add it contracted, and there may be none in the package.
// The gate began on the two-step files (twoStepFiles); since U61
// (cross-arch determinism) the whole package is barriered — the fit,
// statistics, WLSMV, bootstrap and defined-parameter arithmetic too —
// and a contraction inlined from gonum (floats.Dot, which dot.go now
// replaces) counts at its SEM call site. fmagate's probe first proves
// the flag still reports a contraction.
func TestPolychoric_NoFMAContraction(t *testing.T) {
	t.Parallel()

	for _, l := range fmagate.Build(t, "github.com/frankbardon/pulse/internal/sem") {
		t.Errorf("the arm64 compiler contracted a multiply-add in internal/sem: %s", l)
	}
}
