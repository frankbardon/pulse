package vectors

import (
	"bytes"
	"strconv"
	"strings"

	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/types"
)

// reference_expr.go judges the expression form of a reference vector,
// ref("table", key), with one set of rules the predict pass
// (ReferenceIssues) and the runtime build (internal/processing's patch
// of the compiled expression) both read:
//
//   - the table argument is a string literal naming a registered table
//     (else PULSE_VECTOR_REFERENCE_UNKNOWN, reason table_not_literal /
//     table_unknown) — its labels must be known before any row;
//   - a literal key names one of its profiles (reason key_unknown; a
//     computed key is checked per row);
//   - a ref() that is an argument of dot / dist / cosine
//     (exprenv.IsVectorPair) — directly or under vcenter / vzscore
//     / vnormalize (exprenv.PreservesLabels) — is aligned BY LABEL
//     to the other argument: a named vector, another reference, or a
//     label-preserving transform of either. The label sets must be equal
//     (PULSE_VECTOR_REFERENCE_MISMATCH reason labels_differ); an
//     unlabelled other side (a literal array, any other expression) is
//     refused (reason unlabeled_partner) — never paired by position.
//     When both sides are references the second is aligned to the
//     first.
//
// A ref() anywhere else (vsum(ref(...)), an embedder function's
// argument) yields the profile in the table's own label order.

// RefScope is what an expression's references are judged against.
type RefScope struct {
	// LabelsOf returns the labels of the named vector an identifier
	// binds (false when the identifier is not a bound named vector: a
	// schema field of that name shadows the vector, as it does when the
	// expression is bound).
	LabelsOf func(name string) ([]string, bool)
	// Tables are the instance's reference-vector tables.
	Tables ReferenceTables
	// lets are the expression's let-bound names, which shadow vectors.
	lets map[string]bool
}

// WithLets returns s shadowing every let-bound name src declares.
func (s RefScope) WithLets(src string) RefScope {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return s
	}
	lets := map[string]bool{}
	exprast.Walk(&tree.Node, letCollector(lets))
	s.lets = lets
	return s
}

type letCollector map[string]bool

func (l letCollector) Visit(node *exprast.Node) {
	if d, ok := (*node).(*exprast.VariableDeclaratorNode); ok {
		l[d.Name] = true
	}
}

func (s RefScope) labelsOf(name string) ([]string, bool) {
	if s.lets[name] || s.LabelsOf == nil {
		return nil, false
	}
	return s.LabelsOf(name)
}

// ExprRefCall returns n as a call of the ref() built-in with at least
// its two arguments (the runtime build appends a third), else nil.
func ExprRefCall(n exprast.Node) *exprast.CallNode {
	c, ok := n.(*exprast.CallNode)
	if !ok || len(c.Arguments) < 2 {
		return nil
	}
	id, ok := c.Callee.(*exprast.IdentifierNode)
	if !ok || id.Value != exprenv.RefFunc {
		return nil
	}
	return c
}

// ExprRefTable returns a ref() call's table argument when it is a
// string literal.
func ExprRefTable(c *exprast.CallNode) (string, bool) {
	s, ok := c.Arguments[0].(*exprast.StringNode)
	if !ok {
		return "", false
	}
	return s.Value, true
}

// ExprRefKey returns a ref() call's key argument when it is a string
// literal.
func ExprRefKey(c *exprast.CallNode) (string, bool) {
	s, ok := c.Arguments[1].(*exprast.StringNode)
	if !ok {
		return "", false
	}
	return s.Value, true
}

// ExprRefSlot follows slot down the first argument of label-preserving
// transforms to the ref() call at the bottom; nil when the chain ends
// anywhere else.
func ExprRefSlot(slot *exprast.Node) *exprast.Node {
	for slot != nil && *slot != nil {
		if ExprRefCall(*slot) != nil {
			return slot
		}
		c, ok := (*slot).(*exprast.CallNode)
		if !ok || len(c.Arguments) == 0 {
			return nil
		}
		id, ok := c.Callee.(*exprast.IdentifierNode)
		if !ok || !exprenv.PreservesLabels(id.Value) {
			return nil
		}
		slot = &c.Arguments[0]
	}
	return nil
}

// exprLabels returns the labels of a vector-valued node — a bound named
// vector, a ref() of a registered literal table, or a label-preserving
// transform of either — and the name to report it by.
func (s RefScope) exprLabels(n exprast.Node) (labels []string, name string, ok bool) {
	for {
		switch v := n.(type) {
		case *exprast.IdentifierNode:
			l, ok := s.labelsOf(v.Value)
			return l, v.Value, ok
		case *exprast.CallNode:
			if rc := ExprRefCall(v); rc != nil {
				table, ok := ExprRefTable(rc)
				if !ok {
					return nil, "", false
				}
				t, ok := s.Tables[table]
				return t.Labels, "", ok
			}
			id, ok := v.Callee.(*exprast.IdentifierNode)
			if !ok || !exprenv.PreservesLabels(id.Value) || len(v.Arguments) == 0 {
				return nil, "", false
			}
			n = v.Arguments[0]
		default:
			return nil, "", false
		}
	}
}

// CheckRefCall judges one ref() call on its own: a literal registered
// table and, when the key is a literal, a registered key. nil when it
// passes.
func (s RefScope) CheckRefCall(c *exprast.CallNode) *errors.CodedError {
	table, ok := ExprRefTable(c)
	if !ok {
		return errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_REFERENCE_UNKNOWN,
			"ref(): the table argument must be a string literal naming a registered reference vector table, so its labels are known before any row",
			map[string]any{"reason": ReferenceReasonTableNotLiteral, "valid": s.Tables.Names()})
	}
	t, cerr := s.Tables.Table(table)
	if cerr != nil {
		return cerr
	}
	if key, ok := ExprRefKey(c); ok && !t.HasKey(key) {
		return t.KeyUnknown(table, key)
	}
	return nil
}

// RefAlignment is how one ref() argument of a pair call is aligned.
type RefAlignment struct {
	// Slot holds the ref() call (possibly under label-preserving
	// transforms of the pair argument).
	Slot *exprast.Node
	// Call is the ref() call; Table its table.
	Call  *exprast.CallNode
	Table string
	// Perm reorders the table's profile into the other side's label
	// order: element i is the profile's value at Perm[i].
	Perm []int
}

// AlignPair judges one call of a pair built-in (dot, dist, cosine; any
// other node returns nothing): every ref() argument whose table is a
// registered literal gets its alignment to the other argument, or the
// issue that refuses it. A ref() whose own call fails CheckRefCall is
// left to that check.
func (s RefScope) AlignPair(c *exprast.CallNode) ([]RefAlignment, []*errors.CodedError) {
	id, ok := c.Callee.(*exprast.IdentifierNode)
	if !ok || !exprenv.IsVectorPair(id.Value) || len(c.Arguments) != 2 {
		return nil, nil
	}
	var out []RefAlignment
	var issues []*errors.CodedError
	for i := 0; i < 2; i++ {
		slot := ExprRefSlot(&c.Arguments[i])
		if slot == nil {
			continue
		}
		rc := ExprRefCall(*slot)
		table, ok := ExprRefTable(rc)
		if !ok {
			continue
		}
		t, ok := s.Tables[table]
		if !ok {
			continue
		}
		other := c.Arguments[1-i]
		if i == 0 && ExprRefSlot(&c.Arguments[1]) != nil {
			// Both sides are references: the second aligns to the first.
			continue
		}
		labels, name, ok := s.exprLabels(other)
		if !ok {
			issues = append(issues, errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_REFERENCE_MISMATCH,
				id.Value+"(): ref("+strconv.Quote(table)+", …) is compared with a vector that carries no labels (a literal array or a computed vector); a reference is matched by label, never by position — compare it with a named vector, another ref(), or vcenter / vzscore / vnormalize of one",
				map[string]any{"reason": ReferenceReasonUnlabeledPartner, "table": table, "function": id.Value}))
			continue
		}
		perm, cerr := t.Match(table, name, labels)
		if cerr != nil {
			cerr.Details["function"] = id.Value
			issues = append(issues, cerr)
			continue
		}
		out = append(out, RefAlignment{Slot: slot, Call: rc, Table: table, Perm: perm})
	}
	return out, issues
}

// ExprReferenceIssues returns every reference issue in src, in walk
// order (post-order: a ref() call's own check before the pair that reads
// it). Nil when src calls no ref() or does not parse (its compile
// reports that).
func ExprReferenceIssues(src string, s RefScope) []*errors.CodedError {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil
	}
	s = s.WithLets(src)
	v := &refIssueVisitor{s: s}
	exprast.Walk(&tree.Node, v)
	return v.issues
}

type refIssueVisitor struct {
	s      RefScope
	issues []*errors.CodedError
}

func (v *refIssueVisitor) Visit(node *exprast.Node) {
	c, ok := (*node).(*exprast.CallNode)
	if !ok {
		return
	}
	if rc := ExprRefCall(c); rc != nil {
		if len(rc.Arguments) == 2 {
			if cerr := v.s.CheckRefCall(rc); cerr != nil {
				v.issues = append(v.issues, cerr)
			}
		}
		return
	}
	_, issues := v.s.AlignPair(c)
	v.issues = append(v.issues, issues...)
}

// mentionsRef reports, without allocating, whether any expression
// ExprSlots would return names ref at all — the common request skips
// the parse.
func mentionsRef(req *types.Request) bool {
	if req == nil {
		return false
	}
	has := func(fs []*types.Filterer) bool {
		for _, f := range fs {
			if f != nil && f.Type == types.FILTER_EXPRESSION && strings.Contains(f.Expression, exprenv.RefFunc) {
				return true
			}
		}
		return false
	}
	if has(req.Filterers) {
		return true
	}
	for _, a := range req.Attributes {
		if a == nil {
			continue
		}
		if a.Type == types.ATTR_FORMULA && strings.Contains(a.Expression, exprenv.RefFunc) {
			return true
		}
		if bytes.Contains(a.Params, []byte(exprenv.RefFunc)) && has(AttributeFitOn(a.Type, a)) {
			return true
		}
	}
	return false
}

// ExprSlot is one request expression the reference / kind checks read.
type ExprSlot struct {
	// Path is the request path ("filterers[0].expression", ...).
	Path string
	// Src is the expression.
	Src string
}

// ExprSlots returns every request expression an expression-bound vector
// or reference can appear in, in request order: each FILTER_EXPRESSION
// filterer, then per attribute its ATTR_FORMULA expression and its
// params.fit_on (and centroid reference filter) FILTER_EXPRESSION
// filterers.
func ExprSlots(req *types.Request) []ExprSlot {
	if req == nil {
		return nil
	}
	var out []ExprSlot
	filters := func(prefix string, fs []*types.Filterer) {
		for j, f := range fs {
			if f != nil && f.Type == types.FILTER_EXPRESSION && f.Expression != "" {
				out = append(out, ExprSlot{Path: prefix + "[" + strconv.Itoa(j) + "].expression", Src: f.Expression})
			}
		}
	}
	filters("filterers", req.Filterers)
	for i, a := range req.Attributes {
		if a == nil {
			continue
		}
		at := "attributes[" + strconv.Itoa(i) + "]"
		if a.Type == types.ATTR_FORMULA && a.Expression != "" {
			out = append(out, ExprSlot{Path: at + ".expression", Src: a.Expression})
		}
		// AttributeFitOn lists fit_on, then a centroid's filter.
		fs, cf := AttributeFitOn(a.Type, a), ComparisonCentroidFilter(a.Type, a)
		filters(at+".params.fit_on", fs[:len(fs)-len(cf)])
		filters(at+".params.reference.centroid.filter", cf)
	}
	return out
}

// ReferenceIssues returns every reference issue of req's comparison
// attributes' table references (ComparisonReferenceIssues) and of its
// expressions (ExprSlots), each with details.slot, against tables and
// the request's named vectors resolved over schema (a vector named like
// a schema field is shadowed by the field, as at bind time). The
// predict twin of the runtime build's refusal: the build raises the
// first issue of the first slot it binds.
func ReferenceIssues(req *types.Request, schema *encoding.Schema, resolved []Resolved, tables ReferenceTables) []*errors.CodedError {
	out := ComparisonReferenceIssues(req, schema, resolved, tables)
	if !mentionsRef(req) {
		return out
	}
	scope := RefScope{
		Tables: tables,
		LabelsOf: func(name string) ([]string, bool) {
			if schema != nil && schema.Field(name) != nil && !IsNativeVector(name, schema) {
				return nil, false
			}
			v, ok := Find(resolved, name)
			if !ok {
				return nil, false
			}
			return v.Labels, true
		},
	}
	for _, slot := range ExprSlots(req) {
		for _, cerr := range ExprReferenceIssues(slot.Src, scope) {
			if cerr.Details == nil {
				cerr.Details = map[string]any{}
			}
			cerr.Details["slot"] = slot.Path
			out = append(out, cerr)
		}
	}
	return out
}

// ComparisonReferenceIssues returns, in request order, the table issue
// (ComparisonTableIssue) of every comparison attribute slot
// (ATTR_PROFILE_SIMILARITY, ATTR_VEC_DISTANCE) whose members and params
// judge — a slot that does not is the field-reference pass's to refuse.
// resolved are the request's named vectors over schema.
func ComparisonReferenceIssues(req *types.Request, schema *encoding.Schema, resolved []Resolved, tables ReferenceTables) []*errors.CodedError {
	if req == nil {
		return nil
	}
	var out []*errors.CodedError
	for i, a := range req.Attributes {
		if a == nil || !IsComparisonAttribute(a.Type) {
			continue
		}
		at := attributePath(i)
		var members Resolved
		var err *errors.CodedError
		switch {
		case a.Vector != "" && len(a.Fields) == 0:
			var ok bool
			if members, ok = Find(resolved, a.Vector); !ok {
				continue
			}
		case a.Vector == "" && len(a.Fields) > 0:
			if members, err = ResolveSpec(at+".fields", types.VectorSpec{Fields: a.Fields}, schema); err != nil {
				continue
			}
		default:
			continue
		}
		cfg, err := ComparisonParams(at, a.Type, a.Params, members, schema)
		if err != nil {
			continue
		}
		if _, ce := ComparisonTableIssue(at, cfg, tables); ce != nil {
			out = append(out, ce)
		}
	}
	return out
}
