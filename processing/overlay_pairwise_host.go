package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/types"
)

// This file extends CrosstabHostView with the component-reading
// accessors the OVERLAY_PAIRWISE_* family needs: per-cell universal-floor
// counters (n, weight sums), Welford triples ({mean, variance, n}), margin
// record counts, and within-group slab sums for the n_within denominator
// mode. Payload-only overlays (share / index / χ² / Fisher) never call
// these — they read cells + margins straight off the MatrixPayload.
//
// Every accessor is nil-safe and returns ok=false when components were
// disabled or the requested slot/key is absent, so handlers can surface
// PULSE_OVERLAY_COMPONENTS_REQUIRED / per-cell skip warnings instead of
// dividing by a zero sample size.

// HasComponents reports whether the host carries a components block.
// Component-reading handlers gate on this once up front and bail with
// PULSE_OVERLAY_COMPONENTS_REQUIRED when false.
func (h *CrosstabHostView) HasComponents() bool {
	return h != nil && h.components != nil
}

// Components returns the underlying CrosstabComponents pointer (nil when
// components were disabled). Read-only.
func (h *CrosstabHostView) Components() *types.CrosstabComponents {
	if h == nil {
		return nil
	}
	return h.components
}

// CellComponentFloat reads a numeric key from CellComponents[rowIdx][colIdx].
// Returns (0, false) when components are absent or the slot/key is missing
// or non-numeric.
func (h *CrosstabHostView) CellComponentFloat(rowIdx, colIdx int, key string) (float64, bool) {
	if h == nil || h.components == nil || len(h.components.CellComponents) == 0 {
		return 0, false
	}
	if rowIdx < 0 || rowIdx >= len(h.components.CellComponents) {
		return 0, false
	}
	row := h.components.CellComponents[rowIdx]
	if colIdx < 0 || colIdx >= len(row) || row[colIdx] == nil {
		return 0, false
	}
	v, ok := row[colIdx][key]
	if !ok {
		return 0, false
	}
	return componentToFloat(v)
}

// CellN reads the universal-floor "n" counter from CellComponents[r][c].
func (h *CrosstabHostView) CellN(rowIdx, colIdx int) (int, bool) {
	f, ok := h.CellComponentFloat(rowIdx, colIdx, "n")
	if !ok {
		return 0, false
	}
	return int(f), true
}

// CellWeightSum reads the "sum_weights" key from CellComponents[r][c]
// (emitted by AGG_WEIGHTED_MEAN). Used as the prop-Z sample-size leg when
// the cell aggregator is weighted so n matches the weighted denominator.
func (h *CrosstabHostView) CellWeightSum(rowIdx, colIdx int) (float64, bool) {
	return h.CellComponentFloat(rowIdx, colIdx, "sum_weights")
}

// WelfordTriple reads {mean, variance, n} from CellComponents[r][c].
// Used by OVERLAY_PAIRWISE_WELCH_T and OVERLAY_PAIRWISE_TWO_MEANS_Z.
// Returns ok=false unless all three keys are present and numeric.
func (h *CrosstabHostView) WelfordTriple(rowIdx, colIdx int) (mean, variance float64, n int, ok bool) {
	m, mok := h.CellComponentFloat(rowIdx, colIdx, "mean")
	v, vok := h.CellComponentFloat(rowIdx, colIdx, "variance")
	nf, nok := h.CellComponentFloat(rowIdx, colIdx, "n")
	if !mok || !vok || !nok {
		return 0, 0, 0, false
	}
	return m, v, int(nf), true
}

// HasWelfordCells reports whether at least one present cell carries a full
// {mean, variance, n} triple. The Welford-input handlers gate on this so a
// matrix whose cell aggregator is not AGG_WELFORD fails fast with a shape
// error instead of skipping every pair.
func (h *CrosstabHostView) HasWelfordCells() bool {
	if h == nil || h.components == nil {
		return false
	}
	for _, row := range h.components.CellComponents {
		for _, cell := range row {
			if cell == nil {
				continue
			}
			_, hasM := cell["mean"]
			_, hasV := cell["variance"]
			_, hasN := cell["n"]
			if hasM && hasV && hasN {
				return true
			}
		}
	}
	return false
}

// RowMarginN reads the per-row margin record count from
// CrosstabComponents.RowMarginCounts.
func (h *CrosstabHostView) RowMarginN(rowIdx int) (int, bool) {
	if h == nil || h.components == nil ||
		rowIdx < 0 || rowIdx >= len(h.components.RowMarginCounts) {
		return 0, false
	}
	return h.components.RowMarginCounts[rowIdx], true
}

// ColumnMarginN reads the per-column margin record count from
// CrosstabComponents.ColumnMarginCounts.
func (h *CrosstabHostView) ColumnMarginN(colIdx int) (int, bool) {
	if h == nil || h.components == nil ||
		colIdx < 0 || colIdx >= len(h.components.ColumnMarginCounts) {
		return 0, false
	}
	return h.components.ColumnMarginCounts[colIdx], true
}

// ColumnSlabN sums CellCounts at (rowIdx, c') over every column c' whose
// ColumnKey agrees with colIdx on the first `prefix` dim positions — the
// fixed-prefix subgroup that is the denominator under normalize=row when
// NormalizeWithin = prefix-1 (Pulse-W convention). Returns (0, false) when
// components/CellCounts are absent; (sum, true) otherwise (sum may be 0).
func (h *CrosstabHostView) ColumnSlabN(rowIdx, colIdx, prefix int) (int, bool) {
	if h == nil || h.components == nil || len(h.components.CellCounts) == 0 {
		return 0, false
	}
	if rowIdx < 0 || rowIdx >= len(h.components.CellCounts) {
		return 0, false
	}
	anchor := h.columnKey(colIdx)
	if anchor == nil || prefix <= 0 || prefix > len(anchor) {
		return 0, false
	}
	row := h.components.CellCounts[rowIdx]
	total := 0
	for c := 0; c < len(row); c++ {
		k := h.columnKey(c)
		if k == nil || len(k) < prefix {
			continue
		}
		if axisKeyPrefixEqual(anchor, k, prefix) {
			total += row[c]
		}
	}
	return total, true
}

// RowSlabN is the row-axis analog of ColumnSlabN: sums CellCounts at
// (r', colIdx) over rows r' whose RowKey matches rowIdx's on the first
// `prefix` dim positions. Used by row-axis pairwise specs with n_within.
func (h *CrosstabHostView) RowSlabN(rowIdx, colIdx, prefix int) (int, bool) {
	if h == nil || h.components == nil || len(h.components.CellCounts) == 0 {
		return 0, false
	}
	anchor := h.rowKey(rowIdx)
	if anchor == nil || prefix <= 0 || prefix > len(anchor) {
		return 0, false
	}
	total := 0
	for rIdx := 0; rIdx < len(h.components.CellCounts); rIdx++ {
		k := h.rowKey(rIdx)
		if k == nil || len(k) < prefix {
			continue
		}
		if !axisKeyPrefixEqual(anchor, k, prefix) {
			continue
		}
		row := h.components.CellCounts[rIdx]
		if colIdx < 0 || colIdx >= len(row) {
			continue
		}
		total += row[colIdx]
	}
	return total, true
}

// rowKey / columnKey return the raw axis-key tuple at an index, or nil
// when out of range / payload absent. PairAlongDim bucketing and the
// n_within slab sums need positional dim access, not the joined label.
func (h *CrosstabHostView) rowKey(rowIdx int) types.AxisKey {
	if h == nil || h.payload == nil || rowIdx < 0 || rowIdx >= len(h.payload.RowKeys) {
		return nil
	}
	return h.payload.RowKeys[rowIdx]
}

func (h *CrosstabHostView) columnKey(colIdx int) types.AxisKey {
	if h == nil || h.payload == nil || colIdx < 0 || colIdx >= len(h.payload.ColumnKeys) {
		return nil
	}
	return h.payload.ColumnKeys[colIdx]
}

// axisKeyPrefixEqual reports whether a and b agree on the first `prefix`
// positions. Values compare via fmt.Sprintf so int / int64 / string
// round-trip variants collapse to the same string.
func axisKeyPrefixEqual(a, b types.AxisKey, prefix int) bool {
	if len(a) < prefix || len(b) < prefix {
		return false
	}
	for i := 0; i < prefix; i++ {
		if fmt.Sprintf("%v", a[i]) != fmt.Sprintf("%v", b[i]) {
			return false
		}
	}
	return true
}

// axisKeyEqualExceptDim reports whether a and b agree on every position
// EXCEPT the given dim index. PairAlongDim bucketing groups pair-axis
// indexes whose tuples differ only at one specified position.
func axisKeyEqualExceptDim(a, b types.AxisKey, dim int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if i == dim {
			continue
		}
		if fmt.Sprintf("%v", a[i]) != fmt.Sprintf("%v", b[i]) {
			return false
		}
	}
	return true
}

// componentToFloat coerces a generic CellComponents map value into
// float64. Components ride as int / int64 / float64 from the aggregator
// side and may arrive as float64 after a JSON round-trip; both collapse
// here. Returns ok=false for non-numeric values.
func componentToFloat(v any) (float64, bool) {
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
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}
