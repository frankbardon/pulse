package descriptor

import (
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg/metric"
	"github.com/frankbardon/pulse/types"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/fieldaccept"
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

// attributeFloorKeys is the attribute floor every
// Response.Components.Attributes entry carries ahead of the operator's
// own keys: the filter-passing rows the attribute scored, split by
// output presence (n + n_null = the post-filter record count).
func attributeFloorKeys() []descriptor.ComponentKey {
	return []descriptor.ComponentKey{
		{Name: "n", Type: "int", Description: "Filter-passing rows whose attribute output is present (non-null)."},
		{Name: "n_null", Type: "int", Description: "Filter-passing rows whose attribute output is null."},
	}
}

// attrSchema composes an attribute ComponentSchema: the attribute floor,
// then the operator's own keys in emission order. Only an attribute
// that declares one emits a Response.Components.Attributes entry.
func attrSchema(merge descriptor.ComponentsMergeability, extra ...descriptor.ComponentKey) descriptor.ComponentSchema {
	floor := attributeFloorKeys()
	keys := make([]descriptor.ComponentKey, 0, len(floor)+len(extra))
	keys = append(keys, floor...)
	keys = append(keys, extra...)
	return descriptor.ComponentSchema{Keys: keys, Mergeability: merge}
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
			AcceptsTypes:  fieldaccept.TypeNames(string(types.ATTR_ZSCORE), fieldaccept.SlotField),
			EmitsType:     "f64",
			EmitsTypeNote: "one float per record (0 when stddev=0; null when the value is null)",
			Streamable:    true,
		},
		{
			Name:          string(types.ATTR_TSCORE),
			Category:      "attribute",
			Description:   "Per-row T-score: z-score scaled and shifted to mean 50, stddev 10.",
			AcceptsTypes:  fieldaccept.TypeNames(string(types.ATTR_TSCORE), fieldaccept.SlotField),
			EmitsType:     "f64",
			EmitsTypeNote: "one float per record (50 when stddev=0; null when the value is null)",
			Streamable:    true,
		},
		{
			Name:          string(types.ATTR_NORMALIZED),
			Category:      "attribute",
			Description:   "Per-row min-max normalization: (value − min) / (max − min) ∈ [0, 1].",
			AcceptsTypes:  fieldaccept.TypeNames(string(types.ATTR_NORMALIZED), fieldaccept.SlotField),
			EmitsType:     "f64",
			EmitsTypeNote: "one float per record in [0, 1] (0 when max=min; null when the value is null)",
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
			AcceptsTypes:   fieldaccept.TypeNames(string(types.ATTR_PERCENTILE), fieldaccept.SlotField),
			EmitsType:      "f64",
			EmitsTypeNote:  "one float per record in (0, 100]; null when the value is null",
			Streamable:     false,
			StreamableHint: "Use ATTR_NORMALIZED for a streaming-friendly rank proxy.",
		},
		{
			Name:        string(types.ATTR_DATE_PART),
			Category:    "attribute",
			Description: "Extract a calendar component (year, month, day, year_month, year_month_day, month_day, hour, iso_weekday) from a date or datetime field; a datetime reads the wall clock of its resolved time zone, and hour needs a datetime.",
			Params: []descriptor.Param{
				{
					Name:        "part",
					Type:        "enum",
					Required:    true,
					Description: "Which calendar component to extract.",
					EnumValues:  []string{"day", "hour", "iso_weekday", "month", "month_day", "year", "year_month", "year_month_day"},
				},
			},
			AcceptsTypes:  []string{"date", "datetime"},
			EmitsType:     "f64",
			EmitsTypeNote: "encoded integer per record (e.g. year_month=YYYYMM, iso_weekday=1..7 with Monday=1)",
			Streamable:    true,
		},
		{
			Name:          string(types.ATTR_REG_FITTED),
			Category:      "attribute",
			Description:   "Per-row fitted value ŷᵢ = Xᵢ · β + β₀ from a regression refit during the attribute prepass. Carries its own RegressionSpec-shaped fields (Target, Predictors, Penalty, Alpha, L1Ratio); each ATTR_REG_* attribute refits independently (Option A). Accepts any OLS penalty (unpenalized, ridge, lasso, elasticnet).",
			Params:        regAttributeParams(true),
			AcceptsTypes:  fieldaccept.TypeNames(string(types.ATTR_REG_FITTED), fieldaccept.SlotPredictor),
			EmitsType:     "f64",
			EmitsTypeNote: "one fitted value per record (NaN-free)",
			Streamable:    true,
		},
		{
			Name:          string(types.ATTR_REG_RESIDUAL),
			Category:      "attribute",
			Description:   "Per-row residual yᵢ − ŷᵢ from a regression refit during the attribute prepass. Sums to ≈ 0 when the fit includes an intercept (always true for OLS). Accepts any OLS penalty; reuses the same fit machinery as ATTR_REG_FITTED.",
			Params:        regAttributeParams(true),
			AcceptsTypes:  fieldaccept.TypeNames(string(types.ATTR_REG_RESIDUAL), fieldaccept.SlotPredictor),
			EmitsType:     "f64",
			EmitsTypeNote: "one residual per record",
			Streamable:    true,
		},
		{
			Name:           string(types.ATTR_REG_LEVERAGE),
			Category:       "attribute",
			Description:    "Per-row hat-matrix diagonal hᵢᵢ = 1/n + (xᵢ − μ_x)ᵀ · M2_xx⁻¹ · (xᵢ − μ_x) from an unpenalized OLS refit. Range ∈ [0, 1]; Σᵢ hᵢᵢ = p + 1. Restricted to unpenalized OLS — penalized leverage / GLM leverage deferred.",
			Params:         regAttributeParams(false),
			AcceptsTypes:   fieldaccept.TypeNames(string(types.ATTR_REG_LEVERAGE), fieldaccept.SlotPredictor),
			EmitsType:      "f64",
			EmitsTypeNote:  "one leverage per record in [0, 1]",
			Streamable:     true,
			StreamableHint: "Requires unpenalized OLS only; any non-empty Penalty surfaces PROCESSING_CONFIG.",
		},
		{
			Name:        string(types.ATTR_REG_PREDICT),
			Category:    "attribute",
			Description: "Per-row score from a BOUND model (a ComposedRequest `fits` entry, named by params.fit) instead of a refit: intercept + Σ coefficient·predictor at full precision, the GLM inverse link applied on the response scale. Scores REG_OLS and REG_GLM (penalties included; under selection the selected features) and REG_BAYES_LINEAR (posterior mean). The model's predictor list is authoritative and read off the slot's own columns (an absent one-hot column is the reference level); a null in any column the score reads yields null. Row-local once bound.",
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
			AcceptsTypes: tableAcceptsTypes(types.ATTR_SET_POPCOUNT),
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
			EmitsTypeNote: "one integer per record: 0..set width inclusive, so 0..256 at the widest rung (set_u256); null when the set is null",
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
			AcceptsTypes:  tableAcceptsTypes(types.ATTR_SET_HAS),
			EmitsType:     "packed_bool",
			EmitsTypeNote: "one 0/1 per record (0 when the set is null)",
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
					Name:        "fit",
					Type:        "string",
					Description: "Name of a bound vector fit — a `fits` entry of the enclosing compose request over one MAT_* or fitted-attribute battery — to score against instead of fitting (mutually exclusive with fit_on); aligned to the members by label, never by position. The fit's own weight basis governs; a regression fit's name is PULSE_MODEL_KIND_MISMATCH.",
				},
				{
					Name:        "labels",
					Type:        "list",
					Description: "Inline fields only: one unique label per member, the names a bound fit aligns to (default: the field names). A named vector carries its own labels.",
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
					Description: "none: the unrotated component. Otherwise MAT_PCA's orthogonal rotation of the params.components leading components (varimax, quartimax, geomin_orthogonal or entropy; MAT_PCA's rotated order and sign), scoring rotated component params.component. An oblique criterion is refused (rotation_oblique_unsupported).",
				},
				{
					Name:        "epsilon",
					Type:        "float",
					Default:     vectors.GeominDefaultEpsilon,
					Description: "Geomin epsilon, above 0 (default 0.01); rotation geomin_orthogonal only.",
				},
				{
					Name:        "components",
					Type:        "int",
					Description: "With a rotation (required there, refused otherwise): how many leading components to rotate within, at least params.component and at most the member count.",
				},
				{
					Name:        "fit_on",
					Type:        "any",
					Default:     "filtered",
					Description: "Fit population: \"filtered\" (every filtered row) or an inline array of filterer objects selecting a subset of the filtered rows; every filtered row is still scored.",
				},
				{
					Name:        "fit",
					Type:        "string",
					Description: "Name of a bound vector fit — a `fits` entry of the enclosing compose request over one MAT_* or fitted-attribute battery — to score against instead of fitting (mutually exclusive with fit_on); aligned to the members by label, never by position. The fit's own weight basis governs; a regression fit's name is PULSE_MODEL_KIND_MISMATCH.",
				},
				{
					Name:        "labels",
					Type:        "list",
					Description: "Inline fields only: one unique label per member, the names a bound fit aligns to (default: the field names). A named vector carries its own labels.",
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
				{Name: "rotation", Type: "enum", Default: vectors.RotationNone, EnumValues: vectors.FactorRotations(), Description: "MAT_FACTOR's rotation: none, orthogonal (varimax, quartimax, geomin_orthogonal, entropy) or oblique (promax, oblimin, geomin); an oblique rotation's factor correlation enters the regression weights."},
				{Name: "kappa", Type: "float", Default: vectors.FactorDefaultKappa, Description: "Promax power, above 1; rotation promax only."},
				{Name: "gamma", Type: "float", Default: 0, Description: "Oblimin gamma, finite; rotation oblimin only."},
				{Name: "epsilon", Type: "float", Default: vectors.GeominDefaultEpsilon, Description: "Geomin epsilon, above 0 (default 0.01); rotation geomin or geomin_orthogonal only."},
				{
					Name:        "fit_on",
					Type:        "any",
					Default:     "filtered",
					Description: "Fit population: \"filtered\" (every filtered row) or an inline array of filterer objects selecting a subset of the filtered rows; every filtered row is still scored.",
				},
				{
					Name:        "fit",
					Type:        "string",
					Description: "Name of a bound vector fit — a `fits` entry of the enclosing compose request over one MAT_* or fitted-attribute battery — to score against instead of fitting (mutually exclusive with fit_on); aligned to the members by label, never by position. The fit's own weight basis governs; a regression fit's name is PULSE_MODEL_KIND_MISMATCH.",
				},
				{
					Name:        "labels",
					Type:        "list",
					Description: "Inline fields only: one unique label per member, the names a bound fit aligns to (default: the field names). A named vector carries its own labels.",
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
		{
			Name:        string(types.ATTR_SCALE_SCORE),
			Category:    "attribute",
			Description: "Per-row rating-battery score across the p members of vector / fields (exactly one): the mean of the row's valid items, or the prorated sum (that mean × p). Reverse-keyed items score lo + hi − x on the declared range; a value outside the range is missing (never clamped) and warned; fewer than min_valid valid items score null, never 0. Row-local and single-pass; the weight is irrelevant.",
			Params: []descriptor.Param{
				{
					Name:        "stat",
					Type:        "enum",
					Default:     "mean",
					EnumValues:  []string{"mean", "sum"},
					Description: "mean: the valid-item mean; sum: the PRORATED sum, valid-item mean × item count (there is no raw sum, so a skipped item never reads as 0).",
				},
				{
					Name:        "min_valid",
					Type:        "int",
					Description: "Fewest valid (present, in-range) items a row needs for a score, 1 to the item count; below it the score is null. Default: every item.",
				},
				{
					Name:        "reverse",
					Type:        "list",
					Description: "Member labels of the reverse-keyed items, each scored lo + hi − x. Needs a declared range (scale_min / scale_max, or the kind \"scale\" vector's bounds); refused without one.",
				},
				{
					Name:        "scale_min",
					Type:        "float",
					Description: "Lower end of the battery's declared range; set with scale_max (min < max). Without both, a kind \"scale\" vector's bounds apply. A value outside the range is scored as missing and counted in a PULSE_ATTR_SCALE_OUT_OF_RANGE warning.",
				},
				{
					Name:        "scale_max",
					Type:        "float",
					Description: "Upper end of the battery's declared range; set with scale_min.",
				},
			},
			AcceptsTypes:  numericFieldTypesNoDecimal,
			EmitsType:     "f64",
			EmitsTypeNote: "one score per record on the item scale (sum: × item count); null when fewer than min_valid items are valid",
			Streamable:    true,
			ComponentSchema: attrSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "n_out_of_range", Type: "int", Optional: true,
					Description: "Member values read as missing for lying outside the declared range (never clamped); emitted only when a range is declared (scale_min / scale_max or a kind \"scale\" vector's bounds)."},
				descriptor.ComponentKey{Name: "n_rows_out_of_range", Type: "int", Optional: true,
					Description: "Rows holding at least one out-of-range member value; emitted only when a range is declared."},
			),
		},
		comparisonCapability(types.ATTR_PROFILE_SIMILARITY),
		comparisonCapability(types.ATTR_VEC_DISTANCE),
	}
}

// comparisonCapability is the manifest entry of a reference-comparison
// attribute (ATTR_PROFILE_SIMILARITY, ATTR_VEC_DISTANCE): one engine,
// split by direction; its metric enum is the registry's accepted list
// for the operator's consumer (vectors.ComparisonConsumer).
func comparisonCapability(t types.AttributeType) descriptor.Operator {
	c, form, other := vectors.ComparisonConsumer(t)
	accepted := []string{}
	for _, m := range metric.Accepted(c) {
		if mm, _ := metric.Lookup(m); mm.HasForm(form) {
			accepted = append(accepted, m)
		}
	}
	params := []descriptor.Param{
		{
			Name:        "metric",
			Type:        "enum",
			EnumValues:  accepted,
			Description: "The " + string(form) + " metric (capabilities.matrix.metrics). Default: the named vector kind's " + string(form) + " default; with no kind and no metric the slot is refused (PULSE_VECTOR_METRIC_REQUIRED). A " + string(otherForm(form)) + "-direction metric is refused with a pointer to " + string(other) + ", never converted.",
		},
		{
			Name:        "reference",
			Type:        "any",
			Required:    true,
			Description: "The target compared with each row: a literal array (one value per member, member order), a label map {label: value} (matched by label), {\"table\", \"key\"} (one Extensions.ReferenceVectors profile), {\"centroid\": {\"filter\", \"by\"}} (the weighted per-element mean — AGG_VEC_MEAN's — of the filtered rows the optional filter admits; with by, a categorical or unsigned-integer field, one centroid per value and each row compared with its own: a pre-pass, so two-pass for any metric), {\"population\": name} (a bound vector fit's stored mean: a request / registered population or a Compose vector fit; unbound is PULSE_VECTOR_REFERENCE_UNKNOWN), {\"record\": {\"cohort\", \"keys\"}} (one stored record point-looked-up through its cohort's sidecar index — cohort a {filename, data_dir} or omitted for the request's own, keys [{field, value}] in index order, values as text — before the run, read by member COLUMN name, a null member a missing element; predict checks the shape only, the run refuses with the lookup's own PULSE_INDEX_* / PULSE_LOOKUP_* code, never a fallback) or {\"table\", \"key_field\"} (each row's own profile, keyed by a categorical field's label or an unsigned-integer field's (u4, u8, u16, u32, u64) canonical base-10 value; any other field type is refused, SERVICE_VALIDATION reason key_field_type). A table, map or population is matched to the vector BY LABEL (PULSE_VECTOR_REFERENCE_MISMATCH otherwise, checked at predict); a row whose key_field / by is null or has no profile / centroid is null, counted in n_reference_missing and warned once per slot (PULSE_VECTOR_REFERENCE_MISSING, components on or off; strict does not escalate it).",
		},
		{
			Name:        "min_valid",
			Type:        "int",
			Default:     1,
			Description: "Fewest complete member pairs (member and reference both present) a row needs, 1 to the member count; below it the value is null. Distance sums are rescaled by p / p_observed.",
		},
		{
			Name:        "labels",
			Type:        "list",
			Description: "Inline fields only: one unique label per member, the names a label map or table aligns to (default: the field names). A named vector carries its own labels.",
		},
	}
	desc := "Per-row similarity to a reference across the p members of vector / fields (exactly one): correlation, cosine or a set similarity, pairwise-complete (a null member drops its pair). Row-local and single-pass, the weight irrelevant — except over a centroid reference, folded (weighted) in a pre-pass first."
	note := "one similarity per record (correlation / cosine in [-1, 1], set similarities in [0, 1]); null below min_valid or when the row's reference is missing"
	hint := ""
	if t == types.ATTR_VEC_DISTANCE {
		desc = "Per-row distance to a reference across the p members of vector / fields (exactly one), pairwise-complete with the p / p_observed rescaling. Row-local metrics are single-pass; std_euclidean (per-member SDs) and mahalanobis (inverse covariance, every member needed) fit first on the filtered rows params.fit_on admits — ATTR_MAHALANOBIS's fit pass, two-pass, weighted when a weight is in force; N* ≤ p or a singular fit nulls every value with a warning. A centroid reference is folded (weighted) in the same pre-pass, for any metric."
		note = "one distance (≥ 0) per record; null below min_valid, when the row's reference is missing or the fit cannot score"
		hint = "Row-local metrics stream in one pass; std_euclidean / mahalanobis and any centroid reference take the two-pass drive (with groupers or features the request runs buffered). The fit and centroids are cohort-wide, never per group."
		params = append(params,
			descriptor.Param{
				Name:        "fit_on",
				Type:        "any",
				Default:     "filtered",
				Description: "Fitted metrics only (refused beside a row-local one): the fit population, \"filtered\" (every filtered row) or an inline array of filterer objects; every filtered row is still scored.",
			},
			descriptor.Param{
				Name:        "fit",
				Type:        "string",
				Description: "Fitted metrics only: name of a bound vector fit (a `fits` entry of the enclosing compose request) to read instead of fitting (mutually exclusive with fit_on); aligned by label.",
			},
			descriptor.Param{
				Name:        "missing",
				Type:        "enum",
				Default:     "listwise",
				EnumValues:  []string{"listwise"},
				Description: "Fitted metrics only: the fit's missing-data mode; only listwise (a row with a null member is left out of the fit, and is still scored pairwise-complete).",
			},
		)
	}
	return descriptor.Operator{
		Name:           string(t),
		Category:       "attribute",
		Description:    desc,
		Params:         params,
		AcceptsTypes:   numericFieldTypesNoDecimal,
		EmitsType:      "f64",
		EmitsTypeNote:  note,
		Streamable:     true,
		StreamableHint: hint,
		ComponentSchema: attrSchema(descriptor.Mergeable,
			descriptor.ComponentKey{Name: "n_reference_missing", Type: "int",
				Description: "Rows whose params.reference key_field / centroid by value has no profile / centroid (or is null), or every row of a centroid no row reached, scored null; 0 for a literal, table key or population reference. Non-zero also raises PULSE_VECTOR_REFERENCE_MISSING."},
			descriptor.ComponentKey{Name: "n_domain_invalid", Type: "int",
				Description: "Rows scored null because a value lay outside the metric's domain (a negative part under hellinger / aitchison, an aitchison zero, a non-0/1 value under a set metric). Non-zero also raises PULSE_VECTOR_METRIC_DOMAIN."},
		),
	}
}

// otherForm is the direction f is not.
func otherForm(f metric.Form) metric.Form {
	if f == metric.Distance {
		return metric.Similarity
	}
	return metric.Distance
}

// regAttributeParams are the fit fields ATTR_REG_FITTED / RESIDUAL /
// LEVERAGE carry: TOP-LEVEL keys of the attribute entry (Attribute.
// Target / Predictors / Penalty / Alpha / L1Ratio — the RegressionSpec
// shape), not members of its params object; the slot's own field is
// unread. penalized adds the OLS penalty knobs (leverage refuses any
// penalty).
func regAttributeParams(penalized bool) []descriptor.Param {
	ps := []descriptor.Param{
		{Name: "target", Type: "field", Required: true, FieldFilter: "numeric", Description: "Response variable field name; a top-level key of the attribute entry.", Slot: true},
		{Name: "predictors", Type: "list", Required: true, Description: "Predictor field names, at least one; a top-level key of the attribute entry.", Slot: true},
	}
	if penalized {
		ps = append(ps,
			descriptor.Param{Name: "penalty", Type: "enum", Required: false, Default: "", EnumValues: []string{"", "elasticnet", "l1", "l2"}, Description: "Regularization scheme; empty for OLS. Top-level.", Slot: true},
			descriptor.Param{Name: "alpha", Type: "float", Required: false, Description: "Regularization strength (>0 when penalty is set). Top-level.", Slot: true},
			descriptor.Param{Name: "l1_ratio", Type: "float", Required: false, Description: "Elastic-net mixing parameter in (0,1); only read when penalty == elasticnet. Top-level.", Slot: true},
		)
	}
	return ps
}
