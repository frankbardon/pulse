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
	}
	for _, t := range types.AllAggregationTypes() {
		switch t.MarginReducibility() {
		case types.MarginSummable:
			cap.SummableAggregators = append(cap.SummableAggregators, string(t))
		case types.MarginMeanReducible:
			cap.MeanReducibleAggregators = append(cap.MeanReducibleAggregators, string(t))
		case types.MarginRecompute:
			cap.RecomputeAggregators = append(cap.RecomputeAggregators, string(t))
		}
	}
	sort.Strings(cap.SummableAggregators)
	sort.Strings(cap.MeanReducibleAggregators)
	sort.Strings(cap.RecomputeAggregators)
	return cap
}
