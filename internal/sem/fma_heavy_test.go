//go:build heavy

package sem

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/fmagate"
)

// TestPolychoric_NoFMAContraction asks the compiler: internal/sem
// cross-compiled for arm64 with -d=fmahash=vy prints one line per
// multiply-add it contracted, and none may name a two-step file
// (twoStepFiles). fmagate's probe first proves the flag still reports
// a contraction.
func TestPolychoric_NoFMAContraction(t *testing.T) {
	t.Parallel()

	for _, l := range fmagate.Build(t, "github.com/frankbardon/pulse/internal/sem") {
		for _, f := range twoStepFiles {
			if strings.Contains(l, "/"+f+":") || strings.HasPrefix(l, f+":") || strings.Contains(l, " "+f+":") {
				t.Errorf("the arm64 compiler contracted a multiply-add in %s: %s", f, l)
			}
		}
	}
}
