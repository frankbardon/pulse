// Package totalcall finds the total(expr) calls an expression makes and
// judges where they may appear — the one rule shared by the engine
// (internal/processing: ATTR_FORMULA's two-pass build, the expression
// filterer's and overlay formula's refusals), the merge gate
// (internal/mergegate) and the no-execute field-reference pass
// (internal/descriptor: predict and the runtime's entry check). It
// parses expr-lang and nothing else, so internal/descriptor may import
// it without reaching the engine (TestPredictNoExecutionImports).
//
// total(expr) is ATTR_FORMULA's cohort-total term: the sum of a
// row-level expression over the post-filter rows, computed in a
// pre-pass and read as a constant by every row. A formula that calls it
// is therefore NOT row-local — Contains is the per-attribute predicate
// every type-keyed row-local gate must ask beside the attribute type.
package totalcall

import (
	"strings"

	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
)

// Func is the expression function name. Lowercase, like coef / lookup;
// an embedder ExprFunction may not claim it.
const Func = "total"

// FormulaSite is the one expression site that accepts total().
const FormulaSite = "ATTR_FORMULA"

// Contains reports whether src calls total(...) anywhere. A source that
// does not parse reports false — its compile reports the syntax error.
func Contains(src string) bool {
	if !strings.Contains(src, Func) {
		return false
	}
	tree, err := exprparser.Parse(src)
	if err != nil {
		return false
	}
	v := &collector{}
	exprast.Walk(&tree.Node, v)
	return len(v.calls) > 0
}

// Args returns the argument source of every distinct one-argument
// total() call in src, in walk order (post-order: an inner call before
// its parent, left before right), keyed by the argument's canonical
// printed form (ast.Node.String) — the key the engine's patcher reads
// back to find a call's accumulator. nil when src does not parse or
// makes no call.
func Args(src string) []string {
	if !strings.Contains(src, Func) {
		return nil
	}
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil
	}
	v := &collector{}
	exprast.Walk(&tree.Node, v)
	var out []string
	seen := map[string]bool{}
	for _, c := range v.calls {
		if len(c.Arguments) != 1 {
			continue
		}
		key := ArgKey(c)
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	return out
}

// ArgKey is the accumulator key of a one-argument total() call: its
// argument's canonical printed form.
func ArgKey(c *exprast.CallNode) string {
	return c.Arguments[0].String()
}

// IsCall reports whether n is a total(...) call (callee the bare
// identifier total).
func IsCall(n exprast.Node) (*exprast.CallNode, bool) {
	c, ok := n.(*exprast.CallNode)
	if !ok {
		return nil, false
	}
	id, ok := c.Callee.(*exprast.IdentifierNode)
	if !ok || id.Value != Func {
		return nil, false
	}
	return c, true
}

// Refusal is the coded refusal of src's total() calls at the named
// expression site ("ATTR_FORMULA", "FILTER_EXPRESSION",
// "OVERLAY_FORMULA"), nil when every call is admissible:
//
//   - outside ATTR_FORMULA, any call is PULSE_FORMULA_TOTAL_SITE;
//   - in ATTR_FORMULA, a call whose argument itself calls total() is
//     PULSE_FORMULA_TOTAL_NESTED, and one with other than exactly one
//     argument is PULSE_FORMULA_TOTAL_ARITY.
//
// A source that does not parse yields nil (the compile reports it).
func Refusal(src, site string) *errors.CodedError {
	if !strings.Contains(src, Func) {
		return nil
	}
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil
	}
	v := &collector{}
	exprast.Walk(&tree.Node, v)
	if len(v.calls) == 0 {
		return nil
	}
	if site != FormulaSite {
		return errors.NewCodedErrorWithDetails(errors.PULSE_FORMULA_TOTAL_SITE,
			"total() is only available in ATTR_FORMULA; "+site+" cannot call it: "+src,
			map[string]any{"site": site, "expression": src, "valid_sites": []string{FormulaSite}})
	}
	for _, c := range v.calls {
		if len(c.Arguments) != 1 {
			return errors.NewCodedErrorWithDetails(errors.PULSE_FORMULA_TOTAL_ARITY,
				"total() takes exactly one argument (a row-level expression): "+c.String(),
				map[string]any{"site": site, "expression": src, "call": c.String(), "arguments": len(c.Arguments)})
		}
		inner := &collector{}
		exprast.Walk(&c.Arguments[0], inner)
		if len(inner.calls) > 0 {
			return errors.NewCodedErrorWithDetails(errors.PULSE_FORMULA_TOTAL_NESTED,
				"total() cannot be nested inside another total(): "+c.String(),
				map[string]any{"site": site, "expression": src, "call": c.String()})
		}
	}
	return nil
}

type collector struct{ calls []*exprast.CallNode }

func (v *collector) Visit(node *exprast.Node) {
	if c, ok := IsCall(*node); ok {
		v.calls = append(v.calls, c)
	}
}
