//go:build heavy

package integration_test

import (
	"context"
	"strings"
	"testing"
)

// operandCorpus generates ATTR_FORMULA expressions over the operand
// cohort: every operation over the nullable (x, y, c, s) and non-null
// (z) columns, bare and under every guard shape, wrapped as
// `(G) == nil ? 0 : 1` so each result is a number (the `== nil` is
// nil-safe and judges nothing).
func operandCorpus() []string {
	var ops []string
	nums := []string{"x", "y", "z", "2"}
	for _, a := range nums {
		for _, b := range nums {
			if a == b && a == "2" {
				continue
			}
			for _, op := range []string{"+", "*", "/", "%", "**", "<", ">=", "==", "!=", "??"} {
				ops = append(ops, a+" "+op+" "+b)
			}
			ops = append(ops, "max("+a+", "+b+")")
		}
		ops = append(ops, "-"+a, "abs("+a+")", "max("+a+")", "sin("+a+")", "string("+a+")", a+" in [1.5, 2]")
	}
	ops = append(ops,
		`c + "!"`, `c < "m"`, `c == "north"`, `c matches "^n"`, `len(c)`, `upper(c)`, `c ?? "none"`,
		`"t000" in s`, `len(s)`, `s[0]`, `s?.[0]`, `first(s)`, `has_any(s, ["t000"])`, `has_any(["t000"], s)`,
		`map(["a", "b"], x * 2)`, `let v = x; v * 2`, `$env.x + 1`,
	)
	guards := []string{
		"%s",
		"x != nil ? (%s) : nil",
		"x == nil ? nil : (%s)",
		"x != nil && y != nil ? (%s) : nil",
		"c != nil ? (%s) : nil",
		"s == nil ? nil : (%s)",
		"(%s) ?? 0",
		"z > 2 ? (%s) : nil",
		"x != nil && (%s)",
		"x == nil || (%s)",
		"y > 1 && (%s)",
	}
	var out []string
	for _, op := range ops {
		for _, g := range guards {
			out = append(out, "("+strings.ReplaceAll(g, "%s", op)+") == nil ? 0 : 1")
		}
	}
	return out
}

// TestFormulaNullableOperand_CorpusMatchesRuntime is the pin through
// the real runtime: over the generated corpus × the operand cohort's
// null rows, Pulse.Predict raises PULSE_ADVISORY_FORMULA_NULLABLE_OPERAND
// ⇔ Pulse.Process aborts. Expressions predict refuses (a type error)
// are skipped.
func TestFormulaNullableOperand_CorpusMatchesRuntime(t *testing.T) {
	t.Parallel()

	p := openOperandPulse(t)
	ctx := context.Background()
	judged, fired := 0, 0
	for _, src := range operandCorpus() {
		req := formulaOverOperands(src)
		pred, err := p.Predict(ctx, req)
		if err != nil {
			t.Fatalf("Predict(%q): %v", src, err)
		}
		if !pred.Valid {
			continue
		}
		f, aborted := operandVerdict(t, p, req)
		judged++
		if f {
			fired++
		}
		if f != aborted {
			t.Errorf("%q: advisory fired=%v, Process aborted=%v", src, f, aborted)
		}
	}
	if judged < 1500 || fired < 600 || judged-fired < 900 {
		t.Fatalf("corpus judged %d expressions, %d firing: too few to pin", judged, fired)
	}
	t.Logf("judged %d, fired %d", judged, fired)
}
