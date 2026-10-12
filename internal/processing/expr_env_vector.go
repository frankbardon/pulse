package processing

import (
	stderrors "errors"
	"fmt"
	"math"

	"github.com/expr-lang/expr"
	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprbuiltins"
	"github.com/frankbardon/pulse/internal/vecmath"
	"github.com/frankbardon/pulse/linalg/metric"
)

// Built-in vector and scalar-maths expression functions (vsum, dot,
// cosine, vcenter, sqrt, ...). The names live in ONE list,
// internal/exprbuiltins, read by this registration, by filter-precompute
// purity (filter_precompute.go) and by the embedder reservation
// (extensions_validate.go). The metric functions — dist (euclidean),
// cosine (its similarity form) and the set similarities (jaccard, dice,
// hamming, overlap; expr_env_setsim.go) — resolve their metric through
// the linalg/metric registry (consumer "expr"), so they share one
// definition and one NaN policy with every other metric consumer; the
// remaining numeric rules (reductions, dot, the maps) live in
// internal/vecmath. The closures here only adapt arguments.
//
// A vector argument is a []float64 or an expr literal array ([]any) of
// numbers; a nil element (a null field inside `[a, b, c]`) reads as NaN
// and is skipped by the NaN-skipping kernels. Vector-returning functions
// (vcenter, vzscore, vnormalize) return a NEW slice and are usable only
// inside an expression: a vector as the top-level result is refused.

// exprBuiltinFunc is the untyped expr.Function shape every reserved
// built-in uses.
type exprBuiltinFunc = func(args ...any) (any, error)

// exprBuiltinImpls maps every exprbuiltins name to its implementation.
// TestExprBuiltins_EveryNameImplemented pins the two in both directions.
var exprBuiltinImpls = map[string]exprBuiltinFunc{
	"vsum":       vecReduce("vsum", vecmath.Sum),
	"vmean":      vecReduce("vmean", vecmath.Mean),
	"vmin":       vecReduce("vmin", vecmath.Min),
	"vmax":       vecReduce("vmax", vecmath.Max),
	"vsd":        vecReduce("vsd", vecmath.SD),
	"norm":       vecReduce("norm", vecmath.Norm),
	"vcount":     vecCountBuiltin,
	"dot":        vecPair("dot", vecmath.Dot),
	"dist":       vecPair("dist", exprMetricKernel(builtinMetric("dist"))),
	"cosine":     vecPair("cosine", exprMetricKernel(builtinMetric("cosine"))),
	"argmax":     vecArg("argmax", vecmath.ArgMax),
	"argmin":     vecArg("argmin", vecmath.ArgMin),
	"vcenter":    vecMap("vcenter", vecmath.Center),
	"vzscore":    vecMap("vzscore", vecmath.ZScore),
	"vnormalize": vecNormalizeBuiltin,
	"sqrt":       scalarUnary("sqrt", vecmath.Sqrt),
	"log":        scalarUnary("log", vecmath.Log),
	"exp":        scalarUnary("exp", vecmath.Exp),
	"pow":        scalarPowBuiltin,
	"jaccard":    setSimilarity("jaccard", exprMetric(builtinMetric("jaccard"))),
	"dice":       setSimilarity("dice", exprMetric(builtinMetric("dice"))),
	"hamming":    setHammingBuiltin,
	"overlap":    setSimilarity("overlap", exprMetric(builtinMetric("overlap"))),
}

// builtinMetric is the registry metric and form the reserved built-in
// fn evaluates (exprbuiltins.Metric, the one table the vector-kind fit
// check also reads); a name outside it is a programming error.
func builtinMetric(fn string) (string, metric.Form) {
	mf, ok := exprbuiltins.Metric(fn)
	if !ok {
		panic("expr built-in " + fn + " evaluates no registry metric")
	}
	return mf.Metric, metric.Form(mf.Form)
}

// exprMetric resolves name in form f for the expression layer through
// the linalg/metric registry. The names are fixed at build time, so a
// refusal is a programming error (TestExprMetrics_ResolveThroughRegistry
// pins every one).
func exprMetric(name string, f metric.Form) exprMetricRef {
	m, err := metric.ResolveFor(metric.ConsumerExpr, name, f)
	if err != nil {
		panic("expr metric " + name + ": " + err.Error())
	}
	return exprMetricRef{m: m, form: f}
}

// exprMetricRef is a resolved expression metric and the form it answers
// in.
type exprMetricRef struct {
	m    metric.Metric
	form metric.Form
}

// exprMetricKernel is the vecPair kernel of a registry metric: the
// pairwise-complete value (Scaled metrics rescaled by p/p_observed), a
// *metric.LengthMismatchError for unequal lengths.
func exprMetricKernel(name string, f metric.Form) func(a, b []float64) (float64, error) {
	r := exprMetric(name, f)
	return func(a, b []float64) (float64, error) { return r.m.Eval(r.form, a, b, nil) }
}

// exprBuiltinArity is each reserved built-in's [min, max] argument
// count, checked when the expression compiles (vectorStaticRefusal).
var exprBuiltinArity = map[string][2]int{
	"dot": {2, 2}, "dist": {2, 2}, "cosine": {2, 2},
	"vnormalize": {2, 2}, "pow": {2, 2},
	"jaccard": {2, 2}, "dice": {2, 2}, "hamming": {2, 2}, "overlap": {2, 2},
}

// exprBuiltinVectorResult names the built-ins that return a vector.
var exprBuiltinVectorResult = map[string]bool{
	"vcenter": true, "vzscore": true, "vnormalize": true,
}

// vectorExprOptions registers every reserved built-in, in the
// exprbuiltins order. Appended inside setExprOptions, so ATTR_FORMULA,
// FILTER_EXPRESSION, total() arguments and OVERLAY_FORMULA all see them.
func vectorExprOptions() []expr.Option {
	names := exprbuiltins.Names()
	out := make([]expr.Option, 0, len(names))
	for _, name := range names {
		out = append(out, expr.Function(name, exprBuiltinImpls[name]))
	}
	return out
}

func builtinArityError(fn string, want, got int) error {
	return errors.NewCodedError(errors.PROCESSING_RUNTIME,
		fmt.Sprintf("%s() takes %d argument(s); got %d", fn, want, got))
}

// exprFloat reads one numeric expr value; ok is false for any
// non-numeric (nil included).
func exprFloat(a any) (float64, bool) {
	switch v := a.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	}
	return 0, false
}

// vectorArg coerces argument i of fn into a []float64. A []float64 is
// returned as is (the kernels never mutate it); a literal []any is
// copied, nil elements becoming NaN.
func vectorArg(fn string, i int, a any) ([]float64, error) {
	switch v := a.(type) {
	case []float64:
		return v, nil
	case []any:
		out := make([]float64, len(v))
		for j, e := range v {
			if e == nil {
				out[j] = math.NaN()
				continue
			}
			x, ok := exprFloat(e)
			if !ok {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
					fmt.Sprintf("%s(): vector argument %d element %d is %T, not a number", fn, i+1, j, e),
					map[string]any{"function": fn, "argument": i + 1, "element": j})
			}
			out[j] = x
		}
		return out, nil
	}
	return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
		fmt.Sprintf("%s(): argument %d is %T, not a numeric vector (a []float64 or a literal array such as [a, b, c])", fn, i+1, a),
		map[string]any{"function": fn, "argument": i + 1})
}

// scalarArg coerces argument i of fn into a float64.
func scalarArg(fn string, i int, a any) (float64, error) {
	x, ok := exprFloat(a)
	if !ok {
		return 0, errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("%s(): argument %d is %T, not a number", fn, i+1, a),
			map[string]any{"function": fn, "argument": i + 1})
	}
	return x, nil
}

func vecReduce(fn string, k func([]float64) float64) exprBuiltinFunc {
	return func(args ...any) (any, error) {
		if len(args) != 1 {
			return nil, builtinArityError(fn, 1, len(args))
		}
		v, err := vectorArg(fn, 0, args[0])
		if err != nil {
			return nil, err
		}
		return k(v), nil
	}
}

func vecCountBuiltin(args ...any) (any, error) {
	if len(args) != 1 {
		return nil, builtinArityError("vcount", 1, len(args))
	}
	v, err := vectorArg("vcount", 0, args[0])
	if err != nil {
		return nil, err
	}
	return vecmath.Count(v), nil
}

// vectorLengthMismatch is the coded refusal of an unequal-length pair.
func vectorLengthMismatch(fn string, left, right int, cause error) *errors.CodedError {
	ce := errors.NewCodedErrorWithDetails(errors.PROCESSING_VECTOR_LENGTH_MISMATCH,
		fmt.Sprintf("%s(): vector length mismatch: %d vs %d", fn, left, right),
		map[string]any{"function": fn, "left": left, "right": right})
	ce.Cause = cause
	return ce
}

func vecPair(fn string, k func(a, b []float64) (float64, error)) exprBuiltinFunc {
	return func(args ...any) (any, error) {
		if len(args) != 2 {
			return nil, builtinArityError(fn, 2, len(args))
		}
		a, err := vectorArg(fn, 0, args[0])
		if err != nil {
			return nil, err
		}
		b, err := vectorArg(fn, 1, args[1])
		if err != nil {
			return nil, err
		}
		out, err := k(a, b)
		if err != nil {
			var lm *metric.LengthMismatchError
			if stderrors.As(err, &lm) {
				return nil, vectorLengthMismatch(fn, lm.Left, lm.Right, err)
			}
			var vm *vecmath.LengthMismatchError
			if stderrors.As(err, &vm) {
				return nil, vectorLengthMismatch(fn, vm.Left, vm.Right, err)
			}
			return nil, errors.WrapCodedError(err, errors.PROCESSING_RUNTIME, fn+"(): "+err.Error())
		}
		return out, nil
	}
}

// vecArg adapts argmax / argmin: a 0-based int index, NaN when the
// vector has no valid value.
func vecArg(fn string, k func([]float64) (int, bool)) exprBuiltinFunc {
	return func(args ...any) (any, error) {
		if len(args) != 1 {
			return nil, builtinArityError(fn, 1, len(args))
		}
		v, err := vectorArg(fn, 0, args[0])
		if err != nil {
			return nil, err
		}
		i, ok := k(v)
		if !ok {
			return math.NaN(), nil
		}
		return i, nil
	}
}

func vecMap(fn string, k func([]float64) []float64) exprBuiltinFunc {
	return func(args ...any) (any, error) {
		if len(args) != 1 {
			return nil, builtinArityError(fn, 1, len(args))
		}
		v, err := vectorArg(fn, 0, args[0])
		if err != nil {
			return nil, err
		}
		return k(v), nil
	}
}

// invalidNormalizeMode is the coded refusal of a vnormalize mode other
// than "l1" / "l2".
func invalidNormalizeMode(mode string, cause error) *errors.CodedError {
	ce := errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		fmt.Sprintf("vnormalize(): mode %q is not %q or %q (case-sensitive)", mode, vecmath.NormL1, vecmath.NormL2),
		map[string]any{"function": "vnormalize", "mode": mode, "valid": []string{vecmath.NormL1, vecmath.NormL2}})
	ce.Cause = cause
	return ce
}

func vecNormalizeBuiltin(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, builtinArityError("vnormalize", 2, len(args))
	}
	v, err := vectorArg("vnormalize", 0, args[0])
	if err != nil {
		return nil, err
	}
	mode, ok := args[1].(string)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("vnormalize(): argument 2 is %T, not a mode string (\"l1\" or \"l2\")", args[1]),
			map[string]any{"function": "vnormalize", "argument": 2})
	}
	out, err := vecmath.Normalize(v, mode)
	if err != nil {
		return nil, invalidNormalizeMode(mode, err)
	}
	return out, nil
}

func scalarUnary(fn string, k func(float64) float64) exprBuiltinFunc {
	return func(args ...any) (any, error) {
		if len(args) != 1 {
			return nil, builtinArityError(fn, 1, len(args))
		}
		x, err := scalarArg(fn, 0, args[0])
		if err != nil {
			return nil, err
		}
		return k(x), nil
	}
}

func scalarPowBuiltin(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, builtinArityError("pow", 2, len(args))
	}
	x, err := scalarArg("pow", 0, args[0])
	if err != nil {
		return nil, err
	}
	y, err := scalarArg("pow", 1, args[1])
	if err != nil {
		return nil, err
	}
	return vecmath.Pow(x, y), nil
}

// vectorResultError refuses a vector as an expression's top-level
// result: the vector built-ins compose inside an expression, but an
// attribute value, a filter verdict and a total() term are scalars.
func vectorResultError(what string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		fmt.Sprintf("%s expression returns a vector; reduce it to a scalar (vsum, vmean, dot, cosine, argmax, ...) — vcenter / vzscore / vnormalize are usable inside an expression only", what),
		map[string]any{"expression_site": what})
}

// vectorStaticRefusal is the compile-time check of the reserved
// built-ins a parse can judge without a row: argument counts, a literal
// vnormalize mode, two literal arrays of unequal length handed to dot /
// dist / cosine, and a vector-returning call as the whole expression.
// A source that does not parse returns nil — its compile reports the
// syntax error.
func vectorStaticRefusal(src, what string) error {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil
	}
	if c, ok := tree.Node.(*exprast.CallNode); ok {
		if id, ok := c.Callee.(*exprast.IdentifierNode); ok && exprBuiltinVectorResult[id.Value] {
			return vectorResultError(what)
		}
	}
	v := &vectorStaticVisitor{}
	exprast.Walk(&tree.Node, v)
	return v.err
}

type vectorStaticVisitor struct{ err error }

func (v *vectorStaticVisitor) Visit(node *exprast.Node) {
	if v.err != nil {
		return
	}
	c, ok := (*node).(*exprast.CallNode)
	if !ok {
		return
	}
	id, ok := c.Callee.(*exprast.IdentifierNode)
	if !ok || !exprbuiltins.IsReserved(id.Value) {
		return
	}
	fn, n := id.Value, len(c.Arguments)
	ar, ok := exprBuiltinArity[fn]
	if !ok {
		ar = [2]int{1, 1}
	}
	if n < ar[0] || n > ar[1] {
		v.err = errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s() takes %d argument(s); got %d", fn, ar[0], n),
			map[string]any{"function": fn, "arguments": n})
		return
	}
	switch fn {
	case "dot", "dist", "cosine":
		a, aok := c.Arguments[0].(*exprast.ArrayNode)
		b, bok := c.Arguments[1].(*exprast.ArrayNode)
		if aok && bok && len(a.Nodes) != len(b.Nodes) {
			v.err = vectorLengthMismatch(fn, len(a.Nodes), len(b.Nodes), nil)
		}
	case "vnormalize":
		if s, ok := c.Arguments[1].(*exprast.StringNode); ok {
			if _, err := vecmath.Normalize(nil, s.Value); err != nil {
				v.err = invalidNormalizeMode(s.Value, err)
			}
		}
	}
}

// exprRefusal returns the request's own refusal inside an expression
// evaluation error — a coef() model refusal, an unequal-length vector
// pair, or an invalid vnormalize mode — so the run fails under that
// code rather than the generic PROCESSING_RUNTIME wrapper, and a
// null-input row never swallows it.
func exprRefusal(err error) *errors.CodedError {
	if mr := modelRefusal(err); mr != nil {
		return mr
	}
	for e := err; e != nil; {
		var ce *errors.CodedError
		if !stderrors.As(e, &ce) {
			return nil
		}
		if ce.Code == errors.PROCESSING_VECTOR_LENGTH_MISMATCH ||
			(ce.Code == errors.PROCESSING_CONFIG && stderrors.Is(ce.Cause, vecmath.ErrInvalidNormalizeMode)) {
			return ce
		}
		e = ce.Cause
	}
	return nil
}
