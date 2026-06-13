package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Shared assertion helpers for the processing/overlay_*_test.go family.
//
// E2-S12 consolidation: the 10 per-kind overlay test files
// (overlay_chisq_col_test.go, overlay_chisq_matrix_test.go,
// overlay_chisq_row_test.go, overlay_delta_vs_margin_test.go,
// overlay_fisher_exact_cell_test.go, overlay_share_of_col_test.go,
// overlay_share_of_row_test.go, overlay_share_of_total_test.go,
// overlay_zscore_vs_margin_test.go, plus the index-vs-margin family in
// overlay_test.go) all assert on the same overlay-layer shapes:
//
//   - "matrix cell at (i, j) is within tol of an expected float"
//   - "series entry at axisIdx has Statistic / PValue within tol"
//   - "scalar layer's Summary carries Statistic + PValue within tol"
//   - "warnings slice contains exactly N entries with a given code"
//   - "warnings slice is empty"
//
// Centralising the assertion patterns gives E3+ overlay handlers a
// single source of truth. The intent is that new per-kind tests call
// these helpers with their hand-computed expected values rather than
// reinventing the inline float comparison + present-flag dance every
// time.
//
// File-name convention: the trailing `_test.go` is what keeps these
// helpers out of the production build — Go's standard test convention
// only compiles `*_test.go` files into the test binary. Story E2-S12
// names the file `processing/overlay_test_helpers.go` for prose
// reasons; the implementation uses the canonical `_test.go` suffix so
// the file is automatically gated to the test binary (no build tag /
// no manual guard required). The same package (`processing`, NOT
// `processing_test`) keeps the helpers reachable from every existing
// per-kind test file without an import cycle and leaves the door open
// for a future internal-fixture helper that exercises the unexported
// overlayHandlers dispatch table.
//
// Conventions for callers:
//
//   - Pass an explicit tolerance (eps / tol). Default for the overlay
//     family is 1e-9 for plain float ratios, 1e-4 for χ² statistics
//     against survival tables, 5e-4 for p-values.
//   - Use t.Helper() inside the helper so failure lines point at the
//     caller, not the helper internals.
//   - All helpers fatal via t.Fatalf — the existing per-kind tests use
//     t.Fatalf consistently. Switch to t.Errorf only when a helper is
//     called inside a per-(i, j) loop and the caller wants to see every
//     mismatch rather than the first.

// assertMatrixCellWithinTol asserts that layer.Payload.Matrix.Cells[rowIdx][colIdx]
// is present, its Value is a float64, and that value is within tol of
// want. Used by share/index/delta/zscore CELL-scope tests where the
// overlay payload is a MATRIX and the expected values are hand-computed
// closed-form quantities.
//
// Failure modes (all fatal via t.Fatalf):
//
//   - layer / payload / matrix is nil (the test asked for a MATRIX
//     overlay slot that the handler did not populate)
//   - cells[rowIdx][colIdx] is absent (Present=false)
//   - cell.Value is not a float64
//   - |cell.Value - want| > tol
//
// Signature notes: takes layer by pointer so an empty-layer test does
// not silently pass on a zero-valued OverlayLayer struct.
func assertMatrixCellWithinTol(t *testing.T, layer *types.OverlayLayer, rowIdx, colIdx int, want, tol float64) {
	t.Helper()
	if layer == nil {
		t.Fatalf("assertMatrixCellWithinTol: layer is nil")
	}
	if layer.Payload.Shape != types.OverlayShapeMatrix {
		t.Fatalf("assertMatrixCellWithinTol: layer.Payload.Shape = %q, want %q",
			layer.Payload.Shape, types.OverlayShapeMatrix)
	}
	if layer.Payload.Matrix == nil {
		t.Fatalf("assertMatrixCellWithinTol: layer.Payload.Matrix is nil")
	}
	cells := layer.Payload.Matrix.Cells
	if rowIdx < 0 || rowIdx >= len(cells) {
		t.Fatalf("assertMatrixCellWithinTol: rowIdx=%d out of range [0,%d)", rowIdx, len(cells))
	}
	row := cells[rowIdx]
	if colIdx < 0 || colIdx >= len(row) {
		t.Fatalf("assertMatrixCellWithinTol: colIdx=%d out of range [0,%d) for row %d",
			colIdx, len(row), rowIdx)
	}
	cell := row[colIdx]
	if !cell.Present {
		t.Fatalf("assertMatrixCellWithinTol: cell[%d][%d] absent; want present with value %v",
			rowIdx, colIdx, want)
	}
	got, ok := cell.Value.(float64)
	if !ok {
		t.Fatalf("assertMatrixCellWithinTol: cell[%d][%d] Value = %T, want float64",
			rowIdx, colIdx, cell.Value)
	}
	if math.Abs(got-want) > tol {
		t.Fatalf("assertMatrixCellWithinTol: cell[%d][%d] = %v, want %v ± %v",
			rowIdx, colIdx, got, want, tol)
	}
}

// assertSeriesEntryWithinTol asserts that layer.Payload.Series.Entries[axisIdx]
// carries a Summary whose Statistic and PValue are populated and within
// tol of their respective expected values. Used by the CHISQ_ROW /
// CHISQ_COL per-axis-statistic tests.
//
// wantStat / wantP control the centerline; tolStat / tolP control the
// per-field tolerance — the two-field shape lets callers pin the
// statistic tighter than the p-value (which rounds through a survival
// table).
//
// Failure modes (all fatal via t.Fatalf):
//
//   - layer / payload / series is nil
//   - axisIdx out of range
//   - entry.Summary.Statistic or .PValue is nil
//   - |Statistic - wantStat| > tolStat
//   - |PValue - wantP| > tolP
func assertSeriesEntryWithinTol(t *testing.T, layer *types.OverlayLayer, axisIdx int,
	wantStat, tolStat, wantP, tolP float64) {
	t.Helper()
	if layer == nil {
		t.Fatalf("assertSeriesEntryWithinTol: layer is nil")
	}
	if layer.Payload.Shape != types.OverlayShapeSeries {
		t.Fatalf("assertSeriesEntryWithinTol: layer.Payload.Shape = %q, want %q",
			layer.Payload.Shape, types.OverlayShapeSeries)
	}
	if layer.Payload.Series == nil {
		t.Fatalf("assertSeriesEntryWithinTol: layer.Payload.Series is nil")
	}
	entries := layer.Payload.Series.Entries
	if axisIdx < 0 || axisIdx >= len(entries) {
		t.Fatalf("assertSeriesEntryWithinTol: axisIdx=%d out of range [0,%d)",
			axisIdx, len(entries))
	}
	entry := entries[axisIdx]
	if entry.Summary.Statistic == nil {
		t.Fatalf("assertSeriesEntryWithinTol: entry[%d].Summary.Statistic is nil; want %v ± %v",
			axisIdx, wantStat, tolStat)
	}
	if math.Abs(*entry.Summary.Statistic-wantStat) > tolStat {
		t.Fatalf("assertSeriesEntryWithinTol: entry[%d].Summary.Statistic = %v, want %v ± %v",
			axisIdx, *entry.Summary.Statistic, wantStat, tolStat)
	}
	if entry.Summary.PValue == nil {
		t.Fatalf("assertSeriesEntryWithinTol: entry[%d].Summary.PValue is nil; want %v ± %v",
			axisIdx, wantP, tolP)
	}
	if math.Abs(*entry.Summary.PValue-wantP) > tolP {
		t.Fatalf("assertSeriesEntryWithinTol: entry[%d].Summary.PValue = %v, want %v ± %v",
			axisIdx, *entry.Summary.PValue, wantP, tolP)
	}
}

// assertScalarLayerWithinTol asserts that layer.Payload is SCALAR-
// shaped and that layer.Summary.Statistic and layer.Summary.PValue are
// both populated within tol of their expected values. Used by the
// CHISQ_MATRIX whole-table statistic tests.
//
// wantStat / tolStat / wantP / tolP follow the same convention as
// assertSeriesEntryWithinTol — tighter tolerance on the statistic than
// on the p-value.
//
// Failure modes (all fatal via t.Fatalf):
//
//   - layer is nil
//   - layer.Payload.Shape != OverlayShapeScalar
//   - layer.Payload.Scalar is nil
//   - layer.Summary is nil
//   - Summary.Statistic or .PValue is nil
//   - |Statistic - wantStat| > tolStat
//   - |PValue - wantP| > tolP
func assertScalarLayerWithinTol(t *testing.T, layer *types.OverlayLayer,
	wantStat, tolStat, wantP, tolP float64) {
	t.Helper()
	if layer == nil {
		t.Fatalf("assertScalarLayerWithinTol: layer is nil")
	}
	if layer.Payload.Shape != types.OverlayShapeScalar {
		t.Fatalf("assertScalarLayerWithinTol: layer.Payload.Shape = %q, want %q",
			layer.Payload.Shape, types.OverlayShapeScalar)
	}
	if layer.Payload.Scalar == nil {
		t.Fatalf("assertScalarLayerWithinTol: layer.Payload.Scalar is nil; want %v ± %v",
			wantStat, tolStat)
	}
	if math.Abs(*layer.Payload.Scalar-wantStat) > tolStat {
		t.Fatalf("assertScalarLayerWithinTol: layer.Payload.Scalar = %v, want %v ± %v",
			*layer.Payload.Scalar, wantStat, tolStat)
	}
	if layer.Summary == nil {
		t.Fatalf("assertScalarLayerWithinTol: layer.Summary is nil; want Statistic %v ± %v + PValue %v ± %v",
			wantStat, tolStat, wantP, tolP)
	}
	if layer.Summary.Statistic == nil {
		t.Fatalf("assertScalarLayerWithinTol: layer.Summary.Statistic is nil; want %v ± %v",
			wantStat, tolStat)
	}
	if math.Abs(*layer.Summary.Statistic-wantStat) > tolStat {
		t.Fatalf("assertScalarLayerWithinTol: layer.Summary.Statistic = %v, want %v ± %v",
			*layer.Summary.Statistic, wantStat, tolStat)
	}
	if layer.Summary.PValue == nil {
		t.Fatalf("assertScalarLayerWithinTol: layer.Summary.PValue is nil; want %v ± %v",
			wantP, tolP)
	}
	if math.Abs(*layer.Summary.PValue-wantP) > tolP {
		t.Fatalf("assertScalarLayerWithinTol: layer.Summary.PValue = %v, want %v ± %v",
			*layer.Summary.PValue, wantP, tolP)
	}
}

// assertWarningCode asserts that warnings carries exactly wantCount
// entries whose Code equals wantCode. Used by the missing-margin /
// zero-margin defense tests across the share / index / zscore family
// (PULSE_OVERLAY_REF_ZERO emissions) and by the χ² low-expected-count
// tests (PULSE_OVERLAY_EXPECTED_LOW emissions).
//
// Empty wantCount (==0) is rejected with a clearer error message
// directing callers to assertNoOverlayWarnings (which makes the intent
// readable in the test source).
//
// Failure modes (all fatal via t.Fatalf):
//
//   - wantCount <= 0 (use assertNoOverlayWarnings for the no-warnings
//     case so the test reads correctly)
//   - len(warnings) != wantCount
//   - any warnings[i].Code != wantCode
func assertWarningCode(t *testing.T, warnings []OverlayWarning, wantCode string, wantCount int) {
	t.Helper()
	if wantCount <= 0 {
		t.Fatalf("assertWarningCode: wantCount must be > 0 (got %d); use assertNoOverlayWarnings instead",
			wantCount)
	}
	if len(warnings) != wantCount {
		codes := make([]string, len(warnings))
		for i, w := range warnings {
			codes[i] = w.Code
		}
		t.Fatalf("assertWarningCode: len(warnings) = %d, want %d (code=%q); observed codes = %v",
			len(warnings), wantCount, wantCode, codes)
	}
	for i, w := range warnings {
		if w.Code != wantCode {
			t.Fatalf("assertWarningCode: warnings[%d].Code = %q, want %q",
				i, w.Code, wantCode)
		}
	}
}

// assertNoOverlayWarnings asserts that warnings is empty. Used by the
// happy-path overlay tests where the host fixture has no missing /
// zero / low-expected denominator and no warning should fire. Distinct
// from assertWarningCode so the test source reads cleanly:
//
//	assertNoOverlayWarnings(t, warnings)   // intent: "no warnings"
//	assertWarningCode(t, warnings, ..., 9) // intent: "9 of code X"
//
// Failure mode (fatal via t.Fatalf):
//
//   - len(warnings) != 0; the failure message lists every observed
//     warning's Code so the diff is actionable.
func assertNoOverlayWarnings(t *testing.T, warnings []OverlayWarning) {
	t.Helper()
	if len(warnings) == 0 {
		return
	}
	codes := make([]string, len(warnings))
	for i, w := range warnings {
		codes[i] = w.Code
	}
	t.Fatalf("assertNoOverlayWarnings: expected no warnings, got %d: %v",
		len(warnings), codes)
}

// TestOverlay_SharedAssertionHelpers exercises every helper defined in
// this file at least once against real ApplyOverlays output. The test
// is intentionally minimal — its job is to guard the helpers against
// silent rot (staticcheck U1000 sees these as live; future refactors
// that break a helper surface immediately) and to demonstrate the
// intended call shape for the per-kind tests across the rest of the
// processing/overlay_*_test.go family.
//
// Each helper is tested via a known-good code path the per-kind suites
// already cover (synthIndexMarginPayload + INDEX_VS_MARGIN for matrix
// + warnings; chiSqMatrixPayloadKnown + CHISQ_MATRIX for scalar;
// chiSqRowPayloadKnown + CHISQ_ROW for series) so the helper's
// pass-failure semantics are pinned to the same fixtures the production
// tests already validate independently.
func TestOverlay_SharedAssertionHelpers(t *testing.T) {
	// assertMatrixCellWithinTol + assertNoOverlayWarnings via
	// INDEX_VS_MARGIN on a self-indexed matrix. Cell (0, 0) reads
	// (cell / row_margin × 100); with indexUniformPayload every cell
	// equals its row margin so the value is exactly 100.
	t.Run("matrix_cell_and_no_warnings", func(t *testing.T) {
		host := NewCrosstabHostView(indexUniformPayload())
		specs := []types.OverlaySpec{
			{
				Kind:  types.OverlayKindIndexVsMargin,
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
			},
		}
		layers, warnings, err := ApplyOverlays(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlays: %v", err)
		}
		assertNoOverlayWarnings(t, warnings)
		if len(layers) != 1 {
			t.Fatalf("expected 1 layer, got %d", len(layers))
		}
		assertMatrixCellWithinTol(t, &layers[0], 0, 0, 100.0, 1e-9)
		assertMatrixCellWithinTol(t, &layers[0], 2, 2, 100.0, 1e-9)
	})

	// assertScalarLayerWithinTol via CHISQ_MATRIX on the textbook 2×2
	// contingency (χ² = 100/15 ≈ 6.6667, p ≈ 0.00982, df=1).
	t.Run("scalar_layer", func(t *testing.T) {
		host := NewCrosstabHostView(chiSqMatrixPayloadKnown())
		specs := []types.OverlaySpec{
			{
				Kind:  types.OverlayKindChiSqMatrix,
				Scope: types.OverlayScopeMatrix,
			},
		}
		layers, _, err := ApplyOverlays(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlays: %v", err)
		}
		if len(layers) != 1 {
			t.Fatalf("expected 1 layer, got %d", len(layers))
		}
		const wantStat = 100.0 / 15.0
		const wantP = 0.00982
		assertScalarLayerWithinTol(t, &layers[0], wantStat, 1e-4, wantP, 5e-4)
	})

	// assertSeriesEntryWithinTol via CHISQ_ROW on the textbook 2×3
	// contingency (every row has χ² = 8/3, p ≈ 0.2636, df=2).
	t.Run("series_entry", func(t *testing.T) {
		host := NewCrosstabHostView(chiSqRowPayloadKnown())
		specs := []types.OverlaySpec{
			{
				Kind:  types.OverlayKindChiSqRow,
				Scope: types.OverlayScopeRow,
			},
		}
		layers, _, err := ApplyOverlays(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlays: %v", err)
		}
		if len(layers) != 1 {
			t.Fatalf("expected 1 layer, got %d", len(layers))
		}
		const wantStat = 8.0 / 3.0
		const wantP = 0.2636
		assertSeriesEntryWithinTol(t, &layers[0], 0, wantStat, 1e-4, wantP, 5e-4)
		assertSeriesEntryWithinTol(t, &layers[0], 1, wantStat, 1e-4, wantP, 5e-4)
	})

	// assertWarningCode via the missing-row-margin defense path on
	// INDEX_VS_MARGIN — 9 cells × PULSE_OVERLAY_REF_ZERO when the row
	// margin slot is unset. Mirrors the assertion shape every share /
	// index test's defense subtest uses.
	t.Run("warning_code", func(t *testing.T) {
		payload := synthIndexMarginPayload()
		payload.RowMargins = nil
		host := NewCrosstabHostView(payload)
		specs := []types.OverlaySpec{
			{
				Kind:  types.OverlayKindIndexVsMargin,
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
			},
		}
		_, warnings, err := ApplyOverlays(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlays: %v", err)
		}
		assertWarningCode(t, warnings, "PULSE_OVERLAY_REF_ZERO", 9)
	})
}
