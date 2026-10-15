package descriptor

import (
	"encoding/json"

	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/formulaident"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/types"
)

// Predict-time AST identifier validation for OVERLAY_FORMULA — the no-
// execute descriptor mirror of the runtime evaluator wiring in
// `internal/processing/overlay_formula.go`. Implements the algorithm laid out in
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
//     validator (`internal/descriptor/compose.go` `validateComposeOverlaySpec`)
//     reuses the same flatten + walk helpers and widens its own allowed
//     set with the Compose-specific identifiers.
//  5. Widen the allowed set with the embedder-registered
//     `ExprFunctions` from `opts.Extensions` per the unified-
//     expression-environment contract. Functions widen the
//     callable surface only — they do NOT widen the variable surface
//     (mirrors `processing.compileFormulaProgramWithExtensions`).
//  6. Reject every identifier not in the allowed set with
//     `PULSE_OVERLAY_FORMULA_INVALID_IDENT` carrying the offending
//     identifier + the per-shape variable enumerate-set so the LLM
//     caller can fix the formula without round-tripping to runtime.
//
// Slot-namespace handling: `slot.<label>.<field>` member chains are
// rejected at this validator (Request-host has no Compose slots).
// The Compose-side gate (validateComposeFormula →
// formulaident.ComposeRefusal, which the runtime Compose dispatcher
// also calls) accepts them against the slots the spec names.
//
// Structural invariants (CLAUDE.md "Predict / Inspect contracts" +
// "What NOT to Do"):
//
//   - This file MUST NOT import `internal/service/` or `internal/processing/`. The AST
//     walker uses `expr-lang/expr/parser` + `expr-lang/expr/ast`
//     directly — the same packages `processing.NeededFields` uses for
//     `ATTR_FORMULA` projection. Both packages live outside `internal/service/`
//     and `internal/processing/` so the no-execute import gate stays clean.
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
// Compose slot identifiers are always rejected here, so
// `slot.<label>.cell` on Request.Overlays surfaces
// `PULSE_OVERLAY_FORMULA_INVALID_IDENT`.
func validateFormulaOverlay(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, opts *PredictOptions, index int) {
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

	if cerr := totalcall.Refusal(formula, string(types.OverlayKindFormula)); cerr != nil {
		details := map[string]any{"index": index, "kind": string(spec.Kind)}
		for k, v := range cerr.Details {
			details[k] = v
		}
		env.AddError(string(cerr.Code), cerr.Message, details)
		return
	}

	// The shared Request-host identifier gate (internal/formulaident):
	// the runtime MATRIX dispatcher runs it before compiling, so an
	// unknown variable, callee or Compose slot path is
	// PULSE_OVERLAY_FORMULA_INVALID_IDENT on both sides.
	if cerr := formulaident.BuiltinRefusal(spec.Kind, formula, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}
	hostShape := inferRequestHostShape(req)
	if cerr := formulaident.RequestRefusal(spec.Kind, formula, &tree.Node, index, hostShape,
		buildFormulaAllowedVarsRequest(hostShape, spec), buildFormulaAllowedFuncs(opts)); cerr != nil {
		addCodedError(env, cerr)
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
func extractFormulaParamPredict(env *descriptor.Envelope, spec *types.OverlaySpec, index int) (string, bool) {
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
// (`internal/descriptor/compose.go`) and `inferChainStageShape`
// (`internal/descriptor/chain_overlay.go`) — pure function over Request slots:
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
	// SERIES-host baseline opt-in: only when the per-spec
	// `Params["baseline_position"]` is set is the `baseline` identifier
	// reachable. Without the param the runtime never binds the slot, so
	// the predict gate must reject `baseline` references when the param
	// is absent (research note § 2.2 first-entry semantics).
	return formulaident.RequestVars(shape, hasBaselinePosition(spec))
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
//
// Pulse's reserved built-ins are CallNodes too: only the scalar-maths
// subset (`exprbuiltins.OverlayNames` — sqrt / log / exp / pow) widens
// the set. The vector families (vsum ... vnormalize) stay out, so they
// fire the unknown-function arm exactly as the runtime compile refuses
// them.
//
// The set admits exactly what the runtime overlay compile registers
// (processing's setExprOptions plus the registry's own options): the
// unreserved set helpers (`exprbuiltins.SetHelperNames` — has_any ...
// set_xor) always, and `lookup` when the instance registers any
// LookupTable.
func buildFormulaAllowedFuncs(opts *PredictOptions) map[string]struct{} {
	snap := extensionsFromOpts(opts)
	if snap == nil {
		return formulaident.AllowedFuncs(nil, false)
	}
	names := make([]string, 0, len(snap.ExprFunctions))
	for _, fn := range snap.ExprFunctions {
		names = append(names, fn.Name)
	}
	return formulaident.AllowedFuncs(names, len(snap.LookupTables) > 0)
}
