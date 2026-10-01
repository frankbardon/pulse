// Raw-cell lookup helper for COMPOSE overlay handlers. The four
// parity overlays (OVERLAY_T_CELL / OVERLAY_Z_CELL / OVERLAY_T_VS_REF
// / OVERLAY_Z_VS_REF) read `(mean, variance, n)` from
// Response.Components.Crosstab.CellComponents[r][c] (MATRIX arm) or
// the row-shape components map (SERIES arm); the components-source
// equivalents live in welford_components_extract.go.
//
// `buildMatrixCellRawLookup` remains because the MATRIX-arm handlers
// still need to verify a reference cell is `Present` at the matching
// (row_key, col_key) tuple before falling through to the scalar +
// Params additive contract. The lookup carries the raw cell payload
// untouched — the handler reads scalar fallback values via
// `scalarFromCell` only when CellComponents is absent.
package processing

import (
	"github.com/frankbardon/pulse/types"
)

// buildMatrixCellRawLookup folds a MatrixPayload's present cells into
// a `(rowKey, colKey) → MatrixCell` lookup table that PRESERVES the
// raw cell payload (Value + Present). Used by OVERLAY_T_CELL /
// OVERLAY_Z_CELL to gate cell presence on the reference side; the
// handlers consume `(mean, variance, n)` from
// Response.Components.Crosstab.CellComponents[r][c] (looked up by the
// matching (r, c) coordinate via `buildMatrixCellCoordLookup`) and
// fall back to the scalar arm via `scalarFromCell` when the
// components source is absent.
//
// Cells that are absent are skipped so callers can distinguish
// "missing cell" from "cell with zero value".
func buildMatrixCellRawLookup(mx *types.MatrixPayload) map[matrixCellLookupKey]types.MatrixCell {
	if mx == nil {
		return map[matrixCellLookupKey]types.MatrixCell{}
	}
	out := make(map[matrixCellLookupKey]types.MatrixCell, len(mx.RowKeys)*len(mx.ColumnKeys))
	for i := 0; i < len(mx.RowKeys) && i < len(mx.Cells); i++ {
		rowKeyStr := axisKeyToString(mx.RowKeys[i])
		for j := 0; j < len(mx.ColumnKeys) && j < len(mx.Cells[i]); j++ {
			cell := mx.Cells[i][j]
			if !cell.Present {
				continue
			}
			out[matrixCellLookupKey{row: rowKeyStr, col: axisKeyToString(mx.ColumnKeys[j])}] = cell
		}
	}
	return out
}
