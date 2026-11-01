//go:build heavy

package mmmfixture_test

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/testutil/fmagate"
)

// TestMMMFixture_NoFMAContraction asks the compiler: the fixture
// generators cross-compiled for arm64 (which contracts a*b + c into a
// fused multiply-add unless the product is an explicit float64(...),
// inlined callees such as rand's Float64 included) must report no
// contraction, so Rows() and Joint() — and the frozen hash and the
// examples CSV built on them — are the same bits on every
// architecture. fmagate's probe first proves the flag still reports one.
func TestMMMFixture_NoFMAContraction(t *testing.T) {
	t.Parallel()

	if got := fmagate.Build(t, "github.com/frankbardon/pulse/internal/mmmfixture"); len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in mmmfixture; wrap each product in float64(...):\n%s",
			len(got), strings.Join(got, "\n"))
	}
}
