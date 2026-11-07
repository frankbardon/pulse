package exprident

import (
	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"
)

// NullableResult reports what can make an expression's RESULT nil:
// fields is every name nullable reports true for that reaches the
// result position unguarded (first-use order), literalNil whether a
// literal nil does; parsed=false when the expression does not parse.
//
// Only the result position counts. A nil fed to an operator or a
// function is no null result — the runtime raises an evaluation error
// (ATTR_FORMULA) — so `x * 2` never reports. The result position runs
// through both ternary branches, the `;` sequence's last expression, a
// let body (the let name carries its value's reading) and both sides of
// `??` (nil only when the left AND the right can be). A ternary whose
// condition proves `x == nil` false guards x in its else branch, and
// one proving `x != nil` true guards x in its then branch: `x == nil`
// / `x != nil` (either operand order), `!`, `&&` / `and` (both
// conjuncts hold in the then branch), `||` / `or` (both disjuncts fail
// in the else branch). A function call's result is never judged.
func NullableResult(src string, nullable func(name string) bool) (fields []string, literalNil, parsed bool) {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil, false, false
	}
	r := resultReading(tree.Node, nil, nil, nullable)
	return r.fields, r.literal, true
}

// nullReading is what can make a node's value nil.
type nullReading struct {
	fields  []string
	literal bool
}

func (r nullReading) none() bool { return len(r.fields) == 0 && !r.literal }

func (r nullReading) union(o nullReading) nullReading {
	out := nullReading{fields: append([]string(nil), r.fields...), literal: r.literal || o.literal}
	for _, f := range o.fields {
		seen := false
		for _, g := range out.fields {
			seen = seen || g == f
		}
		if !seen {
			out.fields = append(out.fields, f)
		}
	}
	return out
}

func resultReading(node exprast.Node, guarded map[string]bool, lets map[string]nullReading, nullable func(string) bool) nullReading {
	switch n := node.(type) {
	case *exprast.NilNode:
		return nullReading{literal: true}
	case *exprast.IdentifierNode:
		return nameReading(n.Value, true, guarded, lets, nullable)
	case *exprast.MemberNode:
		if name, ok := envMember(n); ok {
			return nameReading(name, false, guarded, lets, nullable)
		}
	case *exprast.ChainNode:
		return resultReading(n.Node, guarded, lets, nullable)
	case *exprast.SequenceNode:
		if len(n.Nodes) > 0 {
			return resultReading(n.Nodes[len(n.Nodes)-1], guarded, lets, nullable)
		}
	case *exprast.VariableDeclaratorNode:
		inner := map[string]nullReading{}
		for k, v := range lets {
			inner[k] = v
		}
		inner[n.Name] = resultReading(n.Value, guarded, lets, nullable)
		shadowed := guarded
		if guarded[n.Name] {
			shadowed = withGuards(guarded, nil)
			delete(shadowed, n.Name)
		}
		return resultReading(n.Expr, shadowed, inner, nullable)
	case *exprast.ConditionalNode:
		whenTrue, whenFalse := nilGuards(n.Cond)
		return resultReading(n.Exp1, withGuards(guarded, whenTrue), lets, nullable).
			union(resultReading(n.Exp2, withGuards(guarded, whenFalse), lets, nullable))
	case *exprast.BinaryNode:
		if n.Operator == "??" {
			left := resultReading(n.Left, guarded, lets, nullable)
			if left.none() {
				return left
			}
			right := resultReading(n.Right, guarded, lets, nullable)
			if right.none() {
				return right
			}
			return left.union(right)
		}
	}
	return nullReading{}
}

// nameReading reads a bare name (letScoped: a let binding may shadow
// it; a $env member always reads the env).
func nameReading(name string, letScoped bool, guarded map[string]bool, lets map[string]nullReading, nullable func(string) bool) nullReading {
	if guarded[name] {
		return nullReading{}
	}
	if letScoped {
		if r, ok := lets[name]; ok {
			return r
		}
	}
	if nullable(name) {
		return nullReading{fields: []string{name}}
	}
	return nullReading{}
}

// envMember is the name `$env.name` / `$env["name"]` reads.
func envMember(n *exprast.MemberNode) (string, bool) {
	id, ok := n.Node.(*exprast.IdentifierNode)
	if !ok || id.Value != "$env" {
		return "", false
	}
	s, ok := n.Property.(*exprast.StringNode)
	if !ok {
		return "", false
	}
	return s.Value, true
}

// guardName is the name a nil comparison operand reads.
func guardName(node exprast.Node) (string, bool) {
	switch n := node.(type) {
	case *exprast.IdentifierNode:
		return n.Value, n.Value != "$env"
	case *exprast.MemberNode:
		return envMember(n)
	}
	return "", false
}

// nilGuards returns the names a condition proves non-nil when it holds
// (whenTrue) and when it fails (whenFalse).
func nilGuards(cond exprast.Node) (whenTrue, whenFalse []string) {
	switch n := cond.(type) {
	case *exprast.UnaryNode:
		if n.Operator == "!" || n.Operator == "not" {
			t, f := nilGuards(n.Node)
			return f, t
		}
	case *exprast.BinaryNode:
		switch n.Operator {
		case "==", "!=":
			var name string
			var ok bool
			if _, isNil := n.Right.(*exprast.NilNode); isNil {
				name, ok = guardName(n.Left)
			} else if _, isNil := n.Left.(*exprast.NilNode); isNil {
				name, ok = guardName(n.Right)
			}
			if !ok {
				return nil, nil
			}
			if n.Operator == "==" {
				return nil, []string{name}
			}
			return []string{name}, nil
		case "&&", "and":
			lt, _ := nilGuards(n.Left)
			rt, _ := nilGuards(n.Right)
			return append(lt, rt...), nil
		case "||", "or":
			_, lf := nilGuards(n.Left)
			_, rf := nilGuards(n.Right)
			return nil, append(lf, rf...)
		}
	}
	return nil, nil
}

// withGuards returns guarded plus names, a fresh map.
func withGuards(guarded map[string]bool, names []string) map[string]bool {
	out := make(map[string]bool, len(guarded)+len(names))
	for k, v := range guarded {
		out[k] = v
	}
	for _, n := range names {
		out[n] = true
	}
	return out
}
