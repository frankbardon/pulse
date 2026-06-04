package descriptor

import "github.com/frankbardon/pulse/types"

// grouperCapabilities returns the metadata for every registered
// GrouperFactory.
func grouperCapabilities() []Operator {
	return []Operator{
		{
			Name:          string(types.GROUP_CATEGORY),
			Category:      "grouper",
			Description:   "Partition records by exact field value; ideal for categorical fields.",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "string group key per row",
			Streamable:    true,
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
		},
	}
}
