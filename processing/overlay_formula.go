package processing

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/expr-lang/expr"
	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Overlay FORMULA evaluator spine + per-shape namespace binders —
// runtime side of the OVERLAY_FORMULA catalog entry.
//
// E8-S2 landed the spine: compile-once / run-many via expr-lang, the
// matrix-host `applyFormula` handler driving the per-cell loop, a
// stub prototype env carrying only `cell`, and the three failure
// modes (`PULSE_OVERLAY_FORMULA_PARSE_ERROR` /
// `PULSE_OVERLAY_FORMULA_TYPE_MISMATCH` / `PULSE_OVERLAY_PARAM_MISSING`).
//
// E8-S3 extends the spine with per-host-shape variable binders per the
// research note
// `.planning/result-overlay-system/research/formula-namespace.md` § 2:
//
//   - `bindFormulaEnvMatrix` fills the MATRIX namespace (`cell`,
//     `margin_row`, `margin_col`, `margin_grand`, `sd_row`, `sd_col`,
//     `sd_grand`, and `ref_cell` when the per-spec FormulaContext
//     carries a Compose-matrix reference). Margins reuse the host's
//     already-recomputed RowMargins / ColumnMargins / GrandTotal
//     slots — no record re-traversal. SDs are precomputed via single-
//     pass Welford-Pébaÿ over the host strip ONCE per axis, NOT per
//     cell (O(cells) finalize cost, not O(cells × cells)).
//   - `bindFormulaEnvSeries` fills the SERIES namespace (`value`,
//     `total`, `prior`, `baseline`, `ref_value` when Compose-series).
//     `prior` reads the lag-1 PRESENT entry (NaN at the first present
//     entry per research note § 2.2 first-entry semantics). `baseline`
//     is the value at `Params["baseline_position"]` — bound only when
//     the caller plumbs `BaselinePosition` on the FormulaContext (the
//     predict gate rejects formulas referencing `baseline` when the
//     param is absent).
//   - `bindFormulaEnvScalar` fills the SCALAR namespace (`value`,
//     `ref` when Compose-scalar). Single binding — no per-iteration
//     overwrite (a SCALAR layer carries one slot).
//   - `bindFormulaEnv` is the umbrella dispatcher matching the AC
//     surface (`bindFormulaEnv(shape OverlayShape, ctx) map[string]any`).
//     Routes to the per-shape binder above based on the resolved
//     OverlayShape.
//
// Compose `slot.<label>.<field>` namespace: the per-spec FormulaContext
// carries an ordered list of formulaSlotBinding entries (label + per-
// shape payload). Each shape's binder writes a nested `slot` map of
// the shape `map[string]any{ "<label>": map[string]any{ "cell"|"value": float64 } }`
// (matching the research note § 2.4 dotted-namespace contract). The
// prototype env carries the same nested-map shape so expr-lang's
// compile-time env validator accepts `slot.<label>.cell` /
// `slot.<label>.value` identifiers as well-typed member accesses.
// Unknown slot labels are rejected at predict time (E8-S4); the
// runtime binder simply does not populate a slot whose Compose payload
// is nil (the renderer-facing payload stays NaN for that cell — the
// predict gate makes this unreachable in production).
//
// Embedder `ExprFunctions` integration is still reserved as a TODO
// (see `compileFormulaProgram` comments). E8-S5 widens the dispatch
// signature to thread the ExtensionRegistry through.
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

// formulaSlotBinding carries one Compose slot's per-shape payload the
// FORMULA env exposes via the dotted `slot.<label>.<field>` namespace
// (research note § 2.4). Exactly one of MatrixCells / SeriesValues /
// Scalar is populated, matching the slot's own host result shape.
//
// MatrixCells is indexed `[rowIdx][colIdx]`, parallel to the host's
// own MatrixPayload.Cells slice — the matrix binder reads
// `MatrixCells[rowIdx][colIdx]` for the current cell coordinate.
// SeriesValues is parallel to the host's GroupKeys slice — the series
// binder reads `SeriesValues[i]` for the current entry index.
// Scalar carries one value and is binding-time-static — the scalar
// binder reads it once.
//
// Present flags ride alongside each shape's value slot so the binder
// can surface NaN for absent coordinates without crashing the formula
// evaluation (mirrors the existing matrix-host absent-cell discipline
// in `applyFormula`). The predict gate (E8-S4) catches structural
// shape divergence across slots — a Compose-matrix host that references
// a Compose-series slot fires `PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT` at
// predict time, so the runtime should never see a binding whose
// per-shape payload disagrees with the host's iteration shape.
type formulaSlotBinding struct {
	// Label is the slot's resolved Request.Label (after the E7-S1
	// `request_<index+1>` auto-default). Used by the prototype env
	// builder to lay out the nested slot.<label> map and by the binder
	// to look up the payload at iteration time. Always non-empty.
	Label string

	// MatrixCells carries the slot's matrix payload values when the
	// slot is matrix-shaped. Indexed `[rowIdx][colIdx]`; absent cells
	// surface as `MatrixPresent[rowIdx][colIdx] == false`.
	MatrixCells   [][]float64
	MatrixPresent [][]bool

	// SeriesValues carries the slot's series payload values when the
	// slot is series-shaped. Parallel to the host's GroupKeys slice;
	// absent entries surface as `SeriesPresent[i] == false`.
	SeriesValues  []float64
	SeriesPresent []bool

	// Scalar carries the slot's scalar payload value when the slot is
	// scalar-shaped. `ScalarPresent` is the present flag.
	Scalar        float64
	ScalarPresent bool
}

// FormulaContext bundles every shape-specific input the per-shape
// binders consume to populate the FORMULA env. Built once per overlay
// spec at handler entry by the per-host orchestrator and passed
// straight to `bindFormulaEnv`. The shape field selects which arm of
// the union the umbrella binder dispatches to; the un-matching arms
// are ignored.
//
// Reference values (RefMatrix / RefSeries / RefScalar) are populated
// when the per-spec OverlayRef carries a Compose Reference slot whose
// host result shape matches the iteration host shape; predict-time
// shape-match enforcement (E7-S7's `checkSlotShapeAndSchema` for
// COMPOSE Reference slots) keeps the per-shape ref payload consistent
// with the iteration shape so the binder doesn't have to validate the
// pairing — it just reads whichever arm matches `Shape`.
//
// SDs (RowSDs / ColumnSDs / GrandSD) are precomputed by the per-shape
// orchestrator BEFORE the cell loop runs — research note § 2.1 SD
// derivation paragraph: SDs are computed once per axis (Welford-Pébaÿ
// over the matching cell strip) and cached for the strip's iterations.
// The binder reads the precomputed slot.
//
// BaselinePosition opt-in: when the formula references `baseline` and
// the per-spec `Params["baseline_position"]` is set, the per-host
// orchestrator resolves the baseline value once and plumbs it through
// `BaselineValue` / `BaselineValuePresent`. The predict gate (E8-S4)
// rejects formulas referencing `baseline` when the param is absent so
// the runtime never sees `BaselineValuePresent == false` paired with
// a `baseline` identifier.
type FormulaContext struct {
	// Shape selects the per-shape binder arm. One of OverlayShapeMatrix
	// / OverlayShapeSeries / OverlayShapeScalar; unknown values produce
	// an empty env.
	Shape types.OverlayShape

	// MATRIX shape inputs ---------------------------------------------

	// MatrixHost is the host view carrying the iteration's host
	// MatrixPayload. The matrix binder reads CellAt + MarginFor against
	// it. May be nil when Shape != OverlayShapeMatrix; the matrix
	// binder no-ops on a nil host.
	MatrixHost *CrosstabHostView

	// RowSDs carries the precomputed per-row standard deviation. Length
	// matches MatrixHost.RowCount(); element i is the Welford SD across
	// every PRESENT column in row i. Population variance (N-divisor),
	// matching the existing OVERLAY_ZSCORE_VS_MARGIN SD derivation.
	// Zero-length when the formula does not reference `sd_row` (the
	// per-host orchestrator short-circuits the recurrence).
	RowSDs []float64

	// ColumnSDs carries the precomputed per-column standard deviation.
	// Length matches MatrixHost.ColumnCount(); element j is the
	// Welford SD across every PRESENT row in column j.
	ColumnSDs []float64

	// GrandSD carries the precomputed whole-matrix standard deviation
	// across every PRESENT cell. Always populated when the formula
	// references `sd_grand`; zero otherwise.
	GrandSD float64

	// RefMatrix carries the Compose reference slot's matrix payload
	// values when the host is a Compose-matrix and the per-spec
	// Reference resolves to a matrix-shaped slot. Indexed
	// `[rowIdx][colIdx]`; parallel to MatrixHost's RowKeys × ColumnKeys
	// grid. Nil when the spec is not Compose OR the Reference slot is
	// not matrix-shaped.
	RefMatrix        [][]float64
	RefMatrixPresent [][]bool

	// SERIES shape inputs ---------------------------------------------

	// SeriesHost is the host view carrying the iteration's host
	// SeriesPayload (the grouped Process orchestrator's per-group
	// metric). The series binder reads ValueAt + GroupKey against it.
	// May be nil when Shape != OverlayShapeSeries.
	SeriesHost *SeriesHostView

	// SeriesTotal carries the precomputed grand total across every
	// PRESENT entry in SeriesHost. Always populated when the formula
	// references `total`; zero otherwise.
	SeriesTotal float64

	// BaselineValue carries the precomputed `Params["baseline_position"]`
	// lookup against SeriesHost. Set only when the per-spec
	// `Params["baseline_position"]` is present AND the formula
	// references `baseline`; the predict gate makes this consistent.
	BaselineValue        float64
	BaselineValuePresent bool

	// RefSeries carries the Compose reference slot's series payload
	// values when the host is a Compose-series and the per-spec
	// Reference resolves to a series-shaped slot. Parallel to
	// SeriesHost's GroupKeys slice.
	RefSeries        []float64
	RefSeriesPresent []bool

	// SCALAR shape inputs ---------------------------------------------

	// ScalarValue carries the host's scalar payload value when the
	// host is scalar-shaped. `ScalarPresent` is the present flag.
	ScalarValue   float64
	ScalarPresent bool

	// RefScalar carries the Compose reference slot's scalar payload
	// value when the host is a Compose-scalar and the per-spec
	// Reference resolves to a scalar-shaped slot.
	RefScalar        float64
	RefScalarPresent bool

	// Compose dotted-namespace inputs ---------------------------------

	// Slots carries the per-spec Compose slot bindings in slot order.
	// Each entry's Label + per-shape payload feeds the dotted
	// `slot.<label>.<field>` namespace per research note § 2.4. The
	// per-shape binder writes the same nested-map shape the prototype
	// env declared at compile time so expr-lang's compile-time env
	// validator accepts the dotted identifiers.
	Slots []formulaSlotBinding
}

// computeMatrixWelfordSDs returns the (rowSDs, columnSDs, grandSD)
// triple for the host matrix's PRESENT cells. Each SD is computed via
// single-pass Welford-Pébaÿ (population variance, N-divisor) over the
// matching cell strip — mirrors `computeSeriesWelfordPopulation`'s
// derivation used by OVERLAY_ZSCORE_VS_TOTAL (SERIES path) for the
// MATRIX axis case.
//
// SDs are computed only when the caller requests them (passed-in
// booleans); the per-host orchestrator lexes the formula AST once at
// compile time to detect which of `sd_row` / `sd_col` / `sd_grand`
// identifiers participate and short-circuits the recurrence for the
// absent arms (research note § 2.1 SD derivation paragraph).
//
// A row / column with zero present cells produces an SD of 0 (the
// Welford SD of an empty set is undefined; the runtime surfaces 0 so
// the formula evaluation does not divide by NaN). Authors who depend
// on a meaningful SD downstream guard their formula with an explicit
// non-zero check (`cell / max(sd_row, 1.0e-9)` etc.).
//
// O(host.RowCount() × host.ColumnCount()) finalize cost — runs ONCE
// per overlay layer at handler entry, NOT per cell.
func computeMatrixWelfordSDs(host *CrosstabHostView, needRow, needCol, needGrand bool) (rowSDs []float64, columnSDs []float64, grandSD float64) {
	if host == nil {
		return nil, nil, 0
	}
	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	if needRow {
		rowSDs = make([]float64, rowCount)
	}
	if needCol {
		columnSDs = make([]float64, colCount)
	}
	if !needRow && !needCol && !needGrand {
		return rowSDs, columnSDs, 0
	}
	// Per-row and per-column Welford accumulators — population
	// variance (N-divisor) so the SD matches the existing
	// OVERLAY_ZSCORE_VS_MARGIN derivation.
	type welford struct {
		n    int
		mean float64
		m2   float64
	}
	var rowAcc []welford
	var colAcc []welford
	if needRow {
		rowAcc = make([]welford, rowCount)
	}
	if needCol {
		colAcc = make([]welford, colCount)
	}
	var grand welford
	for i := 0; i < rowCount; i++ {
		for j := 0; j < colCount; j++ {
			v, present := host.CellAt(i, j)
			if !present {
				continue
			}
			if needRow {
				acc := &rowAcc[i]
				acc.n++
				delta := v - acc.mean
				acc.mean += delta / float64(acc.n)
				acc.m2 += delta * (v - acc.mean)
			}
			if needCol {
				acc := &colAcc[j]
				acc.n++
				delta := v - acc.mean
				acc.mean += delta / float64(acc.n)
				acc.m2 += delta * (v - acc.mean)
			}
			if needGrand {
				grand.n++
				delta := v - grand.mean
				grand.mean += delta / float64(grand.n)
				grand.m2 += delta * (v - grand.mean)
			}
		}
	}
	if needRow {
		for i := 0; i < rowCount; i++ {
			if rowAcc[i].n > 0 {
				rowSDs[i] = math.Sqrt(rowAcc[i].m2 / float64(rowAcc[i].n))
			}
		}
	}
	if needCol {
		for j := 0; j < colCount; j++ {
			if colAcc[j].n > 0 {
				columnSDs[j] = math.Sqrt(colAcc[j].m2 / float64(colAcc[j].n))
			}
		}
	}
	if needGrand && grand.n > 0 {
		grandSD = math.Sqrt(grand.m2 / float64(grand.n))
	}
	return rowSDs, columnSDs, grandSD
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

	// E8-S3: build the matrix FormulaContext ONCE per overlay layer.
	// The in-Request (non-Compose) path leaves RefMatrix nil and Slots
	// empty — the Compose-host wiring lifts the same context shape with
	// the per-spec reference + slot bindings (lands in E7-host glue
	// post-S5). SDs participate only when the formula references the
	// matching identifier; the AST-driven detection ensures the O(cells)
	// recurrence short-circuits for absent SD arms (research note § 2.1
	// SD derivation paragraph).
	needRowSD, needColSD, needGrandSD := detectFormulaSDIdentifiers(formula)
	rowSDs, colSDs, grandSD := computeMatrixWelfordSDs(host, needRowSD, needColSD, needGrandSD)
	ctx := &FormulaContext{
		Shape:      types.OverlayShapeMatrix,
		MatrixHost: host,
		RowSDs:     rowSDs,
		ColumnSDs:  colSDs,
		GrandSD:    grandSD,
	}

	program, err := compileFormulaProgram(spec, formula, buildFormulaPrototypeEnvMatrix(ctx))
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
	env := buildFormulaPrototypeEnvMatrix(ctx)

	cells := make([][]types.MatrixCell, rowCount)
	var (
		minV float64
		maxV float64
		seen int
	)
	for i := 0; i < rowCount; i++ {
		row := make([]types.MatrixCell, colCount)
		for j := 0; j < colCount; j++ {
			_, cellPresent := bindFormulaEnvMatrix(env, ctx, i, j)
			if !cellPresent {
				continue
			}

			out, runErr := expr.Run(program.program, env)
			if runErr != nil {
				return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
					errors.PROCESSING_INTERNAL,
					"overlay "+string(spec.Kind)+" expression evaluation failed",
					map[string]any{
						"code":        string(errors.PULSE_OVERLAY_FORMULA_TYPE_MISMATCH),
						"kind":        string(spec.Kind),
						"formula":     program.formula,
						"row_index":   i,
						"col_index":   j,
						"runtime_err": runErr.Error(),
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
// for every variable in the MATRIX namespace per research note § 2.1
// (`cell`, `margin_row`, `margin_col`, `margin_grand`, `sd_row`,
// `sd_col`, `sd_grand`, and `ref_cell` when the FormulaContext flags
// a Compose-matrix reference). The per-iteration evaluation
// overwrites the value slots (single allocation per overlay layer).
//
// The prototype intentionally uses `0.0` (not `math.NaN()`) so
// compiled programs see a stable type signature — expr-lang infers
// the value type at compile time and rejects mid-run type drift.
//
// Compose slot namespace: when `ctx.Slots` is non-empty the prototype
// carries a nested `slot.<label>` map for every slot binding, with
// the per-slot `cell` / `value` field populated based on the slot's
// own host result shape. The same nested-map shape is re-used at
// iteration time so expr-lang's compile-time env validator accepts
// `slot.<label>.cell` / `slot.<label>.value` identifiers (research
// note § 2.4).
//
// When ctx is nil the helper returns the MATRIX namespace MINUS
// `ref_cell` and the slot map — the minimal in-Request matrix-host
// surface (no Compose-only widening).
func buildFormulaPrototypeEnvMatrix(ctx *FormulaContext) map[string]any {
	env := map[string]any{
		"cell":         float64(0),
		"margin_row":   float64(0),
		"margin_col":   float64(0),
		"margin_grand": float64(0),
		"sd_row":       float64(0),
		"sd_col":       float64(0),
		"sd_grand":     float64(0),
	}
	if ctx == nil {
		return env
	}
	if ctx.RefMatrix != nil {
		env["ref_cell"] = float64(0)
	}
	if len(ctx.Slots) > 0 {
		env["slot"] = buildFormulaPrototypeSlotMap(ctx.Slots)
	}
	return env
}

// buildFormulaPrototypeEnvSeries returns the prototype env the
// SERIES-host FORMULA handler compiles against. Carries zeroed slots
// for every variable in the SERIES namespace per research note § 2.2
// (`value`, `total`, `prior`, `baseline` when opt-in, and `ref_value`
// when Compose-series).
//
// `baseline` is omitted from the prototype when
// `ctx.BaselineValuePresent == false` — the predict gate (E8-S4)
// rejects formulas referencing `baseline` when the opt-in param is
// absent, so a runtime walk that never bound the identifier returns
// an undefined-variable error from `expr.Run` (mapped to
// `PULSE_OVERLAY_FORMULA_TYPE_MISMATCH`). When ctx is nil the helper
// returns the SERIES namespace MINUS `baseline` + `ref_value` + the
// slot map.
func buildFormulaPrototypeEnvSeries(ctx *FormulaContext) map[string]any {
	env := map[string]any{
		"value": float64(0),
		"total": float64(0),
		"prior": math.NaN(),
	}
	if ctx == nil {
		return env
	}
	if ctx.BaselineValuePresent {
		env["baseline"] = float64(0)
	}
	if ctx.RefSeries != nil {
		env["ref_value"] = float64(0)
	}
	if len(ctx.Slots) > 0 {
		env["slot"] = buildFormulaPrototypeSlotMap(ctx.Slots)
	}
	return env
}

// buildFormulaPrototypeEnvScalar returns the prototype env the
// SCALAR-host FORMULA handler compiles against. Carries zeroed slots
// for `value` and `ref` (when Compose-scalar). Single-binding — no
// per-iteration overwrite (a SCALAR layer carries one slot).
func buildFormulaPrototypeEnvScalar(ctx *FormulaContext) map[string]any {
	env := map[string]any{
		"value": float64(0),
	}
	if ctx == nil {
		return env
	}
	if ctx.RefScalarPresent {
		env["ref"] = float64(0)
	}
	if len(ctx.Slots) > 0 {
		env["slot"] = buildFormulaPrototypeSlotMap(ctx.Slots)
	}
	return env
}

// buildFormulaPrototypeSlotMap lays out the nested `slot.<label>` map
// the compile-time env declares so expr-lang's env validator accepts
// `slot.<label>.cell` / `slot.<label>.value` member accesses
// (research note § 2.4). Each slot's prototype carries the per-shape
// field populated based on the slot's own host result shape — matrix-
// shaped slots get `cell`, series / scalar-shaped slots get `value`.
//
// The nested map is rebuilt fresh per layer so the prototype + the
// per-iteration env share NO map pointers — the per-iteration writers
// allocate ONE new `slot.<label>` map per iteration via `bindSlotCells`
// / `bindSlotValues` (the binders own the per-iteration map lifecycle).
func buildFormulaPrototypeSlotMap(slots []formulaSlotBinding) map[string]any {
	out := make(map[string]any, len(slots))
	for _, slot := range slots {
		inner := map[string]any{}
		if slot.MatrixCells != nil {
			inner["cell"] = float64(0)
		}
		if slot.SeriesValues != nil || slot.ScalarPresent {
			inner["value"] = float64(0)
		}
		out[slot.Label] = inner
	}
	return out
}

// bindFormulaEnv is the umbrella dispatcher the per-host overlay
// orchestrator calls to obtain the per-iteration FORMULA env. Returns
// the prototype env for the resolved OverlayShape; the caller is
// expected to (re-)bind the per-iteration values via the matching
// `bindFormulaEnv<Shape>` helper at each step of the per-iteration
// loop.
//
// The shape-specific binders are exposed as separate helpers so the
// per-host orchestrator can pre-allocate the prototype env ONCE at
// handler entry and re-use the same map across every iteration —
// matches the `applyFormula` matrix-host pattern and keeps the per-
// cell hot path tight.
//
// Returns an empty env for an unknown shape (the predict gate makes
// this unreachable in production; the runtime returns an empty env
// so a stray call does not panic).
func bindFormulaEnv(shape types.OverlayShape, ctx *FormulaContext) map[string]any {
	switch shape {
	case types.OverlayShapeMatrix:
		return buildFormulaPrototypeEnvMatrix(ctx)
	case types.OverlayShapeSeries:
		return buildFormulaPrototypeEnvSeries(ctx)
	case types.OverlayShapeScalar:
		return buildFormulaPrototypeEnvScalar(ctx)
	}
	return map[string]any{}
}

// bindFormulaEnvMatrix re-writes the MATRIX namespace value slots in
// `env` for the host cell at (rowIdx, colIdx). The caller passes a
// pre-allocated env (the prototype env returned by
// `buildFormulaPrototypeEnvMatrix`); this binder mutates the entries
// in place so the per-cell hot path stays at one allocation per
// overlay layer.
//
// Cell value: read from `MatrixHost.CellAt(rowIdx, colIdx)`. When the
// cell is absent the binder returns `(false, false)` and the caller
// SKIPS the evaluation — the overlay cell stays absent (mirrors the
// MATRIX-path absent-cell discipline).
//
// Margins: read from `MatrixHost.MarginFor(MarginAxisRow/Column/Grand,
// rowIdx, colIdx)`. When a margin is structurally absent (the host did
// not populate the slot) the binder writes 0 — authors guard their
// formula with a non-zero check (`cell / max(margin_row, 1.0e-9)`)
// when meaningful zero semantics matter.
//
// SDs: read from `ctx.RowSDs[rowIdx]` / `ctx.ColumnSDs[colIdx]` /
// `ctx.GrandSD` precomputed at handler entry. Empty SD slices surface
// as 0 (the per-host orchestrator's "the formula doesn't reference
// this SD identifier" short-circuit; the predict gate ensures the
// runtime never reads from an empty slot whose identifier the formula
// references).
//
// Reference: when `ctx.RefMatrix` is non-nil the binder writes
// `ref_cell` to `ctx.RefMatrix[rowIdx][colIdx]` (or 0 when absent).
// When the slot is nil the prototype env never declared `ref_cell` so
// the slot stays unwritten — the formula evaluator returns the
// undefined-variable error for any reference to it.
//
// Compose slot namespace: for each `ctx.Slots[k]` whose MatrixCells is
// non-nil the binder updates `env["slot"][slot.Label]["cell"]` to
// `ctx.Slots[k].MatrixCells[rowIdx][colIdx]` (or 0 when absent).
//
// Returns (cellValue, true) on a present cell; (0, false) on an
// absent cell so the caller knows to skip evaluation.
func bindFormulaEnvMatrix(env map[string]any, ctx *FormulaContext, rowIdx, colIdx int) (cellValue float64, present bool) {
	if env == nil || ctx == nil || ctx.MatrixHost == nil {
		return 0, false
	}
	cellValue, present = ctx.MatrixHost.CellAt(rowIdx, colIdx)
	if !present {
		return 0, false
	}
	env["cell"] = cellValue
	if v, ok := ctx.MatrixHost.MarginFor(types.MarginAxisRow, rowIdx, colIdx); ok {
		env["margin_row"] = v
	} else {
		env["margin_row"] = float64(0)
	}
	if v, ok := ctx.MatrixHost.MarginFor(types.MarginAxisColumn, rowIdx, colIdx); ok {
		env["margin_col"] = v
	} else {
		env["margin_col"] = float64(0)
	}
	if v, ok := ctx.MatrixHost.MarginFor(types.MarginAxisGrand, rowIdx, colIdx); ok {
		env["margin_grand"] = v
	} else {
		env["margin_grand"] = float64(0)
	}
	if rowIdx >= 0 && rowIdx < len(ctx.RowSDs) {
		env["sd_row"] = ctx.RowSDs[rowIdx]
	} else {
		env["sd_row"] = float64(0)
	}
	if colIdx >= 0 && colIdx < len(ctx.ColumnSDs) {
		env["sd_col"] = ctx.ColumnSDs[colIdx]
	} else {
		env["sd_col"] = float64(0)
	}
	env["sd_grand"] = ctx.GrandSD
	if ctx.RefMatrix != nil {
		var ref float64
		if rowIdx >= 0 && rowIdx < len(ctx.RefMatrix) && colIdx >= 0 && colIdx < len(ctx.RefMatrix[rowIdx]) {
			refPresent := true
			if ctx.RefMatrixPresent != nil &&
				rowIdx < len(ctx.RefMatrixPresent) &&
				colIdx < len(ctx.RefMatrixPresent[rowIdx]) {
				refPresent = ctx.RefMatrixPresent[rowIdx][colIdx]
			}
			if refPresent {
				ref = ctx.RefMatrix[rowIdx][colIdx]
			}
		}
		env["ref_cell"] = ref
	}
	bindFormulaSlotsMatrix(env, ctx.Slots, rowIdx, colIdx)
	return cellValue, true
}

// bindFormulaEnvSeries re-writes the SERIES namespace value slots in
// `env` for the host entry at index `i`. The caller passes a
// pre-allocated env (the prototype env returned by
// `buildFormulaPrototypeEnvSeries`); this binder mutates the entries
// in place.
//
// `value` reads from `SeriesHost.ValueAt(i)`. When the entry is
// absent the binder returns `(0, false)` and the caller SKIPS the
// evaluation. `total` is the precomputed grand total
// (ctx.SeriesTotal). `prior` is the lag-1 PRESENT entry's value —
// resolved via `priorPresent` which scans backwards from `i-1` for
// the first present entry; NaN at the first present entry per
// research note § 2.2 first-entry semantics. `baseline` (opt-in)
// reads from `ctx.BaselineValue`.
//
// Reference: when `ctx.RefSeries` is non-nil the binder writes
// `ref_value` to `ctx.RefSeries[i]` (or 0 when absent).
//
// Compose slot namespace: for each `ctx.Slots[k]` whose SeriesValues
// is non-nil the binder updates `env["slot"][slot.Label]["value"]`
// to `ctx.Slots[k].SeriesValues[i]` (or 0 when absent). Scalar-shaped
// slots get the slot's constant scalar regardless of `i`.
func bindFormulaEnvSeries(env map[string]any, ctx *FormulaContext, i int) (value float64, present bool) {
	if env == nil || ctx == nil || ctx.SeriesHost == nil {
		return 0, false
	}
	value, present = ctx.SeriesHost.ValueAt(i)
	if !present {
		return 0, false
	}
	env["value"] = value
	env["total"] = ctx.SeriesTotal
	env["prior"] = resolveSeriesPrior(ctx.SeriesHost, i)
	if ctx.BaselineValuePresent {
		env["baseline"] = ctx.BaselineValue
	}
	if ctx.RefSeries != nil {
		var ref float64
		if i >= 0 && i < len(ctx.RefSeries) {
			refPresent := true
			if ctx.RefSeriesPresent != nil && i < len(ctx.RefSeriesPresent) {
				refPresent = ctx.RefSeriesPresent[i]
			}
			if refPresent {
				ref = ctx.RefSeries[i]
			}
		}
		env["ref_value"] = ref
	}
	bindFormulaSlotsSeries(env, ctx.Slots, i)
	return value, true
}

// bindFormulaEnvScalar re-writes the SCALAR namespace value slots in
// `env` for a single host scalar. The caller passes a pre-allocated
// env (the prototype env returned by `buildFormulaPrototypeEnvScalar`);
// this binder mutates the entries in place.
//
// `value` reads from `ctx.ScalarValue`. When the scalar is absent the
// binder returns `(0, false)` and the caller skips the evaluation
// (mirroring the absent-host policy on the other shapes).
//
// `ref` (opt-in) reads from `ctx.RefScalar`. Compose slot namespace:
// each `ctx.Slots[k]` carrying a Scalar surfaces as
// `env["slot"][slot.Label]["value"]`.
func bindFormulaEnvScalar(env map[string]any, ctx *FormulaContext) (value float64, present bool) {
	if env == nil || ctx == nil {
		return 0, false
	}
	if !ctx.ScalarPresent {
		return 0, false
	}
	env["value"] = ctx.ScalarValue
	if ctx.RefScalarPresent {
		env["ref"] = ctx.RefScalar
	}
	bindFormulaSlotsScalar(env, ctx.Slots)
	return ctx.ScalarValue, true
}

// resolveSeriesPrior returns the lag-1 PRESENT entry's value
// preceding index `i` in the host series. NaN at the first present
// entry (no lag available) per research note § 2.2 first-entry
// semantics. Walks backwards from `i-1` and returns the first present
// entry's value; returns NaN when no preceding present entry exists.
//
// O(i) worst case (the per-iteration scan); acceptable at v1 because
// SERIES FORMULA is buffered and the typical series length is
// bounded by the grouped Process orchestrator's per-group key count.
// A future story may pre-compute a lag1 array if the linear scan
// becomes hot.
func resolveSeriesPrior(host *SeriesHostView, i int) float64 {
	if host == nil || i <= 0 {
		return math.NaN()
	}
	for k := i - 1; k >= 0; k-- {
		v, present := host.ValueAt(k)
		if present {
			return v
		}
	}
	return math.NaN()
}

// bindFormulaSlotsMatrix updates the `slot.<label>.cell` nested-map
// values for every slot binding whose MatrixCells slot is populated.
// Slots whose payload shape disagrees with the iteration's host shape
// (e.g. a series-shaped slot in a matrix-host iteration) leave the
// prototype's `value` slot at zero — the per-iteration evaluator
// reads the constant scalar / series value via the same key under a
// shape-incompatible reference (the predict gate rejects this
// combination but the runtime still surfaces a sensible binding so
// the formula evaluator does not crash).
func bindFormulaSlotsMatrix(env map[string]any, slots []formulaSlotBinding, rowIdx, colIdx int) {
	if len(slots) == 0 {
		return
	}
	slotMap, ok := env["slot"].(map[string]any)
	if !ok {
		return
	}
	for _, slot := range slots {
		inner, ok := slotMap[slot.Label].(map[string]any)
		if !ok {
			continue
		}
		if slot.MatrixCells != nil {
			var v float64
			if rowIdx >= 0 && rowIdx < len(slot.MatrixCells) &&
				colIdx >= 0 && colIdx < len(slot.MatrixCells[rowIdx]) {
				present := true
				if slot.MatrixPresent != nil &&
					rowIdx < len(slot.MatrixPresent) &&
					colIdx < len(slot.MatrixPresent[rowIdx]) {
					present = slot.MatrixPresent[rowIdx][colIdx]
				}
				if present {
					v = slot.MatrixCells[rowIdx][colIdx]
				}
			}
			inner["cell"] = v
		}
		if slot.SeriesValues != nil || slot.ScalarPresent {
			// A series / scalar slot in a matrix-host iteration surfaces
			// the slot's constant — series slots use SeriesValues[0]
			// when populated (a defensible default; the predict gate
			// rejects this combination but the binder stays no-crash).
			var v float64
			if slot.ScalarPresent {
				v = slot.Scalar
			} else if len(slot.SeriesValues) > 0 {
				v = slot.SeriesValues[0]
			}
			inner["value"] = v
		}
	}
}

// bindFormulaSlotsSeries updates the `slot.<label>.value` nested-map
// values for every slot binding whose SeriesValues / Scalar payload
// is populated. Matrix-shaped slots in a series-host iteration
// surface the slot's [0][0] cell as a defensible default.
func bindFormulaSlotsSeries(env map[string]any, slots []formulaSlotBinding, i int) {
	if len(slots) == 0 {
		return
	}
	slotMap, ok := env["slot"].(map[string]any)
	if !ok {
		return
	}
	for _, slot := range slots {
		inner, ok := slotMap[slot.Label].(map[string]any)
		if !ok {
			continue
		}
		if slot.SeriesValues != nil {
			var v float64
			if i >= 0 && i < len(slot.SeriesValues) {
				present := true
				if slot.SeriesPresent != nil && i < len(slot.SeriesPresent) {
					present = slot.SeriesPresent[i]
				}
				if present {
					v = slot.SeriesValues[i]
				}
			}
			inner["value"] = v
			continue
		}
		if slot.ScalarPresent {
			inner["value"] = slot.Scalar
			continue
		}
		if slot.MatrixCells != nil {
			var v float64
			if len(slot.MatrixCells) > 0 && len(slot.MatrixCells[0]) > 0 {
				present := true
				if len(slot.MatrixPresent) > 0 && len(slot.MatrixPresent[0]) > 0 {
					present = slot.MatrixPresent[0][0]
				}
				if present {
					v = slot.MatrixCells[0][0]
				}
			}
			inner["cell"] = v
		}
	}
}

// bindFormulaSlotsScalar updates the `slot.<label>.value` nested-map
// values for every slot binding for the SCALAR-host iteration (single
// binding — no `i`). Matrix / series-shaped slots in a scalar-host
// iteration surface a defensible scalar default (the predict gate
// rejects cross-shape pairings).
func bindFormulaSlotsScalar(env map[string]any, slots []formulaSlotBinding) {
	if len(slots) == 0 {
		return
	}
	slotMap, ok := env["slot"].(map[string]any)
	if !ok {
		return
	}
	for _, slot := range slots {
		inner, ok := slotMap[slot.Label].(map[string]any)
		if !ok {
			continue
		}
		if slot.ScalarPresent {
			inner["value"] = slot.Scalar
			continue
		}
		if slot.SeriesValues != nil {
			var v float64
			if len(slot.SeriesValues) > 0 {
				present := true
				if len(slot.SeriesPresent) > 0 {
					present = slot.SeriesPresent[0]
				}
				if present {
					v = slot.SeriesValues[0]
				}
			}
			inner["value"] = v
			continue
		}
		if slot.MatrixCells != nil {
			var v float64
			if len(slot.MatrixCells) > 0 && len(slot.MatrixCells[0]) > 0 {
				present := true
				if len(slot.MatrixPresent) > 0 && len(slot.MatrixPresent[0]) > 0 {
					present = slot.MatrixPresent[0][0]
				}
				if present {
					v = slot.MatrixCells[0][0]
				}
			}
			inner["cell"] = v
		}
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

// detectFormulaSDIdentifiers parses `formula` via the expr-lang
// parser and walks the AST collecting identifier-node values that
// match the three MATRIX-host SD variables (research note § 2.1 SD
// derivation paragraph). Returns one boolean per identifier so the
// matrix orchestrator can short-circuit the Welford recurrence for
// absent SD arms (the O(cells) finalize pass skips per-row / per-col
// / per-grand accumulator work when the formula does not reference
// the matching identifier).
//
// On a parse failure the helper conservatively returns `(true, true,
// true)` so the orchestrator computes every SD slot. The eventual
// `expr.Compile` will surface the parse error via
// `PULSE_OVERLAY_FORMULA_PARSE_ERROR` so the conservative arm never
// reaches a per-cell evaluation in practice.
func detectFormulaSDIdentifiers(formula string) (needRow, needCol, needGrand bool) {
	tree, err := exprparser.Parse(formula)
	if err != nil {
		return true, true, true
	}
	v := &formulaSDIdentVisitor{}
	exprast.Walk(&tree.Node, v)
	return v.needRow, v.needCol, v.needGrand
}

// formulaSDIdentVisitor is the AST walker `detectFormulaSDIdentifiers`
// uses to spot the three MATRIX-host SD identifiers
// (`sd_row` / `sd_col` / `sd_grand`). Other identifiers are ignored;
// the predict gate (E8-S4) is the authoritative arbiter of the full
// identifier set.
type formulaSDIdentVisitor struct {
	needRow   bool
	needCol   bool
	needGrand bool
}

func (v *formulaSDIdentVisitor) Visit(node *exprast.Node) {
	id, ok := (*node).(*exprast.IdentifierNode)
	if !ok {
		return
	}
	switch id.Value {
	case "sd_row":
		v.needRow = true
	case "sd_col":
		v.needCol = true
	case "sd_grand":
		v.needGrand = true
	}
}
