package descriptor

import (
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"

	"github.com/frankbardon/pulse/descriptor"
)

// regressionCapabilities returns metadata for every entry in
// types.AllRegressionTypes(). Order is irrelevant; manifest assembly
// sorts by Name.
func regressionCapabilities() []descriptor.RegressionMeta {
	// All three engines consume the analytics-numeric set (integer /
	// float / decimal families, u4, packed_bool, date, datetime) through
	// Record.NumericValue. The declaration IS the accepted-types table's
	// (internal/fieldaccept) predictor slot — the rule predict and
	// regression.BuildWith both enforce, a categorical's index and a
	// set's bitmask refused — so it cannot drift from it. The target
	// slot carries the same set.
	acceptsTypes := func(t types.RegressionType) []string {
		return fieldaccept.TypeNames(string(t), fieldaccept.SlotPredictor)
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
			AcceptsTypes:   acceptsTypes(types.REG_OLS),
			EmitsTypeNote:  "RegressionResult with coefficients, std errors, p-values, ci_lower / ci_upper (t on df), df, conf_level, R², adjusted R², residual std error; a robust covariance adds its covariance echo.",
			Streamable:     types.REG_OLS.Streamable(),
			StreamableHint: commonModifierHint + " A robust covariance (any type but classical) forces it too.",
			Params: []descriptor.Param{
				{Name: "target", Type: "field", Required: true, FieldFilter: "numeric", Description: "Response variable field name."},
				{Name: "predictors", Type: "list", Required: true, Description: "Predictor field names (numeric only in v1)."},
				{Name: "penalty", Type: "enum", Required: false, Default: "", EnumValues: []string{"", "elasticnet", "l1", "l2"}, Description: "Regularization scheme; empty for OLS."},
				{Name: "alpha", Type: "float", Required: false, Description: "Regularization strength (>0 when penalty is set)."},
				{Name: "l1_ratio", Type: "float", Required: false, Description: "Elastic-net mixing parameter in [0,1]; only read when penalty == elasticnet."},
				{Name: "max_iters", Type: "int", Required: false, Description: "Coordinate-descent iteration cap for regularized fits."},
				{Name: "tol", Type: "float", Required: false, Description: "Relative convergence tolerance for regularized fits."},
				{Name: "resample", Type: "enum", Required: false, EnumValues: []string{"", "bootstrap", "jackknife"}, Description: "Orthogonal resampling layer; downgrades streaming."},
				{Name: "bootstrap_iters", Type: "int", Required: false, Default: 1000, Description: "Replicate count when resample == bootstrap, used as given; <= 0 means 1000. Echoed as bootstrap_requested beside bootstrap_succeeded (replicates whose refit succeeded)."},
				{Name: "rng_seed", Type: "int", Required: false, Description: "Seed for the bootstrap RNG."},
				{Name: "selection", Type: "enum", Required: false, EnumValues: []string{"", "backward", "forward", "stepwise"}, Description: "Subset-selection wrapper; downgrades streaming."},
				{Name: "criterion", Type: "enum", Required: false, EnumValues: []string{"aic", "bic"}, Description: "Information criterion driving selection."},
				{Name: "conf_level", Type: "float", Required: false, Default: 0.95, Description: "Coverage of ci_lower / ci_upper, 0 < conf_level < 1: coefficient ∓ t·SE on the residual df (N* − p − 1 weighted); Wald normal under resample; intervals only where a std error is reported."},
				{Name: "vcov", Type: "bool", Required: false, Default: false, Description: "Emit the coefficient covariance (vcov) and its correlation as square matrices keyed (intercept) then predictors; √diag = std_errors. Unpenalized σ̂²(XᵀX)⁻¹ or the ridge sandwich; resample gives the replicate covariance, selection the final refit's; lasso / elasticnet refuse it (PROCESSING_REGRESSION_VCOV_UNSUPPORTED)."},
				{Name: "covariance", Type: "object", Required: false, Description: "{type, cluster, order_by, lags}: the estimator behind std_errors, p_values, ci_lower / ci_upper and vcov. type classical (absent: unchanged), hc0 / hc1 / hc2 / hc3 (heteroskedasticity-robust), cluster (CR1 over cluster's levels — categorical or unsigned-integer field, ≥2 levels — on df G − 1), hac (Newey–West over the rows ordered by order_by — date, datetime or numeric, unique per row — truncated at lags, an integer or \"auto\" = ⌊4(n/100)^(2/9)⌋). Robust types force the buffered path and echo as covariance; penalty / resample / selection refuse them (PROCESSING_REGRESSION_VCOV_UNSUPPORTED)."},
			},
			Modifiers: []descriptor.RegressionModifier{resampleModifier, selectionModifier},
		},
		{
			Name:           string(types.REG_GLM),
			Description:    "Generalized linear model via IRLS; supports binomial (logistic), poisson, and gamma families with family-appropriate links, and l1/l2/elasticnet regularization on binomial and poisson.",
			AcceptsTypes:   acceptsTypes(types.REG_GLM),
			EmitsTypeNote:  "RegressionResult with coefficients, std errors, p-values, Wald ci_lower / ci_upper, conf_level, deviance, null deviance, McFadden pseudo-R²; a penalty echoes penalty / alpha / l1_ratio (l1 / elasticnet: std errors only for the kept predictors); a robust covariance adds its covariance echo.",
			Streamable:     types.REG_GLM.Streamable(),
			StreamableHint: "Always buffered; IRLS requires multiple passes over the data.",
			Params: []descriptor.Param{
				{Name: "target", Type: "field", Required: true, FieldFilter: "numeric", Description: "Response variable field name."},
				{Name: "predictors", Type: "list", Required: true, Description: "Predictor field names (numeric only in v1)."},
				{Name: "family", Type: "enum", Required: true, EnumValues: []string{"binomial", "gamma", "poisson"}, Description: "GLM error family."},
				{Name: "link", Type: "enum", Required: false, EnumValues: []string{"cloglog", "identity", "inverse", "log", "logit", "probit", "sqrt"}, Description: "Link function; family-specific default when empty. Implemented this phase: binomial→logit, poisson→log, gamma→inverse. Other enum values are reserved for future phases and surface PROCESSING_REGRESSION_INVALID_LINK if requested."},
				{Name: "penalty", Type: "enum", Required: false, Default: "", EnumValues: []string{"", "elasticnet", "l1", "l2"}, Description: "Regularization scheme (binomial / poisson; gamma refuses it, PROCESSING_CONFIG); empty for the maximum-likelihood fit. Minimizes -loglik/N* + alpha·[(1-l1_ratio)/2·||b||² + l1_ratio·||b||₁] (glmnet, standardize = FALSE): predictors unstandardized, intercept unpenalized, N* = Σw. l2 keeps a separated fit finite."},
				{Name: "alpha", Type: "float", Required: false, Description: "Regularization strength (>0 when penalty is set; glmnet's lambda)."},
				{Name: "l1_ratio", Type: "float", Required: false, Description: "Elastic-net mixing parameter, 0 < l1_ratio < 1; only read when penalty == elasticnet (glmnet's alpha)."},
				{Name: "max_iters", Type: "int", Required: false, Description: "IRLS iteration cap."},
				{Name: "tol", Type: "float", Required: false, Description: "Relative convergence tolerance."},
				{Name: "resample", Type: "enum", Required: false, EnumValues: []string{"", "bootstrap", "jackknife"}, Description: "Orthogonal resampling layer."},
				{Name: "bootstrap_iters", Type: "int", Required: false, Default: 1000, Description: "Replicate count when resample == bootstrap, used as given; <= 0 means 1000. Echoed as bootstrap_requested beside bootstrap_succeeded (replicates whose refit succeeded)."},
				{Name: "rng_seed", Type: "int", Required: false, Description: "Seed for the bootstrap RNG."},
				{Name: "selection", Type: "enum", Required: false, EnumValues: []string{"", "backward", "forward", "stepwise"}, Description: "Subset-selection wrapper."},
				{Name: "criterion", Type: "enum", Required: false, EnumValues: []string{"aic", "bic"}, Description: "Information criterion driving selection."},
				{Name: "conf_level", Type: "float", Required: false, Default: 0.95, Description: "Coverage of ci_lower / ci_upper, 0 < conf_level < 1: Wald normal coefficient ∓ z·SE (dispersion 1)."},
				{Name: "vcov", Type: "bool", Required: false, Default: false, Description: "Emit the coefficient covariance (vcov, (XᵀWX)⁻¹ at the converged weights, dispersion 1; l2: the penalized sandwich H⁻¹XᵀWXH⁻¹, H = XᵀWX + N*·alpha·D) and its correlation, keyed (intercept) then predictors; √diag = std_errors. Resample gives the replicate covariance, selection the final refit's; l1 / elasticnet refuse it (PROCESSING_REGRESSION_VCOV_UNSUPPORTED)."},
				{Name: "covariance", Type: "object", Required: false, Description: "{type, cluster, order_by, lags}: the estimator behind std_errors, the Wald p_values, ci_lower / ci_upper and vcov — the sandwich over the IRLS scores at the converged weights (R sandwich on glm). type classical (absent: unchanged), hc0–hc3, cluster (CR1 over cluster's levels, ≥2), hac (Newey–West over order_by, unique per row; lags an integer or \"auto\"). Inference stays Wald z, cluster included. Echoes as covariance; resample / selection refuse it (PROCESSING_REGRESSION_VCOV_UNSUPPORTED)."},
			},
			Modifiers: []descriptor.RegressionModifier{resampleModifier, selectionModifier},
		},
		{
			Name:           string(types.REG_BAYES_LINEAR),
			Description:    "Bayesian linear regression with a conjugate Normal-Inverse-Gamma prior; reports posterior means, std errors, and credible intervals.",
			AcceptsTypes:   acceptsTypes(types.REG_BAYES_LINEAR),
			EmitsTypeNote:  "RegressionResult with coefficients (posterior means), std errors, R², adjusted R², residual std error, credible_intervals, ci_lower / ci_upper (posterior t on df), df, conf_level; a robust covariance swaps in the sandwich posterior (normal intervals, no df) and adds its covariance echo.",
			Streamable:     types.REG_BAYES_LINEAR.Streamable(),
			StreamableHint: "Streamable; Resample and Selection are refused (PROCESSING_CONFIG). A robust covariance (any type but classical) forces the buffered path.",
			Params: []descriptor.Param{
				{Name: "target", Type: "field", Required: true, FieldFilter: "numeric", Description: "Response variable field name."},
				{Name: "predictors", Type: "list", Required: true, Description: "Predictor field names (numeric only in v1)."},
				{Name: "prior", Type: "enum", Required: false, Default: "nig", EnumValues: []string{"nig"}, Description: "Prior family; only the conjugate Normal-Inverse-Gamma is shipped in v1."},
				{Name: "prior_mu", Type: "list", Required: false, Description: "Prior mean vector for coefficients."},
				{Name: "prior_precision", Type: "float", Required: false, Description: "Prior precision scalar."},
				{Name: "prior_shape", Type: "float", Required: false, Description: "Inverse-gamma shape parameter for residual variance."},
				{Name: "prior_rate", Type: "float", Required: false, Description: "Inverse-gamma rate parameter for residual variance."},
				{Name: "credible_level", Type: "float", Required: false, Default: 0.95, Description: "Posterior credible-interval mass."},
				{Name: "conf_level", Type: "float", Required: false, Default: 0.95, Description: "Coverage of ci_lower / ci_upper, the posterior Student-t interval on df = 2·a_n; falls back to credible_level when unset, so the bounds then equal credible_intervals."},
				{Name: "vcov", Type: "bool", Required: false, Default: false, Description: "Emit the POSTERIOR covariance of the coefficients, b_n/(a_n−1)·Λ_n⁻¹ (null when a_n ≤ 1), and its correlation, keyed (intercept) then predictors. Its diagonal is not std_errors² (those are the marginal-t scales). Under a robust covariance it is the sandwich posterior's instead, √diag = std_errors."},
				{Name: "covariance", Type: "object", Required: false, Description: "{type, cluster, order_by, lags}: keeps the posterior-mean coefficients but REPLACES the posterior covariance with Müller's (2013) sandwich posterior N(μ_n, Λ_n⁻¹·M·Λ_n⁻¹) behind std_errors, credible_intervals, ci_lower / ci_upper (normal, no df) and vcov. type classical (absent: the NIG posterior), hc0–hc3, cluster (CR1 over cluster's levels, ≥2), hac (Newey–West over order_by, unique per row; lags an integer or \"auto\"). Forces the buffered path; echoes as covariance."},
			},
			// No resample / selection modifiers: validateBayesLinearSpec
			// refuses both with PROCESSING_CONFIG (the posterior already
			// carries the uncertainty), so advertising them would invite
			// a request that can only fail.
		},
	}
}
