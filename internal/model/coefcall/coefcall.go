// Package coefcall finds the coef(fit, term[, stat]) calls an
// ATTR_FORMULA / FILTER_EXPRESSION expression makes with literal
// arguments and judges them by internal/model.CheckCoef — the static
// half of the coef() rule, shared by the engine's build (internal/
// processing) and the no-execute field-reference pass (internal/
// descriptor: predict and the runtime's entry check), so both refuse
// the same call with the same code, message and details. A call with
// any computed argument is left to evaluation, which runs the same
// CheckCoef.
//
// It sits beside internal/model rather than in it because it parses
// expr-lang, and internal/model is a leaf over types and errors.
package coefcall

import (
	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
)

// Call is one all-literal coef() call: Stat is model.StatPoint when the
// call omits it.
type Call struct {
	Fit, Term, Stat string
}

// LiteralCalls returns src's coef() calls whose two or three arguments
// are all string literals, in walk order (an argument's calls before
// the call itself, left before right). A source that does not parse
// returns nil — the compile reports it.
func LiteralCalls(src string) []Call {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil
	}
	v := &visitor{}
	exprast.Walk(&tree.Node, v)
	return v.calls
}

type visitor struct{ calls []Call }

func (v *visitor) Visit(node *exprast.Node) {
	n, ok := (*node).(*exprast.CallNode)
	if !ok {
		return
	}
	id, ok := n.Callee.(*exprast.IdentifierNode)
	if !ok || id.Value != model.CoefFunc || len(n.Arguments) < 2 || len(n.Arguments) > 3 {
		return
	}
	lits := make([]string, len(n.Arguments))
	for i, a := range n.Arguments {
		s, ok := a.(*exprast.StringNode)
		if !ok {
			return
		}
		lits[i] = s.Value
	}
	c := Call{Fit: lits[0], Term: lits[1], Stat: model.StatPoint}
	if len(lits) == 3 {
		c.Stat = lits[2]
	}
	v.calls = append(v.calls, c)
}

// StaticRefusal is the first refusal model.CheckCoef raises over src's
// all-literal coef() calls against the bound names in cat, nil when
// every such call passes.
func StaticRefusal(src string, cat model.Catalog) *errors.CodedError {
	for _, c := range LiteralCalls(src) {
		if cerr := model.CheckCoef(cat, c.Fit, c.Term, c.Stat); cerr != nil {
			return cerr
		}
	}
	return nil
}

// NamesFit reports whether src calls coef() or sweep_axis()
// (model.SweepAxisFunc) with fit as a literal first argument — a static
// read of the bound fit fit. A computed first argument is not one; a
// source that does not parse reads nothing (the compile reports it).
func NamesFit(src, fit string) bool {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return false
	}
	v := &fitNameVisitor{fit: fit}
	exprast.Walk(&tree.Node, v)
	return v.found
}

type fitNameVisitor struct {
	fit   string
	found bool
}

func (v *fitNameVisitor) Visit(node *exprast.Node) {
	n, ok := (*node).(*exprast.CallNode)
	if !ok || v.found || len(n.Arguments) == 0 {
		return
	}
	id, ok := n.Callee.(*exprast.IdentifierNode)
	if !ok || (id.Value != model.CoefFunc && id.Value != model.SweepAxisFunc) {
		return
	}
	if s, ok := n.Arguments[0].(*exprast.StringNode); ok && s.Value == v.fit {
		v.found = true
	}
}
