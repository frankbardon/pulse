package descriptor

import (
	"github.com/frankbardon/pulse/internal/vectors"
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
		{
			Name:        string(types.ATTR_MAHALANOBIS),
			Category:    "attribute",
			Description: "Per-row Mahalanobis distance D² = (x − μ)ᵀS⁻¹(x − μ) across the p members of vector / fields (exactly one; listwise), from a cohort-wide fit: the (weighted) mean and ddof = 1 covariance of the filtered rows params.fit_on admits. Every filtered row is scored; a null member scores null; a singular fit or N* ≤ p nulls every score with a warning. Two-pass.",
			Params: []descriptor.Param{
				{
					Name:        "output",
					Type:        "enum",
					Default:     "d2",
					EnumValues:  []string{"d2", "p"},
					Description: "d2: the squared distance; p: its chi-square upper tail on p degrees of freedom (the outlier p-value under joint normality).",
				},
				{
					Name:        "fit_on",
					Type:        "any",
					Default:     "filtered",
					Description: "Fit population: \"filtered\" (every filtered row) or an inline array of filterer objects selecting a subset of the filtered rows; every filtered row is still scored.",
				},
				{
					Name:        "missing",
					Type:        "enum",
					Default:     "listwise",
					EnumValues:  []string{"listwise"},
					Description: "Missing-data mode; only listwise (a row with any null member is left out of the fit and scores null).",
				},
			},
			AcceptsTypes:   numericFieldTypesNoDecimal,
			EmitsType:      "f64",
			EmitsTypeNote:  "one D² (≥ 0) or p-value in [0, 1] per record (null when a member is null or the fit is singular / too small)",
			Streamable:     true,
			StreamableHint: "Two-pass: streams alone; with groupers or features the request runs buffered. The fit is cohort-wide, never per group.",
		},
		{
			Name:        string(types.ATTR_PC_SCORE),
			Category:    "attribute",
			Description: "Per-row score on one principal component of the p members of vector / fields (exactly one; listwise), standardized to unit variance on a cohort-wide fit: the eigen-decomposition of the fit rows' (weighted) correlation or ddof = 1 covariance, with MAT_PCA's order and sign (a score correlates with each member as MAT_PCA's loading). Optional varimax rotation within the leading components. Every filtered row is scored; a null member scores null; a singular fit or N* ≤ p nulls every score with a warning. Two-pass.",
			Params: []descriptor.Param{
				{
					Name:        "component",
					Type:        "int",
					Required:    true,
					Description: "The component to score, 1-based (1 = the largest eigenvalue; after a rotation, the rotated component with the largest variance), at most the member count.",
				},
				{
					Name:        "on",
					Type:        "enum",
					Default:     vectors.PCAOnCorrelation,
					EnumValues:  vectors.PCAOnValues(),
					Description: "correlation: decompose the correlation (members standardized first); covariance: the covariance (members in their own units; a large-spread member dominates).",
				},
				{
					Name:        "rotation",
					Type:        "enum",
					Default:     vectors.RotationNone,
					EnumValues:  vectors.PCARotations(),
					Description: "none: the unrotated component. varimax: rotate the params.components leading components orthogonally (stats::varimax, Kaiser normalization; MAT_PCA's rotated order and sign) and score rotated component params.component.",
				},
				{
					Name:        "components",
					Type:        "int",
					Description: "With rotation varimax (required there, refused otherwise): how many leading components to rotate within, at least params.component and at most the member count.",
				},
				{
					Name:        "fit_on",
					Type:        "any",
					Default:     "filtered",
					Description: "Fit population: \"filtered\" (every filtered row) or an inline array of filterer objects selecting a subset of the filtered rows; every filtered row is still scored.",
				},
				{
					Name:        "missing",
					Type:        "enum",
					Default:     "listwise",
					EnumValues:  []string{"listwise"},
					Description: "Missing-data mode; only listwise (a row with any null member is left out of the fit and scores null).",
				},
			},
			AcceptsTypes:   numericFieldTypesNoDecimal,
			EmitsType:      "f64",
			EmitsTypeNote:  "one standardized score per record (mean 0, variance 1 over the fit rows; null when a member is null or the fit is singular / too small)",
			Streamable:     true,
			StreamableHint: "Two-pass: streams alone; with groupers or features the request runs buffered. The fit is cohort-wide, never per group.",
		},
		{
			Name:        string(types.ATTR_FACTOR_SCORE),
			Category:    "attribute",
			Description: "Per-row score on one common factor of the p members of vector / fields (exactly one; listwise) from a cohort-wide inline MAT_FACTOR fit of the fit rows' (weighted) correlation — the same factors, extraction and rotation knobs, so the same loadings, order and sign — scored by the regression (Thurstone) or Bartlett method on the members standardized by the fit. Every filtered row is scored; a null member scores null; a singular fit, N* ≤ p, an unidentified factor count or a factor the Kaiser rule does not keep nulls every score with a warning; a Heywood case or an iteration cap scores with a warning. Two-pass.",
			Params: []descriptor.Param{
				{
					Name:        "factor",
					Type:        "int",
					Required:    true,
					Description: "The factor to score, 1-based in MAT_FACTOR's column order (F1 accounts for the most variance), at most params.factors and the member count.",
				},
				{
					Name:        "method",
					Type:        "enum",
					Default:     vectors.FactorScoreRegression,
					EnumValues:  vectors.FactorScoreMethods(),
					Description: "regression: Thurstone's least-squares estimate, weights R⁻¹·LΦ (psych factor.scores \"Thurstone\"; shrunk toward 0, the most accurate per row). bartlett: weighted least squares, weights U⁻²L(LᵀU⁻²L)⁻¹ (psych \"Bartlett\"; unbiased for the factor, noisier); a uniqueness at or below 0 nulls every Bartlett score.",
				},
				{Name: "factors", Type: "any", Default: vectors.FactorsKaiser, Description: "MAT_FACTOR's factors: a positive integer k (at least params.factor, at most the member count) or \"kaiser\" (the fit correlation's eigenvalues above 1)."},
				{Name: "extraction", Type: "enum", Default: vectors.FactorExtractionMinres, EnumValues: vectors.FactorExtractions(), Description: "MAT_FACTOR's extraction: minres, principal_axis or ml."},
				{Name: "rotation", Type: "enum", Default: vectors.RotationNone, EnumValues: vectors.FactorRotations(), Description: "MAT_FACTOR's rotation: none, varimax, promax or oblimin; an oblique rotation's factor correlation enters the regression weights."},
				{Name: "kappa", Type: "float", Default: vectors.FactorDefaultKappa, Description: "Promax power, above 1; rotation promax only."},
				{Name: "gamma", Type: "float", Default: 0, Description: "Oblimin gamma, finite; rotation oblimin only."},
				{
					Name:        "fit_on",
					Type:        "any",
					Default:     "filtered",
					Description: "Fit population: \"filtered\" (every filtered row) or an inline array of filterer objects selecting a subset of the filtered rows; every filtered row is still scored.",
				},
				{
					Name:        "missing",
					Type:        "enum",
					Default:     "listwise",
					EnumValues:  []string{"listwise"},
					Description: "Missing-data mode; only listwise (a row with any null member is left out of the fit and scores null).",
				},
			},
			AcceptsTypes:   numericFieldTypesNoDecimal,
			EmitsType:      "f64",
			EmitsTypeNote:  "one factor score per record (mean 0 over the fit rows; null when a member is null or the fit cannot score)",
			Streamable:     true,
			StreamableHint: "Two-pass: streams alone; with groupers or features the request runs buffered. The fit is cohort-wide, never per group.",
		},
	}
}
