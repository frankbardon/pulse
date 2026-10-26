package mmmpulse

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"hash/fnv"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
)

// bayes.go is REG_MMM_BAYES's registration and fit: the params wire
// format, the time key and row order, the refusals, the seed, and the
// MAP → NUTS run that turns a request into a posterior. The model maths
// (log posterior, gradient, MAP start) is bayes_model.go's.

// BayesName is the Bayesian media-mix model's registered regression
// type.
const BayesName types.RegressionType = "REG_MMM_BAYES"

// DecayPrefix names a channel's adstock decay coefficient in
// REG_MMM_BAYES's table: "decay:" + channel. Its Hill curve rides
// HalfSaturationPrefix and ShapePrefix as in REG_MMM_HILL.
const DecayPrefix = "decay:"

// The coefficient names REG_MMM_BAYES adds beyond its predictors.
const (
	interceptName = "(intercept)"
	sigmaName     = "sigma"
)

// The sampler settings' defaults and hard caps. A cap keeps one slot's
// sampler cost bounded; past it the request is PROCESSING_CONFIG.
const (
	defaultBayesChains       = mcmc.DefaultChains
	defaultBayesWarmup       = mcmc.DefaultWarmup
	defaultBayesDraws        = mcmc.DefaultDraws
	defaultCredibleLevel     = 0.9
	defaultBayesTargetAccept = mcmc.DefaultTargetAccept
	maxBayesChains           = 16
	maxBayesWarmup           = 20000
	maxBayesDraws            = 20000
)

// bayesSampler builds the sampler a fit runs (NUTS at the request's
// target_accept). It is a variable only so the pack's tests can count
// and fail sampler runs; nothing else sets it.
var bayesSampler = func(targetAccept float64) mcmc.Sampler {
	return mcmc.NUTS{TargetAccept: targetAccept}
}

// BayesRegistration is REG_MMM_BAYES's registration: a Bayesian
// media-mix model sampled by NUTS (the public mcmc package) from a MAP
// warm start,
//
//	y_t ~ Normal(α + Σ_j γ_j·z_jt + Σ_c β_c·hill(adstock_c(spend_c)_t, k_c, s_c), σ),
//
// with each channel's raw spend adstocked inside the model (geometric,
// WIN_DISTRIBUTED_LAG's kernel with normalize false, params.max_lag
// weeks), so its decay is a posterior parameter rather than a sweep
// axis. A channel's effect is sampled as its ROI (the default,
// LogNormal(0.2, 0.9)) or, with a "beta" prior, as β. The model is
// bayes_model.go's: internal max-abs scaling, every figure reported in
// original units.
//
// The rows are one market's time series: params.time names an integer
// field the fit sorts the rows by; a gap or a repeated value (a panel
// — several markets or geos per period) is PROCESSING_CONFIG. A null in
// the target or a predictor drops its row listwise and so leaves a gap.
//
// The coefficient table is "(intercept)", each channel's β on the raw
// outcome (contribution_t = β·hill(raw adstock_t, k, s); predictor
// order), the controls (predictor order), then "decay:<channel>",
// "k:<channel>" (raw adstock units), "s:<channel>" for each channel,
// and "sigma": Estimate the posterior mean, StdErr the posterior
// standard deviation, over every retained draw of every chain. Vcov is
// the posterior covariance of the same draws. The engine's Wald
// ci_lower / ci_upper ride the posterior SD; the model reports no
// p-value. outputs.seed is the effective sampler seed.
//
// The seed is rng_seed, or when 0 the FNV-64a of the slot's wire form
// with rng_seed cleared (Pulse's own rng_seed-0 rule), never data or the
// clock: the same request gives the same draws, bit for bit, on every
// architecture and for any worker count.
//
// No Scorer: an adstocked prediction needs the history, so holdout,
// save and ATTR_REG_PREDICT are refused; NoResample: resample and its
// knobs are refused. Every other built-in knob (penalty, selection,
// covariance, family, prior*, credible_level as a slot) is refused as on
// any extension model; the model's own priors and credible level ride
// params.
//
// Data refusals fail the slot alone: too few rows
// (PROCESSING_REGRESSION_INSUFFICIENT_DATA, checked before anything else
// is read, so pulse.New's 8-row probe never samples), an all-zero
// outcome, a channel that never spends or a constant control
// (PROCESSING_REGRESSION_RANK_DEFICIENT), and a sampler that cannot run
// on the posterior — no finite start, a collapsing or unbounded step
// size, a non-finite metric (PROCESSING_REGRESSION_NO_CONVERGE). A
// negative spend, a bad time key, an unknown or ill-typed params key and
// an invalid prior are PROCESSING_CONFIG.
func BayesRegistration() pulse.RegressionRegistration {
	return pulse.RegressionRegistration{
		Name:        BayesName,
		Description: "Bayesian media-mix model: in-model geometric adstock and Hill saturation per channel plus linear controls, sampled by NUTS; coefficients are posterior means with posterior SDs.",
		Purpose: &descriptor.Purpose{
			Plain:     "Fit a media-mix model with uncertainty: each channel's carry-over, response curve and return on spend, jointly with linear controls.",
			Intents:   []string{"drivers"},
			Questions: []string{"What return does each marketing channel earn, and how sure are we?", "How long does each channel's effect carry over?"},
			UseCases:  map[descriptor.Domain]string{descriptor.DomainOps: "Which channels earn their spend, with intervals, before moving budget."},
			NotFor:    []descriptor.Alternative{{When: "a fast point estimate over already-adstocked channels is enough", Use: string(Name)}},
			Level:     descriptor.LevelAdvanced,
		},
		Params: []pulse.ParamMeta{
			{Name: "channels", JSONType: "array", Required: true, Description: "Raw spend predictors (≥ 0) the model adstocks and saturates, at least one; the other predictors are linear controls."},
			{Name: "time", JSONType: "string", Required: true, Example: "week", Description: "Integer time field (one row per period, contiguous); the rows are sorted by it, and a gap or a repeat is refused."},
			{Name: "max_lag", JSONType: "number", Default: defaultMaxLag, Description: "Adstock window in periods (an integer ≥ 1; default 13)."},
			{Name: "adstock", JSONType: "string", Default: "geometric", Description: "Adstock kernel: geometric (the only one)."},
			{Name: "priors", JSONType: "object", Description: "Per-channel priors keyed by channel: {roi | beta, decay, k, s}, each {family, ...} (normal mu/sigma, halfnormal sigma, lognormal mu/sigma, beta alpha/beta, truncnormal mu/sigma/lower/upper). A beta prior samples β instead of the ROI; k is in peak-week units of the channel's adstock."},
			{Name: "intercept_prior", JSONType: "object", Description: "Intercept prior on the max-scaled outcome (default normal 0, 2)."},
			{Name: "control_prior", JSONType: "object", Description: "Prior of every control's coefficient on the centred, scaled control (default normal 0, 1)."},
			{Name: "sigma_prior", JSONType: "object", Description: "Noise SD prior on the max-scaled outcome (default halfnormal 1)."},
			{Name: "chains", JSONType: "number", Default: defaultBayesChains, Description: "Sampler chains (1–16; default 4)."},
			{Name: "warmup", JSONType: "number", Default: defaultBayesWarmup, Description: "Adaptation iterations per chain (1–20000; default 1000)."},
			{Name: "draws", JSONType: "number", Default: defaultBayesDraws, Description: "Retained draws per chain (1–20000; default 1000)."},
			{Name: "credible_level", JSONType: "number", Default: defaultCredibleLevel, Description: "Posterior interval level in (0, 1) (default 0.9)."},
			{Name: "sampler", JSONType: "string", Default: "nuts", Description: "Sampler: nuts (the only one)."},
			{Name: "target_accept", JSONType: "number", Default: defaultBayesTargetAccept, Description: "NUTS step-size adaptation target in (0, 1) (default 0.8); raise it when the fit diverges."},
		},
		OutputSchema: []pulse.RegressionOutputMeta{
			{Key: "seed", JSONType: "number", Description: "The effective sampler seed: rng_seed, or the one derived from the slot when rng_seed is 0."},
		},
		FieldInputs: bayesFieldInputs,
		Factory:     bayesFactory,
		Streamable:  false,
		NoResample:  true,
		Vcov:        true,
		WeightAware: false,
		PValues:     false,
	}
}

// bayesFieldInputs declares params.time, the one field the model reads
// beyond its target and predictors.
func bayesFieldInputs(raw json.RawMessage) []string {
	var p struct {
		Time string `json:"time"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Time == "" {
		return nil
	}
	return []string{p.Time}
}

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

// channelPriorWire is one channel's priors; roi and beta are exclusive.
type channelPriorWire struct {
	ROI   *priorWire `json:"roi"`
	Beta  *priorWire `json:"beta"`
	Decay *priorWire `json:"decay"`
	K     *priorWire `json:"k"`
	S     *priorWire `json:"s"`
}

// bayesParamsWire is the params object.
type bayesParamsWire struct {
	Channels       []string                    `json:"channels"`
	Time           string                      `json:"time"`
	MaxLag         *int                        `json:"max_lag"`
	Adstock        *string                     `json:"adstock"`
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
}

// bayesConfig is one slot's validated configuration.
type bayesConfig struct {
	name, target       string
	channels, controls []string
	time               string
	maxLag             int
	priors             bayesPriors
	chains, warmup     int
	draws              int
	credibleLevel      float64
	targetAccept       float64
	seed               int64
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

// bayesFactory decodes and validates the slot: every refusal here is
// PROCESSING_CONFIG and runs before any row is read.
func bayesFactory(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
	var w bayesParamsWire
	if len(spec.Params) > 0 {
		dec := json.NewDecoder(bytes.NewReader(spec.Params))
		dec.DisallowUnknownFields() // a typo in a prior is refused, never ignored
		if err := dec.Decode(&w); err != nil {
			return nil, bayesSlotError(spec, "params", "params: "+err.Error(), nil)
		}
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
	isChannel := map[string]bool{}
	for _, c := range w.Channels {
		if !slices.Contains(spec.Predictors, c) || isChannel[c] {
			return nil, bayesSlotError(spec, "params.channels", fmt.Sprintf("params.channels: %q is not a predictor, or is listed twice", c),
				map[string]any{"channel": c})
		}
		isChannel[c] = true
	}
	for _, x := range spec.Predictors {
		if x == interceptName || x == sigmaName || strings.HasPrefix(x, DecayPrefix) ||
			strings.HasPrefix(x, HalfSaturationPrefix) || strings.HasPrefix(x, ShapePrefix) {
			return nil, bayesSlotError(spec, "predictors",
				fmt.Sprintf("predictor %q collides with a coefficient name ((intercept), sigma, decay:/k:/s:)", x),
				map[string]any{"predictor": x})
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
	if w.Adstock != nil && *w.Adstock != "geometric" {
		return nil, bayesSlotError(spec, "params.adstock", fmt.Sprintf("params.adstock %q is not one of: geometric", *w.Adstock), nil)
	}
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

	cfg.priors = defaultBayesPriors(len(cfg.channels))
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
		for _, s := range []struct {
			key string
			w   *priorWire
			dst *prior
			sup support
		}{
			{"roi", cw.ROI, &cp.Effect, supportPositive},
			{"beta", cw.Beta, &cp.Effect, supportPositive},
			{"decay", cw.Decay, &cp.Decay, supportUnit},
			{"k", cw.K, &cp.K, supportPositive},
			{"s", cw.S, &cp.S, supportPositive},
		} {
			if s.w == nil {
				continue
			}
			p, err := toPrior(s.w, s.sup)
			if err != nil {
				return nil, bayesSlotError(spec, "params.priors", fmt.Sprintf("params.priors.%s.%s: %v", name, s.key, err), map[string]any{"channel": name})
			}
			*s.dst = p
		}
	}
	cfg.seed = bayesEffectiveSeed(spec)
	return &bayesRegression{cfg: cfg}, nil
}

// bayesEffectiveSeed is spec.RNGSeed when non-zero, else the FNV-64a of
// the spec's wire form with rng_seed cleared followed by a zero row
// count — the derivation every Pulse rng_seed-0 draw shares (a sweep's,
// a bootstrap's over no data cells), reimplemented because Pulse's is
// internal. It reads the spec only, never data or the clock.
func bayesEffectiveSeed(spec *types.RegressionSpec) int64 {
	if spec.RNGSeed != 0 {
		return spec.RNGSeed
	}
	cp := *spec
	cp.RNGSeed = 0
	body, err := json.Marshal(&cp)
	if err != nil {
		body = nil
	}
	h := fnv.New64a()
	_, _ = h.Write(body)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], 0)
	_, _ = h.Write(buf[:])
	return int64(h.Sum64())
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
// read, so a short input (pulse.New's probe) is a cheap coded refusal.
func (r *bayesRegression) FitContext(ctx context.Context, rows extend.Rows) (extend.RegressionOutput, error) {
	cfg := r.cfg
	n, nc, nz := rows.Len(), len(cfg.channels), len(cfg.controls)
	if dim := 2 + nz + 4*nc; n <= dim {
		return extend.RegressionOutput{}, r.slotError(perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			fmt.Sprintf("needs more than %d rows (2 + controls + 4·channels), got %d", dim, n),
			map[string]any{"n": n, "required": dim + 1}))
	}
	data, err := r.read(rows)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	m, err := newBayesModel(data.y, data.spend, data.controls, cfg.maxLag, cfg.priors)
	if err != nil {
		return extend.RegressionOutput{}, r.slotError(err)
	}
	if err := ctx.Err(); err != nil {
		return extend.RegressionOutput{}, err
	}
	post, err := r.sample(ctx, m)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	return r.output(post), nil
}

// read copies the admitted rows out and sorts them by params.time,
// refusing a time key that is not an integer field, a gap, a repeat
// and a negative spend.
func (r *bayesRegression) read(rows extend.Rows) (*bayesData, error) {
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
	for i := 1; i < n; i++ {
		switch d := all[i].t - all[i-1].t; {
		case d == 0:
			return nil, r.configError(fmt.Sprintf("params.time %q repeats %d: the model fits one market's series, one row per period; a panel (several geos or markets per period) is refused — filter to one market", cfg.time, all[i].t),
				map[string]any{"slot": "params.time", "field": cfg.time, "reason": "duplicate_time", "time": all[i].t})
		case d != 1:
			return nil, r.configError(fmt.Sprintf("params.time %q jumps from %d to %d: the periods must be contiguous (a filtered or null row leaves a gap the adstock would carry across)", cfg.time, all[i-1].t, all[i].t),
				map[string]any{"slot": "params.time", "field": cfg.time, "reason": "time_gap", "from": all[i-1].t, "to": all[i].t})
		}
	}
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
	// roi is every draw's per-channel ROI, [chain][channel][draw].
	roi [][][]float64
	// mapOK reports whether the MAP search supplied the start (false:
	// the fixed default start).
	mapOK bool
}

// sample runs MAP → NUTS on m and maps every draw onto the coefficient
// table. A sampler that cannot run on the posterior is
// PROCESSING_REGRESSION_NO_CONVERGE (details.reason the sampler's).
func (r *bayesRegression) sample(ctx context.Context, m *bayesModel) (*bayesPosterior, error) {
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
	post := &bayesPosterior{model: m, theta: res, mapOK: mapOK,
		coefs: make([][][]float64, len(res.Draws)), roi: make([][][]float64, len(res.Draws))}
	dim := m.Dim()
	theta, con := make([]float64, dim), make([]float64, dim)
	for ch, draws := range res.Draws {
		nd := len(draws[0])
		post.coefs[ch] = make([][]float64, dim)
		for j := range dim {
			post.coefs[ch][j] = make([]float64, nd)
		}
		post.roi[ch] = make([][]float64, m.nc)
		for c := range m.nc {
			post.roi[ch][c] = make([]float64, nd)
		}
		for d := range nd {
			for j := range dim {
				theta[j] = draws[j][d]
			}
			m.constrain(theta, con)
			beta, roi := m.channelEffects(theta)
			row := r.coefRow(m, con, beta)
			for j, v := range row {
				post.coefs[ch][j][d] = v
			}
			for c, v := range roi {
				post.roi[ch][c][d] = v
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
// channel betas, the controls, decay:, k:, s: per channel, sigma.
func (r *bayesRegression) coefRow(m *bayesModel, con, beta []float64) []float64 {
	row := make([]float64, 0, m.Dim())
	row = append(row, con[m.iIntercept()])
	row = append(row, beta...)
	for j := range m.nz {
		row = append(row, con[m.iControl(j)])
	}
	for c := range m.nc {
		row = append(row, con[m.iDecay(c)])
	}
	for c := range m.nc {
		row = append(row, con[m.iK(c)])
	}
	for c := range m.nc {
		row = append(row, con[m.iS(c)])
	}
	return append(row, con[m.iSigma()])
}

// coefNames is the coefficient table's names, coefRow's order.
func (r *bayesRegression) coefNames() []string {
	cfg := r.cfg
	names := make([]string, 0, 2+len(cfg.controls)+4*len(cfg.channels))
	names = append(names, interceptName)
	names = append(names, cfg.channels...)
	names = append(names, cfg.controls...)
	for _, prefix := range []string{DecayPrefix, HalfSaturationPrefix, ShapePrefix} {
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

// output is the regression output: posterior mean and SD per
// coefficient, the posterior covariance and the effective seed.
func (r *bayesRegression) output(post *bayesPosterior) extend.RegressionOutput {
	mean, cov := posteriorMoments(post.coefs)
	names := r.coefNames()
	coefs := make([]extend.RegressionCoefficient, len(names))
	for i, name := range names {
		coefs[i] = extend.RegressionCoefficient{Name: name, Estimate: mean[i], StdErr: extend.Figure(math.Sqrt(cov[i][i]))}
	}
	return extend.RegressionOutput{
		Coefficients: coefs,
		Vcov:         cov,
		Outputs:      map[string]any{"seed": r.cfg.seed},
	}
}

var (
	_ extend.BufferedRegression = (*bayesRegression)(nil)
	_ extend.ContextRegression  = (*bayesRegression)(nil)
)
