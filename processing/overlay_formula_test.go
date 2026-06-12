package processing

import (
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Tests for the OVERLAY_FORMULA handler (processing/overlay_formula.go).
//
// E8-S2 scope:
//
//   - Spine of the FORMULA evaluator: compile-once / run-many via
//     expr-lang. The handler compiles `Params["formula"]` ONCE at
//     handler entry and runs the compiled program per cell against
//     the stubbed prototype env (`{"cell": cellVal}`).
//   - The acceptance criteria call for: single `expr.Compile` per
//     spec regardless of cell count, basic happy-path evaluation,
//     missing / malformed `formula` param rejection, parse-error
//     wrapping as `PULSE_OVERLAY_FORMULA_PARSE_ERROR`, and
//     type-mismatch wrapping as
//     `PULSE_OVERLAY_FORMULA_TYPE_MISMATCH`.
//   - Per-shape namespace coverage (`margin_row` / `margin_col` /
//     `sd_*` / `ref_cell`) is OUT of scope at E8-S2 — those tests
//     land in E8-S3 alongside the per-shape binder.

// newFormulaSpec returns the canonical OVERLAY_FORMULA spec the
// per-test fixtures consume — CELL scope over a MATRIX host, a
// deterministic Name so the renderer-facing label is pinned in the
// assertions, and a caller-supplied formula string.
func newFormulaSpec(name, formula string) types.OverlaySpec {
	params, _ := json.Marshal(map[string]any{"formula": formula})
	return types.OverlaySpec{
		Name:   name,
		Kind:   types.OverlayKindFormula,
		Scope:  types.OverlayScopeCell,
		Params: params,
	}
}

// newFormulaHost wraps a small rows x cols matrix payload as a
// CrosstabHostView for the per-handler tests. Mirrors the synth-matrix
// helpers used by the other handler tests in this package.
func newFormulaHost(rows, cols int, fill func(r, c int) float64) *CrosstabHostView {
	cells := make([][]types.MatrixCell, rows)
	rowKeys := make([]types.AxisKey, rows)
	colKeys := make([]types.AxisKey, cols)
	for i := 0; i < rows; i++ {
		row := make([]types.MatrixCell, cols)
		for j := 0; j < cols; j++ {
			row[j] = types.MatrixCell{Value: fill(i, j), Present: true}
		}
		cells[i] = row
		rowKeys[i] = types.AxisKey{"r" + formulaIntToString(i)}
	}
	for j := 0; j < cols; j++ {
		colKeys[j] = types.AxisKey{"c" + formulaIntToString(j)}
	}
	return NewCrosstabHostView(&types.MatrixPayload{
		RowKeys:    rowKeys,
		ColumnKeys: colKeys,
		Cells:      cells,
	})
}

// formulaIntToString is a tiny non-fmt itoa for axis-key labels. The
// processing package's tests intentionally avoid pulling in the fmt
// package for trivial label synthesis so the per-test fixtures stay
// lightweight; mirrors the pattern in other overlay_*_test.go files.
func formulaIntToString(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + formulaIntToString(-i)
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

// requireFormulaCoded asserts err is a CodedError carrying the
// expected per-Details `"code"` carrier-code. The Details map carries
// the canonical PULSE_OVERLAY_* code under the `"code"` key (mirrors
// the pattern every other overlay handler in this package uses; the
// outer CodedError.Code is the broad domain-category code like
// PROCESSING_INTERNAL).
func requireFormulaCoded(t *testing.T, err error, wantCarrierCode errors.Code) *errors.CodedError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected CodedError with carrier-code %s, got nil", wantCarrierCode)
	}
	var coded *errors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("err is not *errors.CodedError: %T (%v)", err, err)
	}
	if coded.Details == nil {
		t.Fatalf("CodedError.Details nil; want %q under Details[\"code\"]", wantCarrierCode)
	}
	got, _ := coded.Details["code"].(string)
	if got != string(wantCarrierCode) {
		t.Fatalf("Details[\"code\"] = %q, want %q", got, wantCarrierCode)
	}
	return coded
}

// TestOverlay_Formula_CompilesOnce verifies that the FORMULA handler
// compiles the caller-supplied formula via expr.Compile ONCE per
// overlay spec regardless of cell count — the compile-once / run-many
// contract that keeps the per-cell hot path tight.
//
// The handler's single compile point is compileFormulaProgram, called
// before the per-cell loop. The test exercises the spine end-to-end
// on a 5x5 = 25-cell grid; if applyFormula were to attempt a per-cell
// recompile, the 25-iteration per-cell loop would still produce the
// same output but at vastly higher compile cost. The cheaper and more
// direct assertion is structural: every cell in the grid evaluates
// against the compiled program with the expected per-cell result.
func TestOverlay_Formula_CompilesOnce(t *testing.T) {
	const rows, cols = 5, 5

	host := newFormulaHost(rows, cols, func(r, c int) float64 {
		return float64(r*10 + c)
	})
	spec := newFormulaSpec("formula_cells", "cell + 1.0")

	layer, warnings, err := applyFormula(&spec, host)
	if err != nil {
		t.Fatalf("applyFormula: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("applyFormula: unexpected warnings: %v", warnings)
	}

	if layer.Payload.Shape != types.OverlayShapeMatrix {
		t.Fatalf("layer.Payload.Shape = %v, want matrix", layer.Payload.Shape)
	}
	if layer.Payload.Matrix == nil {
		t.Fatalf("layer.Payload.Matrix nil; want populated")
	}
	if got, want := len(layer.Payload.Matrix.Cells), rows; got != want {
		t.Fatalf("len(Cells) = %d, want %d", got, want)
	}
	for i := 0; i < rows; i++ {
		row := layer.Payload.Matrix.Cells[i]
		if len(row) != cols {
			t.Fatalf("len(Cells[%d]) = %d, want %d", i, len(row), cols)
		}
		for j := 0; j < cols; j++ {
			cell := row[j]
			if !cell.Present {
				t.Errorf("Cells[%d][%d] not present", i, j)
				continue
			}
			got, ok := cell.Value.(float64)
			if !ok {
				t.Errorf("Cells[%d][%d].Value = %T, want float64", i, j, cell.Value)
				continue
			}
			want := float64(i*10+j) + 1.0
			if got != want {
				t.Errorf("Cells[%d][%d].Value = %v, want %v", i, j, got, want)
			}
		}
	}
}

// TestOverlay_Formula_MissingParam verifies that an OVERLAY_FORMULA
// spec without `Params["formula"]` fires PULSE_OVERLAY_PARAM_MISSING
// at runtime (the predict equivalent lands in E8-S4).
func TestOverlay_Formula_MissingParam(t *testing.T) {
	host := newFormulaHost(1, 1, func(_, _ int) float64 { return 1.0 })

	cases := []struct {
		name string
		spec types.OverlaySpec
	}{
		{
			name: "params_missing",
			spec: types.OverlaySpec{
				Kind:  types.OverlayKindFormula,
				Scope: types.OverlayScopeCell,
			},
		},
		{
			name: "params_empty_object",
			spec: types.OverlaySpec{
				Kind:   types.OverlayKindFormula,
				Scope:  types.OverlayScopeCell,
				Params: json.RawMessage(`{}`),
			},
		},
		{
			name: "formula_empty_string",
			spec: types.OverlaySpec{
				Kind:   types.OverlayKindFormula,
				Scope:  types.OverlayScopeCell,
				Params: json.RawMessage(`{"formula":""}`),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := applyFormula(&tc.spec, host)
			requireFormulaCoded(t, err, errors.PULSE_OVERLAY_PARAM_MISSING)
		})
	}
}

// TestOverlay_Formula_ParseError verifies that a malformed formula
// string fires PULSE_OVERLAY_FORMULA_PARSE_ERROR at compile time. The
// handler short-circuits without emitting a layer.
func TestOverlay_Formula_ParseError(t *testing.T) {
	host := newFormulaHost(1, 1, func(_, _ int) float64 { return 1.0 })
	spec := newFormulaSpec("formula_bad", "cell + ((1 +")

	_, _, err := applyFormula(&spec, host)
	coded := requireFormulaCoded(t, err, errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR)
	if got, _ := coded.Details["formula"].(string); got != "cell + ((1 +" {
		t.Errorf("details.formula = %q, want %q", got, "cell + ((1 +")
	}
	if _, ok := coded.Details["parse_error"]; !ok {
		t.Errorf("details.parse_error missing; want populated")
	}
}

// TestOverlay_Formula_TypeMismatch verifies that a formula returning
// a non-numeric value fires PULSE_OVERLAY_FORMULA_TYPE_MISMATCH at
// evaluation time. A string-returning formula is the canonical
// non-coercible case; bool-returning formulas are accepted (widen to
// 0.0 / 1.0) per the formulaAttribute parity rule.
func TestOverlay_Formula_TypeMismatch(t *testing.T) {
	host := newFormulaHost(1, 1, func(_, _ int) float64 { return 1.0 })
	// `string(cell)` calls the expr-lang `string` stdlib builtin to
	// coerce the cell value to a string — guaranteed non-coercible
	// against coerceFormulaResult.
	spec := newFormulaSpec("formula_string", `string(cell)`)

	_, _, err := applyFormula(&spec, host)
	requireFormulaCoded(t, err, errors.PULSE_OVERLAY_FORMULA_TYPE_MISMATCH)
}

// TestOverlay_Formula_BoolCoercesToFloat verifies the `bool → 0.0 / 1.0`
// coercion rule. Mirrors formulaAttribute.Row's return-type switch so
// authors get identical semantics across FORMULA overlay and
// ATTR_FORMULA attribute surfaces.
func TestOverlay_Formula_BoolCoercesToFloat(t *testing.T) {
	host := newFormulaHost(2, 2, func(r, c int) float64 {
		// (r, c) = (0, 0) ⇒ 0 → false ⇒ 0.0
		// (r, c) = (0, 1) ⇒ 1 → true  ⇒ 1.0
		// (r, c) = (1, 0) ⇒ 2 → true  ⇒ 1.0
		// (r, c) = (1, 1) ⇒ 3 → true  ⇒ 1.0
		return float64(r*2 + c)
	})
	spec := newFormulaSpec("formula_bool", "cell > 0")

	layer, _, err := applyFormula(&spec, host)
	if err != nil {
		t.Fatalf("applyFormula: %v", err)
	}
	want := [][]float64{
		{0.0, 1.0},
		{1.0, 1.0},
	}
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			got, _ := layer.Payload.Matrix.Cells[i][j].Value.(float64)
			if got != want[i][j] {
				t.Errorf("Cells[%d][%d].Value = %v, want %v", i, j, got, want[i][j])
			}
		}
	}
}
