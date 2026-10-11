package processing

import (
	"math"

	"github.com/expr-lang/expr"
	exprast "github.com/expr-lang/expr/ast"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/types"
)

// total(expr) — ATTR_FORMULA's cohort-total term.
//
// A formula that calls total(expr) sums expr, a row-level expression
// (schema fields and earlier attribute labels), over the post-filter
// rows, and every row reads that sum as a constant. It reuses the
// ATTR_ZSCORE two-pass scheme: the formula is a TwoPassAttribute whose
// PrePass evaluates each DISTINCT total() argument (keyed by its
// canonical printed form, totalcall.Args) against the record and folds
// it into a plain sequential float64 sum, Finalize publishes the sums,
// and Row evaluates the formula with every total() call patched to read
// its published sum.
//
// Semantics (owner-decided, pinned by formula_total_test.go):
//   - the population is the rows that pass the filters — the same rows
//     the buffered arm hands Compute and the streaming drive hands
//     PrePass; attributes run before grouping, so a grouped request's
//     total is cohort-wide, never per group;
//   - unweighted: ATTR_FORMULA is not weight-aware, so no weight reaches
//     the sum whatever Request.Weight says;
//   - a row whose argument is null is skipped: the argument evaluates
//     to nil, or raises only because a referenced field is null;
//   - a total over zero non-null rows is NaN (null on the wire), so the
//     formula is NaN on every row that reads it — never 0;
//   - a total of exactly 0 used as a divisor yields ±Inf / NaN for the
//     row, which reaches the wire as null (types.MarshalFinite).
//
// Nested totals and a call with other than one argument are refused at
// build (totalcall.Refusal — the rule predict's field-reference pass
// runs too).

// formulaTotals is a total-bearing formula's pre-pass state.
type formulaTotals struct {
	keys   []string       // distinct argument keys, walk order
	index  map[string]int // key -> position in keys
	args   []*exprProgram // per key: the argument's row-level program
	sums   []float64
	counts []int
	// Locked at Finalize: per key, the sum, or NaN with no row.
	values    []float64
	finalized bool
}

// formulaTotalAttribute is a total-bearing ATTR_FORMULA: the row-local
// formulaAttribute plus PrePass / Finalize, so it satisfies
// TwoPassAttribute and every arm that asserts the interface schedules
// its pre-pass.
type formulaTotalAttribute struct {
	*formulaAttribute
}

var _ TwoPassAttribute = (*formulaTotalAttribute)(nil)

// prepareTotals is prepare for a total-bearing formula: the site rule,
// one program per distinct argument, then the formula itself with each
// total() call patched to read its slot.
func (a *formulaAttribute) prepareTotals() (*exprProgram, error) {
	if cerr := totalcall.Refusal(a.expression, totalcall.FormulaSite); cerr != nil {
		return nil, cerr
	}
	t := a.totals
	t.keys = totalcall.Args(a.expression)
	t.index = make(map[string]int, len(t.keys))
	t.args = make([]*exprProgram, len(t.keys))
	t.sums = make([]float64, len(t.keys))
	t.counts = make([]int, len(t.keys))
	argOpts := a.exts.exprOptionsWithModels(a.models)
	for i, key := range t.keys {
		t.index[key] = i
		prog, err := newExprProgram(key, "formula total() argument", a.schema, argOpts)
		if err != nil {
			return nil, err
		}
		t.args[i] = prog
	}
	opts := a.exts.exprOptionsWithModels(a.models)
	opts = append(opts,
		expr.Function(totalcall.Func, t.read, new(func(int) float64)),
		expr.Patch(&totalPatcher{index: t.index}),
	)
	return newExprProgram(a.expression, "formula", a.schema, opts)
}

// read is the patched total(i): slot i's published sum. Reading before
// Finalize — an arm that drove Row without the pre-pass — is a coded
// internal fault, never a silent 0.
func (t *formulaTotals) read(args ...any) (any, error) {
	if !t.finalized {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"formula total() read before its pre-pass finalized")
	}
	return t.values[args[0].(int)], nil
}

// totalPatcher rewrites every total(<arg>) call to total(<slot>), the
// slot its argument's key holds, so row evaluation reads the published
// constant and never evaluates the argument per row.
type totalPatcher struct{ index map[string]int }

func (p *totalPatcher) Visit(node *exprast.Node) {
	c, ok := totalcall.IsCall(*node)
	if !ok || len(c.Arguments) != 1 {
		return
	}
	i, ok := p.index[totalcall.ArgKey(c)]
	if !ok {
		return
	}
	exprast.Patch(node, &exprast.CallNode{
		Callee:    &exprast.IdentifierNode{Value: totalcall.Func},
		Arguments: []exprast.Node{&exprast.IntegerNode{Value: i}},
	})
}

// PrePass folds one post-filter record into every total.
func (a *formulaTotalAttribute) PrePass(r *Record, _ string) error {
	if err := a.prepare(); err != nil {
		return err
	}
	t := a.totals
	for i, prog := range t.args {
		out, nullInput, err := prog.runRecord(r)
		if err != nil {
			if nullInput && modelRefusal(err) == nil {
				continue // a null referenced field: the row is skipped
			}
			return err
		}
		if out == nil {
			continue
		}
		v, err := formulaNumber(out)
		if err != nil {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
				"formula total() argument must be numeric: total("+t.keys[i]+")",
				map[string]any{"argument": t.keys[i]})
		}
		t.sums[i] += v
		t.counts[i]++
	}
	return nil
}

// Finalize publishes each total: its sum, or NaN when no row
// contributed.
func (a *formulaTotalAttribute) Finalize() error {
	if err := a.prepare(); err != nil {
		return err
	}
	t := a.totals
	t.values = make([]float64, len(t.sums))
	for i, s := range t.sums {
		if t.counts[i] == 0 {
			t.values[i] = math.NaN()
			continue
		}
		t.values[i] = s
	}
	t.finalized = true
	return nil
}

// Compute is the buffered arm: PrePass over every record, Finalize,
// then Row per record.
func (a *formulaTotalAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if err := a.prepare(); err != nil {
		return nil, err
	}
	return computeTwoPass(a, records, field)
}

// formulaCallsTotal is the per-attribute two-pass predicate for
// ATTR_FORMULA: true when its expression calls total(expr).
func formulaCallsTotal(attr *types.Attribute) bool {
	return attr != nil && attr.Type == types.ATTR_FORMULA && totalcall.Contains(attr.Expression)
}
