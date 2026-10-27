package exprenv

import (
	"strings"
	"testing"

	"github.com/expr-lang/expr"
	exprtypes "github.com/expr-lang/expr/types"
)

// TestBoundOptions: the request-bound declarations type-check as the
// runtime always registered them — coef with its two typed overloads
// (only when bound), the embedder functions untyped, lookup only when a
// table is registered.
func TestBoundOptions(t *testing.T) {
	t.Parallel()

	env := expr.Env(exprtypes.Map{"x": DerivedType, "v": VectorType})
	compile := func(b Bound, src string) error {
		_, err := expr.Compile(src, append([]expr.Option{env}, b.Options()...)...)
		return err
	}
	full := Bound{Coef: Placeholder, Lookup: Placeholder, Extensions: []NamedFunc{{Name: "twice", Fn: Placeholder}}}
	for _, c := range []struct {
		b       Bound
		src     string
		wantErr string
	}{
		{full, `coef("m", "x") * x`, ""},
		{full, `coef("m", "x", "se") * x`, ""},
		{full, `coef("m", "x") + "a"`, "mismatched types float64 and string"},
		{full, `coef(1, "x")`, "not enough arguments to call coef"}, // no overload takes an int
		{Bound{}, `coef("m", "x")`, "unknown name coef"},
		{full, `twice(x) + "a"`, ""}, // untyped: any + string compiles
		{full, `lookup("t", "k") * x`, ""},
		{Bound{Coef: Placeholder}, `lookup("t", "k")`, "unknown name lookup"},
		{full, `vsum(v) + x`, ""},
	} {
		err := compile(c.b, c.src)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: %v, want compiled", c.src, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: %v, want %q", c.src, err, c.wantErr)
		}
	}
	if _, err := Placeholder(1); err == nil {
		t.Error("Placeholder ran without error; a declaration-only compile must never be run")
	}
}
