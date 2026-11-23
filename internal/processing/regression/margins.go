package regression

import (
	"math"
	"slices"

	"github.com/frankbardon/pulse/internal/regmargins"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
)

// margins.go is the marginal-effects block (RegressionSpec.Margins, U65
// E3-S1): per term j the effect of a unit change in predictor x_j on the
// mean μ = g⁻¹(η), η = xᵀβ, with a delta-method standard error on the
// fit's own coefficient covariance V (the one StdErrors reads — classical
// or the robust Covariance), so HC, cluster and HAC carry through.
//
//	ame, response:  AME_j = β_j · m̄,         m̄ = Σw·μ′(η_i)/Σw
//	                J_jk = δ_jk·m̄ + β_j·Σw·μ″(η_i)·x_ik/Σw
//	mem, response:  MEM_j = β_j · μ′(η̄),     η̄ = x̄ᵀβ, x̄ = Σw·x_i/Σw
//	                J_jk = δ_jk·μ′(η̄) + β_j·μ″(η̄)·x̄_k
//	link scale, or an identity link (REG_OLS): the effect is β_j and its
//	                standard error the coefficient's (J = e_j exactly)
//
// binary contrast (U69 #641): a term whose fitted values are all 0 or 1
// reports the discrete contrast instead of the slope,
//
//	ame, response:  C_j = Σw·(μ(η_i1) − μ(η_i0))/Σw,
//	                η_i1 / η_i0 the row's linear predictor at x_ij = 1 / 0
//	                J_k  = Σw·(μ′(η_i1)·x1_ik − μ′(η_i0)·x0_ik)/Σw
//	mem, response:  the same at x̄ (x̄_j replaced by 1 and 0), weight 1
//	link scale, or an identity link: η(1) − η(0) = β_j, the coefficient
//
// — marginaleffects' avg_comparisons / comparisons at the means for a
// 0/1 column (its default for one). glmContrast is a pure function of
// the design, the prior weights and β, so a replicate fit can recompute
// it.
//
// x_i0 = 1 (the intercept), w the fit's prior weights (1 unweighted), so
// a frequency-weighted effect is the rep()-expanded fit's
// (marginaleffects avg_slopes(wts =); Stata margins with fweights) and
// mem's means are the weighted means (Stata's atmeans). Var = J·V·Jᵀ.
// Inference reads the coefficient table's reference distribution: a
// Student t on the fit's DF where its coefficients read one (REG_OLS,
// REG_GLM gamma under the classical covariance), else Wald z, at the
// fit's conf_level.
//
// Execution: REG_OLS's effect is its coefficient on either type and
// scale, so it needs no row and streams like the fit (the means of mem's
// At come from the accumulator, merged across shards like every other
// moment). REG_GLM is buffered: the per-row derivative pass runs after
// IRLS converges, over the fitted rows in record order — the same rows
// and order the deviance sums — so the effects are a function of the
// design, the weights and β alone and answer the same under every
// DecodeWorkers / ShardWorkers count the fit itself does. Every float
// product is barriered (float64(a*b)).

// marginsInference is the coefficient table's reference distribution.
type marginsInference struct {
	level    float64
	df       float64
	studentT bool
}

// newMarginsResult is the block's echo and empty maps for terms.
func newMarginsResult(spec *types.RegressionSpec, terms []string, inf marginsInference) *types.RegressionMarginsResult {
	n := len(terms)
	out := &types.RegressionMarginsResult{
		Type:       spec.Margins.EffectiveType(),
		Scale:      spec.Margins.EffectiveScale(),
		Terms:      terms,
		Estimates:  make(map[string]float64, n),
		StdErrors:  make(map[string]float64, n),
		Statistics: make(map[string]float64, n),
		PValues:    make(map[string]float64, n),
		CILower:    make(map[string]float64, n),
		CIUpper:    make(map[string]float64, n),
		ConfLevel:  inf.level,
	}
	if regmargins.Contrast(spec) {
		out.Binary = types.MarginsBinaryContrast
	}
	if inf.studentT {
		out.DF = inf.df
		if !(inf.df > 0) {
			out.DF = math.NaN()
		}
	}
	return out
}

// setMargin writes term's estimate, standard error and inference: the
// statistic est/se, the two-sided p and the interval est ∓ q·se — the
// coefficient table's own p-value functions and interval rule
// (attachIntervals), so on the link scale every figure equals the
// coefficient's bit for bit.
func setMargin(out *types.RegressionMarginsResult, term string, est, se float64, inf marginsInference) {
	out.Estimates[term] = est
	out.StdErrors[term] = se
	stat := math.NaN()
	if !math.IsNaN(est) && !math.IsNaN(se) && se != 0 {
		stat = est / se
	}
	out.Statistics[term] = stat
	var p, q float64
	if inf.studentT {
		p = pValueForCoefficient(est, se, inf.df)
		q = statdist.StudentTQuantile(0.5+float64(inf.level/2), inf.df)
	} else {
		p = waldZTwoSidedP(est, se)
		q = statdist.NormalQuantile(0.5 + float64(inf.level/2))
	}
	out.PValues[term] = p
	if se == 0 && !math.IsNaN(q) {
		out.CILower[term], out.CIUpper[term] = est, est
		return
	}
	half := float64(q * se)
	out.CILower[term], out.CIUpper[term] = est-half, est+half
}

// contrastTerms lists, in terms order, the terms spec reports as a
// discrete contrast: under binary contrast those binary reports all-0/1
// (binary takes the term's design column, its predictor index + 1);
// nil under slope.
func contrastTerms(spec *types.RegressionSpec, terms []string, binary func(col int) bool) []string {
	if !regmargins.Contrast(spec) {
		return nil
	}
	var out []string
	for _, t := range terms {
		if binary(slices.Index(spec.Predictors, t) + 1) {
			out = append(out, t)
		}
	}
	return out
}

// designBinary is the binary test of a row-major design (n × k,
// intercept first): column c is binary when every row's value is 0 or 1.
func designBinary(xRows []float64, k int) func(col int) bool {
	return func(col int) bool {
		for i := col; i < len(xRows); i += k {
			if !regmargins.IsBinaryValue(xRows[i]) {
				return false
			}
		}
		return true
	}
}

// glmContrast is the response-scale discrete contrast of design column j
// (0/1) of a REG_GLM fit and its β-gradient: xRows the fitted design
// (n × k row-major, intercept first), prior the prior weights and beta
// the coefficients — or, under mem, the single point xbar (xRows nil).
// A pure function of its arguments.
func glmContrast(fam glmFamily, xRows, prior, beta, xbar []float64, k, j int) (float64, []float64) {
	jac := make([]float64, k)
	est := 0.0
	add := func(x []float64, w float64) {
		// eta0 is the row's linear predictor with x_j = 0; eta1 adds β_j.
		eta0 := 0.0
		for c := 0; c < k; c++ {
			if c != j {
				eta0 += float64(x[c] * beta[c])
			}
		}
		eta1 := eta0 + beta[j]
		est += float64(w * (fam.InverseLink(eta1) - fam.InverseLink(eta0)))
		d1, d0 := fam.DmuDeta(eta1), fam.DmuDeta(eta0)
		wd := float64(w * (d1 - d0))
		for c := 0; c < k; c++ {
			if c != j {
				jac[c] += float64(wd * x[c])
			}
		}
		jac[j] += float64(w * d1)
	}
	if xbar != nil {
		add(xbar, 1)
		return est, jac
	}
	sw := 0.0
	for i := range prior {
		add(xRows[i*k:(i+1)*k], prior[i])
		sw += prior[i]
	}
	est /= sw
	for c := range jac {
		jac[c] /= sw
	}
	return est, jac
}

// coefficientMargins is the block where every effect is its
// coefficient: the link scale, or an identity link on the response
// scale (REG_OLS; REG_GLM poisson / gamma / negative_binomial identity).
// at, when non-nil, is mem's evaluation point (every predictor's mean).
// contrasts lists the binary terms under binary contrast, whose
// contrast η(1) − η(0) is the coefficient too.
func coefficientMargins(spec *types.RegressionSpec, res *types.RegressionResult, inf marginsInference, at []float64, contrasts []string) *types.RegressionMarginsResult {
	terms := regmargins.Terms(spec)
	out := newMarginsResult(spec, terms, inf)
	out.Contrasts = contrasts
	for _, t := range terms {
		setMargin(out, t, res.Coefficients[t], res.StdErrors[t], inf)
	}
	if out.Type == types.MarginsMEM && at != nil {
		out.At = make(map[string]float64, len(spec.Predictors))
		for j, p := range spec.Predictors {
			out.At[p] = at[j]
		}
	}
	return out
}

// glmMargins is the response-scale block of a REG_GLM fit on a
// non-identity link: xRows the fitted design (n × k row-major, intercept
// first), prior the prior weights, eta the converged linear predictor,
// beta the coefficients and v the coefficient covariance (k × k) the
// standard errors read.
func glmMargins(spec *types.RegressionSpec, fam glmFamily, xRows, prior, eta, beta []float64, v [][]float64, inf marginsInference) *types.RegressionMarginsResult {
	k := len(beta)
	n := len(eta)
	terms := regmargins.Terms(spec)
	out := newMarginsResult(spec, terms, inf)
	out.Contrasts = contrastTerms(spec, terms, designBinary(xRows, k))

	// grad[k] is Σw·μ″·x_k/Σw (ame) or μ″(η̄)·x̄_k (mem); scale is m̄ or
	// μ′(η̄).
	grad := make([]float64, k)
	var scale float64
	var xbar []float64
	if spec.Margins.EffectiveType() == types.MarginsMEM {
		xbar = designMeans(xRows, prior, k)
		etaBar := 0.0
		for c := 0; c < k; c++ {
			etaBar += float64(xbar[c] * beta[c])
		}
		scale = fam.DmuDeta(etaBar)
		d2 := fam.D2muDeta2(etaBar)
		for c := 0; c < k; c++ {
			grad[c] = float64(d2 * xbar[c])
		}
		out.At = make(map[string]float64, k-1)
		for j, p := range spec.Predictors {
			out.At[p] = xbar[j+1]
		}
	} else {
		sw := 0.0
		for i := 0; i < n; i++ {
			sw += prior[i]
		}
		for i := 0; i < n; i++ {
			row := xRows[i*k : (i+1)*k]
			scale += float64(prior[i] * fam.DmuDeta(eta[i]))
			wd2 := float64(prior[i] * fam.D2muDeta2(eta[i]))
			for c := 0; c < k; c++ {
				grad[c] += float64(wd2 * row[c])
			}
		}
		scale /= sw
		for c := 0; c < k; c++ {
			grad[c] /= sw
		}
	}

	jac := make([]float64, k)
	for _, t := range terms {
		j := slices.Index(spec.Predictors, t) + 1
		if slices.Contains(out.Contrasts, t) {
			est, cj := glmContrast(fam, xRows, prior, beta, xbar, k, j)
			setMargin(out, t, est, math.Sqrt(quadForm(v, cj)), inf)
			continue
		}
		for c := 0; c < k; c++ {
			jac[c] = float64(beta[j] * grad[c])
		}
		jac[j] += scale
		varEst := 0.0
		for a := 0; a < k; a++ {
			row := 0.0
			for b := 0; b < k; b++ {
				row += float64(v[a][b] * jac[b])
			}
			varEst += float64(jac[a] * row)
		}
		if varEst < 0 {
			varEst = 0
		}
		setMargin(out, t, float64(beta[j]*scale), math.Sqrt(varEst), inf)
	}
	return out
}

// designMeans is the (prior-)weighted column means Σw·x_ic/Σw of the
// fitted design (n × k row-major), the intercept column exactly 1.
func designMeans(xRows, prior []float64, k int) []float64 {
	xbar := make([]float64, k)
	sw := 0.0
	for i := range prior {
		row := xRows[i*k : (i+1)*k]
		for c := 0; c < k; c++ {
			xbar[c] += float64(prior[i] * row[c])
		}
		sw += prior[i]
	}
	for c := range xbar {
		xbar[c] /= sw
	}
	xbar[0] = 1
	return xbar
}

// identityLink reports whether fam's mean is its linear predictor
// (μ′ = 1, μ″ = 0), where every effect is the coefficient.
func identityLink(fam glmFamily) bool { return fam.Link == "identity" }

// AttachExtensionMargins writes res.Margins from the effects a model
// regression computed itself (extend.MarginsRegression, held to the
// request by the adapter): est and se keyed by regmargins.Key(outcome,
// term) for every outcome of outcomes (none: keyed by term) and every
// resolved term, a NaN se undefined. The inference is the coefficient
// table's (AttachExtensionIntervals): a Student t on the fit's reported
// df, else the standard normal, at conf_level, through setMargin — so an
// extension's effect reads its statistic, p-value and interval exactly
// as a built-in's. at (mem only; nil none) is echoed as the block's At;
// contrasts (verified against the request) lists the terms the model
// reported as a discrete contrast, kept in the terms' order.
func AttachExtensionMargins(res *types.RegressionResult, spec *types.RegressionSpec, outcomes []string, est, se, at map[string]float64, contrasts []string) {
	if spec == nil || spec.Margins == nil {
		return
	}
	inf := marginsInference{level: resolvedConfLevel(spec), df: res.DF,
		studentT: res.Resample == "" && slices.Contains(res.ReportedFigures, "df")}
	out := newMarginsResult(spec, regmargins.Terms(spec), inf)
	out.Contrasts = contrastTerms(spec, out.Terms, func(col int) bool { return slices.Contains(contrasts, spec.Predictors[col-1]) })
	if len(outcomes) > 0 {
		out.Outcomes = slices.Clone(outcomes)
	}
	for _, key := range regmargins.ResultKeys(out) {
		setMargin(out, key, est[key], se[key], inf)
	}
	if out.Type == types.MarginsMEM && len(at) > 0 {
		out.At = make(map[string]float64, len(at))
		for k, v := range at {
			out.At[k] = v
		}
	}
	res.Margins = out
}
