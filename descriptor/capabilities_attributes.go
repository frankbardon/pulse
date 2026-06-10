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
		{
			Name:          string(types.ATTR_REG_FITTED),
			Category:      "attribute",
			Description:   "Per-row fitted value ŷᵢ = Xᵢ · β + β₀ from a regression refit during the attribute prepass. Carries its own RegressionSpec-shaped fields (Target, Predictors, Penalty, Alpha, L1Ratio); each ATTR_REG_* attribute refits independently (Option A). Accepts any OLS penalty (unpenalized, ridge, lasso, elasticnet).",
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one fitted value per record (NaN-free)",
			Streamable:    true,
		},
		{
			Name:          string(types.ATTR_REG_RESIDUAL),
			Category:      "attribute",
			Description:   "Per-row residual yᵢ − ŷᵢ from a regression refit during the attribute prepass. Sums to ≈ 0 when the fit includes an intercept (always true for OLS). Accepts any OLS penalty; reuses the same fit machinery as ATTR_REG_FITTED.",
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one residual per record",
			Streamable:    true,
		},
		{
			Name:           string(types.ATTR_REG_LEVERAGE),
			Category:       "attribute",
			Description:    "Per-row hat-matrix diagonal hᵢᵢ = 1/n + (xᵢ − μ_x)ᵀ · M2_xx⁻¹ · (xᵢ − μ_x) from an unpenalized OLS refit. Range ∈ [0, 1]; Σᵢ hᵢᵢ = p + 1. Restricted to unpenalized OLS — penalized leverage / GLM leverage deferred.",
			AcceptsTypes:   numericFieldTypesNoDecimal,
			EmitsType:      "f64",
			EmitsTypeNote:  "one leverage per record in [0, 1]",
			Streamable:     true,
			StreamableHint: "Requires unpenalized OLS only; any non-empty Penalty surfaces PROCESSING_CONFIG.",
		},
		{
			Name:          string(types.ATTR_SET_POPCOUNT),
			Category:      "attribute",
			Description:   "Per-row popcount of a set field — the number of selected labels.",
			AcceptsTypes:  setFieldTypes,
			EmitsType:     "u8",
			EmitsTypeNote: "one small integer per record (0..set width)",
			Streamable:    true,
		},
		{
			Name:        string(types.ATTR_SET_HAS),
			Category:    "attribute",
			Description: "Per-row boolean: whether the configured label's bit is set on the set field.",
			Params: []Param{
				{
					Name:        "label",
					Type:        "string",
					Required:    true,
					Description: "Dictionary label whose bit position is checked per row.",
				},
			},
			AcceptsTypes:  setFieldTypes,
			EmitsType:     "packed_bool",
			EmitsTypeNote: "one 0/1 per record",
			Streamable:    true,
		},
	}
}
