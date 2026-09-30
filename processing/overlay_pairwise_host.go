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

// --- Distinct-key sample sizes (n_within_distinct) -------------------
//
// n_within_distinct reuses the n_within slab geometry but accumulates a
// DISTINCT-KEY cardinality instead of CellCounts. Two cell aggregators
// carry such a figure and they spell it differently:
//
//	AGG_DISTINCT_SUM   -> "distinct_count" (distinct keys that summed)
//	AGG_DISTINCT_COUNT -> "cardinality"    (distinct non-null values)
//
// AGG_FREQUENCY and AGG_MODE also emit a key literally spelled
// "distinct_count", but theirs counts distinct VALUES OF THE MEASURE
// FIELD — the number of answer codes, not respondents. Reading it as a
// sample size is the silent wrong number this mode exists to remove, so
// the slab accessors below are only ever reached AFTER
// AdmitsDistinctKeyN has identified the cell aggregator. The key probe
// in distinctCellN is a convenience, NOT the safety property: do not
// "simplify" the admission gate away on the grounds that the probe
// already picks a key.

// pairwiseDistinctNKey names, per ADMITTED cell aggregator, the
// component key carrying its distinct-KEY cardinality.
var pairwiseDistinctNKey = map[types.AggregationType]string{
	types.AGG_DISTINCT_SUM:   "distinct_count",
	types.AGG_DISTINCT_COUNT: "cardinality",
}

// PairwiseDistinctNAdmitted returns the admitted cell aggregators in a
// stable order, for diagnostics that must name the admitted set.
func PairwiseDistinctNAdmitted() []types.AggregationType {
	return []types.AggregationType{types.AGG_DISTINCT_SUM, types.AGG_DISTINCT_COUNT}
}

// cellAggregatorIdentitySignatures maps an OPERATOR component key set
// onto the aggregator that emits exactly it. Only the aggregators whose
// ComponentSchema carries "distinct_count" or "cardinality" need an
// entry — anything else cannot be confused for a distinct-key figure
// and classifies as unidentified.
//
// Signatures restate the OPERATOR half of each aggregator's
// ComponentSchema in descriptor/capabilities_aggregators.go, i.e. the
// declared keys MINUS the universal floor {n, n_null} every aggregator
// emits:
//
//	AGG_DISTINCT_COUNT {cardinality}
//	AGG_DISTINCT_SUM   {sum, distinct_count}
//	AGG_FREQUENCY      {distinct_count, mode_value, mode_count}
//	AGG_MODE           {value, count, distinct_count, tie_count}
//
// The restatement exists because the capability table is descriptor's
// and importing it into the runtime path is not worth the edge; the two
// are cross-checked by TestPairwiseCellAggregatorSignaturesMatchCapabilities,
// which reads the same public manifest projection and fails if a
// capability schema moves without this table moving with it. Without
// that gate a renamed component key would silently downgrade every
// admitted cell to "unidentified" — or worse, promote one.
//
// The match is EXACT set equality, not subset containment. A subset
// test admits any aggregator whose keys are a superset of a signature:
// an extension aggregator declaring {sum, distinct_count, ...} would
// classify as AGG_DISTINCT_SUM and have its figure read as a distinct
// respondent count. Exact equality narrows that to an extension
// declaring EXACTLY {sum, distinct_count} — still admitted, and
// accepted as a known residual: closing it needs per-registration
// provenance, which the components block does not carry.
var cellAggregatorIdentitySignatures = []struct {
	agg  types.AggregationType
	keys []string
}{
	{types.AGG_DISTINCT_COUNT, []string{"cardinality"}},
	{types.AGG_DISTINCT_SUM, []string{"sum", "distinct_count"}},
	{types.AGG_FREQUENCY, []string{"distinct_count", "mode_value", "mode_count"}},
	{types.AGG_MODE, []string{"distinct_count", "value", "count", "tie_count"}},
}

// CellAggregatorIdentity classifies the host's CELL aggregator from the
// component key set it emitted. Returns ok=false when components are
// absent, every cell slot is nil, or the key set matches no known
// distinct-bearing aggregator (the safe default: an unidentified cell
// aggregator is never admitted as a distinct-key sample size).
func (h *CrosstabHostView) CellAggregatorIdentity() (types.AggregationType, bool) {
	if h == nil || h.components == nil {
		return "", false
	}
	for _, row := range h.components.CellComponents {
		for _, cell := range row {
			if len(cell) == 0 {
				continue
			}
			for _, sig := range cellAggregatorIdentitySignatures {
				if componentKeysEqual(cell, sig.keys) {
					return sig.agg, true
				}
			}
			return "", false
		}
	}
	return "", false
}

// AdmitsDistinctKeyN reports whether the host's cell aggregator carries
// a distinct-KEY cardinality, returning that aggregator and the
// component key its figure rides on.
func (h *CrosstabHostView) AdmitsDistinctKeyN() (types.AggregationType, string, bool) {
	agg, ok := h.CellAggregatorIdentity()
	if !ok {
		return "", "", false
	}
	key, admitted := pairwiseDistinctNKey[agg]
	if !admitted {
		return agg, "", false
	}
	return agg, key, true
}

// componentFloorKeys are the universal-floor component keys every
// aggregator emits (descriptor's universalAggFloorKeys). They carry no
// operator identity, so the signature match strips them before
// comparing — the signatures restate the OPERATOR half only.
var componentFloorKeys = map[string]bool{"n": true, "n_null": true}

// componentKeysEqual reports whether cell's operator key set — its keys
// MINUS the universal floor — is exactly the set `keys`.
//
// Exact, not subset. A subset test classifies any cell whose keys are a
// SUPERSET of a signature as that aggregator, so an extension
// aggregator emitting {sum, distinct_count, weighted_sum} would be read
// as AGG_DISTINCT_SUM and its "distinct_count" reported as a distinct
// respondent base. That is a silently wrong sample size, which is the
// failure class the distinct-n admission gate exists to remove.
func componentKeysEqual(cell map[string]any, keys []string) bool {
	operatorKeys := 0
	for k := range cell {
		if componentFloorKeys[k] {
			continue
		}
		operatorKeys++
	}
	if operatorKeys != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := cell[k]; !ok {
			return false
		}
	}
	return true
}

// pairwiseDistinctNKeyProbe is the ordered key probe the distinct-n
// readers use once the ADMISSION gate has established the cell
// aggregator is one of the two that carry a distinct-KEY figure. Order
// matches pairwiseDistinctNKey's two entries; it is a convenience on
// top of admission, never a substitute for it.
var pairwiseDistinctNKeyProbe = []string{"distinct_count", "cardinality"}

// distinctCellN reads one cell's distinct-key figure, trying
// "distinct_count" then "cardinality". A nil / absent / non-numeric
// slot contributes ZERO rather than failing the slab — a crosstab cell
// no record reached carries no components at all, and that is a true
// zero, not an unreadable leg.
func (h *CrosstabHostView) distinctCellN(rowIdx, colIdx int) int {
	for _, key := range pairwiseDistinctNKeyProbe {
		if f, ok := h.CellComponentFloat(rowIdx, colIdx, key); ok {
			return int(f)
		}
	}
	return 0
}

// marginDistinctN reads a distinct-key cardinality out of ONE margin
// components map.
//
// The zero-vs-unreadable rule inverts here relative to distinctCellN,
// deliberately. A slab cell no record reached is a true zero and must
// not fail the whole slab; a MARGIN entry that is nil or carries no
// distinct key is not a zero-sized margin, it is a leg that was never
// emitted (components disabled, or the margin display flag off).
// Returning 0 there would hand the test a silently wrong sample size,
// so it returns ok=false and the pair skips with the aggregated
// PULSE_OVERLAY_REF_ZERO warning instead.
func marginDistinctN(comp map[string]any) (int, bool) {
	for _, key := range pairwiseDistinctNKeyProbe {
		v, present := comp[key]
		if !present {
			continue
		}
		f, ok := componentToFloat(v)
		if !ok {
			return 0, false
		}
		return int(f), true
	}
	return 0, false
}

// RowMarginDistinctN is RowMarginN's distinct-key twin: the per-row
// margin read as the cell aggregator's distinct-KEY cardinality out of
// CrosstabComponents.RowMarginComponents[rowIdx].
//
// EXACT BY CONSTRUCTION, and that is the whole point of the mode. The
// row margin recomputes the cell aggregator over the raw records that
// reached the row key — once each, whatever the column axis does — so
// no key is ever counted twice and no partition precondition applies.
// Summing RowSlabDistinctN across a fan-out column axis WOULD
// double-count; this reads the accumulated figure instead.
func (h *CrosstabHostView) RowMarginDistinctN(rowIdx int) (int, bool) {
	if h == nil || h.components == nil ||
		rowIdx < 0 || rowIdx >= len(h.components.RowMarginComponents) {
		return 0, false
	}
	return marginDistinctN(h.components.RowMarginComponents[rowIdx])
}

// ColumnMarginDistinctN is ColumnMarginN's distinct-key twin, reading
// CrosstabComponents.ColumnMarginComponents[colIdx]. Same
// exact-by-construction property as RowMarginDistinctN.
func (h *CrosstabHostView) ColumnMarginDistinctN(colIdx int) (int, bool) {
	if h == nil || h.components == nil ||
		colIdx < 0 || colIdx >= len(h.components.ColumnMarginComponents) {
		return 0, false
	}
	return marginDistinctN(h.components.ColumnMarginComponents[colIdx])
}

// ColumnSlabDistinctN is ColumnSlabN's distinct-key twin: the same
// fixed-prefix walk across columns at a fixed row, summing each cell's
// distinct-key cardinality instead of its record count. Returns
// (0, false) when components / CellComponents are absent or the anchor
// key does not reach `prefix` positions; (sum, true) otherwise.
func (h *CrosstabHostView) ColumnSlabDistinctN(rowIdx, colIdx, prefix int) (int, bool) {
	if h == nil || h.components == nil || len(h.components.CellComponents) == 0 {
		return 0, false
	}
	if rowIdx < 0 || rowIdx >= len(h.components.CellComponents) {
		return 0, false
	}
	anchor := h.columnKey(colIdx)
	if anchor == nil || prefix <= 0 || prefix > len(anchor) {
		return 0, false
	}
	row := h.components.CellComponents[rowIdx]
	total := 0
	for c := 0; c < len(row); c++ {
		k := h.columnKey(c)
		if k == nil || len(k) < prefix {
			continue
		}
		if axisKeyPrefixEqual(anchor, k, prefix) {
			total += h.distinctCellN(rowIdx, c)
		}
	}
	return total, true
}

// RowSlabDistinctN is RowSlabN's distinct-key twin: the same
// fixed-prefix walk across rows at a fixed column, summing each cell's
// distinct-key cardinality instead of its record count.
func (h *CrosstabHostView) RowSlabDistinctN(rowIdx, colIdx, prefix int) (int, bool) {
	if h == nil || h.components == nil || len(h.components.CellComponents) == 0 {
		return 0, false
	}
	anchor := h.rowKey(rowIdx)
	if anchor == nil || prefix <= 0 || prefix > len(anchor) {
		return 0, false
	}
	total := 0
	for rIdx := 0; rIdx < len(h.components.CellComponents); rIdx++ {
		k := h.rowKey(rIdx)
		if k == nil || len(k) < prefix {
			continue
		}
		if !axisKeyPrefixEqual(anchor, k, prefix) {
			continue
		}
		row := h.components.CellComponents[rIdx]
		if colIdx < 0 || colIdx >= len(row) {
			continue
		}
		total += h.distinctCellN(rIdx, colIdx)
	}
	return total, true
}
