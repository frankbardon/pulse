package vectors

import (
	"strings"
	"testing"

	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/internal/exprenv"
)

// TestMathOptions_ArityMatchesStaticPass: the typed signatures
// exprenv.MathOptions registers (the synth row environment's maths)
// accept exactly the argument counts BuiltinArity grants the query
// side, so a call one environment refuses the other refuses too.
func TestMathOptions_ArityMatchesStaticPass(t *testing.T) {
	opts := append([]expr.Option{expr.Env(map[string]any{"x": 1.0})}, exprenv.MathOptions()...)
	call := func(name string, n int) string {
		args := make([]string, n)
		for i := range args {
			args[i] = "x"
		}
		return name + "(" + strings.Join(args, ", ") + ")"
	}
	for _, name := range exprenv.MathNames() {
		lo, hi := 1, 1
		if a, ok := BuiltinArity[name]; ok {
			lo, hi = a[0], a[1]
		}
		for n := 0; n <= hi+1; n++ {
			src := call(name, n)
			_, err := expr.Compile(src, opts...)
			if want := n >= lo && n <= hi; want != (err == nil) {
				t.Errorf("%s: compiled=%v, want %v (BuiltinArity [%d, %d]); err=%v", src, err == nil, want, lo, hi, err)
			}
		}
	}
}
