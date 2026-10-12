package processing

import (
	"fmt"

	exprast "github.com/expr-lang/expr/ast"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprbuiltins"
	"github.com/frankbardon/pulse/internal/vectors"
)

// ref("table", key): a registered reference vector
// (pulse.Options.Extensions.ReferenceVectors) as a numeric vector.
//
// The rules are internal/vectors' (reference_expr.go), read twice: by
// the build's static refusal (refStaticRefusal, the first issue fails
// the build exactly as predict reports it) and by refPatcher, an
// expr.Patch visitor every compile of the expression runs:
//
//   - every ref() call gets a third argument, a constant *refPlan
//     carrying its table (and, inside a pair, the permutation that
//     aligns the profile BY LABEL to the other argument);
//   - inside a pair (dot / dist / cosine, directly or under vcenter /
//     vzscore / vnormalize) a ref() whose key is a literal is folded to
//     the aligned profile itself — a constant []float64, so a row binds
//     nothing and allocates nothing for it.
//
// The plan is unexported, so an expression cannot supply one: a
// three-argument ref() in a request is refused by the arity check
// (vectorStaticRefusal) before the patch runs.

// refPlan is the patched ref() call's resolved table and alignment.
type refPlan struct {
	table string
	t     vectors.ReferenceTable
	perm  []int // nil: the table's own label order
}

// refBuiltin is the reserved ref() implementation. The patched form
// reads its plan; an unpatched call (an expression compiled outside an
// expression program, where nothing is registered) is refused.
func refBuiltin(args ...any) (any, error) {
	if len(args) != 3 {
		if len(args) != 2 {
			return nil, builtinArityError(exprbuiltins.RefFunc, 2, len(args))
		}
		table, _ := args[0].(string)
		_, cerr := vectors.ReferenceTables(nil).Table(table)
		return nil, cerr
	}
	plan, ok := args[2].(*refPlan)
	if !ok {
		return nil, builtinArityError(exprbuiltins.RefFunc, 2, len(args))
	}
	if args[1] == nil {
		// A null key field: the row's null input, not a refusal.
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME, "ref(): key is null")
	}
	key, ok := args[1].(string)
	if !ok {
		key = fmt.Sprintf("%v", args[1])
	}
	v, ok := plan.t.Profile(key, plan.perm)
	if !ok {
		return nil, plan.t.KeyUnknown(plan.table, key)
	}
	return v, nil
}

// refStaticRefusal is the build-time check of every ref() in src: the
// first vectors.ExprReferenceIssues issue, or nil.
func refStaticRefusal(src string, scope vectors.RefScope) error {
	if issues := vectors.ExprReferenceIssues(src, scope); len(issues) > 0 {
		return issues[0]
	}
	return nil
}

// refPatcher rewrites an expression's ref() calls (see the file
// comment). Stateless across compiles: each compile walks a fresh tree.
type refPatcher struct{ scope vectors.RefScope }

func (p *refPatcher) Visit(node *exprast.Node) {
	c, ok := (*node).(*exprast.CallNode)
	if !ok {
		return
	}
	if rc := vectors.ExprRefCall(c); rc != nil {
		if len(rc.Arguments) != 2 {
			return
		}
		table, ok := vectors.ExprRefTable(rc)
		if !ok {
			return
		}
		t, ok := p.scope.Tables[table]
		if !ok {
			return
		}
		rc.Arguments = append(rc.Arguments, &exprast.ConstantNode{Value: &refPlan{table: table, t: t}})
		return
	}
	aligned, _ := p.scope.AlignPair(c)
	for _, a := range aligned {
		if len(a.Call.Arguments) != 3 {
			continue
		}
		plan, ok := a.Call.Arguments[2].(*exprast.ConstantNode).Value.(*refPlan)
		if !ok {
			continue
		}
		if key, ok := vectors.ExprRefKey(a.Call); ok {
			if v, ok := plan.t.Profile(key, a.Perm); ok {
				*a.Slot = &exprast.ConstantNode{Value: v}
				continue
			}
		}
		a.Call.Arguments[2] = &exprast.ConstantNode{Value: &refPlan{table: plan.table, t: plan.t, perm: a.Perm}}
	}
}

// refScope is the reference scope of an expression program: the
// registry's tables, and the named vectors the program binds (a schema
// field shadows a vector of its name, as bindVectors does).
func refScope(src string, tables vectors.ReferenceTables, vecs []vectors.Resolved, isField func(string) bool) vectors.RefScope {
	return vectors.RefScope{
		Tables: tables,
		LabelsOf: func(name string) ([]string, bool) {
			if isField(name) {
				return nil, false
			}
			v, ok := vectors.Find(vecs, name)
			if !ok {
				return nil, false
			}
			return v.Labels, true
		},
	}.WithLets(src)
}

// refRefusalCode reports whether code is a reference refusal an
// evaluation surfaces under its own code (exprRefusal).
func refRefusalCode(code errors.Code) bool {
	return code == errors.PULSE_VECTOR_REFERENCE_UNKNOWN || code == errors.PULSE_VECTOR_REFERENCE_MISMATCH
}
