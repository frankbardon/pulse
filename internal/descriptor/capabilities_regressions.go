package descriptor

import (
	"slices"

	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/reglink"
	"github.com/frankbardon/pulse/internal/regsolve"
	"github.com/frankbardon/pulse/types"

	"github.com/frankbardon/pulse/descriptor"
)

// regressionCapabilities returns metadata for every entry in
// types.AllRegressionTypes(). Order is irrelevant; manifest assembly
// sorts by Name.
// holdoutParamDescription is the manifest description of the shared
// `holdout` slot (every built-in, and an extension model with a scorer).
const holdoutParamDescription = "Boolean expression over the built record (schema, features, attributes, windows, post-window attributes): true rows are held out of the fit and scored after it, null rows join neither. Adds holdout {n, n_null, rmse, mae, r2} on the response scale; forces the buffered path."

// marginsParamDescription is the manifest description of the margins
// block shared by REG_OLS and REG_GLM.
const marginsParamDescription = "{type, terms, scale}: adds margins, the marginal effect of each term with a delta-method std error on the fit's own covariance (classical or covariance's robust one), statistic, p-value and interval at conf_level. type ame (default; the weighted mean over the fitted rows of dmu/dx, marginaleffects avg_slopes) or mem (at the weighted predictor means, echoed as at); terms default every predictor; scale response (default, dmu/dx) or link (deta/dx = the coefficient). Refused (PROCESSING_CONFIG, details.requires) beside penalty, selection, resample or bounds, and when a predictor is a derived column (no chain rule through it). A multiplicity block corrects the effects with the coefficients."

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

	// sharedSlotParams are the top-level RegressionSpec keys every
	// built-in reads beside its own knobs: the slot weight, the
	// multiplicity block (only an engine that emits p-values — the
	// REG_BAYES_LINEAR posterior has none, and its block is refused
	// PULSE_MULTIPLICITY_INVALID), the holdout predicate and the
	// model-file save (every built-in is scorable) and the result name. On a scoped instance a key whose
	// capability is hidden drops from the list (assembleManifest), as
	// the payload schema drops it.
	sharedSlotParams := func(weight string, pValues bool) []descriptor.Param {
		out := []descriptor.Param{
			{Name: "weight", Type: "object", Required: false, Description: weight + "; absent inherits the request's, null opts out.", Slot: true},
		}
		if pValues {
			out = append(out, descriptor.Param{Name: "multiplicity", Type: "object", Required: false, Description: "{method, family, alpha, coefficients}: corrects the coefficient p_values; adds p_adjusted / significant_adjusted.", Slot: true})
		}
		return append(out,
			descriptor.Param{Name: "holdout", Type: "string", Required: false, Description: holdoutParamDescription, Slot: true},
			descriptor.Param{Name: "save", Type: "string", Required: false, Description: "Write the finished fit as a model file (.pulse-model.json).", Slot: true},
			descriptor.Param{Name: "name", Type: "string", Required: false, Description: "Result alias; also the saved model's name (default: the save file's stem).", Slot: true},
		)
	}

	builtins := []descriptor.RegressionMeta{
		{
			Name:           string(types.REG_OLS),
			Description:    "Ordinary least squares with optional l1/l2/elasticnet regularization; covers simple, multiple, ridge, lasso, and elastic-net regression.",
			AcceptsTypes:   acceptsTypes(types.REG_OLS),
			EmitsTypeNote:  "RegressionResult with coefficients, std errors, p-values, ci_lower / ci_upper (t on df), df, conf_level, R², adjusted R², residual std error; l2: the closed-form ridge sandwich SEs; l1 / elasticnet without resample: std errors, p-values and intervals null on every coefficient plus advisories [PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED]; bounds whose constraint binds without resample: the same nulls (vcov entries too) plus advisories [PULSE_ADVISORY_CONSTRAINT_ACTIVE]; a robust covariance adds its covariance echo; margins adds the marginal effects.",
			Streamable:     types.REG_OLS.Streamable(),
			StreamableHint: commonModifierHint + " A robust covariance (any type but classical) forces it too.",
			Params: append([]descriptor.Param{
				{Name: "target", Type: "field", NamesColumn: true, Required: true, FieldFilter: "numeric", Description: "Response variable field name.", Slot: true},
				{Name: "predictors", Type: "list", NamesColumn: true, Required: true, Description: "Predictor field names (numeric only in v1).", Slot: true},
				{Name: "penalty", Type: "enum", Required: false, Default: "", EnumValues: []string{"", "elasticnet", "l1", "l2"}, Description: "Regularization scheme; empty for OLS. Minimizes (1/2N*)·Σw·r² + alpha·[(1-l1_ratio)/2·||b||² + l1_ratio·||b||₁] (glmnet, standardize = FALSE; the same alpha meaning as REG_GLM): predictors unstandardized, intercept unpenalized, N* = Σw.", Slot: true},
				{Name: "alpha", Type: "float", Required: false, Description: "Regularization strength (>0 when penalty is set; glmnet's lambda).", Slot: true},
				{Name: "l1_ratio", Type: "float", Required: false, Description: "Elastic-net mixing parameter, 0 < l1_ratio < 1; only read when penalty == elasticnet (glmnet's alpha).", Slot: true},
				{Name: "unpenalized", Type: "list", NamesColumn: true, Required: false, Description: "penalty l2 only: predictors the ridge penalty skips (no alpha term on their Gram diagonal; glmnet's penalty.factor 0), each a listed predictor, once; the ridge sandwich SEs read the same system. l1 / elasticnet (and no penalty) refuse it (PROCESSING_CONFIG).", Slot: true},
				{Name: "bounds", Type: "object", Required: false, Description: "{nonnegative: [predictors]}: those slopes are constrained >= 0 (Lawson-Hanson non-negative least squares on the centred Gram; intercept and other slopes free), each a listed predictor, once. No bound binding = plain OLS bit for bit; a binding bound keeps the coefficients and R² but nulls every std error, p-value, interval and vcov entry, with advisories [PULSE_ADVISORY_CONSTRAINT_ACTIVE] — resample bootstrap refits the bounded model for replicate inference. A penalty, selection or robust covariance refuses it.", Slot: true},
				{Name: "intercept", Type: "bool", Required: false, Default: true, Description: "false fits through the origin: the intercept is pinned to 0 (its coefficient key stays, 0, with null std_error / p_value / interval), R² is the uncentred Σŷ²/Σy² and df N* − p, as lm(y ~ 0 + x). Unpenalized, unbounded, classical covariance only; not with selection, resample, holdout or solver normal (PROCESSING_CONFIG).", Slot: true},
				{Name: "solver", Type: "enum", Required: false, EnumValues: regsolve.Solvers(), Default: "qr", Description: "Least-squares method of an unpenalized, unbounded fit: qr (default) solves a streaming Givens QR of the design, at κ(X); normal solves the normal equations by Cholesky on the centred Gram, at κ(X)², the pre-alpha.35 method (reproduce older results). Refused with a penalty or bounds (PROCESSING_CONFIG).", Slot: true},
				{Name: "max_iters", Type: "int", Required: false, Description: "Coordinate-descent iteration cap for regularized fits.", Slot: true},
				{Name: "tol", Type: "float", Required: false, Description: "Relative convergence tolerance for regularized fits.", Slot: true},
				{Name: "resample", Type: "enum", Required: false, EnumValues: []string{"", "bootstrap", "jackknife"}, Description: "Orthogonal resampling layer; downgrades streaming.", Slot: true},
				{Name: "bootstrap_iters", Type: "int", Required: false, Default: 1000, Description: "Replicate count when resample == bootstrap, used as given; <= 0 means 1000. Echoed as bootstrap_requested beside bootstrap_succeeded (replicates whose refit succeeded).", Slot: true},
				{Name: "jackknife_groups", Type: "int", Required: false, Description: "resample jackknife only: delete-a-group jackknife over g groups (2 ≤ g ≤ fitted rows) drawn by rng_seed — g refits instead of one per row; SE √((g−1)/g·Σ(β₍ₖ₎−β̄)²), echoed as jackknife_groups. Exclusive with jackknife_by (PROCESSING_CONFIG); absent = leave-one-out over the fitted rows (a row the fit drops is no replicate).", Slot: true},
				{Name: "jackknife_by", Type: "field", NamesColumn: true, Required: false, Description: "resample jackknife only: delete-a-cluster jackknife, one replicate per distinct value of this field (≥ 2); a null value drops the row listwise. Exclusive with jackknife_groups.", Slot: true},
				{Name: "rng_seed", Type: "int", Required: false, Description: "Seed for the bootstrap RNG and the jackknife_groups assignment; 0 derives a deterministic seed from the request and its rows.", Slot: true},
				{Name: "selection", Type: "enum", Required: false, EnumValues: []string{"", "backward", "forward", "stepwise"}, Description: "Subset-selection wrapper; downgrades streaming.", Slot: true},
				{Name: "criterion", Type: "enum", Required: false, EnumValues: []string{"aic", "bic"}, Description: "Information criterion driving selection.", Slot: true},
				{Name: "conf_level", Type: "float", Required: false, Default: 0.95, Description: "Coverage of ci_lower / ci_upper, 0 < conf_level < 1: coefficient ∓ t·SE on the residual df (N* − p − 1 weighted); Wald normal under resample; intervals only where a std error is reported.", Slot: true},
				{Name: "vcov", Type: "bool", Required: false, Default: false, Description: "Emit the coefficient covariance (vcov) and its correlation as square matrices keyed (intercept) then predictors; √diag = std_errors. Unpenalized σ̂²(XᵀX)⁻¹ or the ridge sandwich; resample gives the replicate covariance, selection the final refit's; lasso / elasticnet refuse it (PROCESSING_REGRESSION_VCOV_UNSUPPORTED).", Slot: true},
				{Name: "margins", Type: "object", Required: false, Description: marginsParamDescription + " On REG_OLS every effect is its coefficient (identity link) with its standard error and t on df; streams with the fit.", Slot: true},
				{Name: "covariance", Type: "object", Required: false, Description: "{type, cluster, order_by, lags}: the estimator behind std_errors, p_values, ci_lower / ci_upper and vcov. type classical (absent: unchanged), hc0 / hc1 / hc2 / hc3 (heteroskedasticity-robust), cluster (CR1 over cluster's levels — categorical or unsigned-integer field, ≥2 levels — on df G − 1), hac (Newey–West over the rows ordered by order_by — date, datetime or numeric, unique per row — truncated at lags, an integer or \"auto\" = ⌊4(n/100)^(2/9)⌋). Robust types force the buffered path and echo as covariance; penalty / resample / selection refuse them (PROCESSING_REGRESSION_VCOV_UNSUPPORTED).", Slot: true},
			}, sharedSlotParams("{field, kind}: frequency or probability row weight (WLS; bootstrap / selection refuse one, PULSE_WEIGHT_UNSUPPORTED)", true)...),
			Modifiers: []descriptor.RegressionModifier{resampleModifier, selectionModifier},
		},
		{
			Name:           string(types.REG_GLM),
			Description:    "Generalized linear model via IRLS; supports binomial (logistic), poisson, gamma and negative_binomial (NB2, theta by maximum likelihood) families with family-appropriate links, and l1/l2/elasticnet regularization on binomial and poisson.",
			AcceptsTypes:   acceptsTypes(types.REG_GLM),
			EmitsTypeNote:  "RegressionResult with coefficients, std errors, p-values, ci_lower / ci_upper, conf_level, deviance, null deviance, McFadden pseudo-R² (binomial / poisson: Wald z at dispersion 1; gamma: dispersion estimated as Pearson χ²/df, SEs scaled by it, t p-values and intervals on df, which it emits; negative_binomial: Wald z at dispersion 1 and theta-hat, plus theta, theta_std_err (theta estimated), two_log_lik and aic); a penalty echoes penalty / alpha / l1_ratio (l1 / elasticnet without resample: std errors, p-values and intervals null on every coefficient plus advisories [PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED]); a robust covariance adds its covariance echo; margins adds the marginal effects.",
			Streamable:     types.REG_GLM.Streamable(),
			StreamableHint: "Always buffered; IRLS requires multiple passes over the data.",
			Params: append([]descriptor.Param{
				{Name: "target", Type: "field", NamesColumn: true, Required: true, FieldFilter: "numeric", Description: "Response variable field name.", Slot: true},
				{Name: "predictors", Type: "list", NamesColumn: true, Required: true, Description: "Predictor field names (numeric only in v1).", Slot: true},
				{Name: "family", Type: "enum", Required: true, EnumValues: slices.Sorted(slices.Values(reglink.Families())), Description: "GLM error family. negative_binomial is NB2 (Var = mu + mu^2/theta) for over-dispersed counts: theta estimated by maximum likelihood, alternating IRLS with a Newton step for theta (MASS glm.nb), unless theta fixes it; SEs at dispersion 1 and theta-hat (summary.negbin); penalty, selection and resample are refused (PROCESSING_CONFIG, details.requires).", Slot: true},
				{Name: "link", Type: "enum", Required: false, EnumValues: reglink.All(), EnumBy: "family", EnumValuesBy: reglink.ByFamily(), Description: "Link function, one of the family's (enum_values_by, default first): binomial logit (default), probit, cloglog; poisson log (default), identity, sqrt; gamma inverse (default), log, identity; negative_binomial log (default), identity, sqrt. Any other pair is PROCESSING_REGRESSION_INVALID_LINK. Off the canonical link IRLS is Fisher scoring (linear convergence: coefficients within ~1e-7 of the MLE at the default tol); vcov and SEs read the expected information, as R glm. An identity or sqrt link whose mean would leave its range halves the step.", Slot: true},
				{Name: "theta", Type: "float", Required: false, Description: "negative_binomial only: a fixed theta > 0, R's glm(family = negative.binomial(theta)) — IRLS for the coefficients only, no theta_std_err, aic without theta's parameter. Absent estimates theta. Any other family, or theta <= 0, is PROCESSING_CONFIG.", Slot: true},
				{Name: "penalty", Type: "enum", Required: false, Default: "", EnumValues: []string{"", "elasticnet", "l1", "l2"}, Description: "Regularization scheme (binomial / poisson; gamma and negative_binomial refuse it, PROCESSING_CONFIG); empty for the maximum-likelihood fit. Minimizes -loglik/N* + alpha·[(1-l1_ratio)/2·||b||² + l1_ratio·||b||₁] (glmnet, standardize = FALSE): predictors unstandardized, intercept unpenalized, N* = Σw. l2 keeps a separated fit finite.", Slot: true},
				{Name: "alpha", Type: "float", Required: false, Description: "Regularization strength (>0 when penalty is set; glmnet's lambda).", Slot: true},
				{Name: "l1_ratio", Type: "float", Required: false, Description: "Elastic-net mixing parameter, 0 < l1_ratio < 1; only read when penalty == elasticnet (glmnet's alpha).", Slot: true},
				{Name: "max_iters", Type: "int", Required: false, Description: "IRLS iteration cap.", Slot: true},
				{Name: "tol", Type: "float", Required: false, Description: "Relative convergence tolerance.", Slot: true},
				{Name: "resample", Type: "enum", Required: false, EnumValues: []string{"", "bootstrap", "jackknife"}, Description: "Orthogonal resampling layer.", Slot: true},
				{Name: "bootstrap_iters", Type: "int", Required: false, Default: 1000, Description: "Replicate count when resample == bootstrap, used as given; <= 0 means 1000. Echoed as bootstrap_requested beside bootstrap_succeeded (replicates whose refit succeeded).", Slot: true},
				{Name: "jackknife_groups", Type: "int", Required: false, Description: "resample jackknife only: delete-a-group jackknife over g groups (2 ≤ g ≤ fitted rows) drawn by rng_seed — g refits instead of one per row; SE √((g−1)/g·Σ(β₍ₖ₎−β̄)²), echoed as jackknife_groups. Exclusive with jackknife_by (PROCESSING_CONFIG); absent = leave-one-out over the fitted rows (a row the fit drops is no replicate).", Slot: true},
				{Name: "jackknife_by", Type: "field", NamesColumn: true, Required: false, Description: "resample jackknife only: delete-a-cluster jackknife, one replicate per distinct value of this field (≥ 2); a null value drops the row listwise. Exclusive with jackknife_groups.", Slot: true},
				{Name: "rng_seed", Type: "int", Required: false, Description: "Seed for the bootstrap RNG and the jackknife_groups assignment; 0 derives a deterministic seed from the request and its rows.", Slot: true},
				{Name: "selection", Type: "enum", Required: false, EnumValues: []string{"", "backward", "forward", "stepwise"}, Description: "Subset-selection wrapper.", Slot: true},
				{Name: "criterion", Type: "enum", Required: false, EnumValues: []string{"aic", "bic"}, Description: "Information criterion driving selection.", Slot: true},
				{Name: "conf_level", Type: "float", Required: false, Default: 0.95, Description: "Coverage of ci_lower / ci_upper, 0 < conf_level < 1: Wald normal coefficient ∓ z·SE (binomial / poisson, dispersion 1; any robust covariance or resample); gamma ∓ t·SE on df = N* − p − 1.", Slot: true},
				{Name: "vcov", Type: "bool", Required: false, Default: false, Description: "Emit the coefficient covariance (vcov, (XᵀWX)⁻¹ at the converged weights, dispersion 1 — gamma: φ̂·(XᵀWX)⁻¹, φ̂ the Pearson χ²/(N* − p − 1); l2: the penalized sandwich H⁻¹XᵀWXH⁻¹, H = XᵀWX + N*·alpha·D) and its correlation, keyed (intercept) then predictors; √diag = std_errors. Resample gives the replicate covariance, selection the final refit's; l1 / elasticnet refuse it (PROCESSING_REGRESSION_VCOV_UNSUPPORTED).", Slot: true},
				{Name: "margins", Type: "object", Required: false, Description: marginsParamDescription + " On REG_GLM the response-scale effect reads mu'(eta) per fitted row (one pass after IRLS); an identity link's, and every link-scale one, is the coefficient. t on df for gamma (classical), Wald z otherwise.", Slot: true},
				{Name: "covariance", Type: "object", Required: false, Description: "{type, cluster, order_by, lags}: the estimator behind std_errors, the Wald p_values, ci_lower / ci_upper and vcov — the sandwich over the IRLS scores at the converged weights (R sandwich on glm). type classical (absent: unchanged), hc0–hc3, cluster (CR1 over cluster's levels, ≥2), hac (Newey–West over order_by, unique per row; lags an integer or \"auto\"). Dispersion-free (gamma's estimate cancels, as in R sandwich); inference stays Wald z, cluster and gamma included. Echoes as covariance; resample / selection refuse it (PROCESSING_REGRESSION_VCOV_UNSUPPORTED).", Slot: true},
			}, sharedSlotParams("{field, kind}: frequency or probability row weight (IRLS prior weights; bootstrap / selection refuse one, PULSE_WEIGHT_UNSUPPORTED)", true)...),
			Modifiers: []descriptor.RegressionModifier{resampleModifier, selectionModifier},
		},
		{
			Name:           string(types.REG_BAYES_LINEAR),
			Description:    "Bayesian linear regression with a conjugate Normal-Inverse-Gamma prior; reports posterior means, std errors, and credible intervals.",
			AcceptsTypes:   acceptsTypes(types.REG_BAYES_LINEAR),
			EmitsTypeNote:  "RegressionResult with coefficients (posterior means), std errors, R², adjusted R², residual std error, credible_intervals, ci_lower / ci_upper (posterior t on df), df, conf_level; a robust covariance swaps in the sandwich posterior (normal intervals, no df) and adds its covariance echo.",
			Streamable:     types.REG_BAYES_LINEAR.Streamable(),
			StreamableHint: "Streamable; Resample and Selection are refused (PROCESSING_CONFIG). A robust covariance (any type but classical) forces the buffered path.",
			Params: append([]descriptor.Param{
				{Name: "target", Type: "field", NamesColumn: true, Required: true, FieldFilter: "numeric", Description: "Response variable field name.", Slot: true},
				{Name: "predictors", Type: "list", NamesColumn: true, Required: true, Description: "Predictor field names (numeric only in v1).", Slot: true},
				{Name: "prior", Type: "enum", Required: false, Default: "nig", EnumValues: []string{"nig"}, Description: "Prior family; only the conjugate Normal-Inverse-Gamma is shipped in v1.", Slot: true},
				{Name: "prior_mu", Type: "list", Required: false, Description: "Prior mean vector for coefficients, intercept first then one per predictor (length predictors + 1).", Slot: true},
				{Name: "prior_precision", Type: "any", Required: false, Default: 1e-3, Description: "Prior precision: a number eps (Lambda0 = eps·I; 0 = the default) or a list of predictors + 1 entries, intercept first like prior_mu (Lambda0 = diag): 0 is a flat prior on that coefficient (refused as RANK_DEFICIENT when the data then leave the posterior improper), a large entry pins it to its prior_mu entry. A wrong length or a negative entry is PROCESSING_CONFIG.", Slot: true},
				{Name: "prior_shape", Type: "float", Required: false, Description: "Inverse-gamma shape parameter for residual variance.", Slot: true},
				{Name: "prior_rate", Type: "float", Required: false, Description: "Inverse-gamma rate parameter for residual variance.", Slot: true},
				{Name: "credible_level", Type: "float", Required: false, Default: 0.95, Description: "Posterior credible-interval mass.", Slot: true},
				{Name: "conf_level", Type: "float", Required: false, Default: 0.95, Description: "Coverage of ci_lower / ci_upper, the posterior Student-t interval on df = 2·a_n; falls back to credible_level when unset, so the bounds then equal credible_intervals.", Slot: true},
				{Name: "vcov", Type: "bool", Required: false, Default: false, Description: "Emit the POSTERIOR covariance of the coefficients, b_n/(a_n−1)·Λ_n⁻¹ (null when a_n ≤ 1), and its correlation, keyed (intercept) then predictors. Its diagonal is not std_errors² (those are the marginal-t scales). Under a robust covariance it is the sandwich posterior's instead, √diag = std_errors.", Slot: true},
				{Name: "covariance", Type: "object", Required: false, Description: "{type, cluster, order_by, lags}: keeps the posterior-mean coefficients but REPLACES the posterior covariance with Müller's (2013) sandwich posterior N(μ_n, Λ_n⁻¹·M·Λ_n⁻¹) behind std_errors, credible_intervals, ci_lower / ci_upper (normal, no df) and vcov. type classical (absent: the NIG posterior), hc0–hc3, cluster (CR1 over cluster's levels, ≥2), hac (Newey–West over order_by, unique per row; lags an integer or \"auto\"). Forces the buffered path; echoes as covariance.", Slot: true},
			}, sharedSlotParams("{field, kind}: frequency row weight only (a probability one is PULSE_WEIGHT_UNSUPPORTED)", false)...),
			// No resample / selection modifiers: validateBayesLinearSpec
			// refuses both with PROCESSING_CONFIG (the posterior already
			// carries the uncertainty), so advertising them would invite
			// a request that can only fail.
		},
	}
	return append(builtins, modelRegressionMetas()...)
}
