package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/formulaident"
	"github.com/frankbardon/pulse/internal/overlayhost"
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
	// The shared Request-host gate (internal/formulaident): the runtime
	// MATRIX and SERIES dispatchers run it before compiling, so a
	// missing formula, a parse failure, a total() call, a vector
	// built-in, a bad baseline_position or an unknown variable, callee
	// or Compose slot path refuse with one code and Details on both
	// sides.
	shape := inferRequestHostShape(req)
	// On a SERIES host the Ref / Scope rule comes first — the shared
	// SERIES table every runtime arm raises (refuseWrongHostOverlays).
	if shape == types.OverlayShapeSeries {
		if cerr := overlayhost.SeriesSpecRefusal(spec, spec.Kind, index); cerr != nil {
			addCodedError(env, cerr)
			return
		}
	}
	params, _ := exprenv.NormalizeParams(req.Params)
	if cerr := formulaident.RequestSpecRefusalWithParams(spec, index, shape, buildFormulaAllowedFuncs(opts), params); cerr != nil {
		addCodedError(env, cerr)
	}
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
// subset (`exprenv.OverlayNames` — sqrt / log / exp / pow) widens
// the set. The vector families (vsum ... vnormalize) stay out, so they
// fire the unknown-function arm exactly as the runtime compile refuses
// them.
//
// The set admits exactly what the runtime overlay compile registers
// (exprenv.Options plus the registry's own options): the
// unreserved set helpers (`exprenv.SetHelperNames` — has_any ...
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
