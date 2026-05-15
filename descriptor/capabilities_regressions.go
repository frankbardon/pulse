package descriptor

import "github.com/frankbardon/pulse/types"

// RegressionMeta describes a registered REG_* operator in the manifest.
// The shape mirrors Operator and TestMeta enough that LLM clients can
// reuse the same authoring path, but regression-specific knobs (family,
// link, penalty, modifier slots) live alongside the shared name +
// streamable + params trio so the catalog stays one fetch deep.
type RegressionMeta struct {
	// Name is the operator identifier (REG_OLS, REG_GLM,
	// REG_BAYES_LINEAR).
	Name string `json:"name"`

	// Description is a one-sentence prose summary for LLM-side
	// operator selection.
	Description string `json:"description"`

	// AcceptsTypes lists the schema field types valid as Target /
	// Predictors entries (numeric only in v1).
	AcceptsTypes []string `json:"accepts_types"`

	// EmitsTypeNote describes the structured output (RegressionResult)
	// since no scalar field type captures the fit summary.
	EmitsTypeNote string `json:"emits_type_note"`

	// Streamable mirrors types.RegressionType.Streamable(): true for
	// closed-form fits (REG_OLS, REG_BAYES_LINEAR), false for iterative
	// fits (REG_GLM). Spec-level modifiers (Resample, Selection) can
	// downgrade this further per request — see RegressionSpec.Streamable.
	Streamable bool `json:"streamable"`

	// StreamableHint flags the modifier downgrade rule so clients know
	// non-empty Resample / Selection forces the buffered path even on
	// streamable operators.
	StreamableHint string `json:"streamable_hint,omitempty"`

	// Params lists the per-operator parameter schema, including the
	// regularization, family, prior, resample, and selection knobs that
	// modify the underlying fit.
	Params []Param `json:"params"`

	// Modifiers names the spec-level orthogonal wrappers any regression
	// type supports. Each entry is a top-level RegressionSpec field
	// (Resample, Selection) plus its enum values; the field is
	// duplicated under Params for completeness but exposed at the top
	// level here so request authors can discover the composition story
	// without parsing the full param list.
	Modifiers []RegressionModifier `json:"modifiers,omitempty"`
}

// RegressionModifier describes a spec-level wrapper (Resample,
// Selection) that composes with any regression operator. Each modifier
// downgrades streamability when set; clients combine its EnumValues
// with their chosen Type to author the request.
type RegressionModifier struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	EnumValues  []string `json:"enum_values"`
}

// regressionCapabilities returns metadata for every entry in
// types.AllRegressionTypes(). Order is irrelevant; manifest assembly
// sorts by Name.
func regressionCapabilities() []RegressionMeta {
	numericTypes := []string{"u8", "u16", "u32", "u64", "f32", "f64", "decimal128", "nullable_decimal128"}

	// REG_OLS additionally accepts bit-packed integer types (nullable_u4,
	// nullable_bool, packed_bool) and date as first-class numeric inputs.
	// REG_GLM / REG_BAYES_LINEAR keep the narrower set pending a deliberate
	// widening pass on their fit paths.
	olsNumericTypes := []string{
		"date",
		"decimal128",
		"f32",
		"f64",
		"nullable_bool",
		"nullable_decimal128",
		"nullable_u16",
		"nullable_u4",
		"nullable_u8",
		"packed_bool",
		"u16",
		"u32",
		"u64",
		"u8",
	}

	resampleModifier := RegressionModifier{
		Name:        "resample",
		Description: "Orthogonal resampling layer; non-empty forces the buffered path.",
		EnumValues:  []string{"bootstrap", "jackknife"},
	}
	selectionModifier := RegressionModifier{
		Name:        "selection",
		Description: "Automated subset-selection wrapper; non-empty forces the buffered path. Requires criterion when set.",
		EnumValues:  []string{"backward", "forward", "stepwise"},
	}

	commonModifierHint := "Streamable in isolation; setting Resample or Selection on the request forces the buffered path."

	return []RegressionMeta{
		{
			Name:           string(types.REG_OLS),
			Description:    "Ordinary least squares with optional l1/l2/elasticnet regularization; covers simple, multiple, ridge, lasso, and elastic-net regression.",
			AcceptsTypes:   olsNumericTypes,
			EmitsTypeNote:  "RegressionResult with coefficients, std errors, p-values, R², adjusted R², residual std error.",
			Streamable:     types.REG_OLS.Streamable(),
			StreamableHint: commonModifierHint,
			Params: []Param{
				{Name: "target", Type: "field", Required: true, FieldFilter: "numeric", Description: "Response variable field name."},
				{Name: "predictors", Type: "list", Required: true, Description: "Predictor field names (numeric only in v1)."},
				{Name: "penalty", Type: "enum", Required: false, Default: "", EnumValues: []string{"", "elasticnet", "l1", "l2"}, Description: "Regularization scheme; empty for OLS."},
				{Name: "alpha", Type: "float", Required: false, Description: "Regularization strength (>0 when penalty is set)."},
				{Name: "l1_ratio", Type: "float", Required: false, Description: "Elastic-net mixing parameter in [0,1]; only read when penalty == elasticnet."},
				{Name: "max_iters", Type: "int", Required: false, Description: "Coordinate-descent iteration cap for regularized fits."},
				{Name: "tol", Type: "float", Required: false, Description: "Relative convergence tolerance for regularized fits."},
				{Name: "resample", Type: "enum", Required: false, EnumValues: []string{"", "bootstrap", "jackknife"}, Description: "Orthogonal resampling layer; downgrades streaming."},
				{Name: "bootstrap_iters", Type: "int", Required: false, Description: "Replicate count when resample == bootstrap."},
				{Name: "rng_seed", Type: "int", Required: false, Description: "Seed for the bootstrap RNG."},
				{Name: "selection", Type: "enum", Required: false, EnumValues: []string{"", "backward", "forward", "stepwise"}, Description: "Subset-selection wrapper; downgrades streaming."},
				{Name: "criterion", Type: "enum", Required: false, EnumValues: []string{"aic", "bic"}, Description: "Information criterion driving selection."},
			},
			Modifiers: []RegressionModifier{resampleModifier, selectionModifier},
		},
		{
			Name:           string(types.REG_GLM),
			Description:    "Generalized linear model via IRLS; supports binomial (logistic), poisson, and gamma families with family-appropriate links.",
			AcceptsTypes:   numericTypes,
			EmitsTypeNote:  "RegressionResult with coefficients, std errors, p-values, deviance, null deviance, McFadden pseudo-R².",
			Streamable:     types.REG_GLM.Streamable(),
			StreamableHint: "Always buffered; IRLS requires multiple passes over the data.",
			Params: []Param{
				{Name: "target", Type: "field", Required: true, FieldFilter: "numeric", Description: "Response variable field name."},
				{Name: "predictors", Type: "list", Required: true, Description: "Predictor field names (numeric only in v1)."},
				{Name: "family", Type: "enum", Required: true, EnumValues: []string{"binomial", "gamma", "poisson"}, Description: "GLM error family."},
				{Name: "link", Type: "enum", Required: false, EnumValues: []string{"cloglog", "identity", "inverse", "log", "logit", "probit", "sqrt"}, Description: "Link function; family-specific default when empty. Implemented this phase: binomial→logit, poisson→log, gamma→inverse. Other enum values are reserved for future phases and surface PROCESSING_REGRESSION_INVALID_LINK if requested."},
				{Name: "max_iters", Type: "int", Required: false, Description: "IRLS iteration cap."},
				{Name: "tol", Type: "float", Required: false, Description: "Relative convergence tolerance."},
				{Name: "resample", Type: "enum", Required: false, EnumValues: []string{"", "bootstrap", "jackknife"}, Description: "Orthogonal resampling layer."},
				{Name: "bootstrap_iters", Type: "int", Required: false, Description: "Replicate count when resample == bootstrap."},
				{Name: "rng_seed", Type: "int", Required: false, Description: "Seed for the bootstrap RNG."},
				{Name: "selection", Type: "enum", Required: false, EnumValues: []string{"", "backward", "forward", "stepwise"}, Description: "Subset-selection wrapper."},
				{Name: "criterion", Type: "enum", Required: false, EnumValues: []string{"aic", "bic"}, Description: "Information criterion driving selection."},
			},
			Modifiers: []RegressionModifier{resampleModifier, selectionModifier},
		},
		{
			Name:           string(types.REG_BAYES_LINEAR),
			Description:    "Bayesian linear regression with a conjugate Normal-Inverse-Gamma prior; reports posterior means, std errors, and credible intervals.",
			AcceptsTypes:   numericTypes,
			EmitsTypeNote:  "RegressionResult with coefficients (posterior means), std errors, R², adjusted R², residual std error, credible_intervals.",
			Streamable:     types.REG_BAYES_LINEAR.Streamable(),
			StreamableHint: commonModifierHint,
			Params: []Param{
				{Name: "target", Type: "field", Required: true, FieldFilter: "numeric", Description: "Response variable field name."},
				{Name: "predictors", Type: "list", Required: true, Description: "Predictor field names (numeric only in v1)."},
				{Name: "prior", Type: "enum", Required: false, Default: "nig", EnumValues: []string{"nig"}, Description: "Prior family; only the conjugate Normal-Inverse-Gamma is shipped in v1."},
				{Name: "prior_mu", Type: "list", Required: false, Description: "Prior mean vector for coefficients."},
				{Name: "prior_precision", Type: "float", Required: false, Description: "Prior precision scalar."},
				{Name: "prior_shape", Type: "float", Required: false, Description: "Inverse-gamma shape parameter for residual variance."},
				{Name: "prior_rate", Type: "float", Required: false, Description: "Inverse-gamma rate parameter for residual variance."},
				{Name: "credible_level", Type: "float", Required: false, Default: 0.95, Description: "Posterior credible-interval mass."},
				{Name: "resample", Type: "enum", Required: false, EnumValues: []string{"", "bootstrap", "jackknife"}, Description: "Orthogonal resampling layer."},
				{Name: "bootstrap_iters", Type: "int", Required: false, Description: "Replicate count when resample == bootstrap."},
				{Name: "rng_seed", Type: "int", Required: false, Description: "Seed for the bootstrap RNG."},
				{Name: "selection", Type: "enum", Required: false, EnumValues: []string{"", "backward", "forward", "stepwise"}, Description: "Subset-selection wrapper."},
				{Name: "criterion", Type: "enum", Required: false, EnumValues: []string{"aic", "bic"}, Description: "Information criterion driving selection."},
			},
			Modifiers: []RegressionModifier{resampleModifier, selectionModifier},
		},
	}
}
