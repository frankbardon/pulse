package regmmm

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/distlagparams"
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
)

// bayes.go is REG_MMM_BAYES's factory and fit: the params wire format,
// the time key and row order, the refusals, the seed, and the MAP → NUTS
// run that turns a request into a posterior. The model maths (log
// posterior, gradient, MAP start) is bayes_model.go's; the outputs are
// bayes_output.go's. The registration (params, outputs, flags) is the
// root package's, its static twin internal/descriptor's.

// BayesName is the Bayesian media-mix model's regression type.
const BayesName = types.REG_MMM_BAYES

// DecayPrefix names a channel's adstock decay coefficient in
// REG_MMM_BAYES's table: "decay:" + channel. Its default Hill curve
// rides HalfSaturationPrefix and ShapePrefix as in REG_MMM_HILL; the
// logistic and Michaelis–Menten curves are bayes_curve.go's.
const DecayPrefix = pmodel.MMMDecayPrefix

// defaultMaxLag is the adstock window when params.max_lag is absent: 13
// weeks, a quarter of carry-over (Jin et al. 2017's L). It is the
// model package's, so a fit's adstock record (pmodel.AdstockLineage)
// reads the same default.
const defaultMaxLag = pmodel.MMMBayesDefaultMaxLag

// The coefficient names REG_MMM_BAYES adds beyond its predictors.
const (
	interceptName = "(intercept)"
	sigmaName     = pmodel.MMMSigmaName
)

// The sampler settings' defaults and hard caps. A cap keeps one slot's
// sampler cost bounded; past it the request is PROCESSING_CONFIG.
const (
	defaultBayesChains       = mcmc.DefaultChains
	defaultBayesWarmup       = mcmc.DefaultWarmup
	defaultBayesDraws        = mcmc.DefaultDraws
	defaultCredibleLevel     = 0.9
	defaultBayesTargetAccept = mcmc.DefaultTargetAccept
	// defaultBayesGeoTargetAccept is a geo panel's (params.geo)
	// target_accept default: the hierarchy's intercept, ROI and pooling
	// scales leave ~2.5% of post-warmup transitions divergent at 0.8,
	// under 1% at 0.95 (bayes_geo_sbc_heavy_test.go).
	defaultBayesGeoTargetAccept = 0.95
	maxBayesChains              = 16
	maxBayesWarmup              = 20000
	maxBayesDraws               = 20000
)

// The params defaults the registration declares (the root package's
// RegressionRegistration and internal/descriptor's static twin read
// them, so the manifest's Default is the factory's).
const (
	BayesDefaultMaxLag       = defaultMaxLag
	BayesDefaultChains       = defaultBayesChains
	BayesDefaultWarmup       = defaultBayesWarmup
	BayesDefaultDraws        = defaultBayesDraws
	BayesDefaultCredible     = defaultCredibleLevel
	BayesDefaultTargetAccept = defaultBayesTargetAccept
	// BayesDefaultGeoTargetAccept is target_accept's default on a geo
	// panel (params.geo).
	BayesDefaultGeoTargetAccept = defaultBayesGeoTargetAccept
)

// bayesSampler builds the sampler a fit runs (NUTS at the request's
// target_accept). It is a variable only so the package's tests can count
// and fail sampler runs; nothing else sets it.
var bayesSampler = func(targetAccept float64) mcmc.Sampler {
	return mcmc.NUTS{TargetAccept: targetAccept}
}

// BayesFactory is REG_MMM_BAYES's factory: a Bayesian media-mix model
// sampled by NUTS (the public mcmc package) from a MAP warm start,
//
//	y_t ~ Normal(α + Σ_j γ_j·z_jt + Σ_c β_c·curve(adstock_c(spend_c)_t, q_c), σ),
//
// with each channel's raw spend adstocked inside the model
// (params.adstock: geometric by default, delayed, weibull_cdf or
// weibull_pdf — WIN_DISTRIBUTED_LAG's kernel with normalize false,
// params.max_lag weeks, required for every kernel but geometric; one
// kernel for every channel, bayes_adstock.go), so the kernel's
// parameters are posterior parameters rather than sweep axes. The
// curve is params.saturation: hill (the default, k and s), logistic
// (λ) or michaelis_menten (k) — one curve for every channel,
// bayes_curve.go. A channel's effect is sampled as its ROI (the default,
// LogNormal(0.2, 0.9)) or, with a "beta" prior, as β. The model is
// bayes_model.go's: internal max-abs scaling, every figure reported in
// original units.
//
// The rows are one market's time series: params.time names an integer
// field the fit sorts the rows by; a gap or a repeated value (a panel
// — several markets or geos per period) is PROCESSING_CONFIG. A null in
// the target or a predictor drops its row listwise and so leaves a gap.
// params.time is the one field read beyond the target and predictors
// (pmodel.ModelRegressionFieldInputs, which the projection decodes).
//
// The coefficient table is "(intercept)", each channel's β on the raw
// outcome (contribution_t = β·curve(raw adstock_t); predictor
// order), the controls (predictor order), then the kernel's parameters
// per channel ("decay:<channel>"; with delayed also "peak:<channel>";
// under a Weibull kernel "shape:<channel>" and "scale:<channel>"
// instead — pmodel.MMMBayesAdstockPrefixes), the curve's parameters per
// channel ("k:<channel>" in raw adstock units and "s:<channel>" for
// Hill; "lam:<channel>" per raw adstock unit for logistic; "k:<channel>"
// for michaelis_menten — pmodel.MMMBayesCurvePrefixes), and "sigma":
// Estimate the posterior mean, StdErr the posterior
// standard deviation, over every retained draw of every chain. Vcov is
// the posterior covariance of the same draws. The engine's Wald
// ci_lower / ci_upper ride the posterior SD (a normal approximation);
// the model reports no p-value. The posterior's own answers ride
// Outputs (bayes_output.go): roi, mroi and contribution per channel and
// params per coefficient, each {mean, median, sd, lower, upper} with
// [lower, upper] the equal-tailed credible interval at
// params.credible_level, in original units; diagnostics (R̂, bulk and
// tail ESS per coefficient and per channel ROI, divergences, tree-depth
// hits, step sizes, chains, draws); seed, the effective sampler seed;
// and, with params.holdout_from, holdout. A channel's ROI per draw is
// its summed contribution over its summed spend across the fitted
// rows, against a zero-spend counterfactual; its contribution is the
// same sum; its marginal ROI is the ANALYTIC derivative of the summed
// contribution with respect to a uniform scaling of its spend, at the
// fitted spend, over the summed spend (bayesModel.effects).
//
// Sampler health is reported, never refused: a reported parameter's R̂
// above 1.01 (or undefined, with one chain), a bulk or tail ESS below
// 400, any post-warmup divergence and any transition at the maximum
// tree depth each add a PROCESSING_REGRESSION_DIAGNOSTIC warning
// (details.reason rhat_high, ess_low, divergences,
// treedepth_saturated) to the result and the response; under Strict
// the first fails the slot, and so the run, with that code.
//
// params.holdout_from is the model-owned holdout: the likelihood sees
// only the rows whose time is below it, the held-out rows' prediction
// runs the adstock over the full history (the spend before the cutoff
// carries in), and outputs.holdout reports the held-out rows' RMSE
// against the posterior mean prediction and the share inside the
// posterior predictive interval at credible_level. The ROI, mROI and
// contribution are then the fitted rows'. A cutoff that leaves no row
// on one side is PROCESSING_CONFIG.
//
// params.lift_tests enters measured experiments as data: each test adds
// lift ~ Normal(Δ, se), Δ the model's change in the outcome summed over
// the window's periods when the channel's spend there changes by
// spend_delta (bayes_lift.go; on a geo panel the named geo's). Its spec
// refusals (geo on one market, no geo on a panel, an unknown
// channel, a missing or reversed window, a missing or zero spend_delta,
// a missing lift, se ≤ 0) are the factory's; a window outside the
// fitted periods and a cut below zero spend are the fit's
// (PROCESSING_CONFIG, slot params.lift_tests). A channel with an roi
// prior as well warns PULSE_MMM_LIFT_PRIOR_OVERLAP, first among the
// result's warnings. No lift test (absent or empty) adds no term: the
// fit is byte-identical.
//
// The seed is rng_seed, or when 0 the FNV-64a of the slot's wire form
// with rng_seed cleared (Pulse's own rng_seed-0 rule), never data or the
// clock: the same request gives the same draws, bit for bit, on every
// architecture and for any worker count.
//
// params.return_draws adds outputs.draws, the raw posterior draws
// (every params.draws_thin-th per chain), capped by
// Limits.MaxMMMDrawValues (bayes_draws.go).
//
// No scorer: an adstocked prediction needs the history, so the engine's
// holdout slot (params.holdout_from replaces it), save and
// ATTR_REG_PREDICT are refused; NoResample: resample and its knobs are
// refused. Every other built-in knob (penalty, selection, covariance,
// family, prior*, credible_level as a slot) is refused as on any
// extension-shaped model; the model's own priors and credible level
// ride params.
//
// Data refusals fail the slot alone: too few rows
// (PROCESSING_REGRESSION_INSUFFICIENT_DATA, checked before anything else
// is read), an all-zero outcome, a channel that never spends or a
// constant control (PROCESSING_REGRESSION_RANK_DEFICIENT), and a
// sampler that cannot run on the posterior — no finite start, a
// collapsing or unbounded step size, a non-finite metric
// (PROCESSING_REGRESSION_NO_CONVERGE). A negative spend, a bad time key,
// an unknown or ill-typed params key and an invalid prior are
// PROCESSING_CONFIG.
//
// Every factory refusal reads the spec alone and runs before any row is
// read (BayesSpecRefusal lets predict raise it).

// priorWire is one prior on the wire: a family and that family's keys.
type priorWire struct {
	Family string   `json:"family"`
	Mu     *float64 `json:"mu"`
	Sigma  *float64 `json:"sigma"`
	Alpha  *float64 `json:"alpha"`
	Beta   *float64 `json:"beta"`
	Lower  *float64 `json:"lower"`
	Upper  *float64 `json:"upper"`
}

// channelPriorWire is one channel's priors; roi and beta are exclusive;
// decay, peak, shape and scale are the adstock kernel's parameters, each
// accepted only under a kernel that has it; k, s and lam the saturation
// curve's, each accepted only under a curve that has it.
type channelPriorWire struct {
	ROI   *priorWire `json:"roi"`
	Beta  *priorWire `json:"beta"`
	Decay *priorWire `json:"decay"`
	Peak  *priorWire `json:"peak"`
	Shape *priorWire `json:"shape"`
	Scale *priorWire `json:"scale"`
	K     *priorWire `json:"k"`
	S     *priorWire `json:"s"`
	Lam   *priorWire `json:"lam"`
}

// curvePrior is the wire prior of curve parameter key, nil when unset.
func (c channelPriorWire) curvePrior(key string) *priorWire {
	switch key {
	case "k":
		return c.K
	case "s":
		return c.S
	case "lam":
		return c.Lam
	}
	return nil
}

// kernelPrior is the wire prior of kernel parameter key, nil when unset.
func (c channelPriorWire) kernelPrior(key string) *priorWire {
	switch key {
	case "decay":
		return c.Decay
	case "peak":
		return c.Peak
	case "shape":
		return c.Shape
	case "scale":
		return c.Scale
	}
	return nil
}

// bayesParamsWire is the params object.
type bayesParamsWire struct {
	Channels       []string                    `json:"channels"`
	Time           string                      `json:"time"`
	MaxLag         *int                        `json:"max_lag"`
	Adstock        *string                     `json:"adstock"`
	Saturation     *string                     `json:"saturation"`
	Priors         map[string]channelPriorWire `json:"priors"`
	InterceptPrior *priorWire                  `json:"intercept_prior"`
	ControlPrior   *priorWire                  `json:"control_prior"`
	SigmaPrior     *priorWire                  `json:"sigma_prior"`
	Chains         *int                        `json:"chains"`
	Warmup         *int                        `json:"warmup"`
	Draws          *int                        `json:"draws"`
	CredibleLevel  *float64                    `json:"credible_level"`
	Sampler        *string                     `json:"sampler"`
	TargetAccept   *float64                    `json:"target_accept"`
	HoldoutFrom    *int64                      `json:"holdout_from"`
	LiftTests      []liftTestWire              `json:"lift_tests"`
	Geo            *string                     `json:"geo"`
	PerGeoCurves   *bool                       `json:"per_geo_curves"`
	ReturnDraws    *bool                       `json:"return_draws"`
	DrawsThin      *int                        `json:"draws_thin"`
}

// bayesConfig is one slot's validated configuration.
type bayesConfig struct {
	name, target       string
	channels, controls []string
	time               string
	maxLag             int
	kernel             adstockKernel
	curve              saturationCurve
	priors             bayesPriors
	chains, warmup     int
	draws              int
	credibleLevel      float64
	targetAccept       float64
	seed               int64
	// holdoutFrom is params.holdout_from (nil: every row is fitted).
	holdoutFrom *int64
	// lifts is params.lift_tests (bayes_lift.go); liftOverlap the
	// channels that also carry an roi prior (a warning).
	lifts       []bayesLiftSpec
	liftOverlap []string
	// geo is params.geo: the panel's market field ("" fits one market's
	// series, bayes_geo.go otherwise).
	geo string
	// perGeoCurves is params.per_geo_curves: on a geo panel the curve's
	// parameters are pooled across the geos like the kernel's (false:
	// one curve per channel, shared by every geo).
	perGeoCurves bool
	// returnDraws is params.return_draws: outputs.draws carries the raw
	// posterior draws, every drawsThin-th of each chain (bayes_draws.go).
	returnDraws bool
	drawsThin   int
}

// bayesSlotError is a PROCESSING_CONFIG refusal naming the slot.
func bayesSlotError(spec *types.RegressionSpec, slot, msg string, extra map[string]any) error {
	details := map[string]any{"regression": spec.Name, "type": string(BayesName), "slot": slot}
	for k, v := range extra {
		details[k] = v
	}
	return perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG,
		fmt.Sprintf("%s regression %q: %s", BayesName, spec.Name, msg), details)
}

// toPrior validates w as a prior for a parameter on support s and
// returns it compiled: a family's own keys are required, any other is
// refused.
func toPrior(w *priorWire, s support) (prior, error) {
	need := map[string][]string{
		familyNormal:      {"mu", "sigma"},
		familyHalfNormal:  {"sigma"},
		familyLogNormal:   {"mu", "sigma"},
		familyBeta:        {"alpha", "beta"},
		familyTruncNormal: {"mu", "sigma", "lower", "upper"},
	}
	keys, ok := need[w.Family]
	if !ok {
		return prior{}, fmt.Errorf("family %q is not one of normal, halfnormal, lognormal, beta, truncnormal", w.Family)
	}
	vals := map[string]*float64{"mu": w.Mu, "sigma": w.Sigma, "alpha": w.Alpha, "beta": w.Beta, "lower": w.Lower, "upper": w.Upper}
	for _, k := range []string{"mu", "sigma", "alpha", "beta", "lower", "upper"} {
		want := slices.Contains(keys, k)
		if want && vals[k] == nil {
			return prior{}, fmt.Errorf("family %s needs %s", w.Family, strings.Join(keys, ", "))
		}
		if !want && vals[k] != nil {
			return prior{}, fmt.Errorf("family %s takes no %s (it takes %s)", w.Family, k, strings.Join(keys, ", "))
		}
	}
	get := func(k string) float64 {
		if v := vals[k]; v != nil {
			return *v
		}
		return 0
	}
	p := prior{Family: w.Family}
	switch w.Family {
	case familyBeta:
		p.A, p.B = get("alpha"), get("beta")
	case familyHalfNormal:
		p.B = get("sigma")
	default:
		p.A, p.B, p.Lo, p.Hi = get("mu"), get("sigma"), get("lower"), get("upper")
	}
	if _, err := p.compile(s); err != nil {
		return prior{}, err
	}
	return p, nil
}

func BayesFactory(spec *types.RegressionSpec, schema *encoding.Schema) (extend.Regression, error) {
	var w bayesParamsWire
	if len(spec.Params) > 0 {
		// Strict: a typo in a prior is refused, never ignored.
		if err := paramread.DecodeStrict(paramread.Regression, string(BayesName), spec.Params, &w); err != nil {
			return nil, bayesSlotError(spec, "params", "params: "+err.Error(), nil)
		}
	} else {
		paramread.Declare(paramread.Regression, string(BayesName), &w)
	}
	cfg := &bayesConfig{name: spec.Name, target: spec.Target, time: w.Time, maxLag: defaultMaxLag,
		chains: defaultBayesChains, warmup: defaultBayesWarmup, draws: defaultBayesDraws,
		credibleLevel: defaultCredibleLevel, targetAccept: defaultBayesTargetAccept}
	if len(w.Channels) == 0 {
		return nil, bayesSlotError(spec, "params.channels", "params.channels needs at least one channel", nil)
	}
	if w.Time == "" {
		return nil, bayesSlotError(spec, "params.time", "params.time must name the integer time field", nil)
	}
	if w.Geo != nil {
		if err := geoSpecRefusal(spec, *w.Geo, w.Time); err != nil {
			return nil, err
		}
		cfg.geo = *w.Geo
	}
	if w.PerGeoCurves != nil && *w.PerGeoCurves {
		if cfg.geo == "" {
			return nil, bayesSlotError(spec, "params.per_geo_curves", "params.per_geo_curves pools each geo's curve across a geo panel; it needs params.geo (the market field) — drop it on one market's series",
				map[string]any{"reason": reasonPerGeoCurvesNoGeo})
		}
		cfg.perGeoCurves = true
	}
	// The kernel names the coefficients a predictor may collide with; an
	// unknown name is refused below, where the geometric-only check was,
	// so the refusal order is unchanged (until then it reads as geometric).
	adstockName := ""
	if w.Adstock != nil {
		adstockName = *w.Adstock
	}
	kernel := kernelByName(adstockName)
	var kernelPrefixes []string
	if kernel != nil {
		for _, kp := range kernel.Params() {
			kernelPrefixes = append(kernelPrefixes, kp.prefix)
		}
	} else {
		kernelPrefixes = []string{DecayPrefix}
	}
	// Likewise the curve: an unknown name is refused after the kernel and
	// collides as Hill until then.
	saturationName := ""
	if w.Saturation != nil {
		saturationName = *w.Saturation
	}
	curve := curveByName(saturationName)
	curvePrefixes := []string{HalfSaturationPrefix, ShapePrefix}
	if curve != nil {
		curvePrefixes = curvePrefixes[:0]
		for _, cq := range curve.Params() {
			curvePrefixes = append(curvePrefixes, cq.prefix)
		}
	}
	reserved := slices.Concat(kernelPrefixes, curvePrefixes)
	if cfg.geo != "" {
		// A geo panel's pooling scales are tau:<term>.
		reserved = append(reserved, TauPrefix)
	}
	isChannel := map[string]bool{}
	for _, c := range w.Channels {
		if !slices.Contains(spec.Predictors, c) || isChannel[c] {
			return nil, bayesSlotError(spec, "params.channels", fmt.Sprintf("params.channels: %q is not a predictor, or is listed twice", c),
				map[string]any{"channel": c})
		}
		isChannel[c] = true
	}
	seen := map[string]bool{}
	for _, x := range spec.Predictors {
		if seen[x] {
			// Each predictor names one coefficient in the table.
			return nil, bayesSlotError(spec, "predictors", fmt.Sprintf("predictor %q is listed twice", x),
				map[string]any{"predictor": x})
		}
		seen[x] = true
		if x == interceptName || x == sigmaName || hasAnyPrefix(x, reserved) {
			return nil, bayesSlotError(spec, "predictors",
				fmt.Sprintf("predictor %q collides with a coefficient name ((intercept), sigma, %s)", x, strings.Join(reserved, "/")),
				map[string]any{"predictor": x})
		}
		if strings.ContainsAny(x, geoBrackets) {
			// "tv[NE]" is channel tv's term in geo NE: a bracket in a
			// predictor would make that name ambiguous.
			return nil, bayesSlotError(spec, "predictors",
				fmt.Sprintf("predictor %q contains [ or ], which name a geo panel's per-geo terms (tv[NE]); rename the column", x),
				map[string]any{"predictor": x, "reason": reasonBracketReserved})
		}
		if isChannel[x] {
			cfg.channels = append(cfg.channels, x)
		} else {
			cfg.controls = append(cfg.controls, x)
		}
	}
	if w.MaxLag != nil {
		if *w.MaxLag < 1 {
			return nil, bayesSlotError(spec, "params.max_lag", fmt.Sprintf("params.max_lag must be an integer ≥ 1, got %d", *w.MaxLag), nil)
		}
		cfg.maxLag = *w.MaxLag
	}
	if kernel == nil {
		return nil, bayesSlotError(spec, "params.adstock", fmt.Sprintf("params.adstock %q is not one of: %s", adstockName, strings.Join(kernelNames(), ", ")),
			map[string]any{"adstock": adstockName, "valid": kernelNames()})
	}
	cfg.kernel = kernel
	if w.MaxLag == nil && kernel.Name() != distlagparams.KernelGeometric {
		// Only the geometric kernel has a default window: a delayed or
		// Weibull kernel's reach is the analyst's call.
		return nil, bayesSlotError(spec, "params.max_lag", fmt.Sprintf("params.adstock %q needs params.max_lag (an integer ≥ 1); only geometric defaults it", kernel.Name()),
			map[string]any{"adstock": kernel.Name(), "reason": "max_lag_required"})
	}
	if curve == nil {
		return nil, bayesSlotError(spec, "params.saturation", fmt.Sprintf("params.saturation %q is not one of: %s", saturationName, strings.Join(curveNames(), ", ")),
			map[string]any{"saturation": saturationName, "valid": curveNames()})
	}
	cfg.curve = curve
	if w.Sampler != nil && *w.Sampler != "nuts" {
		return nil, bayesSlotError(spec, "params.sampler", fmt.Sprintf("params.sampler %q is not one of: nuts", *w.Sampler), nil)
	}
	for _, s := range []struct {
		key      string
		v        *int
		dst      *int
		min, max int
	}{
		{"chains", w.Chains, &cfg.chains, 1, maxBayesChains},
		{"warmup", w.Warmup, &cfg.warmup, 1, maxBayesWarmup},
		{"draws", w.Draws, &cfg.draws, 1, maxBayesDraws},
	} {
		if s.v == nil {
			continue
		}
		if *s.v < s.min || *s.v > s.max {
			return nil, bayesSlotError(spec, "params."+s.key, fmt.Sprintf("params.%s must be an integer in [%d, %d], got %d", s.key, s.min, s.max, *s.v),
				map[string]any{"min": s.min, "max": s.max, "got": *s.v})
		}
		*s.dst = *s.v
	}
	if err := drawsConfig(spec, &w, cfg); err != nil {
		return nil, err
	}
	for _, s := range []struct {
		key string
		v   *float64
		dst *float64
	}{
		{"credible_level", w.CredibleLevel, &cfg.credibleLevel},
		{"target_accept", w.TargetAccept, &cfg.targetAccept},
	} {
		if s.v == nil {
			continue
		}
		if !(*s.v > 0 && *s.v < 1) {
			return nil, bayesSlotError(spec, "params."+s.key, fmt.Sprintf("params.%s must be in (0, 1), got %v", s.key, *s.v), nil)
		}
		*s.dst = *s.v
	}
	if w.TargetAccept == nil && cfg.geo != "" {
		cfg.targetAccept = defaultBayesGeoTargetAccept
	}

	cfg.priors = defaultBayesPriors(len(cfg.channels))
	if kernel.Name() != distlagparams.KernelGeometric {
		cfg.priors = defaultKernelPriors(kernel, len(cfg.channels), cfg.maxLag)
	}
	for _, s := range []struct {
		key string
		w   *priorWire
		dst *prior
		sup support
	}{
		{"intercept_prior", w.InterceptPrior, &cfg.priors.Intercept, supportReal},
		{"control_prior", w.ControlPrior, &cfg.priors.Control, supportReal},
		{"sigma_prior", w.SigmaPrior, &cfg.priors.Sigma, supportPositive},
	} {
		if s.w == nil {
			continue
		}
		p, err := toPrior(s.w, s.sup)
		if err != nil {
			return nil, bayesSlotError(spec, "params."+s.key, "params."+s.key+": "+err.Error(), nil)
		}
		*s.dst = p
	}
	for _, name := range slices.Sorted(maps.Keys(w.Priors)) {
		c := slices.Index(cfg.channels, name)
		if c < 0 {
			return nil, bayesSlotError(spec, "params.priors", fmt.Sprintf("params.priors: %q is not a channel", name), map[string]any{"channel": name})
		}
		cw, cp := w.Priors[name], &cfg.priors.Channels[c]
		if cw.ROI != nil && cw.Beta != nil {
			return nil, bayesSlotError(spec, "params.priors", fmt.Sprintf("params.priors.%s: set roi or beta, not both", name), map[string]any{"channel": name})
		}
		if cw.Beta != nil {
			cp.Beta = true
		}
		type priorSlot struct {
			key string
			w   *priorWire
			dst *prior
			sup support
		}
		// roi, beta, the kernel's parameters (decay by default), the
		// curve's (k, s by default): the geometric Hill order of old, so
		// the first refusal is unchanged.
		specs := []priorSlot{
			{"roi", cw.ROI, &cp.Effect, supportPositive},
			{"beta", cw.Beta, &cp.Effect, supportPositive},
		}
		reads := map[string]bool{}
		for _, kp := range kernel.Params() {
			reads[kp.key] = true
			specs = append(specs, priorSlot{kp.key, cw.kernelPrior(kp.key), kp.prior(cp), kp.sup})
		}
		for _, cq := range curve.Params() {
			reads[cq.key] = true
			specs = append(specs, priorSlot{cq.key, cw.curvePrior(cq.key), cq.prior(cp), supportPositive})
		}
		for _, key := range []string{"decay", "peak", "shape", "scale"} {
			if cw.kernelPrior(key) != nil && !reads[key] {
				return nil, bayesSlotError(spec, "params.priors", fmt.Sprintf("params.priors.%s.%s: the %s adstock kernel has no %s parameter", name, key, kernel.Name(), key),
					map[string]any{"channel": name, "adstock": kernel.Name(), "reason": "prior_not_in_kernel"})
			}
		}
		for _, key := range curveKeys {
			if cw.curvePrior(key) != nil && !reads[key] {
				return nil, bayesSlotError(spec, "params.priors", fmt.Sprintf("params.priors.%s.%s: the %s saturation curve has no %s parameter", name, key, curve.Name(), key),
					map[string]any{"channel": name, "saturation": curve.Name(), "reason": "prior_not_in_curve"})
			}
		}
		for _, s := range specs {
			if s.w == nil {
				continue
			}
			p, err := toPrior(s.w, s.sup)
			if err == nil && s.key == "peak" {
				err = peakPriorRefusal(p, cfg.maxLag)
			}
			if err != nil {
				return nil, bayesSlotError(spec, "params.priors", fmt.Sprintf("params.priors.%s.%s: %v", name, s.key, err), map[string]any{"channel": name})
			}
			*s.dst = p
		}
	}
	lifts, err := liftSpecs(spec, w.LiftTests, cfg.channels, cfg.geo != "")
	if err != nil {
		return nil, err
	}
	cfg.lifts = lifts
	cfg.liftOverlap = liftPriorOverlap(lifts, cfg.channels, w.Priors)
	cfg.holdoutFrom = w.HoldoutFrom
	cfg.seed = bayesEffectiveSeed(spec)
	// Last, so a spec refusal (which predict raises from the spec alone)
	// comes first: the geo field's type, read off the schema.
	if cerr := BayesGeoFieldRefusal(spec, schema); cerr != nil {
		return nil, cerr
	}
	return &bayesRegression{cfg: cfg}, nil
}

// hasAnyPrefix reports whether x starts with any of prefixes.
func hasAnyPrefix(x string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(x, p) {
			return true
		}
	}
	return false
}

// peakPriorRefusal is the delayed kernel's peak rule: the prior's
// support lies inside [0, max_lag] — a beta (on (0, 1)) or a truncnormal
// bounded there — so every draw, and the posterior mean the adstock
// record carries, is a peak WIN_DISTRIBUTED_LAG accepts.
func peakPriorRefusal(p prior, maxLag int) error {
	switch {
	case p.Family == familyBeta:
		return nil
	case p.Family == familyTruncNormal && p.Lo >= 0 && p.Hi <= float64(maxLag):
		return nil
	}
	return fmt.Errorf("the peak's prior must lie inside [0, max_lag = %d]: a beta, or a truncnormal with 0 ≤ lower and upper ≤ %d", maxLag, maxLag)
}

// BayesSpecRefusal is BayesFactory's refusal of spec, or nil when it
// builds: every check the factory makes reads the spec alone (params
// decode, channels, the coefficient-name collision, the sampler
// settings, the priors), so predict raises the very error the runtime
// does (internal/descriptor's model-regression declaration calls it).
func BayesSpecRefusal(spec *types.RegressionSpec) *perr.CodedError {
	_, err := BayesFactory(spec, nil)
	if err == nil {
		return nil
	}
	var ce *perr.CodedError
	if stderrors.As(err, &ce) {
		return ce
	}
	return perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG, err.Error(), map[string]any{"type": string(BayesName)})
}

// bayesEffectiveSeed is spec.RNGSeed when non-zero, else
// regresample.DeriveSeed over the spec's wire form with rng_seed cleared
// (the raw params in regresample.CanonicalJSON form, so their key order
// never moves it, less the output-only return_draws and draws_thin, so
// asking for the draws never moves them) and a zero row count — the derivation every Pulse
// rng_seed-0 draw shares. It reads the spec only, never data or the
// clock.
func bayesEffectiveSeed(spec *types.RegressionSpec) int64 {
	if spec.RNGSeed != 0 {
		return spec.RNGSeed
	}
	cp := *spec
	cp.RNGSeed = 0
	cp.Params = regresample.CanonicalJSON(withoutOutputOnlyParams(cp.Params))
	body, err := json.Marshal(&cp)
	if err != nil {
		body = nil
	}
	return regresample.DeriveSeed(body, 0, nil)
}

// bayesRegression is one slot's buffered fit.
type bayesRegression struct {
	cfg *bayesConfig
}

// Fit is FitContext under a background context.
func (r *bayesRegression) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	return r.FitContext(context.Background(), rows)
}

// slotError rewraps a model refusal with the slot's name in its message
// and details.
func (r *bayesRegression) slotError(err error) error {
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		return err
	}
	details := map[string]any{"regression": r.cfg.name, "type": string(BayesName)}
	for k, v := range ce.Details {
		details[k] = v
	}
	msg := strings.TrimPrefix(ce.Message, string(BayesName)+": ")
	msg = strings.TrimPrefix(msg, string(BayesName)+" ")
	return perr.NewCodedErrorWithDetails(ce.Code, fmt.Sprintf("%s regression %q: %s", BayesName, r.cfg.name, msg), details)
}

// configError is a PROCESSING_CONFIG refusal of the rows.
func (r *bayesRegression) configError(msg string, details map[string]any) error {
	return r.slotError(perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG, msg, details))
}

// bayesData is the admitted rows in time order.
type bayesData struct {
	y        []float64
	spend    [][]float64
	controls [][]float64
	time     []int64
}

// FitContext reads the rows, orders them by time, builds the posterior
// and samples it. The row count is checked first, before any value is
// read, so a short input is a cheap coded refusal.
func (r *bayesRegression) FitContext(ctx context.Context, rows extend.Rows) (extend.RegressionOutput, error) {
	cfg := r.cfg
	n, nc, nz := rows.Len(), len(cfg.channels), len(cfg.controls)
	per := len(cfg.kernel.Params()) + len(cfg.curve.Params()) + 1
	if dim := 2 + nz + per*nc; n <= dim {
		return extend.RegressionOutput{}, r.slotError(perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			fmt.Sprintf("needs more than %d rows (2 + controls + %d·channels), got %d", dim, per, n),
			map[string]any{"n": n, "required": dim + 1}))
	}
	if cfg.geo != "" {
		return r.fitGeo(ctx, rows)
	}
	if err := r.checkDrawValues(ctx, 0); err != nil {
		return extend.RegressionOutput{}, err
	}
	data, err := r.read(rows)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	nf, err := r.fitRows(data)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	fitSpend, fitControls := make([][]float64, len(data.spend)), make([][]float64, len(data.controls))
	for c, x := range data.spend {
		fitSpend[c] = x[:nf]
	}
	for j, z := range data.controls {
		fitControls[j] = z[:nf]
	}
	pr := cfg.priors
	pr.Kernel, pr.Curve = cfg.kernel, cfg.curve
	m, err := newBayesModel(data.y[:nf], fitSpend, fitControls, cfg.maxLag, pr)
	if err != nil {
		return extend.RegressionOutput{}, r.slotError(err)
	}
	if err := r.addLiftTests(m, data, nf); err != nil {
		return extend.RegressionOutput{}, err
	}
	if err := ctx.Err(); err != nil {
		return extend.RegressionOutput{}, err
	}
	post, err := r.sample(ctx, m, data, nf)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	return r.output(post), nil
}

// addLiftTests resolves params.lift_tests onto the fitted rows: a
// window outside them (before the first period, at or past
// holdout_from or the last period) and a spend change below zero spend
// are PROCESSING_CONFIG (details.reason lift_window_out_of_range,
// lift_spend_negative).
func (r *bayesRegression) addLiftTests(m *bayesModel, data *bayesData, nf int) error {
	first, last := data.time[0], data.time[nf-1]
	for i, l := range r.cfg.lifts {
		if l.start < first || l.end > last {
			return r.slotError(liftConfigError(i, reasonLiftWindowRange,
				fmt.Sprintf("the window [%d, %d] is outside the fitted periods [%d, %d]", l.start, l.end, first, last),
				map[string]any{"start": l.start, "end": l.end, "first": first, "last": last}))
		}
		lt, err := m.newLiftTest(l.channel, int(l.start-first), int(l.end-first), l.spendDelta, l.lift, l.se)
		if err != nil {
			return r.liftResolveError(i, err, map[string]any{"channel": r.cfg.channels[l.channel]})
		}
		m.lifts = append(m.lifts, lt)
	}
	return nil
}

// liftResolveError is lift test i's refusal by the model's resolution
// (makeLiftTest): its message and reason (lift_spend_negative when it
// carries none) on slot params.lift_tests, with extra in the details.
func (r *bayesRegression) liftResolveError(i int, err error, extra map[string]any) error {
	var ce *perr.CodedError
	msg, reason := err.Error(), reasonLiftSpendNegative
	if stderrors.As(err, &ce) {
		msg = strings.TrimPrefix(ce.Message, string(BayesName)+": ")
		if v, ok := ce.Details["reason"].(string); ok {
			reason = v
		}
	}
	return r.slotError(liftConfigError(i, reason, msg, extra))
}

// fitRows is the number of leading (time-ordered) rows the likelihood
// sees: every row, or with params.holdout_from those before it. A cutoff
// that leaves no fitted row or no held-out row (at or below the first
// time, above the last) is PROCESSING_CONFIG.
func (r *bayesRegression) fitRows(data *bayesData) (int, error) {
	from := r.cfg.holdoutFrom
	n := len(data.time)
	if from == nil {
		return n, nil
	}
	first, last := data.time[0], data.time[n-1]
	if *from <= first || *from > last {
		return 0, r.configError(fmt.Sprintf("params.holdout_from %d is outside the series: it must leave rows on both sides, in (%d, %d]", *from, first, last),
			map[string]any{"slot": "params.holdout_from", "reason": "holdout_out_of_range", "holdout_from": *from, "first": first, "last": last})
	}
	return int(*from - first), nil
}

// read copies the admitted rows out and sorts them by params.time,
// refusing a time key that is not an integer field, a gap, a repeat
// and a negative spend.
func (r *bayesRegression) read(rows extend.Rows) (*bayesData, error) {
	d, err := r.readRows(rows)
	if err != nil {
		return nil, err
	}
	cfg := r.cfg
	for i := 1; i < len(d.time); i++ {
		switch step := d.time[i] - d.time[i-1]; {
		case step == 0:
			return nil, r.configError(fmt.Sprintf("params.time %q repeats %d: without params.geo the model fits one market's series, one row per period; for a panel (several geos or markets per period) name its market field in params.geo, or filter to one market", cfg.time, d.time[i]),
				map[string]any{"slot": "params.time", "field": cfg.time, "reason": "duplicate_time", "time": d.time[i]})
		case step != 1:
			return nil, r.configError(fmt.Sprintf("params.time %q jumps from %d to %d: the periods must be contiguous (a filtered or null row leaves a gap the adstock would carry across)", cfg.time, d.time[i-1], d.time[i]),
				map[string]any{"slot": "params.time", "field": cfg.time, "reason": "time_gap", "from": d.time[i-1], "to": d.time[i]})
		}
	}
	return d, nil
}

// readRows copies the rows out in params.time order, refusing a time
// key that is not an integer field and a negative spend (the grid's
// checks are the caller's: read, readSeries).
func (r *bayesRegression) readRows(rows extend.Rows) (*bayesData, error) {
	cfg := r.cfg
	n := rows.Len()
	type obs struct {
		t      int64
		y      float64
		spend  []float64
		contrl []float64
	}
	all := make([]obs, n)
	for i := range n {
		rec := rows.At(i)
		tv, ok := rec.NumericValue(cfg.time)
		if !ok {
			return nil, r.configError(fmt.Sprintf("params.time %q is null on a row, or is not a numeric field", cfg.time),
				map[string]any{"slot": "params.time", "field": cfg.time, "reason": "time_missing"})
		}
		if tv != math.Trunc(tv) || math.Abs(tv) > 1<<53 {
			return nil, r.configError(fmt.Sprintf("params.time %q must be an integer on every row, got %v", cfg.time, tv),
				map[string]any{"slot": "params.time", "field": cfg.time, "reason": "time_not_integer"})
		}
		o := obs{t: int64(tv), spend: make([]float64, len(cfg.channels)), contrl: make([]float64, len(cfg.controls))}
		o.y, _ = rec.NumericValue(cfg.target)
		for c, name := range cfg.channels {
			v, _ := rec.NumericValue(name)
			if !(v >= 0) {
				return nil, r.configError(fmt.Sprintf("channel %q has a negative value (%v); a channel is raw spend, ≥ 0", name, v),
					map[string]any{"slot": "params.channels", "channel": name, "reason": "negative_spend"})
			}
			o.spend[c] = v
		}
		for j, name := range cfg.controls {
			o.contrl[j], _ = rec.NumericValue(name)
		}
		all[i] = o
	}
	slices.SortStableFunc(all, func(a, b obs) int {
		switch {
		case a.t < b.t:
			return -1
		case a.t > b.t:
			return 1
		}
		return 0
	})
	d := &bayesData{y: make([]float64, n), time: make([]int64, n),
		spend: make([][]float64, len(cfg.channels)), controls: make([][]float64, len(cfg.controls))}
	for c := range d.spend {
		d.spend[c] = make([]float64, n)
	}
	for j := range d.controls {
		d.controls[j] = make([]float64, n)
	}
	for i, o := range all {
		d.y[i], d.time[i] = o.y, o.t
		for c, v := range o.spend {
			d.spend[c][i] = v
		}
		for j, v := range o.contrl {
			d.controls[j][i] = v
		}
	}
	return d, nil
}

// bayesPosterior is one fit's sampled posterior.
type bayesPosterior struct {
	model *bayesModel
	// theta is the sampler's result: unconstrained θ draws,
	// [chain][θ coordinate][draw], with the run's diagnostics.
	theta mcmc.Result
	// coefs is every draw in the coefficient table's order and original
	// units, [chain][coefficient][draw].
	coefs [][][]float64
	// roi, mroi and contrib are every draw's per-channel ROI, marginal
	// ROI and summed contribution over the fitted rows,
	// [chain][channel][draw].
	roi, mroi, contrib [][][]float64
	// mapOK reports whether the MAP search supplied the start (false:
	// the fixed default start).
	mapOK bool
	// hold is the held-out rows' posterior, nil without a holdout.
	hold *bayesHoldout
	// names is the coefficient table's names (nil: coefNames, the
	// one-market table) and effectNames the roi / mroi / contribution
	// entries (nil: the channels); a geo panel sets both, and extra adds
	// its own outputs (hierarchy).
	names, effectNames []string
	extra              map[string]any
}

// bayesHoldout is the held-out rows: their times, outcomes and the
// posterior mean outcome of every draw, [row][pooled draw] (chain order,
// then draw order).
type bayesHoldout struct {
	from     int64
	fitRows  int
	y        []float64
	mu       [][]float64
	sigmaIdx int // sigma's index in the coefficient table
}

// sample runs MAP → NUTS on m and maps every draw onto the coefficient
// table, the channel figures and, with a holdout, the held-out rows'
// mean (data is every row, the first nf of them fitted). A sampler that
// cannot run on the posterior is PROCESSING_REGRESSION_NO_CONVERGE
// (details.reason the sampler's).
func (r *bayesRegression) sample(ctx context.Context, m *bayesModel, data *bayesData, nf int) (*bayesPosterior, error) {
	cfg := r.cfg
	init, mapOK := m.mapStart(mapMaxIter)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res, err := bayesSampler(cfg.targetAccept).Sample(ctx, m, uint64(cfg.seed),
		mcmc.Options{Chains: cfg.chains, Warmup: cfg.warmup, Draws: cfg.draws, Init: init})
	if err != nil {
		return nil, r.samplerError(err)
	}
	nch := len(res.Draws)
	post := &bayesPosterior{model: m, theta: res, mapOK: mapOK,
		coefs: make([][][]float64, nch), roi: make([][][]float64, nch),
		mroi: make([][][]float64, nch), contrib: make([][][]float64, nch)}
	dim := m.Dim()
	nd := len(res.Draws[0][0])
	if nf < len(data.y) {
		post.hold = &bayesHoldout{from: *cfg.holdoutFrom, fitRows: nf, y: data.y[nf:],
			mu: make([][]float64, len(data.y)-nf), sigmaIdx: dim - 1}
		for t := range post.hold.mu {
			post.hold.mu[t] = make([]float64, nch*nd)
		}
	}
	perChannel := func() [][]float64 {
		out := make([][]float64, m.nc)
		for c := range out {
			out[c] = make([]float64, nd)
		}
		return out
	}
	theta, con := make([]float64, dim), make([]float64, dim)
	for ch, draws := range res.Draws {
		post.coefs[ch] = make([][]float64, dim)
		for j := range dim {
			post.coefs[ch][j] = make([]float64, nd)
		}
		post.roi[ch], post.mroi[ch], post.contrib[ch] = perChannel(), perChannel(), perChannel()
		for d := range nd {
			for j := range dim {
				theta[j] = draws[j][d]
			}
			m.constrain(theta, con)
			beta, roi, mroi := m.effects(theta)
			row := r.coefRow(m, con, beta)
			for j, v := range row {
				post.coefs[ch][j][d] = v
			}
			for c := range m.nc {
				post.roi[ch][c][d] = roi[c]
				post.mroi[ch][c][d] = mroi[c]
				post.contrib[ch][c][d] = float64(roi[c] * m.spendTotal[c])
			}
			if post.hold != nil {
				for t, v := range m.predict(theta, data.spend, data.controls, nf) {
					post.hold.mu[t][ch*nd+d] = v
				}
			}
		}
	}
	return post, nil
}

// samplerError maps a sampler refusal: the run's ctx error unchanged, a
// sampler that cannot run on this posterior (PULSE_MCMC_FAILED, or a
// start whose density is not finite) PROCESSING_REGRESSION_NO_CONVERGE
// so the slot fails alone; anything else as is.
func (r *bayesRegression) samplerError(err error) error {
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		return err
	}
	reason, _ := ce.Details["reason"].(string)
	if ce.Code == perr.PULSE_MCMC_FAILED || (ce.Code == perr.PULSE_MCMC_INVALID && reason == "init_density_not_finite") {
		details := map[string]any{"reason": reason, "sampler_code": string(ce.Code)}
		if c, ok := ce.Details["chain"]; ok {
			details["chain"] = c
		}
		return r.slotError(perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_NO_CONVERGE,
			"the sampler could not run on the posterior: "+strings.TrimPrefix(ce.Message, "mcmc: "), details))
	}
	return err
}

// coefRow is one draw in the coefficient table's order: (intercept), the
// channel betas, the controls, each kernel parameter (decay: by
// default), each curve parameter (k:, s: by default) per channel, sigma.
func (r *bayesRegression) coefRow(m *bayesModel, con, beta []float64) []float64 {
	row := make([]float64, 0, m.Dim())
	row = append(row, con[m.iIntercept()])
	row = append(row, beta...)
	for j := range m.nz {
		row = append(row, con[m.iControl(j)])
	}
	for i := range m.np {
		for c := range m.nc {
			row = append(row, con[m.iAdstock(i, c)])
		}
	}
	for q := range m.nq {
		for c := range m.nc {
			row = append(row, con[m.iCurve(q, c)])
		}
	}
	return append(row, con[m.iSigma()])
}

// coefNames is the coefficient table's names, coefRow's order.
func (r *bayesRegression) coefNames() []string {
	cfg := r.cfg
	kp, cq := cfg.kernel.Params(), cfg.curve.Params()
	names := make([]string, 0, 2+len(cfg.controls)+(len(kp)+len(cq)+1)*len(cfg.channels))
	names = append(names, interceptName)
	names = append(names, cfg.channels...)
	names = append(names, cfg.controls...)
	prefixes := make([]string, 0, len(kp)+len(cq))
	for _, p := range kp {
		prefixes = append(prefixes, p.prefix)
	}
	for _, p := range cq {
		prefixes = append(prefixes, p.prefix)
	}
	for _, prefix := range prefixes {
		for _, c := range cfg.channels {
			names = append(names, prefix+c)
		}
	}
	return append(names, sigmaName)
}

// posteriorMoments returns each coefficient's mean and the covariance
// (divisor N − 1) over every draw of every chain, chain order then
// draw order, every product barriered.
func posteriorMoments(coefs [][][]float64) (mean []float64, cov [][]float64) {
	p := len(coefs[0])
	total := 0
	mean = make([]float64, p)
	for _, ch := range coefs {
		total += len(ch[0])
		for j, draws := range ch {
			for _, v := range draws {
				mean[j] += v
			}
		}
	}
	for j := range mean {
		mean[j] /= float64(total)
	}
	cov = make([][]float64, p)
	for i := range cov {
		cov[i] = make([]float64, p)
	}
	for _, ch := range coefs {
		for d := range ch[0] {
			for i := range p {
				di := ch[i][d] - mean[i]
				for j := i; j < p; j++ {
					cov[i][j] += float64(di * (ch[j][d] - mean[j]))
				}
			}
		}
	}
	den := float64(total - 1)
	for i := range p {
		for j := i; j < p; j++ {
			if total > 1 {
				cov[i][j] /= den
			} else {
				cov[i][j] = math.NaN()
			}
			cov[j][i] = cov[i][j]
		}
	}
	return mean, cov
}

var (
	_ extend.BufferedRegression = (*bayesRegression)(nil)
	_ extend.ContextRegression  = (*bayesRegression)(nil)
)
