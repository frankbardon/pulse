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
	t.Parallel()

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

// TestResultRefusal: a filter whose result the checker types as neither
// bool, `any` nor a vector is refused with the compile error's code and
// expr-lang's AsBool sentence; a formula is never judged.
func TestResultRefusal(t *testing.T) {
	t.Parallel()

	decl := map[string]any{"x": 0.0, "s": "", "v": []float64(nil), "f": func(any) any { return nil }}
	for _, c := range []struct {
		src, site string
		want      string // "" = no refusal
	}{
		{"x > 1", SiteFilter, ""},
		{"f(x)", SiteFilter, ""},
		{"v", SiteFilter, ""},
		{"x + 1", SiteFilter, "expected bool, but got float64"},
		{"s", SiteFilter, "expected bool, but got string"},
		{"x + 1", SiteFormula, ""},
	} {
		prog, err := expr.Compile(c.src, expr.Env(decl))
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		ce := ResultRefusal(c.site, c.src, prog)
		if c.want == "" {
			if ce != nil {
				t.Errorf("%s at %s: refused %v", c.src, c.site, ce)
			}
			continue
		}
		if ce == nil || ce.Code != errors.PULSE_FORMULA_COMPILE_ERROR || ce.Details["compile_error"] != c.want ||
			ce.Message != "compiling filter expression \""+c.src+"\": "+c.want {
			t.Errorf("%s: %+v, want %q", c.src, ce, c.want)
		}
	}
}
