package vectors

import (
	"strconv"

	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The static vector-length rule of the pair built-ins (dot / dist /
// cosine — processing's vecPair functions). The runtime raises
// PROCESSING_VECTOR_LENGTH_MISMATCH per row when the two arguments
// differ in length; when both lengths are known from the request alone
// the mismatch holds on EVERY row, so the build refuses it before a
// record is read and predict reports the same code, message and
// details. Known means: a literal array (its element count), or a bare
// identifier naming a vector that binds as a vector (a Request.Vectors
// entry or a native vec_* field, its member count = dim). Anything else
// — a nested call, a slice, a let binding, a fitted-model vector, a
// scalar field shadowing a vector name — is unknown and never refused.

// PairLengthFuncs are the pair built-ins whose two vector arguments must
// have equal length.
var PairLengthFuncs = map[string]bool{"dot": true, "dist": true, "cosine": true}

// LengthMismatch is the coded refusal of an unequal-length pair:
// PROCESSING_VECTOR_LENGTH_MISMATCH, details {function, left, right}.
func LengthMismatch(fn string, left, right int, cause error) *errors.CodedError {
	ce := errors.NewCodedErrorWithDetails(errors.PROCESSING_VECTOR_LENGTH_MISMATCH,
		fn+"(): vector length mismatch: "+strconv.Itoa(left)+" vs "+strconv.Itoa(right),
		map[string]any{"function": fn, "left": left, "right": right})
	ce.Cause = cause
	return ce
}

// LengthScope answers the static length of a pair argument in one
// expression.
type LengthScope struct {
	// LenOf is the member count of the vector a bare identifier binds,
	// false when the name binds no vector.
	LenOf func(name string) (int, bool)
	lets  map[string]bool
}

// NewLengthScope is the scope of src: lenOf, with src's let bindings
// shadowing it.
func NewLengthScope(src string, lenOf func(string) (int, bool)) LengthScope {
	s := LengthScope{LenOf: lenOf, lets: map[string]bool{}}
	if tree, err := exprparser.Parse(src); err == nil {
		exprast.Walk(&tree.Node, letCollector(s.lets))
	}
	return s
}

func (s LengthScope) argLen(n exprast.Node) (int, bool) {
	switch a := n.(type) {
	case *exprast.ArrayNode:
		return len(a.Nodes), true
	case *exprast.IdentifierNode:
		if s.lets[a.Value] || s.LenOf == nil {
			return 0, false
		}
		return s.LenOf(a.Value)
	}
	return 0, false
}

// PairIssue is the length refusal of call c, nil unless c is a
// two-argument pair built-in whose arguments have known, unequal
// lengths.
func (s LengthScope) PairIssue(c *exprast.CallNode) *errors.CodedError {
	id, ok := c.Callee.(*exprast.IdentifierNode)
	if !ok || !PairLengthFuncs[id.Value] || len(c.Arguments) != 2 {
		return nil
	}
	l, lok := s.argLen(c.Arguments[0])
	r, rok := s.argLen(c.Arguments[1])
	if !lok || !rok || l == r {
		return nil
	}
	return LengthMismatch(id.Value, l, r, nil)
}

// ExprLengthIssue is the first PairIssue of src in the runtime build's
// walk order (post-order), nil when there is none or src does not parse.
func ExprLengthIssue(src string, lenOf func(string) (int, bool)) *errors.CodedError {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil
	}
	v := &lengthVisitor{s: NewLengthScope(src, lenOf)}
	exprast.Walk(&tree.Node, v)
	return v.issue
}

type lengthVisitor struct {
	s     LengthScope
	issue *errors.CodedError
}

func (v *lengthVisitor) Visit(node *exprast.Node) {
	if v.issue != nil {
		return
	}
	if c, ok := (*node).(*exprast.CallNode); ok {
		v.issue = v.s.PairIssue(c)
	}
}

// ExprVectorLen is the LenOf of an expression over schema with the
// named vectors resolved (Resolve's result): a name binds a vector when
// Find knows it and it is not a scalar schema field (a scalar field
// shadows a same-named vector, as at bind time); its length is the
// member count.
func ExprVectorLen(resolved []Resolved, schema *encoding.Schema) func(string) (int, bool) {
	return func(name string) (int, bool) {
		if schema != nil && schema.Field(name) != nil && !IsNativeVector(name, schema) {
			return 0, false
		}
		v, ok := Find(resolved, name)
		if !ok {
			return 0, false
		}
		return len(v.Members), true
	}
}

// LengthIssues returns, per request expression (ExprSlots, request
// order), its first static length refusal (ExprLengthIssue) against the
// request's named vectors resolved over schema — the predict twin of
// the runtime build, which refuses the first.
func LengthIssues(req *types.Request, schema *encoding.Schema, resolved []Resolved) []*errors.CodedError {
	var out []*errors.CodedError
	lenOf := ExprVectorLen(resolved, schema)
	for _, slot := range ExprSlots(req) {
		if ce := ExprLengthIssue(slot.Src, lenOf); ce != nil {
			out = append(out, ce)
		}
	}
	return out
}
