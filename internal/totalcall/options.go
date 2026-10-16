package totalcall

import (
	"github.com/expr-lang/expr"
	exprast "github.com/expr-lang/expr/ast"
)

// Index maps every key of keys (Args) to its slot, the position the
// patched total(<slot>) call reads.
func Index(keys []string) map[string]int {
	out := make(map[string]int, len(keys))
	for i, k := range keys {
		out[k] = i
	}
	return out
}

// Options declares total() for the compile of a total-bearing formula:
// the function — read(slot) is the slot's published sum, typed
// func(int) float64 — and the patcher rewriting every total(<arg>) call
// to total(<slot>), the slot index holds for its argument's key
// (ArgKey). The engine binds read to its pre-pass sums; predict, which
// never runs the program, binds a placeholder. Both declare total()
// through here, so a formula type-checks identically on either side
// (`total(qty) + "a"` is a float64 + string on both).
func Options(index map[string]int, read func(args ...any) (any, error)) []expr.Option {
	return []expr.Option{
		expr.Function(Func, read, new(func(int) float64)),
		expr.Patch(&patcher{index: index}),
	}
}

// patcher rewrites every total(<arg>) call to total(<slot>), so row
// evaluation reads the published constant and never evaluates the
// argument per row.
type patcher struct{ index map[string]int }

func (p *patcher) Visit(node *exprast.Node) {
	c, ok := IsCall(*node)
	if !ok || len(c.Arguments) != 1 {
		return
	}
	i, ok := p.index[ArgKey(c)]
	if !ok {
		return
	}
	exprast.Patch(node, &exprast.CallNode{
		Callee:    &exprast.IdentifierNode{Value: Func},
		Arguments: []exprast.Node{&exprast.IntegerNode{Value: i}},
	})
}
