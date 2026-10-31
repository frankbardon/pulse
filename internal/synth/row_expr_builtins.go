package synth

import (
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
)

// The row environment's built-ins beyond expr-lang's stock set and
// isnull (U58).
//
// rowExprOptions appends exprenv.MathOptions — sqrt, exp, log, log1p,
// pow, hill, sin, cos, tan, atan2, pi(), e(), float `%` and `**` / `^` —
// the SAME implementations ATTR_FORMULA runs, on linalg/detmath, so a
// `set_expr` gives identical bits on every architecture and CPU. A rule
// output feeds later `when` comparisons, so a one-ulp drift there is not
// a last-bit difference but a different set of fired rows; that is why
// the maths is portable rather than math.*.
//
// It also disables the stock now(): the wall clock breaks the
// byte-identical (spec, seed) contract by construction. A call is
// refused before a row exists with PULSE_SYNTH_EXPR_NONDETERMINISTIC.
//
// # Field names that collide with a built-in: precedence, not refusal
//
// The rule is the query side's (E1-S2, ATTR_FORMULA / FILTER_EXPRESSION):
// a BARE name is always the field, and CALLING a name a declared field
// shadows is refused. Refusing the field instead was rejected: a spec
// with a column named `e`, `log`, `row` or `lag` that never calls the
// built-in kept generating byte-identical output, and refusing it would
// break a working spec to protect one that does not exist. What makes
// the precedence safe is that NEITHER direction can silently change a
// number:
//
//   - bare `log` with a field `log` reads the field, exactly as before
//     the built-ins existed;
//   - `log(x)` with a field `log` is a refusal naming the shadowing
//     (expr-lang's own text would be "float64 is not callable");
//   - bare `pi` with NO field `pi` is a refusal too: expr-lang compiles a
//     bare function name to a nil value, so without this check a spec
//     that USED to fail with "unknown name pi" would quietly write a
//     null-shaped value. The constants are calls, pi() and e().
//
// rowReservedNames is that set: the maths names plus `row` and `lag`,
// the cross-row built-ins U58's later stories add, so their collision
// rule is pinned before they exist, and the time functions `trend`,
// `season` and `between` (row_time.go, U52) and the draws `uniform`
// and `bernoulli` (row_draw.go, U52).
var rowReservedNames = func() map[string]bool {
	m := map[string]bool{"row": true, "lag": true}
	for _, n := range timeFuncNames {
		m[n] = true
	}
	for _, n := range drawFuncNames {
		m[n] = true
	}
	for _, n := range exprenv.MathNames() {
		m[n] = true
	}
	return m
}()

// rowRefusedFunctions are the stock expr-lang built-ins the row
// environment disables, each refused as nondeterministic.
var rowRefusedFunctions = []string{"now"}

// rowBuiltinOptions is the built-in half of rowExprOptions.
func rowBuiltinOptions() []expr.Option {
	opts := exprenv.MathOptions()
	for _, name := range rowRefusedFunctions {
		opts = append(opts, expr.DisableBuiltin(name))
	}
	return opts
}

// rowExprRefusal is a row expression fault the parse settles before the
// compile: a refused nondeterministic call, a call to a shadowed
// built-in, or a bare built-in name. Callers map it onto their site's
// code (rowExprSiteError).
type rowExprRefusal struct {
	nondeterministic bool
	// code, when set, overrides the site's default code (a malformed
	// lag call: PULSE_SYNTH_LAG_FIELD_INVALID / _OFFSET_INVALID).
	code     errors.Code
	function string
	// field is the field a lag call names, when it names one.
	field string
	msg   string
}

func (r *rowExprRefusal) Error() string { return r.msg }

// rowExprStaticRefusal reports src's first refusal, nil when there is
// none or src does not parse (the compile owns that error). PARSED, never
// substring-matched, like exprReadsIdentifier: `now` inside a string
// literal or a field named `nowcast` must not trip it.
//
// times is the spec's date / datetime declarations (rowTimeFields), which
// the time functions' shape check reads.
func rowExprStaticRefusal(src string, declared map[string]bool, times map[string]rowTimeField) *rowExprRefusal {
	tree, err := parser.Parse(src)
	if err != nil || tree == nil || tree.Node == nil {
		return nil
	}
	w := &rowExprWalker{declared: declared, times: times, callees: map[ast.Node]bool{}}
	// Pass 1 records every callee identifier, so pass 2 can tell a bare
	// reference from a call. (A `let` cannot bind a built-in's name:
	// expr-lang refuses the redeclaration itself.)
	ast.Walk(&tree.Node, rowCalleeCollector{w})
	ast.Walk(&tree.Node, w)
	return w.found
}

type rowCalleeCollector struct{ w *rowExprWalker }

func (c rowCalleeCollector) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.CallNode:
		c.w.callees[n.Callee] = true
	}
}

type rowExprWalker struct {
	declared map[string]bool
	times    map[string]rowTimeField
	callees  map[ast.Node]bool
	found    *rowExprRefusal
}

func (w *rowExprWalker) Visit(node *ast.Node) {
	if w.found != nil {
		return
	}
	switch n := (*node).(type) {
	case *ast.BuiltinNode:
		// The parse runs without the row config, so a stock built-in call
		// arrives as a BuiltinNode.
		w.refuseNondeterministic(n.Name)
	case *ast.CallNode:
		id, ok := n.Callee.(*ast.IdentifierNode)
		if !ok {
			return
		}
		if w.refuseNondeterministic(id.Value) {
			return
		}
		if rowReservedNames[id.Value] && w.declared[id.Value] {
			w.found = &rowExprRefusal{function: id.Value, msg: fmt.Sprintf(
				"%s(...) cannot call the built-in: the spec declares a field named %q, and a bare %s reads that field; rename the field to call %s()",
				id.Value, id.Value, id.Value, id.Value)}
			return
		}
		if id.Value == lagFuncName {
			// lag's name and offset size the cross-row state, so both are
			// literals checked here, before a row exists (row_lag.go).
			if _, _, r := lagCallShape(n, w.declared); r != nil {
				w.found = r
			}
		}
		if isDrawFuncName(id.Value) {
			// A draw's arguments are checked here, before a row exists
			// (row_draw.go); where it may appear is the caller's
			// (constraintExprStaticRefusal).
			if r := drawCallShape(n, id.Value); r != nil {
				w.found = r
				return
			}
		}
		if isTimeFuncName(id.Value) {
			// The time functions' field, unit, weights and bounds are
			// literals checked here, before a row exists (row_time.go).
			if _, r := timeCallShape(n, id.Value, w.declared, w.times); r != nil {
				w.found = r
			}
		}
	case *ast.IdentifierNode:
		if w.callees[n] || w.declared[n.Value] || !rowReservedNames[n.Value] {
			return
		}
		w.found = &rowExprRefusal{function: n.Value, msg: fmt.Sprintf(
			"bare %s is the built-in function, not a value; call it (%s(...)) or declare a field of that name",
			n.Value, n.Value)}
	}
}

func (w *rowExprWalker) refuseNondeterministic(name string) bool {
	for _, refused := range rowRefusedFunctions {
		if name == refused {
			w.found = &rowExprRefusal{nondeterministic: true, function: name, msg: fmt.Sprintf(
				"calls %s(), which reads the wall clock and so differs on every run; synth output must be byte-identical for one spec and seed — use a literal or a drawn date field",
				name)}
			return true
		}
	}
	return false
}

// rowExprRuleRefusal is a rule's `when` / `set_expr` refusal as its
// coded error: a nondeterministic call is PULSE_SYNTH_EXPR_NONDETERMINISTIC,
// a shadowed or bare built-in PULSE_SYNTH_RULE_EXPR_INVALID (the code its
// compile fault would carry). Callers run it BEFORE expr.Compile, whose
// own faults keep their existing text.
func rowExprRuleRefusal(idx int, slot, target, src string, r *rowExprRefusal) error {
	where := fmt.Sprintf("rule %s %q", slot, src)
	if target != "" {
		where = fmt.Sprintf("rule %s[%q] = %q", slot, target, src)
	}
	code := errors.PULSE_SYNTH_RULE_EXPR_INVALID
	if r.nondeterministic {
		code = errors.PULSE_SYNTH_EXPR_NONDETERMINISTIC
	}
	if r.code != "" {
		code = r.code
	}
	details := map[string]any{errors.DetailSynthRule: idx, errors.DetailSynthRuleSlot: slot, "function": r.function, "expr": src}
	if target != "" {
		details["field"] = target
	}
	if r.field != "" {
		details["lag_field"] = r.field
	}
	return errors.NewCodedErrorWithDetails(code, fmt.Sprintf("rule %d: %s %s", idx, where, r.msg), details)
}

// rowExprConstraintRefusal is a constraint's refusal as its coded error:
// PULSE_SYNTH_EXPR_NONDETERMINISTIC for a nondeterministic call,
// SERVICE_VALIDATION (the constraint compile code) otherwise.
func rowExprConstraintRefusal(src string, r *rowExprRefusal) error {
	code := errors.SERVICE_VALIDATION
	if r.nondeterministic {
		code = errors.PULSE_SYNTH_EXPR_NONDETERMINISTIC
	}
	if r.code != "" {
		code = r.code
	}
	details := map[string]any{"expr": src, "function": r.function}
	if r.field != "" {
		details["lag_field"] = r.field
	}
	return errors.NewCodedErrorWithDetails(code, fmt.Sprintf("constraint %q %s", src, r.msg), details)
}

// rowOrdinalFuncName is the expression-visible name of the accepted-row
// ordinal (U58).
const rowOrdinalFuncName = "row"

// rowOrdinalBuiltin answers row(): the 0-based ordinal of the row being
// drawn, counted in ACCEPTED rows only. generate stamps it on every host
// before each attempt (setRowOrdinal), so an attempt a constraint
// rejects redraws at the SAME ordinal — row() over the written cohort is
// exactly 0..row_count-1, with no gaps. It reads a counter, never the
// RNG, so a spec that adds row() moves no drawn value, and a spec that
// does not call it is byte-identical to one generated before it existed.
//
// Typed func() int in rowExprOptions, so row(1) is a compile refusal
// and `row() % 52` stays integer arithmetic.
//
// The --suggest-rules detectors (profile_dependency.go,
// profile_gating.go) only COMPILE their candidates against this
// environment — so a candidate is checked against exactly the
// environment the loader will accept — and never run one over captured
// records, so row() has no record-index meaning there to choose. Their
// probes leave ordinal at 0 and are never evaluated.
//
// AugmentFromProfile runs generate over its GENERATED partition only,
// so there row() counts generated rows from 0 and does not include the
// copied source rows (the same rule monotonic_from follows).
func (c *compiledConstraints) rowOrdinalBuiltin(params ...any) (any, error) {
	if len(params) != 0 {
		return nil, fmt.Errorf("%s expects no arguments, got %d", rowOrdinalFuncName, len(params))
	}
	return c.ordinal, nil
}
