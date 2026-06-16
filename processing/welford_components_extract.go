// Helpers for components-source consumption by the four stat-test
// parity overlay handlers — OVERLAY_T_CELL / OVERLAY_Z_CELL (MATRIX
// arm) and OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF (SERIES arm).
//
// Handlers read `{mean, variance, n}` from the universal Components
// surface — the canonical source of stat-test inputs for the parity
// overlays.
//
//   - MATRIX arm: extractCellComponentsTriple pulls the triple from
//     Response.Components.Crosstab.CellComponents[r][c]. Maps without
//     all three keys fall through to (0, 0, 0, false) so the scalar +
//     Params fallback path engages.
//
//   - SERIES arm: encodeSeriesRowAnyMap / buildSeriesRowLookupAnyMap
//     mirror the row-encoding identity rule of encodeSeriesRowAny but
//     detect a `map[string]any` value column carrying `{mean, variance,
//     n}` keys. Same canonical key rendering — scalar-only rows
//     produce byte-identical keys so the additive contract is
//     preserved.
package processing

import (
	"sort"

	"github.com/frankbardon/pulse/types"
)

// extractCellComponentsTriple reads `(mean, variance, n)` from the
// per-cell components map at Response.Components.Crosstab.CellComponents
// [r][c]. Returns (0, 0, 0, false) when the response carries no
// Components.Crosstab.CellComponents matrix, the (r, c) coordinate is
// out of range, the entry is nil, or any of the `mean` / `variance` /
// `n` keys are missing or non-numeric.
//
// Callers route a (false, false) outcome through the scalar + Params
// fallback path the legacy WelfordTriple consumer used for the
// scalar-cell case.
func extractCellComponentsTriple(resp *types.Response, r, c int) (mean, variance, n float64, ok bool) {
	if resp == nil || resp.Components == nil || resp.Components.Crosstab == nil {
		return 0, 0, 0, false
	}
	matrix := resp.Components.Crosstab.CellComponents
	if r < 0 || r >= len(matrix) {
		return 0, 0, 0, false
	}
	row := matrix[r]
	if c < 0 || c >= len(row) {
		return 0, 0, 0, false
	}
	cell := row[c]
	if cell == nil {
		return 0, 0, 0, false
	}
	meanV, meanOK := componentsNumeric(cell, "mean")
	varV, varOK := componentsNumeric(cell, "variance")
	nV, nOK := componentsNumeric(cell, "n")
	if !meanOK || !varOK || !nOK {
		return 0, 0, 0, false
	}
	return meanV, varV, nV, true
}

// componentsNumeric coerces a components-map value to float64. Accepts
// the JSON-marshalable numeric shapes (int, int64, uint64, float64,
// float32) the operator emits — mirrors coerceNumericValue's coverage
// minus the string parsing arm (component values are typed at emit time
// and never round-trip through string encoding before the handler
// reads them).
func componentsNumeric(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}

// seriesRowComponents carries the per-row triple-aware value for the
// ref lookup map used by the migrated OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF
// SERIES handlers. Exactly one of (HasScalar, HasTriple) is true when
// the entry is addressable; both false means the row had no numeric /
// map-shaped value column.
type seriesRowComponents struct {
	Scalar    float64
	Mean      float64
	Variance  float64
	N         float64
	HasScalar bool
	HasTriple bool
}

// encodeSeriesRowAnyMap is the components-source sibling of
// encodeSeriesRowAny. Mirrors the canonical-key rendering rule (sort
// columns; first numeric-or-triple-map column wins as the value column;
// remaining columns fold into the key) so a scalar-only row produces a
// key BYTE-IDENTICAL to encodeSeriesRowAny's output — the additive
// contract for the scalar fallback path on OVERLAY_T_VS_REF /
// OVERLAY_Z_VS_REF requires this identity AND the pre-Welford-extract series
// encoding identity.
//
// A row whose value column carries a `map[string]any` with `{mean,
// variance, n}` keys sets hasTriple=true (with scalar zero,
// hasScalar=false); a row whose value column is a numeric scalar sets
// hasScalar=true; an empty row returns both flags false. Maps lacking
// any of the three triple keys fall through to the next column (mirrors
// encodeSeriesRowAny's per-column probe order).
func encodeSeriesRowAnyMap(row map[string]any) (key string, valueCol string, scalar float64, mean float64, variance float64, n float64, hasScalar bool, hasTriple bool) {
	if len(row) == 0 {
		return "", "", 0, 0, 0, 0, false, false
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
		if m, tMean, tVar, tN, mapOK := tripleProbe(v); mapOK {
			if !hasValue {
				valueCol = col
				mean = tMean
				variance = tVar
				n = tN
				hasTriple = true
				hasValue = true
				_ = m
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
		return "", valueCol, scalar, mean, variance, n, hasScalar, hasTriple
	}
	out := keyParts[0]
	for i := 1; i < len(keyParts); i++ {
		out += "|" + keyParts[i]
	}
	return out, valueCol, scalar, mean, variance, n, hasScalar, hasTriple
}

// tripleProbe checks whether an `any` is a `map[string]any` carrying
// the `{mean, variance, n}` key set. Returns the underlying map plus
// the extracted triple when so, or nil/false otherwise.
func tripleProbe(v any) (map[string]any, float64, float64, float64, bool) {
	m, isMap := v.(map[string]any)
	if !isMap {
		return nil, 0, 0, 0, false
	}
	meanV, meanOK := componentsNumeric(m, "mean")
	if !meanOK {
		return m, 0, 0, 0, false
	}
	varV, varOK := componentsNumeric(m, "variance")
	if !varOK {
		return m, 0, 0, 0, false
	}
	nV, nOK := componentsNumeric(m, "n")
	if !nOK {
		return m, 0, 0, 0, false
	}
	return m, meanV, varV, nV, true
}

// matrixCellCoord is the (r, c) integer-pair returned by
// buildMatrixCellCoordLookup so the migrated MATRIX-shape parity
// overlay handlers can recover the reference response's CellComponents
// index from its (row_key, col_key) tuple. Same shape as the existing
// matrixCellLookupKey but the value carries the index pair, not the
// cell payload.
type matrixCellCoord struct {
	Row int
	Col int
}

// buildMatrixCellCoordLookup folds a MatrixPayload's RowKeys / ColumnKeys
// into a `(rowKey, colKey) → matrixCellCoord` map keyed by string-form
// axis keys. Used by OVERLAY_T_CELL / OVERLAY_Z_CELL: the handlers
// look up the reference response's CellComponents index from the
// matching key pair on the target side, then pull the triple out of
// Response.Components.Crosstab.CellComponents[r][c] directly.
//
// Mirrors buildMatrixCellLookup's axis-key rendering rule
// (axisKeyToString) so a target row key string keys into the same
// reference slot the legacy lookup found.
func buildMatrixCellCoordLookup(mx *types.MatrixPayload) map[matrixCellLookupKey]matrixCellCoord {
	if mx == nil {
		return map[matrixCellLookupKey]matrixCellCoord{}
	}
	out := make(map[matrixCellLookupKey]matrixCellCoord, len(mx.RowKeys)*len(mx.ColumnKeys))
	for i, rk := range mx.RowKeys {
		rs := axisKeyToString(rk)
		for j, ck := range mx.ColumnKeys {
			cs := axisKeyToString(ck)
			out[matrixCellLookupKey{row: rs, col: cs}] = matrixCellCoord{Row: i, Col: j}
		}
	}
	return out
}

// buildSeriesRowLookupAnyMap folds Response.Data rows into a
// `rowKey → seriesRowComponents` lookup that preserves both scalar and
// map-shaped triple value columns. Mirrors buildSeriesRowLookupAny's
// canonical-key rendering (so scalar-only lookups produce byte-
// identical keys) but reads the components-source map shape instead of
// the legacy typed WelfordTriple. Rows lacking a numeric / triple-map
// value column are silently dropped (mirrors buildSeriesRowLookup's
// drop-on-missing-value policy).
func buildSeriesRowLookupAnyMap(rows []map[string]any) map[string]seriesRowComponents {
	out := make(map[string]seriesRowComponents, len(rows))
	if len(rows) == 0 {
		return out
	}
	for _, row := range rows {
		keyStr, _, scalar, mean, variance, n, hasScalar, hasTriple := encodeSeriesRowAnyMap(row)
		if !hasScalar && !hasTriple {
			continue
		}
		out[keyStr] = seriesRowComponents{
			Scalar:    scalar,
			Mean:      mean,
			Variance:  variance,
			N:         n,
			HasScalar: hasScalar,
			HasTriple: hasTriple,
		}
	}
	return out
}
