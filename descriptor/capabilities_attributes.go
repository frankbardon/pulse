package descriptor

import "github.com/frankbardon/pulse/types"

// attributeCapabilities returns the metadata for every registered
// AttributeComputer. TestManifestOperatorsComplete enforces coverage of
// types.AllAttributeTypes().
func attributeCapabilities() []Operator {
	return []Operator{
		{
			Name:          string(types.ATTR_ZSCORE),
			Category:      "attribute",
			Description:   "Per-row standardized z-score: (value − mean) / stddev computed via Welford two-pass streaming.",
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one float per record (NaN when stddev=0)",
			Streamable:    true,
		},
		{
			Name:          string(types.ATTR_TSCORE),
			Category:      "attribute",
			Description:   "Per-row T-score: z-score scaled and shifted to mean 50, stddev 10.",
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one float per record",
			Streamable:    true,
		},
		{
			Name:          string(types.ATTR_NORMALIZED),
			Category:      "attribute",
			Description:   "Per-row min-max normalization: (value − min) / (max − min) ∈ [0, 1].",
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one float per record in [0, 1]",
			Streamable:    true,
		},
		{
			Name:          string(types.ATTR_FORMULA),
			Category:      "attribute",
			Description:   "Per-row expression evaluation against the record's fields (e.g. \"price * qty\").",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsType:     "f64",
			EmitsTypeNote: "one float per record (booleans coerce to 0/1)",
			Streamable:    true,
		},
		{
			Name:           string(types.ATTR_PERCENTILE),
			Category:       "attribute",
			Description:    "Per-row percentile rank against the post-filter value set; requires sorting.",
			AcceptsTypes:   numericFieldTypesNoDecimal,
			EmitsType:      "f64",
			EmitsTypeNote:  "one float per record in (0, 100]",
			Streamable:     false,
			StreamableHint: "Use ATTR_NORMALIZED for a streaming-friendly rank proxy.",
		},
		{
			Name:        string(types.ATTR_DATE_PART),
			Category:    "attribute",
			Description: "Extract a calendar component (year, month, day, year_month, year_month_day, month_day) from a date field.",
			Params: []Param{
				{
					Name:        "part",
					Type:        "enum",
					Required:    true,
					Description: "Which calendar component to extract.",
					EnumValues:  []string{"day", "month", "month_day", "year", "year_month", "year_month_day"},
				},
			},
			AcceptsTypes:  []string{"date"},
			EmitsType:     "f64",
			EmitsTypeNote: "encoded integer per record (e.g. year_month=YYYYMM)",
			Streamable:    true,
		},
	}
}
