// Package exprident is the one parse-only reading of the names an
// expr-lang expression mentions: the free identifiers it reads from its
// env and the functions it calls by name. It never compiles or runs an
// expression.
//
// It imports only the expr-lang parser and AST, so the no-execute
// layers (internal/vectors, internal/descriptor) and the engine
// (internal/processing) share it: the vector-reference count that
// decides PULSE_VECTOR_UNREFERENCED, the names an expression program
// binds, and the overlay built-in refusal read the same walk and cannot
// disagree about what an expression names.
package exprident

import (
	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"
)

// Free returns the names an expression reads from its env, in first-use
// order: every identifier that is not a callee, a let binding or $env,
// plus `$env.name` / `$env["name"]` members; usesEnv reports any use of
// $env. parsed=false when the expression does not parse (a compile then
// reports the syntax error).
func Free(src string) (names []string, usesEnv, parsed bool) {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil, false, false
	}
	v := &freeVisitor{skip: map[*exprast.IdentifierNode]bool{}, lets: map[string]bool{}, seen: map[string]bool{}}
	// Walk is post-order: a node's children are visited before it, so
	// mark callees / let names / $env members in a first pass.
	exprast.Walk(&tree.Node, &freeMarker{v: v})
	exprast.Walk(&tree.Node, v)
	return v.names, v.usesEnv, true
}

// Callees returns the names an expression calls as functions
// (`name(...)` with an identifier callee), in walk order without
// repeats; parsed=false when the expression does not parse. Stock
// expr-lang built-ins (len, abs, ...) parse as BuiltinNode and are not
// callees.
func Callees(src string) (names []string, parsed bool) {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil, false
	}
	v := &calleeVisitor{seen: map[string]bool{}}
	exprast.Walk(&tree.Node, v)
	return v.names, true
}

type freeMarker struct{ v *freeVisitor }

func (m *freeMarker) Visit(node *exprast.Node) {
	switch n := (*node).(type) {
	case *exprast.CallNode:
		if id, ok := n.Callee.(*exprast.IdentifierNode); ok {
			m.v.skip[id] = true
		}
	case *exprast.VariableDeclaratorNode:
		m.v.lets[n.Name] = true
	case *exprast.MemberNode:
		if id, ok := n.Node.(*exprast.IdentifierNode); ok && id.Value == "$env" {
			if s, ok := n.Property.(*exprast.StringNode); ok {
				m.v.add(s.Value)
			}
		}
	}
}

type freeVisitor struct {
	usesEnv bool
	skip    map[*exprast.IdentifierNode]bool
	lets    map[string]bool
	seen    map[string]bool
	names   []string
}

func (v *freeVisitor) add(name string) {
	if !v.seen[name] {
		v.seen[name] = true
		v.names = append(v.names, name)
	}
}

func (v *freeVisitor) Visit(node *exprast.Node) {
	id, ok := (*node).(*exprast.IdentifierNode)
	if !ok || v.skip[id] || v.lets[id.Value] {
		return
	}
	if id.Value == "$env" {
		v.usesEnv = true
		return
	}
	v.add(id.Value)
}

type calleeVisitor struct {
	seen  map[string]bool
	names []string
}

func (v *calleeVisitor) Visit(node *exprast.Node) {
	c, ok := (*node).(*exprast.CallNode)
	if !ok {
		return
	}
	if id, ok := c.Callee.(*exprast.IdentifierNode); ok && !v.seen[id.Value] {
		v.seen[id.Value] = true
		v.names = append(v.names, id.Value)
	}
}

// FirstCallee returns the first name, in Callees order, the expression
// calls for which match reports true; "" when none does or the
// expression does not parse (a compile then reports the syntax error).
// The overlay formula built-in refusal reads it at both predict hosts
// and the runtime compile with exprbuiltins.OverlayRefused.
func FirstCallee(src string, match func(string) bool) string {
	names, _ := Callees(src)
	for _, n := range names {
		if match(n) {
			return n
		}
	}
	return ""
}
