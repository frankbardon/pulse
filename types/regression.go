package types

// RegressionType identifies a specific regression-modeling operator.
//
// Pulse exposes three top-level regression operators that cover the
// thirteen textbook regression variants through a small, composable
// core:
//
//   - REG_OLS — ordinary least squares; optional l1/l2/elasticnet
//     penalty, simple/multiple predictors, polynomial features supplied
//     upstream by FEAT_POLY for polynomial regression.
//   - REG_GLM — generalized linear model; Family ∈ {binomial, poisson,
//     gamma} with matching Link. Covers logistic and Poisson regression.
//   - REG_BAYES_LINEAR — Bayesian linear regression via conjugate
//     Normal-Inverse-Gamma prior.
//
// Two spec-level modifiers compose with any of the three: `Resample`
// (jackknife / bootstrap) and `Selection` (forward / backward /
// stepwise) plus the matching information criterion (AIC / BIC).
//
// Per-operator semantics, modifier behavior, and the 13-name → spec
// mapping live in skills/regression-modeling.md.
type RegressionType string

const (
	// REG_OLS fits ordinary least squares with optional regularization.
	// Streams over sufficient statistics (n, Σx, Σy, XᵀX, Xᵀy, Σy²) when
	// Penalty is empty; the same Gram matrix plus a regularized
	// finalize-solve handles the l1/l2/elasticnet variants.
	REG_OLS RegressionType = "REG_OLS"

	// REG_GLM fits a generalized linear model via IRLS. Always buffered:
	// Newton-Raphson needs multiple passes over the data.
	REG_GLM RegressionType = "REG_GLM"

	// REG_BAYES_LINEAR fits a Bayesian linear model under a conjugate
	// Normal-Inverse-Gamma prior. Streams the same sufficient statistics
	// as REG_OLS, then applies the closed-form posterior update.
	REG_BAYES_LINEAR RegressionType = "REG_BAYES_LINEAR"
)

// AllRegressionTypes returns every defined regression operator in
// alphabetical order.
func AllRegressionTypes() []RegressionType {
	return []RegressionType{
		REG_BAYES_LINEAR,
		REG_GLM,
		REG_OLS,
	}
}

// RegressionSpec describes a single regression operator entry in a
// Request. Every field of every variant is declared up front; engines
// populate or ignore branches based on Type and modifiers.
//
// The wire shape is shared across the three operator families so a
// request parser only needs to dispatch on Type. Unused branches stay
// at their zero values.
type RegressionSpec struct {
	// Type names the regression operator (REG_OLS, REG_GLM,
	// REG_BAYES_LINEAR).
	Type RegressionType `json:"type"`

	// Name is an optional alias for the result entry; defaults to a
	// synthesized name derived from Type and Target.
	Name string `json:"name,omitempty"`

	// Target is the response variable's field name. Required.
	Target string `json:"target"`

	// Predictors lists the predictor (independent variable) field
	// names. At least one is required. A single predictor produces
	// simple / linear regression; multiple predictors produce multiple
	// linear regression.
	Predictors []string `json:"predictors,omitempty"`

	// Penalty selects the regularization scheme for REG_OLS. One of
	// "" (no penalty), "l1" (lasso), "l2" (ridge), or "elasticnet".
	// Ignored by REG_GLM and REG_BAYES_LINEAR.
	Penalty string `json:"penalty,omitempty"`

	// Alpha is the regularization strength for l1 / l2 / elasticnet
	// penalties. Must be > 0 when Penalty is non-empty.
	Alpha float64 `json:"alpha,omitempty"`

	// L1Ratio is the elastic-net mixing parameter in [0, 1]; 0 is pure
	// l2, 1 is pure l1. Only read when Penalty == "elasticnet".
	L1Ratio float64 `json:"l1_ratio,omitempty"`

	// Family is the GLM error family. One of "binomial" (logistic),
	// "poisson", or "gamma". Required for REG_GLM.
	Family string `json:"family,omitempty"`

	// Link is the GLM link function. Defaults are family-specific
	// ("logit" for binomial, "log" for poisson, "inverse" for gamma —
	// the only gamma link implemented today).
	Link string `json:"link,omitempty"`

	// MaxIters caps the IRLS / coordinate-descent iteration count for
	// iterative fits. Defaults to a registry-specific value when zero.
	MaxIters int `json:"max_iters,omitempty"`

	// Tol is the relative convergence tolerance for iterative fits.
	// Defaults to a registry-specific value when zero.
	Tol float64 `json:"tol,omitempty"`

	// Prior names the prior family for REG_BAYES_LINEAR. Only "nig"
	// (Normal-Inverse-Gamma, conjugate) is supported in v1.
	Prior string `json:"prior,omitempty"`

	// PriorMu is the prior mean vector for the coefficients
	// (REG_BAYES_LINEAR). Length must be len(Predictors)+1 with the
	// intercept FIRST, then one entry per predictor in Predictors
	// order; zero vector when nil.
	PriorMu []float64 `json:"prior_mu,omitempty"`

	// PriorPrecision is the prior precision scalar (REG_BAYES_LINEAR).
	PriorPrecision float64 `json:"prior_precision,omitempty"`

	// PriorShape and PriorRate parameterize the inverse-gamma prior on
	// the residual variance (REG_BAYES_LINEAR).
	PriorShape float64 `json:"prior_shape,omitempty"`
	PriorRate  float64 `json:"prior_rate,omitempty"`

	// CredibleLevel is the credible-interval mass for the posterior
	// summaries (REG_BAYES_LINEAR). Defaults to 0.95 when zero.
	CredibleLevel float64 `json:"credible_level,omitempty"`

	// Resample selects an orthogonal resampling layer applied to any
	// regression family. One of "" (none), "jackknife" (leave-one-out),
	// or "bootstrap". Non-empty forces the buffered path.
	Resample string `json:"resample,omitempty"`

	// BootstrapIters is the bootstrap replicate count when
	// Resample == "bootstrap". Ignored otherwise.
	BootstrapIters int `json:"bootstrap_iters,omitempty"`

	// RNGSeed seeds the bootstrap RNG for reproducibility. Defaults to
	// 0 (a deterministic seed derived from the request).
	RNGSeed int64 `json:"rng_seed,omitempty"`

	// Selection requests an automated subset-selection wrapper around
	// any regression family. One of "" (none), "forward", "backward",
	// or "stepwise". Non-empty forces the buffered path.
	Selection string `json:"selection,omitempty"`

	// Criterion is the information criterion driving Selection. One of
	// "aic" or "bic". Required when Selection is non-empty.
	Criterion string `json:"criterion,omitempty"`

	// Vcov opts this regression into the coefficient covariance matrix
	// (RegressionResult.Vcov) and its diag-scaled correlation
	// (RegressionResult.Correlation). Off by default, so a fit without
	// it is byte-identical on the wire. REG_OLS (unpenalized: σ̂²·(XᵀX)⁻¹;
	// ridge: the sandwich), REG_GLM ((XᵀWX)⁻¹ at the converged weights)
	// and REG_BAYES_LINEAR (the POSTERIOR covariance b_n/(a_n−1)·Λ_n⁻¹)
	// support it. Under Resample it is the replicate covariance (jackknife
	// (n−1)/n·Σ(β₋ᵢ−β̄)(β₋ᵢ−β̄)ᵀ, bootstrap the B−1 sample covariance of
	// the successful replicates); under Selection the final refit's
	// covariance keyed by the selected predictors — so √diag always
	// equals StdErrors. Lasso / elastic net refuse it, alone or under a
	// modifier, with PROCESSING_REGRESSION_VCOV_UNSUPPORTED.
	Vcov bool `json:"vcov,omitempty"`

	// Weight is the per-slot weight override: absent inherits
	// Request.Weight, then pulse.Options.DefaultWeight; `null` opts the
	// slot out (it runs unweighted); a field-name string or a
	// {field, kind} object sets the slot's own weight. See SlotWeight.
	Weight SlotWeight `json:"weight,omitzero"`
}

// RegressionResult is the per-spec outcome embedded in
// Response.Regressions. Fields irrelevant to a given operator carry
// their zero value; engines never partially populate a result on
// failure.
//
// Locked at Phase 0: every field of every operator family is declared
// now. Later phases populate the engine-specific branches but never
// change the struct shape.
type RegressionResult struct {
	// Name echoes RegressionSpec.Name or the synthesized default.
	Name string `json:"name,omitempty"`

	// Type is the regression operator that produced this result.
	Type RegressionType `json:"type"`

	// Family echoes RegressionSpec.Family (GLM only).
	Family string `json:"family,omitempty"`

	// Link echoes RegressionSpec.Link (GLM only).
	Link string `json:"link,omitempty"`

	// Penalty echoes RegressionSpec.Penalty (REG_OLS only).
	Penalty string `json:"penalty,omitempty"`

	// Alpha echoes RegressionSpec.Alpha (regularized OLS only).
	Alpha float64 `json:"alpha,omitempty"`

	// L1Ratio echoes RegressionSpec.L1Ratio (elastic-net OLS only).
	L1Ratio float64 `json:"l1_ratio,omitempty"`

	// Prior echoes RegressionSpec.Prior (Bayes only).
	Prior string `json:"prior,omitempty"`

	// Resample echoes RegressionSpec.Resample when set.
	Resample string `json:"resample,omitempty"`

	// Selection echoes RegressionSpec.Selection when set.
	Selection string `json:"selection,omitempty"`

	// Criterion echoes RegressionSpec.Criterion when Selection is set.
	Criterion string `json:"criterion,omitempty"`

	// Coefficients maps predictor name (including the synthesized
	// "(intercept)" key) to its fitted scalar value.
	Coefficients map[string]float64 `json:"coefficients,omitempty"`

	// StdErrors maps predictor name to its asymptotic standard error.
	StdErrors map[string]float64 `json:"std_errors,omitempty"`

	// PValues maps predictor name to its two-sided p-value under the
	// null β = 0.
	PValues map[string]float64 `json:"p_values,omitempty"`

	// R2 is the coefficient of determination (REG_OLS / REG_BAYES_LINEAR).
	R2 float64 `json:"r2,omitempty"`

	// AdjR2 is the adjusted R² (REG_OLS / REG_BAYES_LINEAR).
	AdjR2 float64 `json:"adj_r2,omitempty"`

	// Deviance is the model deviance (REG_GLM).
	Deviance float64 `json:"deviance,omitempty"`

	// NullDeviance is the deviance of the intercept-only model
	// (REG_GLM).
	NullDeviance float64 `json:"null_deviance,omitempty"`

	// PseudoR2 is a McFadden-style 1 − Deviance/NullDeviance (REG_GLM).
	PseudoR2 float64 `json:"pseudo_r2,omitempty"`

	// NObs is the number of observations used to fit the model — the
	// raw row count, weighted or not (a row whose weight is invalid or
	// zero contributes nothing and is not counted).
	NObs int `json:"n_obs,omitempty"`

	// SumWeights is Σw over the rows the fit used; present only on a
	// weighted fit (absence means unweighted). Point estimates read the
	// weights; standard errors, degrees of freedom and adjusted R² read
	// the inference sample size N* — SumWeights under weight kind
	// frequency, NEff under kind probability.
	SumWeights float64 `json:"sum_weights,omitempty"`

	// NEff is Kish's effective sample size (Σw)²/Σw² of the rows the
	// fit used; present only on a fit weighted under kind probability.
	NEff float64 `json:"n_eff,omitempty"`

	// ResidualStdErr is the residual standard error
	// (REG_OLS / REG_BAYES_LINEAR).
	ResidualStdErr float64 `json:"residual_std_err,omitempty"`

	// ConvergedIters is the IRLS / coordinate-descent iteration count
	// that produced the final estimate (iterative fits only). Zero
	// when the fit is closed-form (REG_OLS without penalty,
	// REG_BAYES_LINEAR).
	ConvergedIters int `json:"converged_iters,omitempty"`

	// SelectedFeatures lists the predictors retained by Selection (in
	// fit order); empty when Selection is unset.
	SelectedFeatures []string `json:"selected_features,omitempty"`

	// CredibleIntervals maps predictor name to its
	// [lower, upper] posterior credible interval (REG_BAYES_LINEAR).
	CredibleIntervals map[string][2]float64 `json:"credible_intervals,omitempty"`

	// Vcov is the coefficient covariance matrix, present only when the
	// spec set Vcov: square symmetric, full encoding, row / column keys
	// "(intercept)" then the predictors in spec order. On REG_OLS and
	// REG_GLM its diagonal is exactly StdErrors² (the variances the
	// standard errors are the square roots of), on the same inference
	// basis — N* (Σw under frequency, n_eff under probability) on a
	// weighted fit. On REG_BAYES_LINEAR it is the posterior covariance
	// b_n/(a_n−1)·Λ_n⁻¹ (null when a_n ≤ 1), whereas StdErrors are the
	// marginal-t scales √(b_n/a_n·(Λ_n⁻¹)_jj). An undefined entry is null.
	Vcov *MatrixValues `json:"vcov,omitempty"`

	// Correlation is Vcov scaled to unit diagonal (R's cov2cor), present
	// exactly when Vcov is.
	Correlation *MatrixValues `json:"correlation,omitempty"`
}

// Streamable reports whether a regression type can run via the
// streaming execution path in isolation, ignoring spec-level modifiers
// like Resample or Selection. REG_OLS and REG_BAYES_LINEAR stream over
// sufficient statistics; REG_GLM always needs IRLS and therefore the
// buffered path.
//
// Use RegressionSpec.Streamable() instead of this type-level method
// when checking a concrete request: the spec-level helper downgrades
// streamability whenever Resample or Selection is set, mirroring the
// runtime gate.
//
// Default branch returns false so newly-added regression types must
// opt in.
func (t RegressionType) Streamable() bool {
	switch t {
	case REG_OLS, REG_BAYES_LINEAR:
		return true
	case REG_GLM:
		return false
	}
	return false
}

// Streamable reports whether this concrete RegressionSpec can run via
// the streaming execution path today. The check folds the type-level
// Streamable() value with one layer:
//
//   - modifier downgrade: any non-empty Resample or Selection forces
//     the buffered path.
//
// REG_OLS streams both unpenalized (Phase 1) and penalized
// (l1 / l2 / elasticnet, Phase 2) — the streaming Gram is identical;
// the iterative solver runs at finalize over a p×p matrix, not over
// rows. REG_BAYES_LINEAR (Phase 4) streams the same Welford sufficient
// stats and applies the conjugate Normal-Inverse-Gamma posterior at
// finalize.
func (s RegressionSpec) Streamable() bool {
	if !s.Type.Streamable() {
		return false
	}
	if s.Resample != "" {
		return false
	}
	if s.Selection != "" {
		return false
	}
	switch s.Type {
	case REG_OLS:
		// Penalized fits (Phase 2) share the unpenalized fit's streaming
		// sufficient statistics — the regularized iteration happens at
		// finalize over the Gram, not over rows. Both branches stream.
		return true
	case REG_BAYES_LINEAR:
		// Phase 4: conjugate-NIG posterior consumes the same streaming
		// sufficient statistics as REG_OLS plus a finalize-time prior
		// update. No iteration over rows.
		return true
	}
	return false
}
