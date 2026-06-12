package processing

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Overlay FORMULA evaluator spine — runtime side of the OVERLAY_FORMULA
// catalog entry.
//
// E8-S2 scope (spine, not namespace):
//
//   - The formulaOverlayHandler compiles the caller-supplied
//     `Params["formula"]` string via expr-lang/expr ONCE per overlay
//     spec at handler entry and runs the compiled program via
//     `expr.Run` per cell. The compile-once / run-many split keeps the
//     per-cell hot path tight (single allocation per overlay layer;
//     O(1) writes per iteration). Mirrors the
//     `processing.formulaAttribute.Row` compile / run pattern (see
//     `processing/attribute.go`).
//
//   - The per-shape variable namespace is STUBBED at this story —
//     `buildFormulaCellEnvStub` populates `{"cell": cellVal}` per
//     iteration so the spine is exercised end-to-end without depending
//     on the per-shape binder (margins / SDs / refs) landing in E8-S3.
//     S3 replaces the stub with the per-shape binder that fills
//     `margin_row` / `margin_col` / `margin_grand` / `sd_*` / etc. per
//     the research note
//     `.planning/result-overlay-system/research/formula-namespace.md`
//     § 2.
//
//   - Embedder `ExprFunctions` integration is reserved as a TODO hook
//     (see `compileFormulaProgram` comments). The hook is plumbed at
//     E8-S5 once the per-shape binder + extension snapshot are in
//     place — the ExtensionRegistry pointer is not currently threaded
//     into the overlay handler dispatch surface, and widening the
//     dispatch signature without the per-shape binder would be wasted
//     surface churn. E8-S5 carries both changes together (signature
//     widening + ExprFunctions plumb-through).
//
// Failure modes:
//
//   - `PULSE_OVERLAY_FORMULA_PARSE_ERROR` — `expr.Compile` returned a
//     parse error. The handler short-circuits with a CodedError; no
//     layer is emitted.
//   - `PULSE_OVERLAY_FORMULA_TYPE_MISMATCH` — `expr.Run` returned a
//     value whose type cannot be coerced to float64. Per-cell failure
//     propagates as a CodedError so the orchestrator surfaces the same
//     shape predict (E8-S4) would have flagged.
//
// Structural invariants (CLAUDE.md "Predict / Inspect contracts" +
// "What NOT to Do"):
//
//   - This file MUST NOT import `service/` or `descriptor/`. Runtime
//     overlay execution rides inside `processing/` alongside the
//     aggregator / attribute / grouper layers.
//   - No `fmt.Sprintf` in any JSON-bearing path. CodedError Details
//     are built as `map[string]any` and surfaced via
//     `errors.NewCodedErrorWithDetails`; only the `returned_type`
//     debug string uses `fmt.Sprintf("%T", v)` which is NOT a
//     JSON-bearing path (it lands inside the Details map as a string
//     and is serialised by `encoding/json` downstream).

// formulaParseErrorCode mirrors `errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR`
// as a string constant so the spine references the code without
// pulling in the typed constant directly — the code is registered in
// `errors/codes.go` + `errors/fixup_metadata.go` in the same PR.
const (
	formulaParamFormula = "formula"
)

// formulaProgram bundles a compiled expr-lang program with the source
// formula string so the runtime can echo the offending expression in
// CodedError Details when a type mismatch fires at evaluation time.
//
// Forward-compat (E8-S5): when the embedder ExprFunctions wiring lands
// this struct will carry an additional reference to the
// `ExtensionsSnapshot` used at compile time so the runtime can fall
// back to a re-compile when the extension registry is rebuilt mid-
// session.
type formulaProgram struct {
	// formula is the source string the caller supplied via
	// `OverlaySpec.Params["formula"]`. Echoed verbatim in CodedError
	// Details so the renderer can highlight the failing expression.
	formula string

	// program is the compiled expr-lang bytecode. Compiled ONCE per
	// overlay spec at handler entry (compile-once / run-many) so the
	// per-cell hot path is just `expr.Run`.
	program *vm.Program
}

// applyFormula is the OVERLAY_FORMULA matrix-host handler. CELL scope
// over a MATRIX (Crosstab) host. Each present host cell becomes one
// overlay cell whose value is the formula's `expr.Run` result coerced
// to float64. Absent host cells stay absent on the overlay (the
// formula is not evaluated).
//
// E8-S2 spine: the per-cell env carries only `{"cell": cellVal}` —
// the per-shape namespace binder lands in E8-S3 and will populate
// `margin_row` / `margin_col` / `margin_grand` / `sd_row` / `sd_col`
// / `sd_grand` per the research note § 2.1. Until then, formulas that
// reference any identifier beyond `cell` will fire at predict time
// (E8-S4) or at runtime via `expr.Run`'s undefined-variable handling.
//
// Compile-once / run-many: the formula is compiled via `expr.Compile`
// ONCE at handler entry against a prototype env (zeroed slots for the
// variable namespace). The per-cell evaluation overwrites the
// prototype's value slots (single allocation per overlay layer; O(1)
// writes per iteration). Mirrors the `processing.formulaAttribute.Row`
// compile / run pattern. The single-compile contract is exercised by
// `TestOverlay_Formula_CompilesOnce`.
//
// Output shape: MATRIX payload mirroring the host's RowKeys /
// ColumnKeys / headers so renderers can lay the overlay on top of the
// base matrix with the same header machinery as INDEX_VS_MARGIN.
func applyFormula(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []OverlayWarning, error) {
	formula, err := extractFormulaParam(spec)
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}

	program, err := compileFormulaProgram(spec, formula, buildFormulaPrototypeEnvMatrix())
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}

	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	payload := host.Payload()

	// Prototype env reused per-iteration — single allocation per
	// overlay layer, O(1) writes per cell. The expr-lang program is
	// compiled against the prototype's value types so reassigning to
	// the same keys at run time is type-stable.
	env := buildFormulaPrototypeEnvMatrix()

	cells := make([][]types.MatrixCell, rowCount)
	var (
		minV float64
		maxV float64
		seen int
	)
	for i := 0; i < rowCount; i++ {
		row := make([]types.MatrixCell, colCount)
		for j := 0; j < colCount; j++ {
			cellVal, cellPresent := host.CellAt(i, j)
			if !cellPresent {
				continue
			}
			// E8-S2 stub namespace: only `cell` is bound. S3 fills
			// `margin_row` / `margin_col` / `margin_grand` / `sd_*` per
			// research note § 2.1. Reassigning a map key whose entry
			// was already declared in the prototype is type-stable
			// against the compiled program.
			env["cell"] = cellVal

			out, runErr := expr.Run(program.program, env)
			if runErr != nil {
				return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
					errors.PROCESSING_INTERNAL,
					"overlay "+string(spec.Kind)+" expression evaluation failed",
					map[string]any{
						"code":         string(errors.PULSE_OVERLAY_FORMULA_TYPE_MISMATCH),
						"kind":         string(spec.Kind),
						"formula":      program.formula,
						"row_index":    i,
						"col_index":    j,
						"runtime_err":  runErr.Error(),
					})
			}
			stat, ok := coerceFormulaResult(out)
			if !ok {
				return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
					errors.PROCESSING_INTERNAL,
					"overlay "+string(spec.Kind)+" expression returned a non-numeric value",
					map[string]any{
						"code":          string(errors.PULSE_OVERLAY_FORMULA_TYPE_MISMATCH),
						"kind":          string(spec.Kind),
						"formula":       program.formula,
						"returned_type": fmt.Sprintf("%T", out),
						"row_index":     i,
						"col_index":     j,
					})
			}
			if math.IsNaN(stat) || math.IsInf(stat, 0) {
				// Non-finite results pass through verbatim — renderers
				// decide how to surface NaN / Inf (mirrors the existing
				// INDEX_VS_MARGIN policy on non-finite scores). The
				// summary Min / Max tracking skips non-finite values so
				// the renderer-facing range stays meaningful.
				row[j] = types.MatrixCell{Value: stat, Present: true}
				continue
			}
			row[j] = types.MatrixCell{Value: stat, Present: true}
			if seen == 0 {
				minV, maxV = stat, stat
			} else {
				if stat < minV {
					minV = stat
				}
				if stat > maxV {
					maxV = stat
				}
			}
			seen++
		}
		cells[i] = row
	}

	overlayPayload := &types.MatrixPayload{
		RowHeader:        payload.RowHeader,
		ColumnHeader:     payload.ColumnHeader,
		RowKeys:          append([]types.AxisKey(nil), payload.RowKeys...),
		ColumnKeys:       append([]types.AxisKey(nil), payload.ColumnKeys...),
		Cells:            cells,
		CellLabel:        overlayLayerName(spec),
		NormalizeApplied: types.CrosstabNormalizeNone,
	}

	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeMatrix,
			Matrix: overlayPayload,
		},
	}

	// Summary: Min / Max / Count populated over finite cells. No
	// Baseline — FORMULA has no canonical centerpoint (a `cell -
	// margin_row` expression centers on 0, a `cell / margin_row * 100`
	// expression centers on 100; the centerpoint is author-defined,
	// so the spine intentionally leaves Baseline unset and renderers
	// honour the expression's intent without overriding).
	summary := &types.OverlaySummary{}
	if seen > 0 {
		mn, mx, count := minV, maxV, seen
		summary.Min = &mn
		summary.Max = &mx
		summary.Count = &count
	} else {
		zeroCount := 0
		summary.Count = &zeroCount
	}
	layer.Summary = summary

	return layer, nil, nil
}

// extractFormulaParam reads `OverlaySpec.Params["formula"]` as a
// non-empty string. Returns (formula, nil) on a valid value; returns
// ("", coded error) on a missing or non-string value.
//
// JSON-encoded Params is the canonical authoring shape (the
// `OverlaySpec.Params` field is `json.RawMessage`); the helper
// decodes once and pulls the formula slot. Mirrors the
// `extractWindowParam` helper for OVERLAY_INDEX_VS_ROLLING_MEAN.
func extractFormulaParam(spec *types.OverlaySpec) (string, error) {
	if len(spec.Params) == 0 {
		return "", errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires Params[\"formula\"] (non-empty string); Params is missing or empty",
			map[string]any{
				"code":  string(errors.PULSE_OVERLAY_PARAM_MISSING),
				"kind":  string(spec.Kind),
				"param": formulaParamFormula,
			})
	}
	var m map[string]any
	if err := json.Unmarshal(spec.Params, &m); err != nil {
		return "", errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" Params must be a JSON object carrying \"formula\"",
			map[string]any{
				"code":  string(errors.PULSE_OVERLAY_PARAM_MISSING),
				"kind":  string(spec.Kind),
				"param": formulaParamFormula,
			})
	}
	raw, present := m[formulaParamFormula]
	if !present {
		return "", errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires Params[\"formula\"] (non-empty string)",
			map[string]any{
				"code":  string(errors.PULSE_OVERLAY_PARAM_MISSING),
				"kind":  string(spec.Kind),
				"param": formulaParamFormula,
			})
	}
	formula, ok := raw.(string)
	if !ok || formula == "" {
		return "", errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" Params[\"formula\"] must be a non-empty string",
			map[string]any{
				"code":  string(errors.PULSE_OVERLAY_PARAM_MISSING),
				"kind":  string(spec.Kind),
				"param": formulaParamFormula,
			})
	}
	return formula, nil
}

// compileFormulaProgram compiles the caller-supplied formula via
// `expr.Compile` against the prototype env. Returns a formulaProgram
// carrying the compiled bytecode + the source formula string for
// later runtime error wrapping.
//
// E8-S2: compiled against the static prototype env only. The
// embedder ExprFunctions hook is reserved as a TODO — when E8-S5
// widens the overlay-handler dispatch signature to thread the
// ExtensionRegistry through, the registry's `ExprOptions()` slice
// will be appended to the `[]expr.Option` here so embedder-registered
// functions become reachable from FORMULA expressions (mirrors
// `formulaAttribute.Row` which already calls `a.exts.ExprOptions()`
// at compile time — see `processing/attribute.go`). LookupTables are
// intentionally NOT registered (research note § 4.3); embedders who
// need lookup-driven overlays register the lookup as a custom
// `ExprFunctions` entry instead.
//
// Failure mode: a non-nil error from `expr.Compile` propagates as a
// `PULSE_OVERLAY_FORMULA_PARSE_ERROR` CodedError carrying both the
// source formula and the parser's error message in Details.
func compileFormulaProgram(spec *types.OverlaySpec, formula string, protoEnv map[string]any) (*formulaProgram, error) {
	opts := []expr.Option{expr.Env(protoEnv)}
	// TODO(E8-S5): when the overlay-handler dispatch signature widens
	// to thread the ExtensionRegistry through, append
	// `exts.ExprOptions()` to opts so embedder-registered ExprFunctions
	// are reachable from FORMULA expressions. Until then the FORMULA
	// surface is limited to the expr-lang stdlib + the per-host-shape
	// variable namespace. LookupTables stay out of scope at v1 per
	// research note § 4.3.
	program, err := expr.Compile(formula, opts...)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" expression failed to parse",
			map[string]any{
				"code":        string(errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR),
				"kind":        string(spec.Kind),
				"formula":     formula,
				"parse_error": err.Error(),
			})
	}
	return &formulaProgram{
		formula: formula,
		program: program,
	}, nil
}

// buildFormulaPrototypeEnvMatrix returns the prototype env the
// MATRIX-host FORMULA handler compiles against. Carries zeroed slots
// for every variable in the MATRIX namespace; the per-iteration
// evaluation overwrites the value slots (single allocation per
// overlay layer).
//
// E8-S2 spine: only `cell` is populated. S3 fills the per-shape
// binder which will return a prototype carrying every variable from
// research note § 2.1 (`cell`, `margin_row`, `margin_col`,
// `margin_grand`, `sd_row`, `sd_col`, `sd_grand`, and `ref_cell`
// when COMPOSE). Until then, formulas referencing any other
// identifier fire at predict time (E8-S4) or surface an undefined-
// variable error from `expr.Run` which the runtime catches and
// wraps as `PULSE_OVERLAY_FORMULA_TYPE_MISMATCH`.
//
// The prototype intentionally uses `0.0` (not `math.NaN()`) so
// compiled programs see a stable type signature — expr-lang infers
// the value type at compile time and rejects mid-run type drift.
func buildFormulaPrototypeEnvMatrix() map[string]any {
	// E8-S2 stub: only `cell` is bound. S3 widens this to the full
	// MATRIX namespace per research note § 2.1.
	return map[string]any{
		"cell": float64(0),
	}
}

// coerceFormulaResult coerces an `expr.Run` return value to float64.
// Mirrors `processing.formulaAttribute.Row`'s return-type switch
// (`processing/attribute.go` lines 292-309):
//
//   - float64 / float32 / int / int64 widen natively.
//   - bool widens to 0.0 / 1.0 (false / true respectively).
//   - Any other type returns (0, false) so the caller can surface
//     `PULSE_OVERLAY_FORMULA_TYPE_MISMATCH`.
//
// The acceptance subset matches `formulaAttribute.Row` byte-for-byte
// so authors get the same coercion semantics across the FORMULA
// overlay surface and the ATTR_FORMULA attribute surface.
func coerceFormulaResult(out any) (float64, bool) {
	switch v := out.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case bool:
		if v {
			return 1.0, true
		}
		return 0.0, true
	}
	return 0, false
}
