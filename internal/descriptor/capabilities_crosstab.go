package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// crosstabCapability returns the canonical Crosstab capability block for
// the manifest. Deterministic; the aggregator name lists are derived
// from AggregationType.MarginReducibility at build time so a new
// aggregator that opts in (or defaults to recompute) is reflected
// automatically.
func crosstabCapability() descriptor.CrosstabCapability {
	cap := descriptor.CrosstabCapability{
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
			"crosstab requires a cell aggregation — cell, or several measures in cells (PULSE_CROSSTAB_MISSING_CELL)",
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
		SupportsMarginAggregations: true,
		MarginAggregationRules: []string{
			"every margin_aggregations entry needs an aggregation type; null entries are refused (PULSE_CROSSTAB_MARGIN_AGG_INVALID)",
			"margin_aggregations effective labels (label, else TYPE_field) must be unique across the slot and distinct from the cell's (PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL)",
			"auxiliary figures are emitted only where the margin is DISPLAYED (margins.rows / margins.columns / margins.grand), matching the emission rule the cell aggregator's own margin counts and components follow; declaring margin_aggregations on a section that displays no margin computes them into nowhere — advisory warning, not a refusal (PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED) — and a normalize direction does not satisfy it, because the margin it requires is a denominator and an auxiliary is never a denominator",
		},
	}
	cap.SupportsCells = true
	cap.CellsRules = []string{
		"cell and cells are mutually exclusive; a null cells entry, or one with no aggregation type smart defaults can infer, is refused (PULSE_CROSSTAB_CELLS_INVALID)",
		"cells effective labels (label, else TYPE_field) must be unique (PULSE_CROSSTAB_CELLS_DUPLICATE_LABEL), and distinct from every margin_aggregations label (PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL)",
		"each measure runs exactly as a single-cell crosstab would — its own cells, margins, normalize denominators, auxiliary margins and components; nothing is pooled across measures",
		"the matrix row axis gains an innermost MEASURE position (row_header field _measure, type MEASURE): spec row r becomes len(cells) rows, row key + measure label, measures in declaration order; matrix.cell_labels names each row's measure and cell_label is empty",
		"column margins and the grand total are per measure: matrix.measures[m] {label, column_margins, grand_total} and components.crosstab.measures[m]; the single-measure column_margins / grand_total slots stay empty",
		"shape=long emits each measure's rows in declaration order, every row stamped _measure",
		"only OVERLAY_PAIRWISE_* overlays run on a multi-measure crosstab, once per measure (pairs never cross measures); every other request overlay, and any Compose overlay reading such a slot, is refused (PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)",
	}
	cap.RejectionRules = append(cap.RejectionRules,
		"map-valued cell aggregators (AGG_SET_FREQUENCY) cannot pair with normalize=row/column/total (PULSE_CROSSTAB_NORMALIZE_MAP_VALUED)",
		"array-valued aggregators (array_valued_aggregators: AGG_VEC_MEAN, AGG_VEC_SUM, AGG_VEC_SD) are refused as the cell or a margin aggregation, whatever normalize says (PROCESSING_CONFIG, reason array_valued) — unless params.element selects one element (built-in AGG_VEC_* only), which makes the slot a scalar that margins and fuses like its scalar twin (mean_reducible / summable; an AGG_VEC_SD element is recompute, like AGG_STDDEV)")
	for _, t := range types.AllAggregationTypes() {
		// One arm per MarginReducibility class, deliberately with NO
		// default: an unhandled class would fall out of every list
		// silently. TestManifest_CrosstabCapabilityPopulated totals the
		// four lists against len(AllAggregationTypes()) and is what
		// reddens if a fifth class arrives without an arm here.
		switch t.MarginReducibility() {
		case types.MarginSummable:
			cap.SummableAggregators = append(cap.SummableAggregators, string(t))
		case types.MarginMeanReducible:
			cap.MeanReducibleAggregators = append(cap.MeanReducibleAggregators, string(t))
		case types.MarginIndependent:
			cap.IndependentAggregators = append(cap.IndependentAggregators, string(t))
		case types.MarginRecompute:
			cap.RecomputeAggregators = append(cap.RecomputeAggregators, string(t))
		}
		if t.MapValued() {
			cap.MapValuedCellAggregators = append(cap.MapValuedCellAggregators, string(t))
		}
		if t.ArrayValued() {
			cap.ArrayValuedAggregators = append(cap.ArrayValuedAggregators, string(t))
		}
	}
	sort.Strings(cap.SummableAggregators)
	sort.Strings(cap.MeanReducibleAggregators)
	sort.Strings(cap.IndependentAggregators)
	sort.Strings(cap.RecomputeAggregators)
	sort.Strings(cap.MapValuedCellAggregators)
	sort.Strings(cap.ArrayValuedAggregators)
	return cap
}
