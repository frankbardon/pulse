// Helpers for triple-aware COMPOSE overlay handlers (E1-S9 / E1-S10).
// The MATRIX-arm (OVERLAY_T_CELL / OVERLAY_Z_CELL) and SERIES-arm
// (OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF) inferential overlays both
// need to detect a WelfordTriple payload on their target / reference
// cells (or series entries) and fall back to the Params-default path
// when the cell carries a scalar float64 instead. The detection and
// the raw-cell ref lookup live here so the two host shapes share one
// extraction surface and so the Z-family handlers (E1-S17 / E1-S18)
// reuse the same primitives without duplicating the type-switch.
package processing

import (
	"sort"

	"github.com/frankbardon/pulse/types"
)

// welfordTripleFromCell extracts a processing.WelfordTriple from a
// MatrixCell when the cell's Value carries a triple. Returns
// (WelfordTriple{}, false) when the cell is absent OR when Value is
// any other type (including scalar float64 — scalar cells route
// through scalarFromCell instead). Mirrors scalarFromCell's
// (value, ok) convention so consumers can compose a single
// type-switch against the cell.
func welfordTripleFromCell(cell types.MatrixCell) (WelfordTriple, bool) {
	if !cell.Present {
		return WelfordTriple{}, false
	}
	if t, ok := cell.Value.(WelfordTriple); ok {
		return t, true
	}
	return WelfordTriple{}, false
}

// buildMatrixCellRawLookup folds a MatrixPayload's present cells into
// a `(rowKey, colKey) → MatrixCell` lookup table that PRESERVES the
// raw cell (Value + Present + any rich payload). The scalar
// buildMatrixCellLookup helper at processing/overlay_index_vs_stage.go
// drops non-scalar cells (it returns float64 values only); this
// variant keeps the cell intact so the triple-aware MATRIX-arm
// inferential overlays (OVERLAY_T_CELL, OVERLAY_Z_CELL) can detect
// a WelfordTriple payload on the reference side and re-dispatch the
// appropriate triple / scalar arm symmetrically with the target
// side.
//
// Cells that are absent are skipped; rich cells are retained so the
// handler's per-cell type switch can decide whether to consume them.
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

// encodeSeriesRowAny is the triple-aware sibling of encodeSeriesRow
// (processing/overlay_index_vs_stage.go). Mirrors the canonical-key
// rendering rule (sort columns; first numeric-or-triple column wins
// as the value column; remaining columns fold into the key) so a
// scalar-only row produces a key BYTE-IDENTICAL to encodeSeriesRow's
// output — the additive contract for the scalar fallback path on
// OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF requires this identity.
//
// A row whose value column carries a WelfordTriple sets hasTriple=true
// (with scalar zero, hasScalar=false); a row whose value column is a
// numeric scalar sets hasScalar=true; an empty row returns both flags
// false. The keying rule stays identical: subsequent numeric columns
// fold into the key as `col=<canonical>` so multi-aggregator rows
// stay uniquely keyed, regardless of the value column's shape.
func encodeSeriesRowAny(row map[string]any) (key string, valueCol string, scalar float64, triple WelfordTriple, hasScalar bool, hasTriple bool) {
	if len(row) == 0 {
		return "", "", 0, WelfordTriple{}, false, false
	}
	cols := make([]string, 0, len(row))
	for k := range row {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	keyParts := make([]string, 0, len(row))
	hasValue := false
	for _, col := range cols {
		v := row[col]
		if t, ok := v.(WelfordTriple); ok {
			if !hasValue {
				valueCol = col
				triple = t
				hasTriple = true
				hasValue = true
				continue
			}
			keyParts = append(keyParts, col+"="+anyToCanonicalString(v))
			continue
		}
		if num, ok := coerceNumericValue(v); ok {
			if !hasValue {
				valueCol = col
				scalar = num
				hasScalar = true
				hasValue = true
				continue
			}
			keyParts = append(keyParts, col+"="+anyToCanonicalString(v))
			continue
		}
		keyParts = append(keyParts, col+"="+anyToCanonicalString(v))
	}
	if len(keyParts) == 0 {
		return "", valueCol, scalar, triple, hasScalar, hasTriple
	}
	out := keyParts[0]
	for i := 1; i < len(keyParts); i++ {
		out += "|" + keyParts[i]
	}
	return out, valueCol, scalar, triple, hasScalar, hasTriple
}

// seriesRowEntry carries the per-row triple-aware value for the
// ref lookup map used by OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF.
// Exactly one of (HasScalar, HasTriple) is true when the entry is
// addressable; both false means the row had no numeric / triple
// value column.
type seriesRowEntry struct {
	Scalar    float64
	Triple    WelfordTriple
	HasScalar bool
	HasTriple bool
}

// buildSeriesRowLookupAny folds Response.Data rows into a
// `rowKey → seriesRowEntry` lookup that preserves both scalar and
// WelfordTriple value columns. Mirrors buildSeriesRowLookup's
// canonical-key rendering (so scalar-only lookups produce byte-
// identical keys) but retains the triple payload when present so the
// SERIES-arm inferential overlays can detect a triple on the
// reference side and dispatch the appropriate triple / scalar arm
// symmetrically with the target side.
//
// Rows lacking a numeric / triple value column are silently dropped
// (mirrors buildSeriesRowLookup's drop-on-missing-value policy).
func buildSeriesRowLookupAny(rows []map[string]any) map[string]seriesRowEntry {
	out := make(map[string]seriesRowEntry, len(rows))
	if len(rows) == 0 {
		return out
	}
	for _, row := range rows {
		keyStr, _, scalar, triple, hasScalar, hasTriple := encodeSeriesRowAny(row)
		if !hasScalar && !hasTriple {
			continue
		}
		out[keyStr] = seriesRowEntry{
			Scalar:    scalar,
			Triple:    triple,
			HasScalar: hasScalar,
			HasTriple: hasTriple,
		}
	}
	return out
}
