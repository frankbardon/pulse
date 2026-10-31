//go:build heavy

package regression

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/fmagate"
)

// TestOLSQR_NoFMAContraction: the streaming QR behind every plain OLS
// fit (ols_qr.go) cross-compiled for arm64 must report no fused
// multiply-add, so a fit's bits are the same on every architecture.
// fmagate's probe first proves the compiler flag still reports one.
func TestOLSQR_NoFMAContraction(t *testing.T) {
	t.Parallel()

	var hit []string
	for _, l := range fmagate.Build(t, "github.com/frankbardon/pulse/internal/processing/regression") {
		if strings.Contains(l, "ols_qr.go:") {
			hit = append(hit, l)
		}
	}
	if len(hit) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in ols_qr.go; wrap each product in float64(...):\n%s",
			len(hit), strings.Join(hit, "\n"))
	}
}
