package descriptor

import (
	"encoding/json"
	"sort"

	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Predict-time AST identifier validation for OVERLAY_FORMULA — the no-
// execute descriptor mirror of the runtime evaluator wiring in
// `processing/overlay_formula.go`. Implements the algorithm laid out in
// the research note
// `.planning/result-overlay-system/research/formula-namespace.md` § 3:
//
//  1. Pull `Params["formula"]` (non-empty string).
//  2. Parse via `expr-lang/expr/parser.Parse` — failures surface as
//     `PULSE_OVERLAY_FORMULA_PARSE_ERROR` with the parser's position
//     info in Details so MCP / CLI envelopes can highlight the failing
//     character offset.
//  3. Walk the AST via `expr-lang/expr/ast.Walk` and collect every
//     identifier reference, distinguishing variable references from
//     call sites (function names live in the function-table arm, not
//     the per-shape variable table).
//  4. Resolve the host shape (MATRIX when Request.Crosstab is set;
//     SERIES when Request.Groups is non-empty; SCALAR otherwise) and
//     build the per-shape allowed set off `types.FormulaNamespace`
//     plus the Compose-specific extras (`ref_cell` / `ref_value` /
//     `ref`) when the spec carries a Compose Reference shape — but the
//     Request-host path here is NOT Compose so those extras do NOT
//     widen the allowed set on this validator. The Compose-side
//     validator (`descriptor/compose.go` `validateComposeOverlaySpec`)
//     reuses the same flatten + walk helpers and widens its own allowed
//     set with the Compose-specific identifiers.
//  5. Widen the allowed set with the embedder-registered
//     `ExprFunctions` from `opts.Extensions` per E5's
//     unified-expression-environment contract. Functions widen the
//     callable surface only — they do NOT widen the variable surface
//     (mirrors `processing.compileFormulaProgramWithExtensions`).
//  6. Reject every identifier not in the allowed set with
//     `PULSE_OVERLAY_FORMULA_INVALID_IDENT` carrying the offending
//     identifier + the per-shape variable enumerate-set so the LLM
//     caller can fix the formula without round-tripping to runtime.
//
// Slot-namespace handling: `slot.<label>.<field>` member chains are
// rejected at this validator (Request-host has no Compose slots).
// The Compose-side validator passes a non-nil `composeLabels` set so
// the same helpers accept slot identifiers against that set instead.
//
// Structural invariants (CLAUDE.md "Predict / Inspect contracts" +
// "What NOT to Do"):
//
//   - This file MUST NOT import `service/` or `processing/`. The AST
//     walker uses `expr-lang/expr/parser` + `expr-lang/expr/ast`
//     directly — the same packages `processing.NeededFields` uses for
//     `ATTR_FORMULA` projection. Both packages live outside `service/`
//     and `processing/` so the no-execute import gate stays clean.
//   - No `fmt.Sprintf` in any JSON-bearing path. Error messages are
//     built with string concatenation; Details maps go through
//     `encoding/json` downstream.

// validateFormulaOverlay is the predict-time identifier walk for
// OVERLAY_FORMULA on a Request-host (in-Request `Request.Overlays`).
// Called from `validateOverlaySpec` for `OverlayKindFormula`.
//
// The validator runs the four-step algorithm: extract Params.formula,
// parse, walk AST, enforce allowed-identifier set. Each failure path
// emits ONE coded error so callers see a single, deterministic
// diagnostic per spec (a malformed spec does not also fire downstream
// scope / ref errors — the unknown-kind gate / per-kind dispatch in
// `validateOverlaySpec` already short-circuited for unknown catalog
// entries, and FORMULA has no Ref / Scope contract worth gating
// further beyond the namespace check).
//
// `composeLabels` is reserved for the Compose-host re-entry point and
// MUST be nil for the Request-host dispatch — Compose slot identifiers
// are rejected against a nil set so `slot.<label>.cell` on
// Request.Overlays surfaces `PULSE_OVERLAY_FORMULA_INVALID_IDENT`.
func validateFormulaOverlay(env *Envelope, req *types.Request, spec *types.OverlaySpec, opts *PredictOptions, index int) {
	formula, ok := extractFormulaParamPredict(env, spec, index)
	if !ok {
		return
	}
	tree, err := exprparser.Parse(formula)
	if err != nil {
		env.AddError(string(errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR),
			"overlay "+string(spec.Kind)+" formula failed to parse: "+err.Error(),
			map[string]any{
				"index":       index,
				"kind":        string(spec.Kind),
				"formula":     formula,
				"parse_error": err.Error(),
			})
		return
	}

	hostShape := inferRequestHostShape(req)
	allowedVars := buildFormulaAllowedVarsRequest(hostShape, spec)
	allowedFuncs := buildFormulaAllowedFuncs(opts)

	// Two-pass traversal: pre-pass collects IdentifierNode pointers
	// acting as call callees and as MemberNode roots (e.g. the leading
	// `slot` in `slot.foo.cell`) so the main walk can SKIP them when
	// it visits IdentifierNode leaves directly. `exprast.Walk` is
	// post-order — children visit BEFORE their parents — so an in-walk
	// "am I a memberRoot?" check would race the parent MemberNode's
	// Visit. The pre-pass fixes the ordering.
	pre := &formulaIdentPrescan{
		callees:      map[*exprast.IdentifierNode]struct{}{},
		memberRoots:  map[*exprast.IdentifierNode]struct{}{},
		innerMembers: map[*exprast.MemberNode]struct{}{},
	}
	exprast.Walk(&tree.Node, pre)

	visitor := &formulaIdentVisitor{
		variables:    map[string]struct{}{},
		funcs:        map[string]struct{}{},
		slotPaths:    map[string]struct{}{},
		callees:      pre.callees,
		memberRoots:  pre.memberRoots,
		innerMembers: pre.innerMembers,
	}
	exprast.Walk(&tree.Node, visitor)

	for ident := range visitor.variables {
		if _, ok := allowedVars[ident]; ok {
			continue
		}
		env.AddError(string(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT),
			"overlay "+string(spec.Kind)+" formula references unknown identifier "+ident+" for "+string(hostShape)+"-shape host",
			map[string]any{
				"index":              index,
				"kind":               string(spec.Kind),
				"unknown_identifier": ident,
				"host_shape":         string(hostShape),
				"allowed":            sortedStringSet(allowedVars),
				"formula":            formula,
			})
		return
	}
	for fn := range visitor.funcs {
		if _, ok := allowedFuncs[fn]; ok {
			continue
		}
		env.AddError(string(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT),
			"overlay "+string(spec.Kind)+" formula calls unknown function "+fn,
			map[string]any{
				"index":              index,
				"kind":               string(spec.Kind),
				"unknown_identifier": fn,
				"host_shape":         string(hostShape),
				"allowed_funcs":      sortedStringSet(allowedFuncs),
				"formula":            formula,
			})
		return
	}
	// Slot identifiers (`slot.<label>.<field>`) fire on Request-host
	// because the Compose slot namespace is meaningless without a
	// ComposedRequest. The Compose-side validator handles its own
	// member-chain dispatch and never calls this Request-host helper.
	for slotIdent := range visitor.slotPaths {
		env.AddError(string(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT),
			"overlay "+string(spec.Kind)+" formula references Compose slot "+slotIdent+" on a Request host (slot namespace is Compose-only)",
			map[string]any{
				"index":              index,
				"kind":               string(spec.Kind),
				"unknown_identifier": slotIdent,
				"host_shape":         string(hostShape),
				"reason":             "slot namespace is Compose-only",
				"formula":            formula,
			})
		return
	}
}

// extractFormulaParamPredict reads `OverlaySpec.Params["formula"]` as
// a non-empty string. Emits an envelope error (and returns ok=false)
// when the formula slot is missing / non-string / empty. The error
// uses `PULSE_OVERLAY_FORMULA_INVALID_IDENT` with a `reason` Detail to
// distinguish it from a successfully-parsed-but-bad-identifier
// failure — keeping the error catalog tight (no separate
// PARAM_MISSING code at predict time; PARAM_MISSING is reserved for
// runtime PARAM-shape failures the runtime helper surfaces).
func extractFormulaParamPredict(env *Envelope, spec *types.OverlaySpec, index int) (string, bool) {
	if len(spec.Params) == 0 {
		env.AddError(string(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT),
			"overlay "+string(spec.Kind)+" requires Params[\"formula\"] (non-empty string); Params is missing or empty",
			map[string]any{
				"index":  index,
				"kind":   string(spec.Kind),
				"reason": "missing formula param",
			})
		return "", false
	}
	var m map[string]any
	if err := json.Unmarshal(spec.Params, &m); err != nil {
		env.AddError(string(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT),
			"overlay "+string(spec.Kind)+" Params must be a JSON object carrying \"formula\"",
			map[string]any{
				"index":  index,
				"kind":   string(spec.Kind),
				"reason": "params is not a JSON object",
			})
		return "", false
	}
	raw, present := m["formula"]
	if !present {
		env.AddError(string(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT),
			"overlay "+string(spec.Kind)+" requires Params[\"formula\"] (non-empty string)",
			map[string]any{
				"index":  index,
				"kind":   string(spec.Kind),
				"reason": "missing formula param",
			})
		return "", false
	}
	formula, ok := raw.(string)
	if !ok || formula == "" {
		env.AddError(string(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT),
			"overlay "+string(spec.Kind)+" Params[\"formula\"] must be a non-empty string",
			map[string]any{
				"index":  index,
				"kind":   string(spec.Kind),
				"reason": "formula param is empty or non-string",
			})
		return "", false
	}
	return formula, true
}

// inferRequestHostShape resolves the OverlayShape implied by a
// Request-host (`Request.Overlays`). Mirrors `inferComposeSlotShape`
// (`descriptor/compose.go`) and `inferChainStageShape`
// (`descriptor/chain_overlay.go`) — pure function over Request slots:
//
//   - Request.Crosstab != nil ⇒ OverlayShapeMatrix.
//   - len(Request.Groups) > 0 ⇒ OverlayShapeSeries.
//   - otherwise              ⇒ OverlayShapeScalar.
//
// A nil request collapses to OverlayShapeScalar so callers can ride
// the FORMULA path on a degenerate fixture without crashing the walk.
func inferRequestHostShape(req *types.Request) types.OverlayShape {
	if req == nil {
		return types.OverlayShapeScalar
	}
	if req.Crosstab != nil {
		return types.OverlayShapeMatrix
	}
	if len(req.Groups) > 0 {
		return types.OverlayShapeSeries
	}
	return types.OverlayShapeScalar
}

// buildFormulaAllowedVarsRequest returns the allowed variable
// identifier set for a Request-host FORMULA spec at the given host
// shape. Built off `types.FormulaNamespace(shape)` plus the opt-in
// `baseline` identifier (SERIES host with `Params["baseline_position"]`
// set per the research note § 2.2).
//
// The Compose-specific identifiers (`ref_cell` / `ref_value` / `ref`)
// are NOT added here — the Request-host path has no Compose
// reference. The Compose-side validator builds its own set with the
// Compose identifiers and the slot namespace.
func buildFormulaAllowedVarsRequest(shape types.OverlayShape, spec *types.OverlaySpec) map[string]struct{} {
	out := map[string]struct{}{}
	for _, v := range types.FormulaNamespace(shape) {
		out[v] = struct{}{}
	}
	// SERIES-host baseline opt-in: only when the per-spec
	// `Params["baseline_position"]` is set is the `baseline` identifier
	// reachable. Without the param the runtime never binds the slot, so
	// the predict gate must reject `baseline` references when the param
	// is absent (research note § 2.2 first-entry semantics).
	if shape == types.OverlayShapeSeries && hasBaselinePosition(spec) {
		out["baseline"] = struct{}{}
	}
	return out
}

// hasBaselinePosition reports whether `OverlaySpec.Params` carries a
// non-empty `baseline_position` slot. Accepts any non-nil JSON value
// (string / number / object — the runtime resolver coerces); the
// presence check is the predict-time gate, not the type check.
func hasBaselinePosition(spec *types.OverlaySpec) bool {
	if spec == nil || len(spec.Params) == 0 {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(spec.Params, &m); err != nil {
		return false
	}
	v, present := m["baseline_position"]
	return present && v != nil
}

// buildFormulaAllowedFuncs returns the allowed function identifier set
// reachable from a FORMULA expression. At v1 this covers:
//
//   - The embedder-registered `ExprFunctions` exposed via
//     `opts.Extensions.ExprFunctions`. Each function name widens the
//     set; collision with the per-shape variable namespace is rejected
//     at `pulse.New()` time via `extensions_validate.go` so the predict
//     gate never sees a registry whose functions shadow a reserved
//     variable.
//
// Built-in expr-lang stdlib helpers (`abs` / `ceil` / `floor` / `len`
// / `min` / `max` / `sum` / etc.) are recognised by the parser as
// `BuiltinNode`s — those nodes do NOT surface in the visitor's `funcs`
// set, so the allowed-funcs check is only consulted for user-defined
// `CallNode` callees. Stdlib references therefore pass through without
// any explicit table lookup; an unknown user function still fires the
// gate.
func buildFormulaAllowedFuncs(opts *PredictOptions) map[string]struct{} {
	out := map[string]struct{}{}
	snap := extensionsFromOpts(opts)
	if snap == nil {
		return out
	}
	for _, fn := range snap.ExprFunctions {
		if fn.Name == "" {
			continue
		}
		out[fn.Name] = struct{}{}
	}
	return out
}

// sortedStringSet returns the sorted keys of `set` as a freshly
// allocated slice. Used to populate Details maps with the available-
// identifier list so the LLM caller's error renderer can suggest the
// closest-matching variable.
func sortedStringSet(set map[string]struct{}) []string {
	if len(set) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// formulaIdentPrescan is the pre-walk pass that collects the
// IdentifierNode pointers acting as MemberNode roots and CallNode
// callees so the main walk can skip them.
//
// `exprast.Walk` is POST-ORDER: children visit before their parent.
// Without this pre-pass the leaf IdentifierNode("slot") would record
// itself in `variables` BEFORE the outer MemberNode("slot.foo.cell")
// could classify it as a member-chain root, producing duplicate error
// noise on every Compose slot reference. The pre-pass walks the tree
// once and stamps every IdentifierNode pointer that participates as a
// MemberNode root or a CallNode callee, then the main visitor consults
// the stamps to filter the IdentifierNode arm.
type formulaIdentPrescan struct {
	callees     map[*exprast.IdentifierNode]struct{}
	memberRoots map[*exprast.IdentifierNode]struct{}
	// innerMembers tracks MemberNode pointers that appear as the
	// `Node` field of ANOTHER MemberNode — they are inner segments of
	// a longer member chain, not the chain's outermost node. The
	// visitor's MemberNode arm only emits a slot path when the
	// MemberNode is NOT inner, so a `slot.foo.cell` expression emits
	// exactly one slot path ("slot.foo.cell") instead of two
	// ("slot.foo" + "slot.foo.cell").
	innerMembers map[*exprast.MemberNode]struct{}
}

func (p *formulaIdentPrescan) Visit(node *exprast.Node) {
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
		// Stamp the inner MemberNode (if any) so the visitor only
		// emits the OUTERMOST chain root's path.
		if inner, ok := n.Node.(*exprast.MemberNode); ok {
			p.innerMembers[inner] = struct{}{}
		}
	}
}

// formulaIdentVisitor is the main AST walker `validateFormulaOverlay`
// uses to collect identifier references. The visitor distinguishes
// three surfaces per the research note § 3.1:
//
//  1. Plain identifier (`IdentifierNode`) NOT acting as a call callee
//     OR a MemberNode root → variable reference. Surfaces in
//     `variables`.
//  2. Identifier acting as a call callee (`CallNode.Callee` is an
//     `IdentifierNode`) → function reference. Surfaces in `funcs`.
//  3. Member chain rooted at the `slot` identifier
//     (`MemberNode` rooted at `IdentifierNode("slot")`) → Compose slot
//     reference. Surfaces in `slotPaths` as the flattened dotted
//     string (e.g. `slot.us.cell`). Member chains rooted at any other
//     identifier are silently ignored (the root IdentifierNode is
//     skipped via `memberRoots` — this matches expr-lang's compile-
//     time env-validation policy where a `foo.bar` field-style access
//     is governed by the same identifier table as `foo`).
//
// `BuiltinNode` references (`len(foo)` / `min(...)`) are NOT recorded
// in either set — expr-lang's parser routes stdlib helpers through a
// distinct node type so they bypass the validator's allowed-funcs
// check.
//
// The visitor uses three maps (instead of one slice each) so the
// per-spec error path emits ONE coded error per offending identifier
// without duplicating the same identifier surfaced via multiple AST
// nodes (a formula referencing `cell + cell - cell` records `cell`
// once, not three times).
type formulaIdentVisitor struct {
	variables map[string]struct{}
	funcs     map[string]struct{}
	slotPaths map[string]struct{}

	// callees / memberRoots / innerMembers are populated by the prescan
	// above. The visitor consults them to filter IdentifierNode visits
	// that would otherwise duplicate the call-callee / member-chain
	// surfaces and to emit one slot path per OUTERMOST chain.
	callees      map[*exprast.IdentifierNode]struct{}
	memberRoots  map[*exprast.IdentifierNode]struct{}
	innerMembers map[*exprast.MemberNode]struct{}
}

func (v *formulaIdentVisitor) Visit(node *exprast.Node) {
	if node == nil || *node == nil {
		return
	}
	switch n := (*node).(type) {
	case *exprast.CallNode:
		if id, ok := n.Callee.(*exprast.IdentifierNode); ok {
			v.funcs[id.Value] = struct{}{}
		}
	case *exprast.MemberNode:
		// Skip inner segments of a longer chain — only the OUTERMOST
		// MemberNode emits the full dotted path. Without this skip a
		// `slot.foo.cell` would emit both "slot.foo" and
		// "slot.foo.cell" as separate slot references.
		if _, inner := v.innerMembers[n]; inner {
			return
		}
		// Only surface member chains rooted at `slot` — every other
		// MemberNode chain is governed by the same identifier table
		// as its root IdentifierNode, so the root's IdentifierNode
		// visit will fire the allowed-vars check on the root's name.
		root, path, ok := flattenMemberChain(n)
		if !ok {
			return
		}
		if root.Value == "slot" {
			v.slotPaths[path] = struct{}{}
		}
	case *exprast.IdentifierNode:
		if _, isCallee := v.callees[n]; isCallee {
			return
		}
		if _, isRoot := v.memberRoots[n]; isRoot {
			return
		}
		v.variables[n.Value] = struct{}{}
	}
}

// flattenMemberChain walks a MemberNode chain and returns
// (rootIdentifier, dotted-path, ok). The chain `slot.foo.cell` is
// represented in expr-lang as:
//
//	MemberNode{
//	  Node:     MemberNode{
//	    Node:     IdentifierNode("slot"),
//	    Property: StringNode("foo"),
//	  },
//	  Property: StringNode("cell"),
//	}
//
// The function unwinds the chain, returning the root
// IdentifierNode("slot") and the flattened path "slot.foo.cell". The
// `ok` flag is false when the chain root is not a single
// IdentifierNode (e.g. a chain rooted at a CallNode such as
// `foo().bar` is not a flattenable slot reference).
//
// Used by the visitor's MemberNode arm to recognise Compose
// `slot.<label>.<field>` member-chain identifiers BEFORE the walker
// reaches the chain's IdentifierNode leaves and double-emits them.
func flattenMemberChain(n *exprast.MemberNode) (*exprast.IdentifierNode, string, bool) {
	parts := []string{}
	cur := exprast.Node(n)
	for {
		switch node := cur.(type) {
		case *exprast.MemberNode:
			if str, ok := node.Property.(*exprast.StringNode); ok {
				parts = append([]string{str.Value}, parts...)
			} else {
				return nil, "", false
			}
			cur = node.Node
		case *exprast.IdentifierNode:
			parts = append([]string{node.Value}, parts...)
			return node, joinDottedPath(parts), true
		default:
			return nil, "", false
		}
	}
}

// joinDottedPath joins identifier parts with "." separators —
// stdlib-free so the helper does not pull in `strings` for one call
// site.
func joinDottedPath(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += "." + parts[i]
	}
	return out
}
