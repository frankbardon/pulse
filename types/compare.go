package types

// FitComparisonSpec is one entry of ComposedRequest.Compare: a
// nested-model comparison between two regression fits of the batch's
// `fits` list, run once the fits have run (no record is read) — R's
// anova(m0, m1) / lmtest::waldtest(m0, m1). The Restricted fit's terms
// (its predictors, and its intercept when it fits one) must be a strict
// subset of the Full fit's, and the two fits must be taken on the same
// rows: their requests equal but for the regression's predictors,
// intercept and reporting knobs (name, vcov, conf_level, margins,
// collinearity, save, multiplicity) and the request's label, return,
// disable_components and multiplicity, both REG_OLS, both REG_GLM on
// one family and link, both REG_MULTINOMIAL (its terms the predictors,
// its reference equal; each tested term's K − 1 "<category>:<term>"
// coefficients tested) or both one model whose declaration carries
// Compare (REG_ORDINAL — its terms the predictors, its params.levels
// equal — or an embedder model, pulse.RegressionRegistration.Compare) —
// refused PULSE_COMPARISON_INVALID otherwise, at
// predict and at run alike — and, once fitted, on the same row count
// (PULSE_COMPARISON_ROWS_DIFFER, the comparison failing alone). Equal
// requests and nested terms make the full fit's rows a subset of the
// restricted fit's, so an equal count is the same rows.
//
// Two vector fits compare member-wise (U69 #643): the Restricted
// battery's members (each listed — `fields`, or a `vectors` entry with
// `fields`) a strict subset of the Full battery's, the requests equal
// but for the member lists and the battery's reporting knobs, no
// probability weight; each restricted member j is one test of the
// Gaussian lm(j ~ restricted others) against lm(j ~ full others), read
// exactly off the full fit's co-moment (FitComparisonResult.Members).
//
// Two SEM fits (a MAT_SEM fit request each, U69 #643) compare by the
// chi-square difference — lavaan's lavTestLRT: the plain difference of
// the fits' model χ² under ml / dwls, the Satorra–Bentler (2001) scaled
// difference under wlsmv. Their nesting is declared, never proven: the
// fit requests equal but for params.model, group_equal, group_partial
// and the reporting and solver knobs; the test lrt (the default); the
// restricted fit's degrees of freedom above the full fit's (judged at
// run, the comparison failing alone).
//
// The result is ComposedResponse.Comparisons, in declaration order. A
// nil / empty list is absent: the slot is `omitempty`, so a
// comparison-free request is byte-identical on the wire and under
// CanonicalHash. Gated by capability:compose_compare (which depends on
// capability:compose_fits: it reads the `fits`).
type FitComparisonSpec struct {
	// Name names the comparison in the response. Required and unique
	// among the comparisons.
	Name string `json:"name"`

	// Restricted is the `fits` entry name of the smaller model — the
	// null hypothesis that the Full model's extra coefficients are zero.
	Restricted string `json:"restricted"`

	// Full is the `fits` entry name of the larger model.
	Full string `json:"full"`

	// Test is the comparison's test (ComparisonTest*); empty selects R's
	// anova default: ComparisonTestF on REG_OLS and on a REG_GLM family
	// that estimates its dispersion (gamma; a fixed-θ negative binomial
	// under dispersion "estimated"), ComparisonTestLRT on every other
	// family, on REG_MULTINOMIAL (anova.multinom) and on a declared model
	// (REG_ORDINAL: anova.polr) — whose F test is refused (no estimated
	// dispersion).
	Test ComparisonTest `json:"test,omitempty"`

	// Multiplicity is the comparison's multiple-comparison correction:
	// absent inherits ComposedRequest.Multiplicity, then
	// pulse.Options.DefaultMultiplicity (a comparison is one hypothesis
	// test, inheriting like a TEST_* slot). The only family is
	// `compose` (the default): the comparisons pool with every other
	// compose-family member of the batch. Gated by
	// capability:multiplicity.
	Multiplicity *Multiplicity `json:"multiplicity,omitempty"`
}

// ComparisonTest names a nested-model comparison's test.
type ComparisonTest string

// The comparison tests.
const (
	// ComparisonTestF is the F test on the residual (deviance) change:
	// F = ((D₀ − D₁)/q)/φ̂₁ on (q, df₁), φ̂₁ the full fit's estimated
	// dispersion (σ̂² = RSS₁/df₁ on REG_OLS, the Pearson χ²/df₁ on a
	// REG_GLM family that estimates it) — R's anova(m0, m1) on lm and
	// anova(m0, m1, test = "F") on glm. Refused on a family that fixes
	// the dispersion (binomial, poisson, a negative binomial with θ
	// estimated or φ fixed).
	ComparisonTestF ComparisonTest = "f"

	// ComparisonTestLRT is the likelihood-ratio (deviance) test:
	// χ² = (D₀ − D₁)/φ₁ on q df, φ₁ = 1 on a fixed-dispersion family and
	// the full fit's estimate otherwise — R's anova(m0, m1, test =
	// "Chisq" | "LRT"); a negative binomial with θ estimated reads
	// 2ℓ₁ − 2ℓ₀, each model at its own θ̂ (MASS's anova.negbin).
	ComparisonTestLRT ComparisonTest = "lrt"

	// ComparisonTestWald is the Wald test of the full fit's extra
	// coefficients b_S against its own coefficient covariance V_SS (the
	// classical one, or the robust sandwich its `covariance` chose):
	// W = b_Sᵀ V_SS⁻¹ b_S — lmtest::waldtest(m0, m1, vcov = V). On the
	// coefficient table's reference distribution: F = W/q on (q, the
	// table's df) where the coefficient p-values are Student t (REG_OLS;
	// a classical REG_GLM gamma or estimated-dispersion fit), χ² = W on
	// q df otherwise.
	ComparisonTestWald ComparisonTest = "wald"
)

// AllComparisonTests returns every comparison test, in a stable order.
func AllComparisonTests() []ComparisonTest {
	return []ComparisonTest{ComparisonTestF, ComparisonTestLRT, ComparisonTestWald}
}

// ComparisonDistribution names the reference distribution of a
// comparison's statistic.
type ComparisonDistribution string

// The comparison reference distributions.
const (
	// ComparisonDistributionF is Snedecor's F on (DF, DFResidual).
	ComparisonDistributionF ComparisonDistribution = "f"
	// ComparisonDistributionChiSquare is the chi-square on DF.
	ComparisonDistributionChiSquare ComparisonDistribution = "chisq"
)

// FitComparisonResult is one ComposedResponse.Comparisons entry: a
// nested-model comparison's test, at full precision. Like `fits` it sits
// outside the Compose `return` — never rounded. The per-model figures
// are R's anova table columns (Res.Df, RSS / Resid. Dev).
//
// A comparison that fails on its data — a fit it reads failed on its
// data (the fit's own code, details.fit naming it), or the two fits'
// row counts differ (PULSE_COMPARISON_ROWS_DIFFER) — keeps its entry:
// Name, Restricted, Full, Test and the error, every figure absent on the
// wire (see SlotError), and a PULSE_SLOT_FAILED warning (slot_kind
// comparisons) lifts to the batch; under Strict the batch fails with the
// comparison's own code instead.
type FitComparisonResult struct {
	// Name, Restricted and Full echo the spec; Test is the resolved
	// test.
	Name       string         `json:"name"`
	Restricted string         `json:"restricted"`
	Full       string         `json:"full"`
	Test       ComparisonTest `json:"test"`

	// Distribution is the statistic's reference distribution.
	Distribution ComparisonDistribution `json:"distribution"`

	// Terms are the tested coefficients: the full fit's terms the
	// restricted fit lacks, in the full fit's order ("(intercept)"
	// first when only the full fit has one; on REG_MULTINOMIAL the added
	// predictors). DF = len(Terms), (K − 1)·len(Terms) on
	// REG_MULTINOMIAL.
	// Empty on a SEM comparison: DF is the df difference d₀ − d₁ there.
	Terms []string `json:"terms"`

	// Statistic is F (Distribution f) or χ² (chisq); null when
	// undefined (a residual df ≤ 0, or a Wald V_SS not positive
	// definite).
	Statistic float64 `json:"statistic"`

	// DF is the numerator degrees of freedom q: the number of tested
	// coefficients — of Terms, or K − 1 per term on REG_MULTINOMIAL
	// (every non-reference category's "<category>:<term>").
	DF float64 `json:"df"`

	// DFResidual is the F statistic's denominator df: the full fit's
	// residual df (F test), or the coefficient table's df (Wald F —
	// on a cluster-robust REG_OLS fit G − 1). Absent on a chi-square
	// test.
	DFResidual float64 `json:"df_residual,omitempty"`

	// PValue is P(statistic ≥ observed) under the null that the Terms
	// are zero; null where Statistic is.
	PValue float64 `json:"p_value"`

	// NObs is the row count both fits used (RegressionResult.NObs).
	NObs int `json:"n_obs"`

	// RestrictedDFResidual and FullDFResidual are each fit's residual
	// df N* − rank (N* = n, Σw under a frequency weight, Kish n_eff
	// under a probability weight; R's Res.Df) — on REG_MULTINOMIAL
	// (K − 1)·N* − rank, anova.multinom's Resid. df.
	RestrictedDFResidual float64 `json:"restricted_df_residual"`
	FullDFResidual       float64 `json:"full_df_residual"`

	// RestrictedDeviance and FullDeviance are each fit's residual
	// deviance: the (weighted) RSS on REG_OLS (R's deviance(lm)), the
	// RegressionResult.Deviance on REG_GLM (a negative binomial's at its
	// own θ).
	//
	// On a SEM comparison the residual df are each fit's model df and
	// the deviances each fit's model χ² against the saturated model
	// (lavaan's standard test statistic).
	RestrictedDeviance float64 `json:"restricted_deviance"`
	FullDeviance       float64 `json:"full_deviance"`

	// Dispersion is the φ the F / likelihood-ratio statistic is scaled
	// by: the full fit's σ̂² (REG_OLS) or Pearson estimate (a REG_GLM
	// family that estimates it), 1 on a fixed-dispersion family. Absent
	// on a Wald test and on a negative binomial with θ estimated (its
	// likelihood ratio reads TwoLogLik).
	Dispersion float64 `json:"dispersion,omitempty"`

	// RestrictedTwoLogLik and FullTwoLogLik are the fits' 2ℓ
	// (RegressionResult.TwoLogLik) where the likelihood ratio reads them
	// — a negative binomial with θ estimated; absent otherwise.
	RestrictedTwoLogLik float64 `json:"restricted_two_log_lik,omitempty"`
	FullTwoLogLik       float64 `json:"full_two_log_lik,omitempty"`

	// ScalingFactor is a SEM comparison's Satorra–Bentler (2001)
	// difference scaling factor c_d = (d₀c₀ − d₁c₁)/(d₀ − d₁), each c the
	// fit's mean-adjusted scaling factor tr(UΓ)/df — present only on two
	// WLSMV fits (Statistic is then (T₀ − T₁)/c_d), null where c_d < 0
	// (lavaan's NA; Statistic and PValue null too); absent on every other
	// comparison.
	ScalingFactor float64 `json:"scaling_factor,omitempty"`

	// PAdjusted is the multiplicity-adjusted p-value, present only when
	// the comparison's resolved correction is not none; it rides BESIDE
	// PValue, which never moves. SignificantAdjusted is PAdjusted <
	// Multiplicity.Alpha (absent when PAdjusted is null); Multiplicity
	// echoes the correction (family `compose`).
	PAdjusted           *float64             `json:"p_adjusted,omitempty"`
	SignificantAdjusted *bool                `json:"significant_adjusted,omitempty"`
	Multiplicity        *AppliedMultiplicity `json:"multiplicity,omitempty"`

	// Members are a comparison of two vector fits' per-member tests
	// (U69 #643): one row per restricted battery member j, in the
	// restricted battery's order — the Gaussian linear model of j on
	// the other restricted members against j on the other full members,
	// both from the full fit's co-moment. Terms are then the tested
	// members (the full battery's members the restricted one lacks), DF
	// their count, the residual dfs N* − p of each battery; the
	// single-pair Statistic, PValue and deviances are undefined (null)
	// and every row's PValue is one member of the multiplicity family.
	// Absent on a regression or SEM comparison.
	Members []FitComparisonMember `json:"members,omitempty"`

	// Error is set when the comparison failed on its data: the entry
	// keeps Name, Restricted, Full, Test and the error, every figure
	// absent on the wire (see SlotError).
	Error *SlotError `json:"error,omitempty"`
}

// FitComparisonMember is one row of a vector-fit comparison
// (FitComparisonResult.Members): member j's nested test — R's
// anova(lm(j ~ restricted others), lm(j ~ full others)).
type FitComparisonMember struct {
	// Member is the restricted battery member's label.
	Member string `json:"member"`

	// Statistic is F (f, wald: the classical Wald W/q, equal to F on a
	// linear model) or χ² (lrt); null when undefined (df₁ ≤ 0, a
	// singular co-moment block).
	Statistic float64 `json:"statistic"`

	// PValue is P(statistic ≥ observed); null where Statistic is.
	PValue float64 `json:"p_value"`

	// RestrictedDeviance and FullDeviance are the two models' residual
	// sums of squares (weighted under a frequency weight).
	RestrictedDeviance float64 `json:"restricted_deviance"`
	FullDeviance       float64 `json:"full_deviance"`

	// Dispersion is σ̂² = FullDeviance / FullDFResidual, the scale the f
	// and lrt statistics divide by; absent on a Wald test.
	Dispersion float64 `json:"dispersion,omitempty"`

	// PAdjusted, SignificantAdjusted and Multiplicity are the row's
	// adjusted p-value beside PValue, as FitComparisonResult's.
	PAdjusted           *float64             `json:"p_adjusted,omitempty"`
	SignificantAdjusted *bool                `json:"significant_adjusted,omitempty"`
	Multiplicity        *AppliedMultiplicity `json:"multiplicity,omitempty"`
}

// slotFailureComparison is the wire form of a FitComparisonResult
// carrying an Error: its identity and the error, no figure.
type slotFailureComparison struct {
	Name       string         `json:"name"`
	Restricted string         `json:"restricted"`
	Full       string         `json:"full"`
	Test       ComparisonTest `json:"test"`
	Error      *SlotError     `json:"error"`
}

// MarshalJSON writes the comparison with every non-finite float as
// null (MarshalFinite); a failed comparison writes its identity and the
// error only.
func (c FitComparisonResult) MarshalJSON() ([]byte, error) {
	if c.Error != nil {
		return MarshalFinite(slotFailureComparison{Name: c.Name, Restricted: c.Restricted, Full: c.Full, Test: c.Test, Error: c.Error})
	}
	type alias FitComparisonResult
	return MarshalFinite((*alias)(&c))
}
