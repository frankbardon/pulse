// Package coefcall finds the coef(fit, term[, stat]),
// sweep_axis(fit, axis) and response(fit, channel, spend) calls an
// ATTR_FORMULA / FILTER_EXPRESSION / optimize expression makes with
// literal arguments (response(): a literal fit and channel, any spend)
// and judges them by internal/model.CheckCoef / CheckSweepAxis /
// CheckResponse — the static
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
// all-literal coef() calls — or model.CheckSweepAxis over its
// all-literal sweep_axis() calls, model.CheckResponse over its
// literal-fit-and-channel response() calls — against the bound names in
// cat, in walk order; nil when every such call passes.
func StaticRefusal(src string, cat model.Catalog) *errors.CodedError {
	return staticRefusal(src, cat, true)
}

// SweepAxisRefusal is StaticRefusal over src's sweep_axis() and
// response() calls alone — for a site where an embedder function named
// coef owns that name (sweep_axis and response are reserved, so they
// are always judged).
func SweepAxisRefusal(src string, cat model.Catalog) *errors.CodedError {
	return staticRefusal(src, cat, false)
}

func staticRefusal(src string, cat model.Catalog, coef bool) *errors.CodedError {
	for _, c := range literalReads(src) {
		var cerr *errors.CodedError
		switch {
		case c.axis:
			cerr = model.CheckSweepAxis(cat, c.Fit, c.Term)
		case c.response:
			cerr = model.CheckResponse(cat, c.Fit, c.Term)
		case coef:
			cerr = model.CheckCoef(cat, c.Fit, c.Term, c.Stat)
		}
		if cerr != nil {
			return cerr
		}
	}
	return nil
}

// SweepAxisCall is one all-literal sweep_axis(fit, axis) call.
type SweepAxisCall struct {
	Fit, Axis string
}

// LiteralSweepAxisCalls returns src's sweep_axis() calls whose two
// arguments are string literals, in walk order; nil on a source that
// does not parse.
func LiteralSweepAxisCalls(src string) []SweepAxisCall {
	var out []SweepAxisCall
	for _, c := range literalReads(src) {
		if c.axis {
			out = append(out, SweepAxisCall{Fit: c.Fit, Axis: c.Term})
		}
	}
	return out
}

// LiteralFits are the fit names src's all-literal sweep_axis() and
// literal-fit response() calls — and, with coef set, its all-literal
// coef() calls — name, in walk order (repeats kept); nil on a source
// that does not parse.
func LiteralFits(src string, coef bool) []string {
	var out []string
	for _, c := range literalReads(src) {
		if c.axis || c.response || coef {
			out = append(out, c.Fit)
		}
	}
	return out
}

// read is one all-literal coef() call, sweep_axis() call (axis: Term
// is the axis) or response() call with a literal fit and channel
// (response: Term is the channel).
type read struct {
	Call
	axis, response bool
}

// ResponseCall is one response(fit, channel, spend) call whose fit and
// channel are string literals (spend is any expression).
type ResponseCall struct {
	Fit, Channel string
}

// LiteralResponseCalls returns src's response() calls with a literal
// fit and channel, in walk order; nil on a source that does not parse.
func LiteralResponseCalls(src string) []ResponseCall {
	var out []ResponseCall
	for _, c := range literalReads(src) {
		if c.response {
			out = append(out, ResponseCall{Fit: c.Fit, Channel: c.Term})
		}
	}
	return out
}

// literalReads are src's all-literal coef() and sweep_axis() calls and
// its literal-fit-and-channel response() calls, in walk order.
func literalReads(src string) []read {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil
	}
	v := &readVisitor{}
	exprast.Walk(&tree.Node, v)
	return v.reads
}

type readVisitor struct{ reads []read }

func (v *readVisitor) Visit(node *exprast.Node) {
	n, ok := (*node).(*exprast.CallNode)
	if !ok {
		return
	}
	id, ok := n.Callee.(*exprast.IdentifierNode)
	if !ok {
		return
	}
	switch {
	case id.Value == model.CoefFunc && len(n.Arguments) >= 2 && len(n.Arguments) <= 3:
	case id.Value == model.SweepAxisFunc && len(n.Arguments) == 2:
	case id.Value == model.ResponseFunc && len(n.Arguments) == 3:
	default:
		return
	}
	args := n.Arguments
	if id.Value == model.ResponseFunc {
		// The spend is the evaluation's; the fit and channel judge it.
		args = args[:2]
	}
	lits := make([]string, len(args))
	for i, a := range args {
		s, ok := a.(*exprast.StringNode)
		if !ok {
			return
		}
		lits[i] = s.Value
	}
	r := read{Call: Call{Fit: lits[0], Term: lits[1], Stat: model.StatPoint}, axis: id.Value == model.SweepAxisFunc, response: id.Value == model.ResponseFunc}
	if len(lits) == 3 {
		r.Stat = lits[2]
	}
	v.reads = append(v.reads, r)
}

// NamesFit reports whether src calls coef(), sweep_axis()
// (model.SweepAxisFunc) or response() (model.ResponseFunc) with fit as
// a literal first argument — a static
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
	if !ok || (id.Value != model.CoefFunc && id.Value != model.SweepAxisFunc && id.Value != model.ResponseFunc) {
		return
	}
	if s, ok := n.Arguments[0].(*exprast.StringNode); ok && s.Value == v.fit {
		v.found = true
	}
}
