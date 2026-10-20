package types

import "encoding/json"

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
// at their zero values. An extension REG_* model honours or refuses
// every field (declare or refuse): a new field needs its row in
// internal/descriptor/extensions_regression_slots.go, or
// TestExtensions_RegressionSharedSlotsRefused fails.
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

	// Penalty selects the regularization scheme for REG_OLS and REG_GLM
	// (binomial / poisson; gamma refuses it). One of "" (no penalty),
	// "l1" (lasso), "l2" (ridge), or "elasticnet". REG_BAYES_LINEAR
	// refuses it.
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
	// regression family. One of "" (none), "jackknife" (leave-one-out,
	// or delete-a-group with JackknifeGroups / JackknifeBy), or
	// "bootstrap". Non-empty forces the buffered path.
	Resample string `json:"resample,omitempty"`

	// JackknifeGroups turns the leave-one-out jackknife into a
	// delete-a-group one (Resample "jackknife" only): the fitted rows are
	// assigned to this many groups by a seeded random permutation
	// (RNGSeed; zero derives the seed as the bootstrap does), sizes
	// differing by at most one, and each replicate refits without one
	// group — g refits instead of n. 2 ≤ g ≤ the fitted row count; zero
	// is absent: leave-one-out over the fitted rows — a row the fit drops
	// (null target or predictor, zero or invalid weight) is no replicate
	// and no part of n. Exclusive with JackknifeBy.
	JackknifeGroups int `json:"jackknife_groups,omitempty"`

	// JackknifeBy names a field whose values are the jackknife's delete
	// groups (a delete-a-cluster jackknife; Resample "jackknife" only):
	// one replicate per distinct value. A row whose value is null drops
	// from the fit listwise, like a null predictor. Exclusive with
	// JackknifeGroups.
	JackknifeBy string `json:"jackknife_by,omitempty"`

	// BootstrapIters is the bootstrap replicate count when
	// Resample == "bootstrap", used as given; zero or negative means the
	// default 1,000. Echoed as RegressionResult.BootstrapRequested.
	// Ignored otherwise.
	BootstrapIters int `json:"bootstrap_iters,omitempty"`

	// RNGSeed seeds the bootstrap RNG, and the JackknifeGroups
	// assignment, for reproducibility. Defaults to 0: a deterministic
	// seed derived from the request and the rows the fit reads (never
	// the clock), so the same request over the same cohort is
	// bit-identical; pin non-zero to keep it across cohorts.
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
	// (g−1)/g·Σ(β₋ₖ−β̄)(β₋ₖ−β̄)ᵀ over its g replicates, g = n for
	// leave-one-out; bootstrap the B−1 sample covariance of
	// the successful replicates); under Selection the final refit's
	// covariance keyed by the selected predictors — so √diag always
	// equals StdErrors. Lasso / elastic net refuse it, alone or under a
	// modifier, with PROCESSING_REGRESSION_VCOV_UNSUPPORTED.
	Vcov bool `json:"vcov,omitempty"`

	// Covariance selects the coefficient covariance estimator behind
	// StdErrors, PValues, CILower / CIUpper / DF and Vcov (REG_OLS,
	// REG_GLM; REG_BAYES_LINEAR's sandwich posterior replaces its
	// posterior covariance): classical, hc0–hc3, cluster (CR1; G − 1 df on
	// REG_OLS) or hac (Newey–West).
	// Absent means classical, byte-identical on the wire. A robust type
	// forces the buffered path and is refused with
	// PROCESSING_REGRESSION_VCOV_UNSUPPORTED alongside a Penalty, a
	// Resample or a Selection. See RegressionCovariance.
	Covariance *RegressionCovariance `json:"covariance,omitempty"`

	// ConfLevel is the coverage of the coefficient confidence intervals
	// (RegressionResult.CILower / CIUpper), 0 < ConfLevel < 1; zero
	// selects 0.95. REG_OLS uses a Student-t critical value on the
	// residual degrees of freedom (N* − p − 1 on a weighted fit), REG_GLM
	// a Wald normal one, REG_BAYES_LINEAR the posterior Student-t
	// (credible_level is the fallback when this is unset, so the
	// intervals then equal CredibleIntervals). Under Resample the
	// intervals are Wald normal on the resampled standard errors.
	ConfLevel float64 `json:"conf_level,omitempty"`

	// Save writes the finished fit as a model file (model_format_version
	// 1, or 2 when a robust Covariance produced its standard errors;
	// `.pulse-model.json`) at this path on the instance filesystem,
	// relative to its root, atomically (temp + fsync + rename; parent
	// directories are created). Overwriting an existing file succeeds
	// with the PULSE_MODEL_OVERWRITTEN warning. The path must end in
	// `.pulse-model.json` and stay under the root
	// (PULSE_MODEL_SAVE_INVALID); a failed write is
	// PULSE_MODEL_WRITE_FAILED. A `return` never skips a regression
	// that saves. Gated by capability:model_save. Empty writes nothing.
	Save string `json:"save,omitempty"`

	// Weight is the per-slot weight override: absent inherits
	// Request.Weight, then pulse.Options.DefaultWeight; `null` opts the
	// slot out (it runs unweighted); a field-name string or a
	// {field, kind} object sets the slot's own weight. See SlotWeight.
	Weight SlotWeight `json:"weight,omitzero"`

	// Multiplicity opts this regression's coefficient p-values into a
	// multiple-comparison family (see RegressionMultiplicity). Absent
	// means the regression joins no family — a request-level or
	// instance-default block never reaches a regression that carries no
	// block of its own — so a request without one is byte-identical.
	// Gated by capability:multiplicity.
	Multiplicity *RegressionMultiplicity `json:"multiplicity,omitempty"`

	// Params is the model's own configuration object, read by an
	// extension regression (pulse.RegressionRegistration.Params declares
	// its keys; the factory sees it on the spec). A key the registration
	// does not declare is refused PULSE_REQUEST_UNKNOWN_FIELD at
	// regressions[i].params.<key>, at predict and at runtime alike. The
	// built-ins take every knob as a typed field above and declare no
	// params, so any params key on REG_OLS / REG_GLM / REG_BAYES_LINEAR
	// is refused. Absent is byte-identical.
	Params json.RawMessage `json:"params,omitempty"`
}

// RegressionMultiplicity is a regression's own multiple-comparison
// block: the Multiplicity fields plus the coefficients that join the
// family. Setting it is what makes the regression a member; its Method,
// Family and Alpha each fall through on their own like any slot's
// (slot → request → ComposedRequest → pulse.Options.
// DefaultMultiplicity → none), the family defaulting to `request`
// (pooled with the request's tests, post-tests, `request`-family
// overlays and matrix specs); `compose` is offered inside Compose.
// Alpha is the level RegressionResult.SignificantAdjusted compares
// against (0.05 when nothing sets one). REG_BAYES_LINEAR emits no
// p-values, so a block resolving to any method but none is refused
// (PULSE_MULTIPLICITY_INVALID).
type RegressionMultiplicity struct {
	// Method is the correction procedure (MultiplicityMethod*). Empty
	// inherits.
	Method MultiplicityMethod `json:"method,omitempty"`

	// Family is `request` (the default) or, inside Compose, `compose`.
	// Empty inherits, then takes `request`.
	Family MultiplicityFamily `json:"family,omitempty"`

	// Alpha is the significance level SignificantAdjusted compares
	// against, in (0, 1); zero inherits, then DefaultMultiplicityAlpha.
	Alpha float64 `json:"alpha,omitempty"`

	// Coefficients names the coefficients (keys of
	// RegressionResult.PValues: a predictor, or "(intercept)") whose
	// p-values join the family. Empty means every non-intercept
	// predictor that has a p-value. A name that is neither a predictor
	// nor "(intercept)", or a repeated name, is refused
	// (PULSE_MULTIPLICITY_INVALID, key "coefficients"). A named
	// coefficient the fit reports no p-value for (dropped by Selection)
	// stays a member with an undefined p: excluded from m, its
	// p_adjusted null.
	Coefficients []string `json:"coefficients,omitempty"`
}

// Block returns the {method, family, alpha} half of m as a
// Multiplicity (nil for a nil m), the shape every resolver rule reads.
func (m *RegressionMultiplicity) Block() *Multiplicity {
	if m == nil {
		return nil
	}
	return &Multiplicity{Method: m.Method, Family: m.Family, Alpha: m.Alpha}
}

// RegressionResult is the per-spec outcome embedded in
// Response.Regressions. Fields irrelevant to a given operator carry
// their zero value; engines never partially populate a result: a fit
// that fails on its data carries Name / Type and Error only.
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

	// Penalty echoes RegressionSpec.Penalty (REG_OLS / REG_GLM).
	Penalty string `json:"penalty,omitempty"`

	// Alpha echoes RegressionSpec.Alpha (regularized OLS / GLM).
	Alpha float64 `json:"alpha,omitempty"`

	// L1Ratio echoes RegressionSpec.L1Ratio (elastic-net OLS / GLM).
	L1Ratio float64 `json:"l1_ratio,omitempty"`

	// Prior echoes RegressionSpec.Prior (Bayes only).
	Prior string `json:"prior,omitempty"`

	// Resample echoes RegressionSpec.Resample when set.
	Resample string `json:"resample,omitempty"`

	// BootstrapRequested echoes the bootstrap replicate count the fit
	// ran (Resample "bootstrap" only): RegressionSpec.BootstrapIters as
	// given, or the default 1,000 when that is zero or negative.
	BootstrapRequested int `json:"bootstrap_requested,omitempty"`

	// BootstrapSucceeded counts the replicates whose refit succeeded —
	// the ones StdErrors, PValues and Vcov are computed over. A replicate
	// whose refit fails (e.g. rank deficiency on an unlucky resample) is
	// skipped, so BootstrapSucceeded < BootstrapRequested exactly when
	// some failed. Bootstrap only.
	BootstrapSucceeded int `json:"bootstrap_succeeded,omitempty"`

	// JackknifeGroups is the delete-a-group jackknife's replicate count
	// g — RegressionSpec.JackknifeGroups, or the distinct JackknifeBy
	// values among the fitted rows. Absent on a leave-one-out jackknife
	// and every other fit.
	JackknifeGroups int `json:"jackknife_groups,omitempty"`

	// Selection echoes RegressionSpec.Selection when set.
	Selection string `json:"selection,omitempty"`

	// Criterion echoes RegressionSpec.Criterion when Selection is set.
	Criterion string `json:"criterion,omitempty"`

	// Coefficients maps predictor name (including the synthesized
	// "(intercept)" key) to its fitted scalar value.
	Coefficients map[string]float64 `json:"coefficients,omitempty"`

	// StdErrors maps predictor name to its asymptotic standard error.
	// On an l1 / elasticnet fit without Resample every entry (the
	// intercept included) is NaN — null on the wire, key kept: the
	// penalty selects the predictors from the data, so no analytical
	// standard error is valid (Advisories says so).
	StdErrors map[string]float64 `json:"std_errors,omitempty"`

	// PValues maps predictor name to its two-sided p-value under the
	// null β = 0; null on every key exactly where StdErrors is.
	PValues map[string]float64 `json:"p_values,omitempty"`

	// Statistics maps coefficient name to its test statistic. Only an
	// extension regression (pulse.RegressionRegistration) reports it —
	// the built-ins leave it absent. On an extension fit StdErrors,
	// Statistics and PValues each carry every coefficient key, a figure
	// the model did not compute null (the table stays rectangular).
	Statistics map[string]float64 `json:"statistics,omitempty"`

	// CILower and CIUpper map predictor name (keyed like Coefficients) to
	// the lower / upper bound of the coefficient's two-sided confidence
	// interval at ConfLevel: coefficient ∓ q·StdErrors, q the Student-t
	// critical value on DF (REG_OLS; REG_GLM gamma, whose dispersion is
	// estimated; REG_BAYES_LINEAR's posterior t, where the bounds equal
	// CredibleIntervals at the same level) or the standard normal one
	// when DF is absent (REG_GLM binomial / poisson Wald; REG_GLM and
	// REG_BAYES_LINEAR under a robust covariance; any fit under
	// Resample). A key is present exactly when StdErrors carries it, so
	// a fit without standard errors carries no intervals. An undefined
	// bound (DF ≤ 0) is null.
	CILower map[string]float64 `json:"ci_lower,omitempty"`
	CIUpper map[string]float64 `json:"ci_upper,omitempty"`

	// DF is the degrees of freedom of the Student-t critical value the
	// intervals read: the residual df N* − p − 1 on REG_OLS (n − p − 1
	// unweighted; Σw or Kish n_eff in place of n on a weighted fit, so
	// fractional under kind probability) on REG_OLS and on a REG_GLM gamma
	// fit (the df its estimated dispersion Pearson χ²/DF and its Student-t
	// p-values read), 2·a_n on REG_BAYES_LINEAR. Absent when the
	// intervals are Wald normal; null when undefined.
	DF float64 `json:"df,omitempty"`

	// ConfLevel is the resolved interval coverage (RegressionSpec
	// ConfLevel, 0.95 by default); present exactly when CILower is.
	ConfLevel float64 `json:"conf_level,omitempty"`

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

	// Covariance echoes the robust estimator the standard errors,
	// p-values, intervals and Vcov came from (RegressionSpec.Covariance),
	// "auto" lags resolved; absent on a classical fit.
	Covariance *RegressionCovarianceResult `json:"covariance,omitempty"`

	// Outputs holds an extension regression's own named extras, each key
	// declared in its registration's OutputSchema; a non-finite float is
	// null. The built-ins leave it absent.
	Outputs map[string]any `json:"outputs,omitempty"`

	// PAdjusted maps each family-member coefficient (keyed like PValues)
	// to its multiplicity-adjusted p-value, present only when the spec's
	// own multiplicity block resolved to a method other than none. It
	// rides BESIDE PValues, which never move. A member whose p-value is
	// undefined (absent, or NaN) is excluded from the family's m and its
	// adjusted value is null.
	PAdjusted map[string]float64 `json:"p_adjusted,omitempty"`

	// SignificantAdjusted maps each member coefficient whose adjusted
	// p-value is defined to PAdjusted < the resolved multiplicity alpha
	// (Multiplicity.Alpha); a member with an undefined adjusted p has no
	// key here.
	SignificantAdjusted map[string]bool `json:"significant_adjusted,omitempty"`

	// Multiplicity echoes the correction that ran over the member
	// coefficients: method, family, alpha, the family size m and the
	// excluded (undefined-p) member count. Absent when none ran.
	Multiplicity *AppliedMultiplicity `json:"multiplicity,omitempty"`

	// Advisories lists the coded, non-blocking notes the analysis
	// choice raises on this fit (PULSE_ADVISORY_* codes, the ones
	// predict raises for the same spec): today
	// PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED on an l1 / elasticnet
	// fit without Resample, whose StdErrors, PValues, CILower and
	// CIUpper are null on every coefficient. Never a warning, never
	// escalated by Strict; a code in Options.SuppressAdvisories is
	// dropped. Absent when none applies.
	Advisories []Advisory `json:"advisories,omitempty"`

	// Error is the data-dependent refusal that failed this fit alone
	// (PROCESSING_REGRESSION_SEPARATION, _NO_CONVERGE, _RANK_DEFICIENT,
	// _INSUFFICIENT_DATA) while the rest of the response survived; see
	// SlotError. A result carrying one has Name / Type and the error
	// only: every coefficient and figure is absent on the wire.
	Error *SlotError `json:"error,omitempty"`
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
//   - modifier downgrade: any non-empty Resample or Selection, or a
//     robust Covariance, forces the buffered path.
//
// REG_OLS streams both unpenalized (Phase 1) and penalized
// (l1 / l2 / elasticnet, Phase 2) — the streaming Gram is identical;
// the iterative solver runs at finalize over a p×p matrix, not over
// rows. REG_BAYES_LINEAR (Phase 4) streams the same Welford sufficient
// stats and applies the conjugate Normal-Inverse-Gamma posterior at
// finalize.
//
// REG_OLS penalized fits (Phase 2) share the unpenalized fit's
// streaming sufficient statistics — the regularized iteration happens at
// finalize over the Gram, not over rows — and REG_BAYES_LINEAR's
// conjugate-NIG posterior consumes the same statistics plus a
// finalize-time prior update, so both stream whatever their penalty or
// prior.
func (s RegressionSpec) Streamable() bool {
	return s.StreamableAs(s.Type.Streamable())
}

// StreamableAs is Streamable for a spec whose type streams exactly when
// typeStreamable says so — an extension regression's declared form,
// which RegressionType.Streamable cannot know. The modifier downgrade is
// the same: any non-empty Resample or Selection, or a robust Covariance
// (a second pass for every fitted row's residual), forces the buffered
// path.
func (s RegressionSpec) StreamableAs(typeStreamable bool) bool {
	if !typeStreamable {
		return false
	}
	if s.Resample != "" || s.Selection != "" {
		return false
	}
	return !s.Covariance.Robust()
}
