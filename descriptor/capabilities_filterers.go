package descriptor

import "github.com/frankbardon/pulse/types"

// universalFiltererFloorKeys is the {n_in, n_out, n_null_input} triple
// every filterer's component schema declares. The orchestrator's
// universal-floor pass emits these keys unconditionally from the filter
// pass's per-record counter, so embedders can rely on them being
// present even when MetaFilterer returns an empty extra map. Declared
// centrally so the floor stays byte-equal across every entry.
//
// Semantics:
//   - n_in: records that entered the filter pass (post-source, pre-
//     predicate).
//   - n_out: records that passed (entered the downstream stage).
//   - n_null_input: records where the filter input field was null at
//     evaluation time. The filterer's per-operator behaviour on null
//     remains operator-specific (FILTER_NULL is the canonical
//     null-aware operator; FILTER_INCLUDE / FILTER_EXCLUDE treat null
//     as non-matching; FILTER_RANGE treats null as out-of-range); the
//     n_null_input counter is uniform book-keeping for "how many
//     records had no value on the input axis".
//
// Note these names differ from the aggregator floor ({n, n_null}) and
// the grouper floor ({total_n, n_null}) by design — every category
// counts a different surface (aggregator: non-null inputs; grouper:
// post-filter total; filterer: pre-predicate inputs vs. post-predicate
// survivors plus a null-input book-keep).
func universalFiltererFloorKeys() []ComponentKey {
	return []ComponentKey{
		{Name: "n_in", Type: "int", Description: "Records that entered this filter pass."},
		{Name: "n_out", Type: "int", Description: "Records that passed (entered the downstream stage)."},
		{Name: "n_null_input", Type: "int", Description: "Records where the filter input field was null at evaluation time."},
	}
}

// filterSchema composes a ComponentSchema by prepending the universal
// filterer floor to any operator-specific keys (in emission order) and
// tagging the result with the operator's mergeability classification.
// Keep the extra slice nil for floor-only operators (every built-in
// filterer today) so the call site stays clean. Future per-filter
// specifics (e.g. FILTER_RANGE n_below / n_above) append via the
// variadic extra argument without re-opening the floor list.
func filterSchema(merge ComponentsMergeability, extra ...ComponentKey) ComponentSchema {
	floor := universalFiltererFloorKeys()
	keys := make([]ComponentKey, 0, len(floor)+len(extra))
	keys = append(keys, floor...)
	keys = append(keys, extra...)
	return ComponentSchema{Keys: keys, Mergeability: merge}
}

// filtererCapabilities returns the metadata for every registered
// FiltererBuilder.
func filtererCapabilities() []Operator {
	return []Operator{
		{
			Name:            string(types.FILTER_INCLUDE),
			Category:        "filterer",
			Description:     "Keep records whose field value appears in the supplied Values list.",
			AcceptsTypes:    allCohortFieldTypes,
			EmitsTypeNote:   "record-level predicate (no emitted column)",
			Streamable:      true,
			ComponentSchema: filterSchema(Mergeable),
		},
		{
			Name:            string(types.FILTER_EXCLUDE),
			Category:        "filterer",
			Description:     "Drop records whose field value appears in the supplied Values list.",
			AcceptsTypes:    allCohortFieldTypes,
			EmitsTypeNote:   "record-level predicate (no emitted column)",
			Streamable:      true,
			ComponentSchema: filterSchema(Mergeable),
		},
		{
			Name:            string(types.FILTER_RANGE),
			Category:        "filterer",
			Description:     "Keep records whose numeric field value falls within [low, high]; Values=[low, high].",
			AcceptsTypes:    numericFieldTypes,
			EmitsTypeNote:   "record-level predicate",
			Streamable:      true,
			ComponentSchema: filterSchema(Mergeable),
		},
		{
			Name:            string(types.FILTER_EXPRESSION),
			Category:        "filterer",
			Description:     "Keep records for which Expression evaluates truthy; reads any record field.",
			AcceptsTypes:    allCohortFieldTypes,
			EmitsTypeNote:   "record-level predicate",
			Streamable:      true,
			ComponentSchema: filterSchema(Mergeable),
		},
		{
			Name:            string(types.FILTER_NULL),
			Category:        "filterer",
			Description:     "Keep records based on null state of Field. Values=[\"is_null\"] keeps null-valued records; Values=[\"is_not_null\"] keeps non-null records.",
			AcceptsTypes:    allCohortFieldTypes,
			EmitsTypeNote:   "record-level predicate",
			Streamable:      true,
			ComponentSchema: filterSchema(Mergeable),
		},
		{
			Name:            string(types.FILTER_TRUE),
			Category:        "filterer",
			Description:     "Keep records where Field is logically true. Strict mode (default, omit Values) requires packed_bool. Opt into JavaScript-style coercion across any field type with Values=[\"truthy\"].",
			AcceptsTypes:    allCohortFieldTypes,
			EmitsTypeNote:   "record-level predicate",
			Streamable:      true,
			ComponentSchema: filterSchema(Mergeable),
		},
		{
			Name:            string(types.FILTER_FALSE),
			Category:        "filterer",
			Description:     "Keep records where Field is logically false. Strict mode (default, omit Values) requires packed_bool. Opt into JavaScript-style coercion across any field type with Values=[\"truthy\"].",
			AcceptsTypes:    allCohortFieldTypes,
			EmitsTypeNote:   "record-level predicate",
			Streamable:      true,
			ComponentSchema: filterSchema(Mergeable),
		},
		{
			Name:            string(types.FILTER_SET_CONTAINS_ANY),
			Category:        "filterer",
			Description:     "Keep records where the set field shares at least one bit with the supplied Values labels.",
			AcceptsTypes:    setFieldTypes,
			EmitsTypeNote:   "record-level predicate",
			Streamable:      true,
			ComponentSchema: filterSchema(Mergeable),
		},
		{
			Name:            string(types.FILTER_SET_CONTAINS_ALL),
			Category:        "filterer",
			Description:     "Keep records where the set field has every bit in the supplied Values labels set.",
			AcceptsTypes:    setFieldTypes,
			EmitsTypeNote:   "record-level predicate",
			Streamable:      true,
			ComponentSchema: filterSchema(Mergeable),
		},
		{
			Name:            string(types.FILTER_SET_CONTAINS_NONE),
			Category:        "filterer",
			Description:     "Keep records where the set field shares no bits with the supplied Values labels (null rows pass).",
			AcceptsTypes:    setFieldTypes,
			EmitsTypeNote:   "record-level predicate",
			Streamable:      true,
			ComponentSchema: filterSchema(Mergeable),
		},
		{
			Name:            string(types.FILTER_SET_EQUALS),
			Category:        "filterer",
			Description:     "Keep records whose set field mask exactly matches the supplied Values labels.",
			AcceptsTypes:    setFieldTypes,
			EmitsTypeNote:   "record-level predicate",
			Streamable:      true,
			ComponentSchema: filterSchema(Mergeable),
		},
	}
}
