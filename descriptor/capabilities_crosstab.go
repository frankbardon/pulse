package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/types"
)

// CrosstabCapability describes the cross-tabulation endpoint surfaced via
// Request.Crosstab. The manifest carries one CrosstabCapability entry
// under Manifest.Crosstab so LLM clients can detect the section, learn
// the supported normalize / shape values, and route between the matrix
// and long shapes appropriately.
type CrosstabCapability struct {
	// Name is the canonical entry identifier ("crosstab").
	Name string `json:"name"`

	// NormalizeModes lists every valid CrosstabSpec.Normalize value,
	// sorted alphabetically.
	NormalizeModes []string `json:"normalize_modes"`

	// Shapes lists every valid CrosstabSpec.Shape value, sorted
	// alphabetically.
	Shapes []string `json:"shapes"`

	// SummableAggregators is the alphabetized list of aggregator names
	// whose margin equals the sum of cells. v1 recomputes every margin
	// from raw rows for correctness; the classification is exposed so
	// callers can reason about future fast-path optimizations.
	SummableAggregators []string `json:"summable_aggregators"`
	// MeanReducibleAggregators lists aggregators whose margin is
	// derivable when each cell also carries its count.
	MeanReducibleAggregators []string `json:"mean_reducible_aggregators"`
	// RecomputeAggregators lists aggregators whose margin cannot be
	// derived from cells and must be recomputed over the raw filter-
	// passing rows.
	RecomputeAggregators []string `json:"recompute_aggregators"`

	// StreamingExceptions documents the only request shape that may
	// stream through the standard grouped path: shape=long, no
	// margins, no normalization. Intended for LLM-side reasoning.
	StreamingExceptions []string `json:"streaming_exceptions"`

	// RejectionRules names the structural failures the validator
	// emits. Intended for LLM-side autocomplete; not a strict schema.
	RejectionRules []string `json:"rejection_rules"`

	// SupportsNormalizeLevel reports whether the engine honors
	// CrosstabSpec.NormalizeLevel — the depth-index selector that
	// picks a parent grouper as the 100% denominator on nested row /
	// column axes. Always true in v1; carried so clients can detect
	// the feature without inferring it from CLI version.
	SupportsNormalizeLevel bool `json:"supports_normalize_level"`

	// NormalizeLevelRules names the rejection rules specific to the
	// NormalizeLevel slot. Intended for LLM-side autocomplete.
	NormalizeLevelRules []string `json:"normalize_level_rules"`

	// SupportsNormalizeWithin reports whether the engine honors
	// CrosstabSpec.NormalizeWithin — the cross-axis partition
	// selector that fixes a prefix of the OPPOSITE axis (columns
	// when normalize=row, rows when normalize=column) inside the
	// 100% denominator. Composes independently with NormalizeLevel.
	SupportsNormalizeWithin bool `json:"supports_normalize_within"`

	// NormalizeWithinRules names the rejection rules specific to the
	// NormalizeWithin slot.
	NormalizeWithinRules []string `json:"normalize_within_rules"`

	// MapValuedCellAggregators is the alphabetized list of aggregator
	// names whose Crosstab Cell output is map-valued (rich payload
	// per cell, e.g. AGG_SET_FREQUENCY's per-label row counts). These
	// aggregators emit map[string]int into MatrixCell.Value and the
	// long-shape cell rows; normalize modes (row / column / total) are
	// incompatible because dividing one map by another is undefined.
	// Derived from types.AggregationType.MapValued() at build time.
	MapValuedCellAggregators []string `json:"map_valued_cell_aggregators"`
}

// crosstabCapability returns the canonical Crosstab capability block for
// the manifest. Deterministic; the aggregator name lists are derived
// from AggregationType.MarginReducibility at build time so a new
// aggregator that opts in (or defaults to recompute) is reflected
// automatically.
func crosstabCapability() CrosstabCapability {
	cap := CrosstabCapability{
		Name: "crosstab",
		NormalizeModes: []string{
			string(types.CrosstabNormalizeColumn),
			string(types.CrosstabNormalizeNone),
			string(types.CrosstabNormalizeRow),
			string(types.CrosstabNormalizeTotal),
		},
		Shapes: []string{
			string(types.CrosstabShapeLong),
			string(types.CrosstabShapeMatrix),
		},
		StreamingExceptions: []string{
			"shape=long with margins.rows / margins.columns / margins.grand all false and normalize=none streams through the standard grouped path",
		},
		RejectionRules: []string{
			"crosstab requires at least one grouper in rows (PULSE_CROSSTAB_EMPTY_ROWS)",
			"crosstab requires at least one grouper in columns (PULSE_CROSSTAB_EMPTY_COLUMNS)",
			"crosstab requires a cell aggregation (PULSE_CROSSTAB_MISSING_CELL)",
			"crosstab cannot coexist with top-level groups or aggregations (PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS)",
			"matrix shape, any margin, or any normalization forces the buffered path",
		},
		SupportsNormalizeLevel: true,
		NormalizeLevelRules: []string{
			"normalize_level must be in [0, len(axis)-1] for the axis selected by normalize (PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE)",
			"normalize_level requires normalize to be row or column (PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS)",
			"normalize_level cannot be combined with normalize=total (PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE)",
		},
		SupportsNormalizeWithin: true,
		NormalizeWithinRules: []string{
			"normalize_within must be in [0, len(other-axis)-1] where other-axis is columns when normalize=row and rows when normalize=column (PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE)",
			"normalize_within requires normalize to be row or column (PULSE_CROSSTAB_NORMALIZE_WITHIN_WITHOUT_AXIS)",
			"normalize_within cannot be combined with normalize=total (PULSE_CROSSTAB_NORMALIZE_WITHIN_INCOMPATIBLE)",
		},
	}
	cap.RejectionRules = append(cap.RejectionRules,
		"map-valued cell aggregators (AGG_SET_FREQUENCY) cannot pair with normalize=row/column/total (PULSE_CROSSTAB_NORMALIZE_MAP_VALUED)")
	for _, t := range types.AllAggregationTypes() {
		switch t.MarginReducibility() {
		case types.MarginSummable:
			cap.SummableAggregators = append(cap.SummableAggregators, string(t))
		case types.MarginMeanReducible:
			cap.MeanReducibleAggregators = append(cap.MeanReducibleAggregators, string(t))
		case types.MarginRecompute:
			cap.RecomputeAggregators = append(cap.RecomputeAggregators, string(t))
		}
		if t.MapValued() {
			cap.MapValuedCellAggregators = append(cap.MapValuedCellAggregators, string(t))
		}
	}
	sort.Strings(cap.SummableAggregators)
	sort.Strings(cap.MeanReducibleAggregators)
	sort.Strings(cap.RecomputeAggregators)
	sort.Strings(cap.MapValuedCellAggregators)
	return cap
}
