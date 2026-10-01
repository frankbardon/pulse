package descriptor

import (
	"github.com/frankbardon/pulse/types"

	"github.com/frankbardon/pulse/descriptor"
)

// regressionCapabilities returns metadata for every entry in
// types.AllRegressionTypes(). Order is irrelevant; manifest assembly
// sorts by Name.
func regressionCapabilities() []descriptor.RegressionMeta {
	// All three engines consume the analytics-numeric set: integer /
	// float / decimal families plus the bit-packed integer encodings
	// (u4, packed_bool) and date. Runtime collection routes every entry
	// through Record.NumericValue, which hands the fit algorithms
	// float64 + null status uniformly, and the predict-side gate is
	// encoding.FieldType.IsNumericForAnalytics (predict_regression.go).
	//
	// Nullability is orthogonal to type — the `nullable_*` spellings
	// this list once carried were stale aliases for `u4` / `u8` / `u16`
	// / `packed_bool` / `decimal128`, every one of which is named here
	// directly. See capabilities_aggregators.go's declaration policy.
	numericTypes := []string{
		"date",
		"decimal128",
		"f32",
		"f64",
		"packed_bool",
		"u16",
		"u32",
		"u4",
		"u64",
		"u8",
	}

	resampleModifier := descriptor.RegressionModifier{
		Name:        "resample",
		Description: "Orthogonal resampling layer; non-empty forces the buffered path.",
		EnumValues:  []string{"bootstrap", "jackknife"},
	}
	selectionModifier := descriptor.RegressionModifier{
		Name:        "selection",
		Description: "Automated subset-selection wrapper; non-empty forces the buffered path. Requires criterion when set.",
		EnumValues:  []string{"backward", "forward", "stepwise"},
	}

	commonModifierHint := "Streamable in isolation; setting Resample or Selection on the request forces the buffered path."

	return []descriptor.RegressionMeta{
		{
			Name:           string(types.REG_OLS),
			Description:    "Ordinary least squares with optional l1/l2/elasticnet regularization; covers simple, multiple, ridge, lasso, and elastic-net regression.",
			AcceptsTypes:   numericTypes,
			EmitsTypeNote:  "RegressionResult with coefficients, std errors, p-values, R², adjusted R², residual std error.",
			Streamable:     types.REG_OLS.Streamable(),
			StreamableHint: commonModifierHint,
			Params: []descriptor.Param{
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
			Modifiers: []descriptor.RegressionModifier{resampleModifier, selectionModifier},
		},
		{
			Name:           string(types.REG_GLM),
			Description:    "Generalized linear model via IRLS; supports binomial (logistic), poisson, and gamma families with family-appropriate links.",
			AcceptsTypes:   numericTypes,
			EmitsTypeNote:  "RegressionResult with coefficients, std errors, p-values, deviance, null deviance, McFadden pseudo-R².",
			Streamable:     types.REG_GLM.Streamable(),
			StreamableHint: "Always buffered; IRLS requires multiple passes over the data.",
			Params: []descriptor.Param{
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
			Modifiers: []descriptor.RegressionModifier{resampleModifier, selectionModifier},
		},
		{
			Name:           string(types.REG_BAYES_LINEAR),
			Description:    "Bayesian linear regression with a conjugate Normal-Inverse-Gamma prior; reports posterior means, std errors, and credible intervals.",
			AcceptsTypes:   numericTypes,
			EmitsTypeNote:  "RegressionResult with coefficients (posterior means), std errors, R², adjusted R², residual std error, credible_intervals.",
			Streamable:     types.REG_BAYES_LINEAR.Streamable(),
			StreamableHint: commonModifierHint,
			Params: []descriptor.Param{
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
			Modifiers: []descriptor.RegressionModifier{resampleModifier, selectionModifier},
		},
	}
}
