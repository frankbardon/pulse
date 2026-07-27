package descriptor

import "github.com/frankbardon/pulse/types"

// universalGrouperFloorKeys is the {total_n, n_null} pair every grouper's
// component schema declares. The orchestrator's universal-floor pass
// emits these keys unconditionally so embedders can rely on them being
// present even when MetaGrouper returns an empty extra map. Declared
// centrally so the floor stays byte-equal across every entry.
//
// Note these names differ from the aggregator floor ({n, n_null}) by
// design — groupers partition all post-filter records (`total_n`), not
// just non-null inputs, and the null-bucket count (`n_null`) is the
// records that landed in the null/skip path during partition.
func universalGrouperFloorKeys() []ComponentKey {
	return []ComponentKey{
		{Name: "total_n", Type: "int", Description: "Total records partitioned across all buckets (post-filter)."},
		{Name: "n_null", Type: "int", Description: "Records that landed in the null/skip path (no bucket assignment)."},
	}
}

// groupSchema composes a ComponentSchema by prepending the universal
// grouper floor to the operator-specific keys (in emission order) and
// tagging the result with the operator's mergeability classification.
// Keep the extra slice nil for floor-only operators so the call site
// stays clean.
func groupSchema(merge ComponentsMergeability, extra ...ComponentKey) ComponentSchema {
	floor := universalGrouperFloorKeys()
	keys := make([]ComponentKey, 0, len(floor)+len(extra))
	keys = append(keys, floor...)
	keys = append(keys, extra...)
	return ComponentSchema{Keys: keys, Mergeability: merge}
}

// grouperCapabilities returns the metadata for every registered
// GrouperFactory.
//
// Group.Include ordering note: for the three groupers that honour
// Group.Include (GROUP_CATEGORY on the label string, GROUP_SET_VALUE on
// the pipe-joined composite key, GROUP_SET_PER_ELEMENT on each individual
// fan-out label) a non-empty Include is order-significant — output buckets
// emit in the order values are listed in Include, across both the plain
// grouped payload (Response.Data + Response.Components buckets) and each
// crosstab axis independently (an axis without Include keeps the prior
// alphabetical / dict-index order). An empty or absent Include preserves
// the prior alphabetical (dict-index for GROUP_SET_PER_ELEMENT) order and
// is byte-identical to the pre-Include output. Include values that match
// zero records are still dropped, never emitted as empty buckets. This is
// a runtime emission-ordering property only — no ComponentSchema key,
// capability shape, or manifest field changes, so the descriptor surface
// (and its goldens) is unaffected.
func grouperCapabilities() []Operator {
	return []Operator{
		{
			Name:          string(types.GROUP_CATEGORY),
			Category:      "grouper",
			Description:   "Partition records by exact field value; ideal for categorical fields.",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "string group key per row",
			Streamable:    true,
			ComponentSchema: groupSchema(Mergeable,
				ComponentKey{Name: "dict_size", Type: "int", Description: "Number of distinct values observed across all buckets (cardinality of the partition key)."},
				ComponentKey{Name: "buckets", Type: "[]bucket", Description: "Per-bucket records, ordered by emission; each entry carries {key, label, count}."},
			),
		},
		{
			Name:        string(types.GROUP_DATE),
			Category:    "grouper",
			Description: "Partition date-typed records by a calendar component (year, quarter, month, week, day, day_of_week). Optional fiscal_offset shifts year/quarter bucketing onto a fiscal calendar with end-year labels (FY-prefixed keys).",
			Params: []Param{
				{
					Name:        "component",
					Type:        "enum",
					Required:    false,
					Default:     "month",
					Description: "Calendar component to bucket by.",
					EnumValues:  []string{"day", "day_of_week", "month", "quarter", "week", "year"},
				},
				{
					Name:        "fiscal_offset",
					Type:        "int",
					Required:    false,
					Default:     0,
					Description: "Months after January when the fiscal year starts; valid only with component=year or component=quarter. Range [-11, 11]; offsets normalise mod 12 (offset=-3 == offset=9 => October start). 0 (default) = calendar year. Non-zero offsets prefix keys with FY using the end-year convention (Apr 2024-Mar 2025 with offset=3 => FY2025).",
				},
			},
			AcceptsTypes:   []string{"date"},
			EmitsTypeNote:  "string group key per row (e.g. 2024-Q1, 2024-01, FY2025-Q1)",
			Streamable:     false,
			StreamableHint: "Use GROUP_CATEGORY on an ATTR_DATE_PART output column for a streaming-friendly substitute.",
			ComponentSchema: groupSchema(Mergeable,
				ComponentKey{Name: "granularity", Type: "string", Description: "Calendar component used to bucket (day, day_of_week, week, month, quarter, year)."},
				ComponentKey{Name: "range_start", Type: "string", Description: "ISO date string of the earliest period observed."},
				ComponentKey{Name: "range_end", Type: "string", Description: "ISO date string of the latest period observed."},
				ComponentKey{Name: "n_buckets", Type: "int", Description: "Number of distinct calendar buckets observed."},
				ComponentKey{Name: "buckets", Type: "[]bucket", Description: "Per-bucket records, ordered by emission; each entry carries {key, period_start, period_end, count}."},
			),
		},
		{
			Name:        string(types.GROUP_DATE_RANGES),
			Category:    "grouper",
			Description: "Partition date-typed records by a set of inline labeled date ranges ({label, start, end}); the bucket key is the matching range's label. Out-of-range rows land in a configurable unmatched bucket (default label \"unmatched\"). Buckets emit in supplied range order.",
			Params: []Param{
				{
					Name:        "ranges",
					Type:        "array",
					Required:    true,
					Description: "Ordered list of {label, start, end} labeled date ranges. start/end are ISO date literals; an omitted/empty bound is open. Bounds are inclusive; ranges must be non-overlapping with distinct labels.",
				},
				{
					Name:        "unmatched_label",
					Type:        "string",
					Required:    false,
					Default:     "unmatched",
					Description: "Bucket label for rows outside every configured range. Must not collide with a range label.",
				},
			},
			AcceptsTypes:  []string{"date"},
			EmitsTypeNote: "labeled range key per row (the matching range's label, or the unmatched label)",
			Streamable:    true,
			ComponentSchema: groupSchema(Mergeable,
				ComponentKey{Name: "n_ranges", Type: "int", Description: "Number of configured labeled date ranges."},
				ComponentKey{Name: "unmatched_label", Type: "string", Description: "Bucket label used for rows outside every configured range."},
				ComponentKey{Name: "buckets", Type: "[]bucket", Description: "Per-bucket records in supplied range order (unmatched last when observed); each entry carries {key, label, count}."},
			),
		},
		{
			Name:        string(types.GROUP_QUANTILE),
			Category:    "grouper",
			Description: "Partition records into N equal-population quantile buckets (Q1..Q4 / D1..D10 / P1..P100).",
			Params: []Param{
				{
					Name:        "interval",
					Type:        "int",
					Required:    false,
					Default:     4,
					Description: "Number of buckets. Conventional values 4 (quartiles), 10 (deciles), 100 (percentiles); set on the request's Group.Interval field, not in Params.",
				},
			},
			AcceptsTypes:   numericFieldTypesNoDecimal,
			EmitsTypeNote:  "bucket label per row (Qk / Dk / Pk)",
			Streamable:     false,
			StreamableHint: "Quantile cutoffs need the sorted value set; use GROUP_RANGE or GROUP_ROUNDED with known bounds for streaming.",
			ComponentSchema: groupSchema(None,
				ComponentKey{Name: "n_quantiles", Type: "int", Description: "Number of equal-population quantile buckets requested (Group.Interval)."},
				ComponentKey{Name: "method", Type: "string", Description: "Quantile interpolation method (e.g. \"linear\")."},
				ComponentKey{Name: "edges", Type: "[]float64", Description: "Sorted cutpoints separating adjacent quantile buckets."},
				ComponentKey{Name: "buckets", Type: "[]bucket", Description: "Per-bucket records, ordered by emission; each entry carries {key, low, high, count}."},
			),
		},
		{
			Name:        string(types.GROUP_RANGE),
			Category:    "grouper",
			Description: "Partition numeric records into half-open ranges [a, b); Interval controls the bucket width.",
			Params: []Param{
				{
					Name:        "interval",
					Type:        "float",
					Required:    true,
					Description: "Bucket width on the value axis; set on the request's Group.Interval field.",
				},
			},
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsTypeNote: "string bucket label per row (e.g. \"[10, 20)\")",
			Streamable:    true,
			ComponentSchema: groupSchema(Mergeable,
				ComponentKey{Name: "interval", Type: "float64", Description: "Bucket width on the value axis (Group.Interval)."},
				ComponentKey{Name: "range_min", Type: "float64", Description: "Smallest non-null value observed across all buckets."},
				ComponentKey{Name: "range_max", Type: "float64", Description: "Largest non-null value observed across all buckets."},
				ComponentKey{Name: "n_buckets", Type: "int", Description: "Number of half-open range buckets emitted."},
				ComponentKey{Name: "edges", Type: "[]float64", Description: "Sorted cutpoints separating adjacent range buckets."},
				ComponentKey{Name: "buckets", Type: "[]bucket", Description: "Per-bucket records, ordered by emission; each entry carries {key, low, high, count}."},
				ComponentKey{Name: "underflow_count", Type: "int", Description: "Records below the lowest configured bucket edge."},
				ComponentKey{Name: "overflow_count", Type: "int", Description: "Records at or above the highest configured bucket edge."},
			),
		},
		{
			Name:        string(types.GROUP_ROUNDED),
			Category:    "grouper",
			Description: "Round each numeric value to the nearest multiple of Interval and group by the rounded scalar.",
			Params: []Param{
				{
					Name:        "interval",
					Type:        "float",
					Required:    true,
					Description: "Rounding increment; set on the request's Group.Interval field.",
				},
			},
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsTypeNote: "rounded numeric key per row",
			Streamable:    true,
			ComponentSchema: groupSchema(Mergeable,
				ComponentKey{Name: "precision", Type: "float64", Description: "Rounding increment used as the bucket scalar (Group.Interval)."},
				ComponentKey{Name: "edges", Type: "[]float64", Description: "Sorted rounded scalars separating adjacent buckets."},
				ComponentKey{Name: "buckets", Type: "[]bucket", Description: "Per-bucket records, ordered by emission; each entry carries {key, low, high, count}."},
			),
		},
		{
			Name:          string(types.GROUP_SET_VALUE),
			Category:      "grouper",
			Description:   "Partition rows by the exact set mask (one bucket per unique combination). Bucket key = sorted label list joined with \"|\".",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "string bucket key per row (e.g. \"AMEX|VISA\")",
			Streamable:    true,
			ComponentSchema: groupSchema(Mergeable,
				ComponentKey{Name: "n_empty_mask", Type: "int", Description: "Records whose set mask was the empty selection (zero-bit mask is a valid distinct bucket from null)."},
				ComponentKey{Name: "buckets", Type: "[]bucket", Description: "Per-bucket records, ordered by emission; each entry carries {key, mask, count, labels}."},
			),
		},
		{
			Name:          string(types.GROUP_SET_PER_ELEMENT),
			Category:      "grouper",
			Description:   "Fan each row into one bucket per selected label (multi-key). Cardinality multiplies with set popcount. Implements MultiKeyStreamingGrouper.",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "string bucket key per selected label per row",
			Streamable:    true,
			ComponentSchema: groupSchema(Mergeable,
				ComponentKey{Name: "total_label_observations", Type: "int", Description: "Sum of buckets[].count across the partition; may exceed total_n because each row fans into one bucket per selected label."},
				ComponentKey{Name: "buckets", Type: "[]bucket", Description: "Per-label records, ordered by emission; each entry carries {key, label, count, dict_index}."},
			),
		},
	}
}
