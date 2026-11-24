package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// subsetSection lists the hand-kept lists that name a SUBSET of the
// category's operators (vectors.VectorAggregationTypes: three of the
// aggregators): non-test Go composite literals at least two of whose
// elements (or map keys) each spell one operator, naming at least two
// and fewer than the every-op share of the category. A new operator of
// the family the list selects joins it; the registry-wide enumerators
// never reach it. new-op folds such symbols away (each names too few
// operators), so they get their own section.
func (r *repo) subsetSection(c *category) (Section, bool) {
	opOf := map[string]int{}
	for i, op := range c.Ops {
		opOf[op.Name] = i
		for _, k := range op.Consts {
			opOf[k] = i
		}
	}
	spelled := func(e ast.Expr) (int, bool) {
		switch e := e.(type) {
		case *ast.Ident:
			i, ok := opOf[e.Name]
			return i, ok
		case *ast.SelectorExpr:
			i, ok := opOf[e.Sel.Name]
			return i, ok
		case *ast.BasicLit:
			if e.Kind != token.STRING {
				return 0, false
			}
			v, err := strconv.Unquote(e.Value)
			if err != nil {
				return 0, false
			}
			i, ok := opOf[v]
			return i, ok
		case *ast.CallExpr: // a conversion: string(types.AGG_SUM)
			if len(e.Args) == 1 {
				i, ok := opOf[exprIdent(e.Args[0])]
				return i, ok
			}
		}
		return 0, false
	}
	n := len(c.Ops)
	sec := Section{
		Class: "subsets",
		Title: "Operator subsets — hand-kept lists naming some of the category: a new operator of the family a list selects joins it",
		Hint:  fmt.Sprintf("non-test composite literals whose elements (or map keys) spell >= 2 and < %d%% of the %d operators; the registry enumerators never reach them", int(everyThreshold*100), n),
	}
	var sa sectionAdder
	for _, p := range r.files {
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || strings.HasPrefix(p, selfDir) || classify(p) == "history" {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		ast.Inspect(g.file, func(nd ast.Node) bool {
			cl, ok := nd.(*ast.CompositeLit)
			if !ok {
				return true
			}
			named := map[int]bool{}
			for _, e := range cl.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					e = kv.Key
				}
				if i, ok := spelled(e); ok {
					named[i] = true
				}
			}
			if len(named) < 2 || float64(len(named)) >= everyThreshold*float64(n) {
				return true
			}
			var names []string
			for i := range named {
				names = append(names, c.Ops[i].Name)
			}
			sort.Strings(names)
			sa.add(p, g.symbolAt(int(cl.Pos())-g.base), []int{r.fset.Position(cl.Pos()).Line},
				fmt.Sprintf("lists %d/%d: %s", len(names), n, abbreviate(names, 4)))
			return false
		})
	}
	sec.Sites = sa.list()
	return sec, len(sec.Sites) > 0
}

// exprIdent is the identifier an expression ends in (types.AGG_SUM ->
// AGG_SUM), "" for anything else.
func exprIdent(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}
