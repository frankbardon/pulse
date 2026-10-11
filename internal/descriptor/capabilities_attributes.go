package descriptor

import (
	"github.com/frankbardon/pulse/types"

	"github.com/frankbardon/pulse/descriptor"
)

// codeInFieldTypes is what ATTR_CODE_IN accepts: the categorical
// widths (codes are dictionary labels) and the unsigned integers (codes
// are whole numbers). Sorted byte order like every AcceptsTypes list.
var codeInFieldTypes = []string{
	"categorical_u16",
	"categorical_u32",
	"categorical_u8",
	"u16",
	"u32",
	"u4",
	"u64",
	"u8",
}

// attributeCapabilities returns the metadata for every registered
// AttributeComputer. TestManifestOperatorsComplete enforces coverage of
// types.AllAttributeTypes().
func attributeCapabilities() []descriptor.Operator {
	return []descriptor.Operator{
		{
			Name:          string(types.ATTR_ZSCORE),
			Category:      "attribute",
			Description:   "Per-row standardized z-score: (value − mean) / stddev computed via Welford two-pass streaming.",
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one float per record (0 when stddev=0 or the value is null)",
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
			// The formula is Attribute.Expression, outside Params.
			ReadsExpression: true,
		},
		{
			Name:           string(types.ATTR_PERCENTILE),
			Category:       "attribute",
			Description:    "Per-row percentile rank against the post-filter value set; requires sorting.",
			AcceptsTypes:   numericFieldTypesNoDecimal,
			EmitsType:      "f64",
			EmitsTypeNote:  "one float per record in (0, 100]; 0 when the value is null",
			Streamable:     false,
			StreamableHint: "Use ATTR_NORMALIZED for a streaming-friendly rank proxy.",
		},
		{
			Name:        string(types.ATTR_DATE_PART),
			Category:    "attribute",
			Description: "Extract a calendar component (year, month, day, year_month, year_month_day, month_day, hour) from a date or datetime field; a datetime reads the wall clock of its resolved time zone, and hour needs a datetime.",
			Params: []descriptor.Param{
				{
					Name:        "part",
					Type:        "enum",
					Required:    true,
					Description: "Which calendar component to extract.",
					EnumValues:  []string{"day", "hour", "month", "month_day", "year", "year_month", "year_month_day"},
				},
			},
			AcceptsTypes:  []string{"date", "datetime"},
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
			Name:        string(types.ATTR_REG_PREDICT),
			Category:    "attribute",
			Description: "Per-row score from a BOUND model (a ComposedRequest `fits` entry, named by params.fit) instead of a refit: intercept + Σ coefficient·predictor at full precision, the GLM inverse link applied on the response scale. Scores REG_OLS (penalties; under selection the selected features), REG_GLM and REG_BAYES_LINEAR (posterior mean). The model's predictor list is authoritative and read off the slot's own columns (an absent one-hot column is the reference level); a null in any column the score reads yields null. Row-local once bound.",
			Params: []descriptor.Param{
				{
					Name:        "fit",
					Type:        "string",
					Required:    true,
					Description: "Name of a bound model: a `fits` entry of the enclosing compose request.",
				},
				{
					Name:        "terms",
					Type:        "list",
					Description: "Score only these terms (fit predictors; \"(intercept)\" to include the intercept), summed in fit order — a contribution column. Omit to score the whole model.",
				},
				{
					Name:        "scale",
					Type:        "enum",
					Default:     "response",
					EnumValues:  []string{"response", "link"},
					Description: "REG_GLM only: response applies the inverse link (the fit's fitted means); link returns the linear predictor.",
				},
				{
					Name:        "bound",
					Type:        "enum",
					Default:     "point",
					EnumValues:  []string{"point", "lower", "upper"},
					Description: "Coefficient figure the score multiplies: point (the coefficient) or lower / upper (its confidence bound at the fit's conf_level). A non-point bound needs terms to list exactly one predictor — a contribution's interval column, never a prediction interval; a term with no bound scores null.",
				},
			},
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one score per record (null when a column the score reads is null)",
			Streamable:    true,
		},
		{
			Name:         string(types.ATTR_SET_POPCOUNT),
			Category:     "attribute",
			Description:  "Per-row popcount of a set field — the number of selected labels.",
			AcceptsTypes: setFieldTypes,
			// u16, not u8. The popcount of a fully-selected column is the
			// rung's width, and the widest rung is set_u256 — 256, which
			// is one past u8's 255 ceiling. u8 was right while set_u64
			// topped the ladder and became wrong the moment set_u256
			// landed, at exactly one value, only when every member is
			// ticked. Nothing was truncated on the wire — the attribute
			// channel is float64 end to end — but a caller sizing a
			// destination column from the manifest would have built one
			// that cannot hold the answer. The declaration is now checked
			// against a real run by
			// processing.TestManifestEmitsTypeHoldsAtRuntime, which
			// probes a fully-selected column at the widest rung.
			EmitsType:     "u16",
			EmitsTypeNote: "one integer per record: 0..set width inclusive, so 0..256 at the widest rung (set_u256)",
			Streamable:    true,
		},
		{
			Name:        string(types.ATTR_SET_HAS),
			Category:    "attribute",
			Description: "Per-row boolean: whether the configured label's bit is set on the set field.",
			Params: []descriptor.Param{
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
		{
			Name:        string(types.ATTR_CODE_IN),
			Category:    "attribute",
			Description: "Per-row 0/1: whether the field's value is one of the listed codes. Every row stays in the base (a null reads 0), so a mean of the output is the share of the whole base; FILTER_INCLUDE on the same codes drops the other rows instead.",
			Params: []descriptor.Param{
				{
					Name:        "codes",
					Type:        "list",
					Required:    true,
					Description: "Non-empty list of codes (JSON strings or integers; duplicates ignored). On a categorical field each is a dictionary label, and one absent from the dictionary matches nothing; on an integer field each must be a whole number within the field's width.",
				},
			},
			AcceptsTypes:  codeInFieldTypes,
			EmitsType:     "packed_bool",
			EmitsTypeNote: "one 0/1 per record (0 when the value is null)",
			Streamable:    true,
		},
	}
}
