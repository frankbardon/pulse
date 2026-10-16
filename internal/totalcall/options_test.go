package totalcall

import (
	"strings"
	"testing"

	"github.com/expr-lang/expr"
	exprtypes "github.com/expr-lang/expr/types"
)

// TestOptions: the shared total() declaration patches each call to its
// slot and types it float64 — the result type-checks, whatever the
// argument's type, and reads slot i at run time.
func TestOptions(t *testing.T) {
	env := exprtypes.Map{"qty": exprtypes.TypeOf(float64(0))}
	src := `qty / total(qty) + total(qty * 2)`
	keys := Args(src)
	if len(keys) != 2 {
		t.Fatalf("Args = %v", keys)
	}
	sums := []float64{4, 10}
	read := func(args ...any) (any, error) { return sums[args[0].(int)], nil }
	prog, err := expr.Compile(src, append([]expr.Option{expr.Env(env)}, Options(Index(keys), read)...)...)
	if err != nil {
		t.Fatal(err)
	}
	out, err := expr.Run(prog, map[string]any{"qty": 2.0})
	if err != nil || out != 2.0/4+10 {
		t.Fatalf("run = %v, %v; want %v", out, err, 2.0/4+10)
	}
	_, err = expr.Compile(`total(qty) + "a"`, append([]expr.Option{expr.Env(env)}, Options(Index(Args(`total(qty) + "a"`)), read)...)...)
	if err == nil || !strings.Contains(err.Error(), "mismatched types float64 and string") {
		t.Fatalf("total(qty) + \"a\" compiled: %v", err)
	}
}
