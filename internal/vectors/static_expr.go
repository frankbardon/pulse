package vectors

import (
	"strconv"
	"strings"

	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/internal/vecmath"
	"github.com/frankbardon/pulse/types"
)

// The static rule of the reserved expression built-ins: every fault a
// parse can judge without a row. The runtime build
// (processing.newExprProgram) refuses the first before any record is
// read, and predict reports the same code, message and details through
// StaticIssues, so the two cannot drift. In walk order (post-order,
// first hit wins), after a top-level check:
//
//   - a vector-returning built-in (VectorResultBuiltins) as the whole
//     expression: VectorResultRefusal (PROCESSING_CONFIG);
//   - a reserved built-in called with an argument count outside
//     BuiltinArity (default exactly one): PROCESSING_CONFIG {function,
//     arguments};
//   - dot / dist / cosine over two arguments of known unequal length:
//     LengthScope.PairIssue (PROCESSING_VECTOR_LENGTH_MISMATCH);
//   - vnormalize with a literal mode other than "l1" / "l2":
//     InvalidNormalizeMode (PROCESSING_CONFIG). A non-literal mode is
//     judged per row.
//
// A source that does not parse has no static issue — its compile
// reports the syntax error.

// BuiltinArity is each reserved built-in's [min, max] argument count;
// a reserved name absent here takes exactly one.
var BuiltinArity = map[string][2]int{
	"dot": {2, 2}, "dist": {2, 2}, "cosine": {2, 2},
	"vnormalize": {2, 2}, "pow": {2, 2},
	"jaccard": {2, 2}, "dice": {2, 2}, "hamming": {2, 2}, "overlap": {2, 2},
	"ref": {2, 2},
}

// VectorResultBuiltins names the built-ins that return a vector.
var VectorResultBuiltins = map[string]bool{
	"vcenter": true, "vzscore": true, "vnormalize": true, "ref": true,
}

// VectorResultRefusal refuses a vector as an expression's top-level
// result: the vector built-ins compose inside an expression, but an
// attribute value, a filter verdict and a total() term are scalars.
// what names the site ("formula", "filter", "formula total()
// argument").
func VectorResultRefusal(what string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		what+" expression returns a vector; reduce it to a scalar (vsum, vmean, dot, cosine, argmax, ...) — vcenter / vzscore / vnormalize are usable inside an expression only",
		map[string]any{"expression_site": what})
}

// InvalidNormalizeMode is the coded refusal of a vnormalize mode other
// than "l1" / "l2" (case-sensitive): PROCESSING_CONFIG {function, mode,
// valid}, cause attached.
// The one constructor is exprenv.InvalidNormalizeMode, which the
// run-time vnormalize raises.
func InvalidNormalizeMode(mode string, cause error) *errors.CodedError {
	return exprenv.InvalidNormalizeMode(mode, cause)
}

// ExprStaticIssue is src's first static built-in issue at site what,
// lenOf answering the member count of a bare vector name (nil: no
// named vectors) — nil when there is none or src does not parse.
func ExprStaticIssue(src, what string, lenOf func(string) (int, bool)) *errors.CodedError {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil
	}
	if c, ok := tree.Node.(*exprast.CallNode); ok {
		if id, ok := c.Callee.(*exprast.IdentifierNode); ok && VectorResultBuiltins[id.Value] {
			return VectorResultRefusal(what)
		}
	}
	v := &staticVisitor{lengths: NewLengthScope(src, lenOf)}
	exprast.Walk(&tree.Node, v)
	return v.issue
}

type staticVisitor struct {
	lengths LengthScope
	issue   *errors.CodedError
}

func (v *staticVisitor) Visit(node *exprast.Node) {
	if v.issue != nil {
		return
	}
	c, ok := (*node).(*exprast.CallNode)
	if !ok {
		return
	}
	id, ok := c.Callee.(*exprast.IdentifierNode)
	if !ok || !exprenv.IsReserved(id.Value) {
		return
	}
	fn, n := id.Value, len(c.Arguments)
	ar, ok := BuiltinArity[fn]
	if !ok {
		ar = [2]int{1, 1}
	}
	if n < ar[0] || n > ar[1] {
		v.issue = errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fn+"() takes "+strconv.Itoa(ar[0])+" argument(s); got "+strconv.Itoa(n),
			map[string]any{"function": fn, "arguments": n})
		return
	}
	switch fn {
	case "dot", "dist", "cosine":
		v.issue = v.lengths.PairIssue(c)
	case "vnormalize":
		if s, ok := c.Arguments[1].(*exprast.StringNode); ok {
			if _, err := vecmath.Normalize(nil, s.Value); err != nil {
				v.issue = InvalidNormalizeMode(s.Value, err)
			}
		}
	}
}

// StaticIssues returns, per request expression (ExprSlots, request
// order), its first static built-in issue against the request's named
// vectors resolved over schema — the predict twin of the runtime
// build, which refuses the first. Each slot is judged at the site the
// build compiles it as: an ATTR_FORMULA as "formula", its total()
// arguments first (each as "formula total() argument"; skipped when
// totalcall refuses the formula — that refusal comes first), every
// filter expression as "filter".
func StaticIssues(req *types.Request, schema *encoding.Schema, resolved []Resolved) []*errors.CodedError {
	var out []*errors.CodedError
	lenOf := ExprVectorLen(resolved, schema)
	for _, slot := range ExprSlots(req) {
		if ce := slotStaticIssue(slot, lenOf); ce != nil {
			out = append(out, ce)
		}
	}
	return out
}

func slotStaticIssue(slot ExprSlot, lenOf func(string) (int, bool)) *errors.CodedError {
	if !strings.HasPrefix(slot.Path, "attributes[") || !strings.HasSuffix(slot.Path, "].expression") {
		return ExprStaticIssue(slot.Src, "filter", lenOf)
	}
	if totalcall.Contains(slot.Src) {
		if totalcall.Refusal(slot.Src, totalcall.FormulaSite) != nil {
			return nil
		}
		for _, arg := range totalcall.Args(slot.Src) {
			if ce := ExprStaticIssue(arg, "formula total() argument", lenOf); ce != nil {
				return ce
			}
		}
	}
	return ExprStaticIssue(slot.Src, "formula", lenOf)
}
