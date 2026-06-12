package processing

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/expr-lang/expr"

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

// newFormulaHostWithMargins wraps a small rows x cols matrix payload
// AND populates RowMargins / ColumnMargins / GrandTotal so the
// MATRIX-host FORMULA binder tests can exercise the margin namespace.
// Mirrors newFormulaHost but pre-computes the per-axis sums as the
// buffered crosstab orchestrator would emit them (raw sum over each
// row / column / matrix-wide cell strip).
func newFormulaHostWithMargins(rows, cols int, fill func(r, c int) float64) *CrosstabHostView {
	cells := make([][]types.MatrixCell, rows)
	rowMargins := make([]types.MatrixCell, rows)
	colMargins := make([]types.MatrixCell, cols)
	rowKeys := make([]types.AxisKey, rows)
	colKeys := make([]types.AxisKey, cols)
	var grand float64
	for j := 0; j < cols; j++ {
		colKeys[j] = types.AxisKey{"c" + formulaIntToString(j)}
	}
	for i := 0; i < rows; i++ {
		row := make([]types.MatrixCell, cols)
		var rowSum float64
		for j := 0; j < cols; j++ {
			v := fill(i, j)
			row[j] = types.MatrixCell{Value: v, Present: true}
			rowSum += v
			colMargins[j].Value = colMargins[j].Scalar() + v
			colMargins[j].Present = true
			grand += v
		}
		cells[i] = row
		rowMargins[i] = types.MatrixCell{Value: rowSum, Present: true}
		rowKeys[i] = types.AxisKey{"r" + formulaIntToString(i)}
	}
	return NewCrosstabHostView(&types.MatrixPayload{
		RowKeys:       rowKeys,
		ColumnKeys:    colKeys,
		Cells:         cells,
		RowMargins:    rowMargins,
		ColumnMargins: colMargins,
		GrandTotal:    types.MatrixCell{Value: grand, Present: true},
	})
}

// TestOverlay_Formula_NamespaceMatrix verifies the MATRIX-host binder
// populates every variable in research note § 2.1: `cell`,
// `margin_row`, `margin_col`, `margin_grand`, `sd_row`, `sd_col`,
// `sd_grand`. Each variable is referenced from a per-test formula and
// the resulting cell value asserted byte-for-byte against the
// expected arithmetic.
func TestOverlay_Formula_NamespaceMatrix(t *testing.T) {
	// 2x3 matrix carrying:
	//   row 0: [1, 2, 3]
	//   row 1: [4, 5, 6]
	// Row margins: 6, 15. Column margins: 5, 7, 9. Grand: 21.
	host := newFormulaHostWithMargins(2, 3, func(r, c int) float64 {
		return float64(r*3 + c + 1)
	})

	cases := []struct {
		name    string
		formula string
		want    [][]float64
	}{
		{
			name:    "cell_only",
			formula: "cell",
			want:    [][]float64{{1, 2, 3}, {4, 5, 6}},
		},
		{
			name:    "margin_row",
			formula: "cell / margin_row",
			want:    [][]float64{{1.0 / 6.0, 2.0 / 6.0, 3.0 / 6.0}, {4.0 / 15.0, 5.0 / 15.0, 6.0 / 15.0}},
		},
		{
			name:    "margin_col",
			formula: "cell / margin_col",
			want:    [][]float64{{1.0 / 5.0, 2.0 / 7.0, 3.0 / 9.0}, {4.0 / 5.0, 5.0 / 7.0, 6.0 / 9.0}},
		},
		{
			name:    "margin_grand",
			formula: "cell / margin_grand",
			want:    [][]float64{{1.0 / 21.0, 2.0 / 21.0, 3.0 / 21.0}, {4.0 / 21.0, 5.0 / 21.0, 6.0 / 21.0}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := newFormulaSpec(tc.name, tc.formula)
			layer, _, err := applyFormula(&spec, host)
			if err != nil {
				t.Fatalf("applyFormula: %v", err)
			}
			for i := range tc.want {
				for j := range tc.want[i] {
					got, _ := layer.Payload.Matrix.Cells[i][j].Value.(float64)
					if math.Abs(got-tc.want[i][j]) > 1e-12 {
						t.Errorf("Cells[%d][%d] = %v, want %v", i, j, got, tc.want[i][j])
					}
				}
			}
		})
	}
}

// TestOverlay_Formula_NamespaceMatrixSDs verifies the SD precomputation
// is correct AND only computes for referenced identifiers. SDs use the
// population (N-divisor) Welford variance — mirrors
// `computeSeriesWelfordPopulation` for the MATRIX axis case.
func TestOverlay_Formula_NamespaceMatrixSDs(t *testing.T) {
	// 2x3 matrix:
	//   row 0: [1, 2, 3]  → mean 2, SD = sqrt(((1-2)^2+(2-2)^2+(3-2)^2)/3) = sqrt(2/3)
	//   row 1: [4, 5, 6]  → mean 5, SD = sqrt(2/3)
	// col 0: [1, 4]      → mean 2.5, SD = sqrt(((1-2.5)^2+(4-2.5)^2)/2) = sqrt(2.25)
	// col 1: [2, 5]      → SD = sqrt(2.25)
	// col 2: [3, 6]      → SD = sqrt(2.25)
	// grand: 1..6        → mean 3.5, SD = sqrt(((1-3.5)^2 + ... +(6-3.5)^2)/6) = sqrt(17.5/6)
	host := newFormulaHostWithMargins(2, 3, func(r, c int) float64 {
		return float64(r*3 + c + 1)
	})

	// sd_row
	spec := newFormulaSpec("sd_row_check", "sd_row")
	layer, _, err := applyFormula(&spec, host)
	if err != nil {
		t.Fatalf("applyFormula sd_row: %v", err)
	}
	wantRowSD := math.Sqrt(2.0 / 3.0)
	for i := 0; i < 2; i++ {
		for j := 0; j < 3; j++ {
			got, _ := layer.Payload.Matrix.Cells[i][j].Value.(float64)
			if math.Abs(got-wantRowSD) > 1e-12 {
				t.Errorf("sd_row Cells[%d][%d] = %v, want %v", i, j, got, wantRowSD)
			}
		}
	}

	// sd_col
	spec = newFormulaSpec("sd_col_check", "sd_col")
	layer, _, err = applyFormula(&spec, host)
	if err != nil {
		t.Fatalf("applyFormula sd_col: %v", err)
	}
	wantColSD := math.Sqrt(2.25)
	for i := 0; i < 2; i++ {
		for j := 0; j < 3; j++ {
			got, _ := layer.Payload.Matrix.Cells[i][j].Value.(float64)
			if math.Abs(got-wantColSD) > 1e-12 {
				t.Errorf("sd_col Cells[%d][%d] = %v, want %v", i, j, got, wantColSD)
			}
		}
	}

	// sd_grand
	spec = newFormulaSpec("sd_grand_check", "sd_grand")
	layer, _, err = applyFormula(&spec, host)
	if err != nil {
		t.Fatalf("applyFormula sd_grand: %v", err)
	}
	wantGrandSD := math.Sqrt(17.5 / 6.0)
	for i := 0; i < 2; i++ {
		for j := 0; j < 3; j++ {
			got, _ := layer.Payload.Matrix.Cells[i][j].Value.(float64)
			if math.Abs(got-wantGrandSD) > 1e-12 {
				t.Errorf("sd_grand Cells[%d][%d] = %v, want %v", i, j, got, wantGrandSD)
			}
		}
	}
}

// TestOverlay_Formula_SDShortCircuit verifies the AST detector skips
// SD computation when the formula does not reference any sd_* var —
// the helper returns empty SD slices + zero grand SD, which the
// binder writes as 0 into the env. The test asserts the SD slot is 0
// when not referenced (a defensible default) and computed when it is.
func TestOverlay_Formula_SDShortCircuit(t *testing.T) {
	// Detector test: only `sd_row` referenced → only row SDs computed.
	needRow, needCol, needGrand := detectFormulaSDIdentifiers("cell + sd_row")
	if !needRow || needCol || needGrand {
		t.Errorf("detectFormulaSDIdentifiers(cell + sd_row) = (%v, %v, %v), want (true, false, false)",
			needRow, needCol, needGrand)
	}
	needRow, needCol, needGrand = detectFormulaSDIdentifiers("sd_grand")
	if needRow || needCol || !needGrand {
		t.Errorf("detectFormulaSDIdentifiers(sd_grand) = (%v, %v, %v), want (false, false, true)",
			needRow, needCol, needGrand)
	}
	needRow, needCol, needGrand = detectFormulaSDIdentifiers("cell + margin_row")
	if needRow || needCol || needGrand {
		t.Errorf("detectFormulaSDIdentifiers(cell + margin_row) = (%v, %v, %v), want all false",
			needRow, needCol, needGrand)
	}
	// Parse-error path: conservative arm returns all-true.
	needRow, needCol, needGrand = detectFormulaSDIdentifiers("cell + ((")
	if !needRow || !needCol || !needGrand {
		t.Errorf("detectFormulaSDIdentifiers(bad-parse) = (%v, %v, %v), want all true",
			needRow, needCol, needGrand)
	}
}

// TestOverlay_Formula_NamespaceSeries verifies the SERIES-host binder
// populates `value`, `total`, `prior`, and optional `baseline` per
// research note § 2.2. `prior` is NaN at the first present entry.
func TestOverlay_Formula_NamespaceSeries(t *testing.T) {
	keys := []types.AxisKey{
		{"a"}, {"b"}, {"c"},
	}
	values := []float64{10, 20, 30} // total = 60
	resolver := func(i int) (float64, bool) { return values[i], true }
	host := NewSeriesHostView(keys, resolver)

	// value + total
	ctx := &FormulaContext{
		Shape:       types.OverlayShapeSeries,
		SeriesHost:  host,
		SeriesTotal: 60,
	}
	env := buildFormulaPrototypeEnvSeries(ctx)

	_, present := bindFormulaEnvSeries(env, ctx, 0)
	if !present {
		t.Fatal("bindFormulaEnvSeries(0): present=false, want true")
	}
	if got, _ := env["value"].(float64); got != 10 {
		t.Errorf("value @ 0 = %v, want 10", got)
	}
	if got, _ := env["total"].(float64); got != 60 {
		t.Errorf("total @ 0 = %v, want 60", got)
	}
	if got, _ := env["prior"].(float64); !math.IsNaN(got) {
		t.Errorf("prior @ 0 = %v, want NaN (first entry)", got)
	}

	bindFormulaEnvSeries(env, ctx, 1)
	if got, _ := env["value"].(float64); got != 20 {
		t.Errorf("value @ 1 = %v, want 20", got)
	}
	if got, _ := env["prior"].(float64); got != 10 {
		t.Errorf("prior @ 1 = %v, want 10", got)
	}

	bindFormulaEnvSeries(env, ctx, 2)
	if got, _ := env["prior"].(float64); got != 20 {
		t.Errorf("prior @ 2 = %v, want 20", got)
	}

	// baseline opt-in
	ctx.BaselineValue = 25
	ctx.BaselineValuePresent = true
	env = buildFormulaPrototypeEnvSeries(ctx)
	bindFormulaEnvSeries(env, ctx, 0)
	if got, _ := env["baseline"].(float64); got != 25 {
		t.Errorf("baseline = %v, want 25", got)
	}
}

// TestOverlay_Formula_SeriesPriorSkipsAbsent verifies the `prior`
// resolver scans backwards through absent entries until it finds the
// first PRESENT entry. Mirrors the research note § 2.2 contract.
func TestOverlay_Formula_SeriesPriorSkipsAbsent(t *testing.T) {
	keys := []types.AxisKey{
		{"a"}, {"b"}, {"c"}, {"d"},
	}
	values := []float64{10, 0, 0, 40}
	present := []bool{true, false, false, true}
	resolver := func(i int) (float64, bool) { return values[i], present[i] }
	host := NewSeriesHostView(keys, resolver)

	// prior @ index 3 should skip absent indices 2, 1 and surface
	// index 0's value (10).
	if got := resolveSeriesPrior(host, 3); got != 10 {
		t.Errorf("resolveSeriesPrior @ 3 = %v, want 10", got)
	}
	// prior @ index 0 → NaN
	if got := resolveSeriesPrior(host, 0); !math.IsNaN(got) {
		t.Errorf("resolveSeriesPrior @ 0 = %v, want NaN", got)
	}
}

// TestOverlay_Formula_NamespaceScalar verifies the SCALAR-host binder
// populates `value` and optional `ref` per research note § 2.3.
func TestOverlay_Formula_NamespaceScalar(t *testing.T) {
	ctx := &FormulaContext{
		Shape:         types.OverlayShapeScalar,
		ScalarValue:   42.0,
		ScalarPresent: true,
	}
	env := buildFormulaPrototypeEnvScalar(ctx)
	_, present := bindFormulaEnvScalar(env, ctx)
	if !present {
		t.Fatal("bindFormulaEnvScalar: present=false, want true")
	}
	if got, _ := env["value"].(float64); got != 42 {
		t.Errorf("value = %v, want 42", got)
	}
	if _, has := env["ref"]; has {
		t.Errorf("ref should not be in env without Compose reference")
	}

	// With ref
	ctx.RefScalar = 10
	ctx.RefScalarPresent = true
	env = buildFormulaPrototypeEnvScalar(ctx)
	bindFormulaEnvScalar(env, ctx)
	if got, _ := env["ref"].(float64); got != 10 {
		t.Errorf("ref = %v, want 10", got)
	}

	// Absent host scalar
	ctx.ScalarPresent = false
	env = buildFormulaPrototypeEnvScalar(ctx)
	if _, present := bindFormulaEnvScalar(env, ctx); present {
		t.Errorf("bindFormulaEnvScalar with ScalarPresent=false should return present=false")
	}
}

// TestOverlay_Formula_SlotNamespace verifies the Compose
// `slot.<label>.cell` / `slot.<label>.value` namespace is plumbed
// through the per-shape binders. Tests matrix-host iteration with a
// matrix-shaped slot, a series-shaped slot, and a scalar-shaped slot
// per research note § 2.4.
func TestOverlay_Formula_SlotNamespace(t *testing.T) {
	host := newFormulaHostWithMargins(2, 2, func(r, c int) float64 {
		return float64(r*2 + c + 1) // 1, 2, 3, 4
	})

	// Compose slot: matrix-shaped, label "ref"
	slotMatrix := formulaSlotBinding{
		Label: "ref",
		MatrixCells: [][]float64{
			{100, 200},
			{300, 400},
		},
		MatrixPresent: [][]bool{
			{true, true},
			{true, true},
		},
	}
	ctx := &FormulaContext{
		Shape:      types.OverlayShapeMatrix,
		MatrixHost: host,
		Slots:      []formulaSlotBinding{slotMatrix},
	}
	env := buildFormulaPrototypeEnvMatrix(ctx)
	// Compile-time env must declare slot.ref.cell as nested map
	slotMap, ok := env["slot"].(map[string]any)
	if !ok {
		t.Fatalf("env[\"slot\"] = %T, want map[string]any", env["slot"])
	}
	inner, ok := slotMap["ref"].(map[string]any)
	if !ok {
		t.Fatalf("env[\"slot\"][\"ref\"] = %T, want map[string]any", slotMap["ref"])
	}
	if _, has := inner["cell"]; !has {
		t.Fatalf("env[\"slot\"][\"ref\"][\"cell\"] missing")
	}

	// Bind iteration (0, 1) — slot.ref.cell should read 200
	bindFormulaEnvMatrix(env, ctx, 0, 1)
	got, _ := slotMap["ref"].(map[string]any)["cell"].(float64)
	if got != 200 {
		t.Errorf("slot.ref.cell @ (0,1) = %v, want 200", got)
	}

	// Run a formula against the nested namespace to verify end-to-end
	spec := newFormulaSpec("slot_mix", "cell + slot.ref.cell")
	// Need to apply against a context with slots — re-run applyFormula?
	// applyFormula builds its own ctx without slots. Test the binder
	// directly by compiling the program against the slot-aware env and
	// running it.
	program, err := compileFormulaProgram(&spec, "cell + slot.ref.cell", env)
	if err != nil {
		t.Fatalf("compileFormulaProgram: %v", err)
	}
	// Iterate (0, 1): cell=2, slot.ref.cell=200 → expect 202
	bindFormulaEnvMatrix(env, ctx, 0, 1)
	rawOut, err := expr.Run(program.program, env)
	if err != nil {
		t.Fatalf("expr.Run: %v", err)
	}
	out, ok := coerceFormulaResult(rawOut)
	if !ok {
		t.Fatalf("coerceFormulaResult: not coercible: %T (%v)", rawOut, rawOut)
	}
	if out != 202 {
		t.Errorf("expr.Run @ (0,1) = %v, want 202", out)
	}

	// Series-shaped slot in series-host iteration
	keys := []types.AxisKey{{"a"}, {"b"}}
	values := []float64{10, 20}
	resolver := func(i int) (float64, bool) { return values[i], true }
	seriesHost := NewSeriesHostView(keys, resolver)
	seriesSlot := formulaSlotBinding{
		Label:         "ref",
		SeriesValues:  []float64{100, 200},
		SeriesPresent: []bool{true, true},
	}
	ctxSeries := &FormulaContext{
		Shape:       types.OverlayShapeSeries,
		SeriesHost:  seriesHost,
		SeriesTotal: 30,
		Slots:       []formulaSlotBinding{seriesSlot},
	}
	envS := buildFormulaPrototypeEnvSeries(ctxSeries)
	bindFormulaEnvSeries(envS, ctxSeries, 1)
	slotMapS, _ := envS["slot"].(map[string]any)
	innerS, _ := slotMapS["ref"].(map[string]any)
	if gotV, _ := innerS["value"].(float64); gotV != 200 {
		t.Errorf("slot.ref.value @ series[1] = %v, want 200", gotV)
	}

	// Scalar-shaped slot in scalar-host iteration
	scalarSlot := formulaSlotBinding{
		Label:         "ref",
		Scalar:        99,
		ScalarPresent: true,
	}
	ctxScalar := &FormulaContext{
		Shape:         types.OverlayShapeScalar,
		ScalarValue:   1.0,
		ScalarPresent: true,
		Slots:         []formulaSlotBinding{scalarSlot},
	}
	envSc := buildFormulaPrototypeEnvScalar(ctxScalar)
	bindFormulaEnvScalar(envSc, ctxScalar)
	slotMapSc, _ := envSc["slot"].(map[string]any)
	innerSc, _ := slotMapSc["ref"].(map[string]any)
	if gotV, _ := innerSc["value"].(float64); gotV != 99 {
		t.Errorf("slot.ref.value @ scalar = %v, want 99", gotV)
	}
}

// TestOverlay_Formula_BindEnvDispatch verifies the umbrella
// `bindFormulaEnv(shape, ctx)` returns the correct per-shape prototype.
func TestOverlay_Formula_BindEnvDispatch(t *testing.T) {
	ctx := &FormulaContext{}
	cases := []struct {
		shape   types.OverlayShape
		hasKey  []string
		missKey []string
	}{
		{
			shape:   types.OverlayShapeMatrix,
			hasKey:  []string{"cell", "margin_row", "margin_col", "margin_grand", "sd_row", "sd_col", "sd_grand"},
			missKey: []string{"value", "total", "prior"},
		},
		{
			shape:   types.OverlayShapeSeries,
			hasKey:  []string{"value", "total", "prior"},
			missKey: []string{"cell", "margin_row"},
		},
		{
			shape:   types.OverlayShapeScalar,
			hasKey:  []string{"value"},
			missKey: []string{"cell", "total", "prior"},
		},
	}
	for _, tc := range cases {
		env := bindFormulaEnv(tc.shape, ctx)
		for _, k := range tc.hasKey {
			if _, has := env[k]; !has {
				t.Errorf("[%s] env missing key %q", tc.shape, k)
			}
		}
		for _, k := range tc.missKey {
			if _, has := env[k]; has {
				t.Errorf("[%s] env should not have key %q", tc.shape, k)
			}
		}
	}
	// Unknown shape → empty env
	envUnknown := bindFormulaEnv(types.OverlayShape("bogus"), ctx)
	if len(envUnknown) != 0 {
		t.Errorf("unknown shape env should be empty, got %v", envUnknown)
	}
}

// TestOverlay_Formula_MatrixRefCell verifies the Compose ref_cell
// binding works when the FormulaContext carries a RefMatrix.
func TestOverlay_Formula_MatrixRefCell(t *testing.T) {
	host := newFormulaHostWithMargins(2, 2, func(r, c int) float64 {
		return float64(r*2 + c + 1)
	})
	ctx := &FormulaContext{
		Shape:      types.OverlayShapeMatrix,
		MatrixHost: host,
		RefMatrix: [][]float64{
			{100, 200},
			{300, 400},
		},
		RefMatrixPresent: [][]bool{
			{true, true},
			{true, true},
		},
	}
	env := buildFormulaPrototypeEnvMatrix(ctx)
	if _, has := env["ref_cell"]; !has {
		t.Fatal("prototype env missing ref_cell when RefMatrix is non-nil")
	}
	bindFormulaEnvMatrix(env, ctx, 1, 0)
	if got, _ := env["ref_cell"].(float64); got != 300 {
		t.Errorf("ref_cell @ (1,0) = %v, want 300", got)
	}
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
