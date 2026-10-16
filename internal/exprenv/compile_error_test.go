package exprenv

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/errors"
)

// TestCompileError: a failed expr-lang compile becomes
// PULSE_FORMULA_COMPILE_ERROR with expr-lang's reason in the message and
// in details.compile_error, the source in details.expression, and the
// compile error kept as the cause.
func TestCompileError(t *testing.T) {
	schema := prototypeTestSchema()
	for _, c := range []struct {
		src, what, reason string
	}{
		{"x >", "filter", "unexpected token EOF"},
		{"cat > 5", "formula", "invalid operation: >"},
		{"nosuch + 1", "formula total() argument", "unknown name nosuch"},
	} {
		opts := append([]expr.Option{expr.Env(Prototype(schema))}, Options()...)
		_, cerr := expr.Compile(c.src, opts...)
		if cerr == nil {
			t.Fatalf("%q: expected a compile failure", c.src)
		}
		ce := CompileError(c.what, c.src, cerr)
		if ce.Code != errors.PULSE_FORMULA_COMPILE_ERROR {
			t.Fatalf("%q: code %s, want PULSE_FORMULA_COMPILE_ERROR", c.src, ce.Code)
		}
		reason, _ := ce.Details["compile_error"].(string)
		if !strings.Contains(reason, c.reason) || strings.Contains(reason, "\n") {
			t.Fatalf("%q: compile_error %q, want one line naming %q", c.src, reason, c.reason)
		}
		if ce.Details["expression"] != c.src {
			t.Fatalf("%q: expression %v, want the source", c.src, ce.Details["expression"])
		}
		if !strings.HasPrefix(ce.Message, "compiling "+c.what+" expression") || !strings.Contains(ce.Message, reason) {
			t.Fatalf("%q: message %q, want the site and the reason %q", c.src, ce.Message, reason)
		}
		if !stderrors.Is(ce, cerr) {
			t.Fatalf("%q: the compile error is not the cause", c.src)
		}
	}
}
