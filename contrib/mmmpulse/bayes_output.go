package mmmpulse

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
)

// bayes_output.go turns REG_MMM_BAYES's posterior into its outputs: the
// coefficient table (posterior mean and SD), the posterior summaries of
// the ROI, the marginal ROI, the contribution and every coefficient at
// params.credible_level, the sampler diagnostics, the model-owned
// holdout's scores and the coded diagnostic warnings.
//
// A summary is {mean, median, sd, lower, upper} over every retained draw
// of every chain (chain order, then draw order): the mean, the median
// and the equal-tailed credible interval [lower, upper] at
// credible_level — the (1 − level)/2 and (1 + level)/2 quantiles, each
// the linear interpolation between order statistics (Hyndman–Fan type
// 7, R's and NumPy's default) over the sorted draws — and the SD
// (divisor N − 1). Every figure is in original units. A summary over a
// draw that is not finite is NaN throughout (null on the wire).

// The sampler-health thresholds (Vehtari et al. 2021; Stan's): a
// reported parameter's R̂ above bayesRHatMax or a bulk or tail ESS below
// bayesESSMin warns, as does any post-warmup divergence or any
// transition that hit the maximum tree depth.
const (
	bayesRHatMax = 1.01
	bayesESSMin  = 400
)

// The warning reasons (details.reason of PROCESSING_REGRESSION_DIAGNOSTIC).
const (
	reasonRHatHigh           = "rhat_high"
	reasonESSLow             = "ess_low"
	reasonDivergences        = "divergences"
	reasonTreeDepthSaturated = "treedepth_saturated"
)

// holdoutNoiseStream separates the held-out rows' predictive-noise
// stream from the sampler's chains, which derive theirs from the same
// seed.
const holdoutNoiseStream = 0x686f6c646f7574 // "holdout"

// bayesOutputSchema is REG_MMM_BAYES's declared Outputs.
var bayesOutputSchema = []pulse.RegressionOutputMeta{
	{Key: "seed", JSONType: "number", Description: "The effective sampler seed: rng_seed, or the one derived from the slot when rng_seed is 0."},
	{Key: "roi", JSONType: "object", Description: "Per channel: the posterior of its ROI — summed contribution over summed spend across the fitted rows, against a zero-spend counterfactual — as {mean, median, sd, lower, upper} at credible_level, in outcome units per unit of spend."},
	{Key: "mroi", JSONType: "object", Description: "Per channel: the posterior of its marginal ROI at the fitted spend — the analytic derivative of the summed contribution with respect to a uniform scaling of the channel's spend, over the summed spend — as {mean, median, sd, lower, upper} at credible_level."},
	{Key: "contribution", JSONType: "object", Description: "Per channel: the posterior of its contribution summed over the fitted rows, in outcome units, as {mean, median, sd, lower, upper} at credible_level."},
	{Key: "params", JSONType: "object", Description: "Per coefficient (the coefficient table's names): its posterior as {mean, median, sd, lower, upper} at credible_level, in original units."},
	{Key: "diagnostics", JSONType: "object", Description: "Sampler health: parameters and roi (per coefficient / channel {rhat, ess_bulk, ess_tail}), max_rhat, min_ess_bulk, min_ess_tail, divergences, treedepth_saturated, max_tree_depth, step_size and accept_stat (per chain), chains, warmup, draws (per chain) and map_start."},
	{Key: "holdout", JSONType: "object", Description: "With params.holdout_from: from, fit_rows, holdout_rows, credible_level, rmse (held-out outcome against the posterior mean prediction) and coverage (the share of held-out rows inside the posterior predictive interval at credible_level). Absent without a holdout."},
}

// bayesInterpretation reads REG_MMM_BAYES's figures.
var bayesInterpretation = []descriptor.Interpretation{
	{Field: "coefficients.*", Means: "Each coefficient's posterior mean in original units: a channel's effect at full saturation, a control's effect per unit, the decay, half-saturation and shape of each channel's curve, the intercept and the noise SD.",
		Caveats: []string{"A channel's effect is associational unless its spend varied for reasons unrelated to the outcome; an experiment-measured prior anchors it."}},
	{Field: "outputs.roi", Means: "The outcome each unit of a channel's spend returned over the fitted period, against spending nothing on it; lower and upper bound the credible interval at credible_level.",
		Caveats: []string{"A credible interval is conditional on the model and the priors: a wrong adstock window, a missing control or a seasonal confounder moves it without widening it.", "An ROI below 1 means the channel returned less outcome than it cost only when the outcome and the spend are in the same currency."}},
	{Field: "outputs.mroi", Means: "The return on the next unit of a channel's spend at the fitted spend level; below the channel's ROI when it is saturating.",
		Caveats: []string{"It is a local slope: it holds the fitted response curve and spread of spend fixed, and is not the return of a large budget shift."}},
	{Field: "outputs.contribution", Means: "The outcome a channel produced over the fitted period, summed, against spending nothing on it.",
		Caveats: []string{"Contributions are attributed by the model; they are not observed."}},
	{Field: "outputs.params", Means: "Each coefficient's posterior median, SD and credible interval at credible_level, beside the coefficient table's mean."},
	{Field: "outputs.diagnostics", Means: "Whether the sampler's chains agree and explored the posterior: R-hat near 1 (at most 1.01), effective sample sizes of at least 400, and no divergences or tree-depth hits.",
		Caveats: []string{"A fit that fails any of them carries a PROCESSING_REGRESSION_DIAGNOSTIC warning naming it; its intervals may be too narrow or off-centre."}},
	{Field: "outputs.holdout", Means: "How well the fit predicts the held-out periods: rmse in outcome units, and coverage, the share of held-out periods inside the predictive interval — near credible_level when the intervals are honest.",
		Caveats: []string{"A short holdout gives a noisy coverage: with ten periods each one moves it by a tenth."}},
	{Field: "outputs.seed", Means: "The seed the sampler ran with; the same seed and request reproduce the same draws."},
}

// pool concatenates per-chain draws, chain order.
func pool(chains [][]float64) []float64 {
	out := make([]float64, 0, len(chains)*len(chains[0]))
	for _, ch := range chains {
		out = append(out, ch...)
	}
	return out
}

// quantile7 is the p-quantile of sorted (Hyndman–Fan type 7).
func quantile7(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 1 {
		return sorted[0]
	}
	h := float64(float64(n-1) * p)
	lo := int(math.Floor(h))
	if lo >= n-1 {
		return sorted[n-1]
	}
	return sorted[lo] + float64((h-float64(lo))*(sorted[lo+1]-sorted[lo]))
}

// summarize is the {mean, median, sd, lower, upper} of draws at level.
func summarize(draws []float64, level float64) map[string]float64 {
	nan := math.NaN()
	out := map[string]float64{"mean": nan, "median": nan, "sd": nan, "lower": nan, "upper": nan}
	if len(draws) == 0 {
		return out
	}
	sum := 0.0
	for _, v := range draws {
		if !finite(v) {
			return out
		}
		sum += v
	}
	mean := sum / float64(len(draws))
	ss := 0.0
	for _, v := range draws {
		d := v - mean
		ss += float64(d * d)
	}
	sorted := slices.Clone(draws)
	slices.Sort(sorted)
	out["mean"] = mean
	if len(draws) > 1 {
		out["sd"] = math.Sqrt(ss / float64(len(draws)-1))
	}
	out["median"] = quantile7(sorted, 0.5)
	out["lower"] = quantile7(sorted, (1-level)/2)
	out["upper"] = quantile7(sorted, (1+level)/2)
	return out
}

// convergence is one parameter's {rhat, ess_bulk, ess_tail} over its
// chains.
func convergence(chains [][]float64) map[string]float64 {
	return map[string]float64{"rhat": mcmc.RHat(chains), "ess_bulk": mcmc.ESSBulk(chains), "ess_tail": mcmc.ESSTail(chains)}
}

// paramChains is draws[chain][j] as [chain][draw].
func paramChains(draws [][][]float64, j int) [][]float64 {
	out := make([][]float64, len(draws))
	for ch := range draws {
		out[ch] = draws[ch][j]
	}
	return out
}

// health accumulates the convergence checks over the reported
// parameters: the worst figures and who failed each check.
type health struct {
	maxRHat, minBulk, minTail float64
	rhatParams, rhatChannels  []string
	essParams, essChannels    []string
}

// check folds one parameter's convergence in; a NaN figure (a single
// chain's R̂, too few draws, a non-finite draw) cannot certify the
// chains and fails its check, and makes the worst figure NaN.
func (h *health) check(conv map[string]float64, name string, channel bool) {
	worst := func(cur, v float64, lower bool) float64 {
		switch {
		case math.IsNaN(cur) || math.IsNaN(v):
			return math.NaN()
		case lower:
			return min(cur, v)
		}
		return max(cur, v)
	}
	h.maxRHat = worst(h.maxRHat, conv["rhat"], false)
	h.minBulk = worst(h.minBulk, conv["ess_bulk"], true)
	h.minTail = worst(h.minTail, conv["ess_tail"], true)
	if !(conv["rhat"] <= bayesRHatMax) {
		if channel {
			h.rhatChannels = append(h.rhatChannels, name)
		} else {
			h.rhatParams = append(h.rhatParams, name)
		}
	}
	if !(conv["ess_bulk"] >= bayesESSMin) || !(conv["ess_tail"] >= bayesESSMin) {
		if channel {
			h.essChannels = append(h.essChannels, name)
		} else {
			h.essParams = append(h.essParams, name)
		}
	}
}

// nonNil keeps an empty name list a JSON array.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// output is the regression output: the coefficient table (posterior
// mean and SD), the posterior covariance, the Outputs and the coded
// sampler-health warnings.
func (r *bayesRegression) output(post *bayesPosterior) extend.RegressionOutput {
	cfg := r.cfg
	mean, cov := posteriorMoments(post.coefs)
	names := r.coefNames()
	coefs := make([]extend.RegressionCoefficient, len(names))
	params, paramConv := map[string]any{}, map[string]any{}
	h := &health{maxRHat: math.Inf(-1), minBulk: math.Inf(1), minTail: math.Inf(1)}
	for i, name := range names {
		coefs[i] = extend.RegressionCoefficient{Name: name, Estimate: mean[i], StdErr: extend.Figure(math.Sqrt(cov[i][i]))}
		chains := paramChains(post.coefs, i)
		params[name] = summarize(pool(chains), cfg.credibleLevel)
		conv := convergence(chains)
		paramConv[name] = conv
		h.check(conv, name, false)
	}
	roi, mroi, contrib, roiConv := map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}
	for c, name := range cfg.channels {
		chains := paramChains(post.roi, c)
		roi[name] = summarize(pool(chains), cfg.credibleLevel)
		mroi[name] = summarize(pool(paramChains(post.mroi, c)), cfg.credibleLevel)
		contrib[name] = summarize(pool(paramChains(post.contrib, c)), cfg.credibleLevel)
		conv := convergence(chains)
		roiConv[name] = conv
		h.check(conv, name, true)
	}

	stats := post.theta.Diagnostics.Chains
	steps, accept := make([]float64, len(stats)), make([]float64, len(stats))
	divergences, saturated := 0, 0
	perChainDiv := make([]int, len(stats))
	for i, st := range stats {
		steps[i], accept[i] = st.StepSize, st.MeanAcceptStat
		divergences += st.Divergences
		saturated += st.TreeDepthSaturated
		perChainDiv[i] = st.Divergences
	}
	draws := len(post.coefs[0][0])
	total := len(post.coefs) * draws
	diagnostics := map[string]any{
		"parameters": paramConv, "roi": roiConv,
		"max_rhat": h.maxRHat, "min_ess_bulk": h.minBulk, "min_ess_tail": h.minTail,
		"divergences": divergences, "treedepth_saturated": saturated, "max_tree_depth": mcmc.DefaultMaxTreeDepth,
		"step_size": steps, "accept_stat": accept,
		"chains": len(post.coefs), "warmup": cfg.warmup, "draws": draws, "map_start": post.mapOK,
	}
	outputs := map[string]any{
		"seed": cfg.seed, "roi": roi, "mroi": mroi, "contribution": contrib, "params": params, "diagnostics": diagnostics,
	}
	if post.hold != nil {
		outputs["holdout"] = r.holdoutScores(post)
	}

	var warnings []*types.ResponseWarning
	warn := func(reason, msg string, details map[string]any) {
		details["reason"] = reason
		warnings = append(warnings, &types.ResponseWarning{Code: string(perr.PROCESSING_REGRESSION_DIAGNOSTIC),
			Message: fmt.Sprintf("%s regression %q: %s; the posterior summaries may not be trustworthy", BayesName, cfg.name, msg), Details: details})
	}
	if len(h.rhatParams)+len(h.rhatChannels) > 0 {
		warn(reasonRHatHigh, fmt.Sprintf("R-hat is above %v (or undefined, with one chain) on %d parameter(s): the chains disagree — raise warmup / draws",
			bayesRHatMax, len(h.rhatParams)+len(h.rhatChannels)),
			map[string]any{"threshold": bayesRHatMax, "max_rhat": h.maxRHat, "parameters": nonNil(h.rhatParams), "channels": nonNil(h.rhatChannels)})
	}
	if len(h.essParams)+len(h.essChannels) > 0 {
		warn(reasonESSLow, fmt.Sprintf("the bulk or tail effective sample size is below %d on %d parameter(s) — raise draws",
			bayesESSMin, len(h.essParams)+len(h.essChannels)),
			map[string]any{"threshold": bayesESSMin, "min_ess_bulk": h.minBulk, "min_ess_tail": h.minTail,
				"parameters": nonNil(h.essParams), "channels": nonNil(h.essChannels)})
	}
	if divergences > 0 {
		warn(reasonDivergences, fmt.Sprintf("%d of %d post-warmup transitions diverged — raise target_accept or tighten the priors", divergences, total),
			map[string]any{"count": divergences, "draws": total, "per_chain": perChainDiv})
	}
	if saturated > 0 {
		warn(reasonTreeDepthSaturated, fmt.Sprintf("%d of %d post-warmup transitions hit the maximum tree depth %d — the sampler is crawling; tighten the priors or rescale the inputs",
			saturated, total, mcmc.DefaultMaxTreeDepth),
			map[string]any{"count": saturated, "draws": total, "max_tree_depth": mcmc.DefaultMaxTreeDepth})
	}
	return extend.RegressionOutput{
		Coefficients: coefs,
		Vcov:         cov,
		Outputs:      outputs,
		Warnings:     warnings,
	}
}

// holdoutScores scores the held-out rows: the RMSE of the outcome
// against the posterior mean prediction, and the share of rows whose
// outcome falls inside the posterior predictive interval at
// credible_level. The predictive draw of a row is each posterior draw's
// mean plus its σ times a standard-normal draw from the seed's own
// holdout stream (rows in time order, draws in pooled order), so the
// scores are the seed's alone on every architecture.
func (r *bayesRegression) holdoutScores(post *bayesPosterior) map[string]any {
	cfg, hold := r.cfg, post.hold
	sigma := pool(paramChains(post.coefs, hold.sigmaIdx))
	s := uint64(cfg.seed) ^ holdoutNoiseStream
	rng := rand.New(rand.NewPCG(s, s^0x9E3779B97F4A7C15))
	ssr, covered := 0.0, 0
	rep := make([]float64, len(sigma))
	for t, mus := range hold.mu {
		sum := 0.0
		for d, mu := range mus {
			sum += mu
			rep[d] = mu + float64(sigma[d]*detmath.NormFloat64(rng))
		}
		e := hold.y[t] - sum/float64(len(mus))
		ssr += float64(e * e)
		ps := summarize(rep, cfg.credibleLevel)
		if hold.y[t] >= ps["lower"] && hold.y[t] <= ps["upper"] {
			covered++
		}
	}
	n := len(hold.mu)
	return map[string]any{
		"from": hold.from, "fit_rows": hold.fitRows, "holdout_rows": n, "credible_level": cfg.credibleLevel,
		"rmse":     math.Sqrt(ssr / float64(n)),
		"coverage": float64(covered) / float64(n),
	}
}
