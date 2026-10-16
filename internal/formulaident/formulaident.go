// Package formulaident is the one OVERLAY_FORMULA identifier reading:
// the AST walk that splits a formula's references into variables,
// called functions and Compose `slot.<label>.<field>` paths (Collect),
// the function set an overlay formula may call (AllowedFuncs), and the
// Compose-host namespace plus its refusal (ComposeRefusal).
//
// Predict (internal/descriptor) and the runtime Compose dispatcher
// (internal/processing) both call ComposeRefusal on the same inputs —
// the reference slot's authored shape, the slots the spec names and
// the instance's functions — so a Compose FORMULA that names an
// identifier outside its namespace is refused with the same code,
// message and Details on both sides, before the runtime compiles it.
//
// Leaf: stdlib + expr-lang parser/ast + types + errors +
// internal/exprenv. No-execute.
package formulaident

import (
	"sort"
	"strings"

	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/types"
)

// Refs is the identifier split of one formula.
type Refs struct {
	// Vars holds every plain identifier that is neither a call callee
	// nor a member-chain root.
	Vars map[string]struct{}
	// Funcs holds every identifier called as a function (a CallNode
	// callee). expr-lang stdlib helpers parse as BuiltinNodes and never
	// land here.
	Funcs map[string]struct{}
	// SlotPaths holds every outermost member chain rooted at `slot`,
	// flattened ("slot.us.cell"). Chains rooted elsewhere are dropped:
	// their root identifier governs them.
	SlotPaths map[string]struct{}
}

// Collect walks node and splits its identifier references.
func Collect(node *exprast.Node) Refs {
	pre := &prescan{
		callees:      map[*exprast.IdentifierNode]struct{}{},
		memberRoots:  map[*exprast.IdentifierNode]struct{}{},
		innerMembers: map[*exprast.MemberNode]struct{}{},
	}
	exprast.Walk(node, pre)
	v := &visitor{
		refs: Refs{
			Vars:      map[string]struct{}{},
			Funcs:     map[string]struct{}{},
			SlotPaths: map[string]struct{}{},
		},
		pre: pre,
	}
	exprast.Walk(node, v)
	return v.refs
}

// prescan stamps the IdentifierNodes acting as call callees or
// member-chain roots, and the MemberNodes nested inside a longer chain.
// exprast.Walk is post-order (children first), so the main walk could
// not classify a leaf before visiting its parent without it.
type prescan struct {
	callees      map[*exprast.IdentifierNode]struct{}
	memberRoots  map[*exprast.IdentifierNode]struct{}
	innerMembers map[*exprast.MemberNode]struct{}
}

func (p *prescan) Visit(node *exprast.Node) {
	if node == nil || *node == nil {
		return
	}
	switch n := (*node).(type) {
	case *exprast.CallNode:
		if id, ok := n.Callee.(*exprast.IdentifierNode); ok {
			p.callees[id] = struct{}{}
		}
	case *exprast.MemberNode:
		if root, _, ok := flattenMemberChain(n); ok {
			p.memberRoots[root] = struct{}{}
		}
		if inner, ok := n.Node.(*exprast.MemberNode); ok {
			p.innerMembers[inner] = struct{}{}
		}
	}
}

type visitor struct {
	refs Refs
	pre  *prescan
}

func (v *visitor) Visit(node *exprast.Node) {
	if node == nil || *node == nil {
		return
	}
	switch n := (*node).(type) {
	case *exprast.CallNode:
		if id, ok := n.Callee.(*exprast.IdentifierNode); ok {
			v.refs.Funcs[id.Value] = struct{}{}
		}
	case *exprast.MemberNode:
		if _, inner := v.pre.innerMembers[n]; inner {
			return
		}
		root, path, ok := flattenMemberChain(n)
		if ok && root.Value == "slot" {
			v.refs.SlotPaths[path] = struct{}{}
		}
	case *exprast.IdentifierNode:
		if _, isCallee := v.pre.callees[n]; isCallee {
			return
		}
		if _, isRoot := v.pre.memberRoots[n]; isRoot {
			return
		}
		v.refs.Vars[n.Value] = struct{}{}
	}
}

// flattenMemberChain unwinds `slot.foo.cell` to (IdentifierNode("slot"),
// "slot.foo.cell", true); false when the chain is not rooted at a
// single identifier or a property is not a literal string.
func flattenMemberChain(n *exprast.MemberNode) (*exprast.IdentifierNode, string, bool) {
	var parts []string
	cur := exprast.Node(n)
	for {
		switch node := cur.(type) {
		case *exprast.MemberNode:
			str, ok := node.Property.(*exprast.StringNode)
			if !ok {
				return nil, "", false
			}
			parts = append([]string{str.Value}, parts...)
			cur = node.Node
		case *exprast.IdentifierNode:
			path := node.Value
			for _, p := range parts {
				path += "." + p
			}
			return node, path, true
		default:
			return nil, "", false
		}
	}
}

// AllowedFuncs is the function set an OVERLAY_FORMULA may call: the
// overlay scalar-maths built-ins, the set helpers, every embedder
// ExprFunction name and, when the instance registers a LookupTable,
// `lookup` — exactly what the runtime overlay compile registers.
func AllowedFuncs(exprFunctions []string, hasLookup bool) map[string]struct{} {
	out := map[string]struct{}{}
	for _, name := range exprenv.OverlayNames() {
		out[name] = struct{}{}
	}
	for _, name := range exprenv.SetHelperNames() {
		out[name] = struct{}{}
	}
	for _, name := range exprFunctions {
		if name != "" {
			out[name] = struct{}{}
		}
	}
	if hasLookup {
		out["lookup"] = struct{}{}
	}
	return out
}

// Slot is one Compose slot a FORMULA spec names (its reference or a
// target): the resolved label and the slot's authored host shape.
type Slot struct {
	Label string
	Shape types.OverlayShape
}

// ComposeRefVar is the reference-slot variable of a Compose FORMULA
// over a host of shape: ref_cell (MATRIX), ref_value (SERIES), ref
// (SCALAR).
func ComposeRefVar(shape types.OverlayShape) string {
	switch shape {
	case types.OverlayShapeMatrix:
		return "ref_cell"
	case types.OverlayShapeSeries:
		return "ref_value"
	}
	return "ref"
}

// SlotField is the field a `slot.<label>.<field>` path reads on a slot
// of shape: cell (MATRIX) or value (SERIES / SCALAR).
func SlotField(shape types.OverlayShape) string {
	if shape == types.OverlayShapeMatrix {
		return "cell"
	}
	return "value"
}

// ComposeVars is a Compose FORMULA's variable namespace over a host of
// shape: types.FormulaNamespace(shape) plus ComposeRefVar(shape).
func ComposeVars(shape types.OverlayShape) map[string]struct{} {
	out := map[string]struct{}{}
	for _, v := range types.FormulaNamespace(shape) {
		out[v] = struct{}{}
	}
	out[ComposeRefVar(shape)] = struct{}{}
	return out
}

// ComposeSlotPaths is every `slot.<label>.<field>` path a Compose
// FORMULA naming slots may read.
func ComposeSlotPaths(slots []Slot) map[string]struct{} {
	out := map[string]struct{}{}
	for _, s := range slots {
		out["slot."+s.Label+"."+SlotField(s.Shape)] = struct{}{}
	}
	return out
}

// ComposeFormula reads params["formula"]: (formula, "") when it is a
// non-empty string, else ("", reason).
func ComposeFormula(params map[string]any) (formula, reason string) {
	raw, present := params["formula"]
	if !present {
		return "", ReasonMissingFormula
	}
	f, ok := raw.(string)
	if !ok || f == "" {
		return "", ReasonEmptyFormula
	}
	return f, ""
}

// ComposeRefusal judges the Compose OVERLAY_FORMULA spec at index over
// a host of shape (the reference slot's authored shape), naming slots
// (its reference and targets) and allowed to call funcs. nil when the
// formula reads only its namespace; otherwise, first hit wins:
//
//   - no usable params.formula: MissingFormula's
//     PULSE_OVERLAY_PARAM_MISSING, Details {index, kind, param, reason};
//   - a parse failure: PULSE_OVERLAY_FORMULA_PARSE_ERROR, Details
//     {index, kind, formula, parse_error};
//   - an unknown variable, function or slot path (each in sorted
//     order): PULSE_OVERLAY_FORMULA_INVALID_IDENT, Details
//     {index, kind, unknown_identifier, host_shape, formula} plus
//     `allowed`, `allowed_funcs` or `allowed_slots`.
func ComposeRefusal(kind types.OverlayKind, params map[string]any, index int, shape types.OverlayShape, slots []Slot, funcs map[string]struct{}) *errors.CodedError {
	formula, reason := ComposeFormula(params)
	if reason != "" {
		return MissingFormula(string(kind), "overlay "+string(kind)+" requires params.formula (a non-empty string)", index, reason)
	}
	tree, err := exprparser.Parse(formula)
	if err != nil {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR,
			"overlay "+string(kind)+" formula failed to parse: "+err.Error(),
			map[string]any{"index": index, "kind": string(kind), "formula": formula, "parse_error": err.Error()})
	}
	refs := Collect(&tree.Node)
	unknown := func(ident, msg, allowedKey string, allowed map[string]struct{}) *errors.CodedError {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT,
			"overlay "+string(kind)+" formula "+msg,
			map[string]any{
				"index":              index,
				"kind":               string(kind),
				"unknown_identifier": ident,
				"host_shape":         string(shape),
				allowedKey:           sorted(allowed),
				"formula":            formula,
			})
	}
	vars := ComposeVars(shape)
	for _, ident := range sorted(refs.Vars) {
		if _, ok := vars[ident]; !ok {
			return unknown(ident, "references unknown identifier "+ident+" for "+string(shape)+"-shape Compose host", "allowed", vars)
		}
	}
	for _, fn := range sorted(refs.Funcs) {
		if _, ok := funcs[fn]; !ok {
			return unknown(fn, "calls unknown function "+fn, "allowed_funcs", funcs)
		}
	}
	paths := ComposeSlotPaths(slots)
	for _, p := range sorted(refs.SlotPaths) {
		if _, ok := paths[p]; !ok {
			return unknown(p, "references "+p+", not a field of a slot this spec names (reference or targets)", "allowed_slots", paths)
		}
	}
	return nil
}

// BuiltinRefusal refuses a FORMULA calling a reserved built-in outside
// the overlay scalar subset (exprenv.OverlayRefused — the vector
// families and the calendar helpers) with PULSE_OVERLAY_FORMULA_INVALID_IDENT, Details {kind,
// unknown_identifier, allowed_builtins, formula} plus `index` when
// index >= 0. nil when the formula calls none (or fails to parse).
// Every host runs it before its identifier gate, predict and runtime
// alike, so a vector built-in reads this message, not "unknown
// function".
func BuiltinRefusal(kind types.OverlayKind, formula string, index int) *errors.CodedError {
	refused := exprident.FirstCallee(formula, exprenv.OverlayRefused)
	if refused == "" {
		return nil
	}
	details := map[string]any{
		"kind":               string(kind),
		"unknown_identifier": refused,
		"allowed_builtins":   exprenv.OverlayNames(),
		"formula":            formula,
	}
	if index >= 0 {
		details["index"] = index
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT,
		"overlay "+string(kind)+" formula calls "+refused+", a vector or calendar built-in OVERLAY_FORMULA does not accept (scalar maths only: "+strings.Join(exprenv.OverlayNames(), ", ")+")",
		details)
}

// RequestVars is a Request-host FORMULA's variable namespace over a
// host of shape: types.FormulaNamespace(shape), plus `baseline` on a
// SERIES host whose spec sets params.baseline_position (baseline).
func RequestVars(shape types.OverlayShape, baseline bool) map[string]struct{} {
	out := map[string]struct{}{}
	for _, v := range types.FormulaNamespace(shape) {
		out[v] = struct{}{}
	}
	if shape == types.OverlayShapeSeries && baseline {
		out["baseline"] = struct{}{}
	}
	return out
}

// RequestRefusal judges the identifiers of the Request-host
// OVERLAY_FORMULA spec at index whose parsed formula is node, over a
// host of shape, against the variables vars (RequestVars) and the
// callable funcs (AllowedFuncs). nil when every reference is in reach;
// otherwise PULSE_OVERLAY_FORMULA_INVALID_IDENT for the first unknown
// variable, then function, then `slot.<label>.<field>` path (the slot
// namespace is Compose-only), each class in sorted order. Details
// {index, kind, unknown_identifier, host_shape, formula} plus
// `allowed`, `allowed_funcs` or `reason`.
func RequestRefusal(kind types.OverlayKind, formula string, node *exprast.Node, index int, shape types.OverlayShape, vars, funcs map[string]struct{}) *errors.CodedError {
	refs := Collect(node)
	details := func(ident, key string, val any) map[string]any {
		return map[string]any{
			"index":              index,
			"kind":               string(kind),
			"unknown_identifier": ident,
			"host_shape":         string(shape),
			key:                  val,
			"formula":            formula,
		}
	}
	for _, ident := range sorted(refs.Vars) {
		if _, ok := vars[ident]; !ok {
			return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT,
				"overlay "+string(kind)+" formula references unknown identifier "+ident+" for "+string(shape)+"-shape host",
				details(ident, "allowed", sorted(vars)))
		}
	}
	for _, fn := range sorted(refs.Funcs) {
		if _, ok := funcs[fn]; !ok {
			return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT,
				"overlay "+string(kind)+" formula calls unknown function "+fn,
				details(fn, "allowed_funcs", sorted(funcs)))
		}
	}
	if paths := sorted(refs.SlotPaths); len(paths) > 0 {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT,
			"overlay "+string(kind)+" formula references Compose slot "+paths[0]+" on a Request host (slot namespace is Compose-only)",
			details(paths[0], "reason", "slot namespace is Compose-only"))
	}
	return nil
}

func sorted(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
