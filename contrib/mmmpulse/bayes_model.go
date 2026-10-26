package mmmpulse

import (
	"fmt"
	"math"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/optimize"
)

// bayes_model.go is REG_MMM_BAYES's model maths: the log posterior on
// the unconstrained space, its analytic gradient and a MAP warm start —
// an mcmc.Model. It reads no request: the registration, the params
// decoding and the sampler run are the regression's, not the model's.
//
// The model, per week t (rows in time order) on the internally scaled
// data:
//
//	ỹ_t = y_t / max|y|
//	ã_ct = adstock(spend_c, decay_c)_t / max(spend_c)
//	h_ct = hill(ã_ct, k_c, s_c) = 1 / (1 + (k_c/ã_ct)^s_c)
//	z̃_jt = (z_jt − mean z_j) / max|z_j − mean z_j|
//	ỹ_t ~ Normal(α + Σ_j γ_j·z̃_jt + Σ_c β_c·h_ct, σ)
//
// adstock is WIN_DISTRIBUTED_LAG's geometric kernel with normalize
// false — Σ_{l=0..maxLag} decay^l·spend_{t−l}, lag 0 first, the same
// weights and the same products, so the model's adstock is bit-equal to
// the window's. The adstock runs on the raw spend and is scaled after,
// so the scaled ã is the window column over the channel's peak spend.
// The controls are centred as well as scaled: a control far from zero
// (a price near 10) would otherwise make it and the intercept a ridge
// the diagonal-metric sampler crawls along.
//
// A channel's effect is either its ROI (the default) or its β. In ROI
// mode the sampled primitive is ROI_c in original units — summed
// contribution over summed spend — and β_c is derived per evaluation,
//
//	β_c = ROI_c · Σ_t spend_ct / (max|y| · Σ_t h_ct),
//
// so the gradient with respect to decay, k and s flows through β_c as
// well as through h. No Jacobian is owed for β: it is a deterministic
// function of the sampled ROI, decay, k and s.
//
// The parameter vector θ (the unconstrained space) is
//
//	[α, γ_1…γ_J, decay_1…decay_C, k_1…k_C, s_1…s_C, e_1…e_C, σ]
//
// with e_c the channel's ROI or β; each coordinate maps onto its
// prior's support through the prior's transform (logit for a beta,
// bounded logit for a truncnormal, log for a lognormal or halfnormal,
// the identity for a normal), whose log-Jacobian is part of the density.
// Every value inside the density is on the scaled data; constrain maps a
// draw back to original units.
//
// Every product feeding an add is float64()-barriered, the
// transcendentals are detmath's and every sum runs in index order, so
// the density's bits — and NUTS's draws — are the same on every
// architecture. The model holds no mutable state: each call allocates
// its own scratch, so the sampler's chains may share it.

// defaultMaxLag is the adstock window when params.max_lag is absent: 13
// weeks, a quarter of carry-over (Jin et al. 2017's L).
const defaultMaxLag = 13

// bayesChannelPriors is one channel's priors; beta selects β mode
// (Effect is then β's prior on the scaled outcome) over ROI mode
// (Effect is the ROI's prior in original units).
type bayesChannelPriors struct {
	Decay, K, S, Effect prior
	Beta                bool
}

// bayesPriors is every prior of one model.
type bayesPriors struct {
	Intercept, Control, Sigma prior
	Channels                  []bayesChannelPriors
}

// The default priors, all on the scaled data except the ROI.
//
//   - decay: Beta(1, 1), uniform on (0, 1) (Meridian's default). PyMC-
//     Marketing's Beta(1, 3) puts three times the density at 0 that it
//     puts at 0.5 and a sixth of it at 0.6 — a pull toward fast decay
//     that, on the ridge decay shares with k and β, biases a long-memory
//     channel's ROI (the joint fixture's TV decays at 0.6). Uniform lets
//     the data place the decay; a practitioner with a belief sets beta.
//   - k: TruncNormal(1, 1, 0.02, 10) on the scaled adstock, whose unit is
//     the channel's peak weekly spend: centred at a half-saturation of
//     one peak week, sd one peak week, floored at 2% (below it the curve
//     is a "did it spend" step) and capped at ten peak weeks (above it
//     only β/k^s is identified, the linear ridge) — Meridian's
//     TruncNormal(0.8, 0.8, 0.1, 10) carried to max scaling.
//   - s: LogNormal(0.3, 0.5): median 1.35, 95% of the mass on
//     [0.5, 3.6]. It sits between the concave reach form (s = 1, Jin et
//     al. and Meridian's fixed default) and an S-curve (s ≈ 2), covers
//     both with room, and keeps the mass where REG_MMM_HILL bounds the
//     shape ([0.5, 5]); Meridian's LogNormal(0.7, 0.4) puts s = 1 below
//     its 5th percentile, which would bend every concave channel.
//   - ROI: LogNormal(0.2, 0.9), Meridian's (mean ≈ 1.83).
//   - β (beta mode): HalfNormal(1) on the max-scaled outcome: a channel
//     at full saturation moving the outcome by more than its peak is in
//     the tail.
//   - intercept Normal(0, 2), controls Normal(0, 1) on the centred and
//     scaled control (γ = 1 moves the outcome by its peak across the
//     control's range), σ HalfNormal(1) — weakly informative on the
//     scaled outcome.
func defaultChannelPriors() bayesChannelPriors {
	return bayesChannelPriors{
		Decay:  betaPrior(1, 1),
		K:      truncNormalPrior(1, 1, 0.02, 10),
		S:      logNormalPrior(0.3, 0.5),
		Effect: defaultROIPrior(),
	}
}

func defaultROIPrior() prior  { return logNormalPrior(0.2, 0.9) }
func defaultBetaPrior() prior { return halfNormalPrior(1) }

func defaultBayesPriors(channels int) bayesPriors {
	p := bayesPriors{Intercept: normalPrior(0, 2), Control: normalPrior(0, 1), Sigma: halfNormalPrior(1)}
	for range channels {
		p.Channels = append(p.Channels, defaultChannelPriors())
	}
	return p
}

// bayesModel is one fit's posterior. Build it with newBayesModel; it is
// read-only afterwards.
type bayesModel struct {
	n, nc, nz int
	maxLag    int
	ys        []float64   // scaled outcome
	spend     [][]float64 // raw spend per channel, time order
	zs        [][]float64 // centred, scaled controls
	yScale    float64     // max|y|
	xScale    []float64   // per channel: max spend
	zMean     []float64
	zScale    []float64
	// spendShare is Σ_t spend_ct / yScale: ROI_c · spendShare_c / Σ h_c
	// is β_c on the scaled outcome.
	spendShare []float64
	priors     []prior // per θ coordinate
	isBeta     []bool  // per channel: β mode
}

func bayesConfigError(msg string, details map[string]any) error {
	return perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG, msg, details)
}

// newBayesModel builds the posterior over y (time order), each
// channel's raw spend (≥ 0) and each control, all of one length. The
// refusals: rows not above the parameter count
// (PROCESSING_REGRESSION_INSUFFICIENT_DATA); a negative spend, a
// non-finite value, a mis-sized column or an invalid prior
// (PROCESSING_CONFIG); an outcome that is all zero, a channel that
// never spends or a constant control
// (PROCESSING_REGRESSION_RANK_DEFICIENT).
func newBayesModel(y []float64, spend, controls [][]float64, maxLag int, pr bayesPriors) (*bayesModel, error) {
	n, nc, nz := len(y), len(spend), len(controls)
	if nc == 0 {
		return nil, bayesConfigError("REG_MMM_BAYES needs at least one channel", nil)
	}
	if maxLag < 1 {
		return nil, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: max_lag must be ≥ 1 (got %d)", maxLag), map[string]any{"max_lag": maxLag})
	}
	if len(pr.Channels) != nc {
		return nil, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: %d channel priors for %d channels", len(pr.Channels), nc), nil)
	}
	m := &bayesModel{n: n, nc: nc, nz: nz, maxLag: maxLag, spend: spend,
		ys: make([]float64, n), zs: make([][]float64, nz), xScale: make([]float64, nc),
		zMean: make([]float64, nz), zScale: make([]float64, nz), spendShare: make([]float64, nc),
		isBeta: make([]bool, nc)}
	if dim := m.Dim(); n <= dim {
		return nil, perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			fmt.Sprintf("REG_MMM_BAYES needs more than %d rows (2 + controls + 4·channels), got %d", dim, n),
			map[string]any{"n": n, "required": dim + 1})
	}
	for _, v := range y {
		if !finite(v) {
			return nil, bayesConfigError("REG_MMM_BAYES: the outcome has a non-finite value", nil)
		}
		m.yScale = max(m.yScale, math.Abs(v))
	}
	if !(m.yScale > 0) {
		return nil, perr.NewCodedError(perr.PROCESSING_REGRESSION_RANK_DEFICIENT, "REG_MMM_BAYES: the outcome is zero in every row")
	}
	for t, v := range y {
		m.ys[t] = v / m.yScale
	}
	for c, x := range spend {
		if len(x) != n {
			return nil, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: channel %d has %d rows, the outcome %d", c, len(x), n), nil)
		}
		total := 0.0
		for _, v := range x {
			if !(v >= 0) || math.IsInf(v, 0) {
				return nil, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: channel %d has a negative or non-finite spend (%v)", c, v),
					map[string]any{"channel": c})
			}
			m.xScale[c] = max(m.xScale[c], v)
			total += v
		}
		if !(m.xScale[c] > 0) {
			return nil, perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_RANK_DEFICIENT,
				fmt.Sprintf("REG_MMM_BAYES: channel %d never spends", c), map[string]any{"channel": c})
		}
		m.spendShare[c] = total / m.yScale
	}
	for j, z := range controls {
		if len(z) != n {
			return nil, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: control %d has %d rows, the outcome %d", j, len(z), n), nil)
		}
		mean := 0.0
		for _, v := range z {
			if !finite(v) {
				return nil, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: control %d has a non-finite value", j), map[string]any{"control": j})
			}
			mean += v
		}
		mean /= float64(n)
		scale := 0.0
		for _, v := range z {
			scale = max(scale, math.Abs(v-mean))
		}
		if !(scale > 0) {
			return nil, perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_RANK_DEFICIENT,
				fmt.Sprintf("REG_MMM_BAYES: control %d is constant", j), map[string]any{"control": j})
		}
		m.zMean[j], m.zScale[j] = mean, scale
		m.zs[j] = make([]float64, n)
		for t, v := range z {
			m.zs[j][t] = (v - mean) / scale
		}
	}

	m.priors = make([]prior, m.Dim())
	set := func(i int, p prior, s support, what string) error {
		cp, err := p.compile(s)
		if err != nil {
			return bayesConfigError("REG_MMM_BAYES: "+what+": "+err.Error(), map[string]any{"prior": what})
		}
		m.priors[i] = cp
		return nil
	}
	if err := set(m.iIntercept(), pr.Intercept, supportReal, "intercept"); err != nil {
		return nil, err
	}
	for j := range nz {
		if err := set(m.iControl(j), pr.Control, supportReal, "control"); err != nil {
			return nil, err
		}
	}
	for c, cp := range pr.Channels {
		m.isBeta[c] = cp.Beta
		for _, s := range []struct {
			i    int
			p    prior
			sup  support
			what string
		}{
			{m.iDecay(c), cp.Decay, supportUnit, "decay"},
			{m.iK(c), cp.K, supportPositive, "k"},
			{m.iS(c), cp.S, supportPositive, "s"},
			{m.iEffect(c), cp.Effect, supportPositive, "effect"},
		} {
			if err := set(s.i, s.p, s.sup, fmt.Sprintf("channel %d %s", c, s.what)); err != nil {
				return nil, err
			}
		}
	}
	if err := set(m.iSigma(), pr.Sigma, supportPositive, "sigma"); err != nil {
		return nil, err
	}
	return m, nil
}

// The θ layout.
func (m *bayesModel) iIntercept() int    { return 0 }
func (m *bayesModel) iControl(j int) int { return 1 + j }
func (m *bayesModel) iDecay(c int) int   { return 1 + m.nz + c }
func (m *bayesModel) iK(c int) int       { return 1 + m.nz + m.nc + c }
func (m *bayesModel) iS(c int) int       { return 1 + m.nz + 2*m.nc + c }
func (m *bayesModel) iEffect(c int) int  { return 1 + m.nz + 3*m.nc + c }
func (m *bayesModel) iSigma() int        { return 1 + m.nz + 4*m.nc }

// Dim is 2 + controls + 4·channels.
func (m *bayesModel) Dim() int { return 2 + m.nz + 4*m.nc }

// geometricWeights returns WIN_DISTRIBUTED_LAG's geometric kernel
// w_l = decay^l (l = 0..maxLag, normalize false) — the same detmath.Pow
// — and its derivative dw_l/d decay = l·decay^(l−1).
func geometricWeights(decay float64, maxLag int) (w, dw []float64) {
	w, dw = make([]float64, maxLag+1), make([]float64, maxLag+1)
	for l := range w {
		w[l] = detmath.Pow(decay, float64(l))
		if l > 0 {
			dw[l] = float64(float64(l) * w[l-1])
		}
	}
	return w, dw
}

// adstock writes a_t = Σ_{l=0..min(t, maxLag)} w_l·x_{t−l} into a (lag
// 0 first, each product barriered — WIN_DISTRIBUTED_LAG's loop) and, when
// da is non-nil, the same sum over dw into da.
func adstock(x, w, dw, a, da []float64) {
	for t := range x {
		acc, dacc := 0.0, 0.0
		for l := 0; l < len(w) && l <= t; l++ {
			acc += float64(w[l] * x[t-l])
			if da != nil {
				dacc += float64(dw[l] * x[t-l])
			}
		}
		a[t] = acc
		if da != nil {
			da[t] = dacc
		}
	}
}

// hillGrad is hill(a, k, s) = 1/(1 + (k/a)^s) (hillCurve's formula, bit
// for bit) and its partials in a, k and s. At a = 0 the curve is 0 for
// every k and s, and so are its partials.
func hillGrad(a, k, s float64) (h, dha, dhk, dhs float64) {
	if !(a > 0) {
		return 0, 0, 0, 0
	}
	q := detmath.Pow(k/a, s)
	h = 1 / (1 + q)
	if math.IsInf(q, 1) || q == 0 {
		return h, 0, 0, 0
	}
	// h·(1 − h) with 1 − h = q·h, exact near saturation.
	hh := float64(h * float64(q*h))
	sh := float64(s * hh)
	return h, sh / a, -sh / k, float64(hh * detmath.Log(a/k))
}

// coordinate is one θ coordinate mapped onto its support: the value,
// dvalue/dθ and the prior-plus-Jacobian log density with its θ
// derivative.
type coordinate struct {
	y, dy  float64
	lp, dl float64
}

func (m *bayesModel) mapCoordinate(i int, x float64) coordinate {
	p := m.priors[i]
	f := p.transform().Forward(x)
	lp, dlp := p.logDensity(f.Y)
	return coordinate{y: f.Y, dy: f.DyDx, lp: lp + f.LogJac, dl: float64(dlp*f.DyDx) + f.DLogJac}
}

// channelState is one channel's curve over the rows at one θ.
type channelState struct {
	decay, k, s float64
	h           []float64 // h_t
	dhd         []float64 // ∂h_t/∂decay
	dhk, dhs    []float64 // ∂h_t/∂k, ∂h_t/∂s
	hSum        float64   // Σ_t h_t
	beta        float64   // β on the scaled outcome (derived in ROI mode)
}

// curve evaluates channel c's adstock and Hill curve at the given
// constrained decay, k and s; grad asks for the partials too.
func (m *bayesModel) curve(c int, decay, k, s float64, grad bool) channelState {
	st := channelState{decay: decay, k: k, s: s, h: make([]float64, m.n)}
	a := make([]float64, m.n)
	w, dw := geometricWeights(decay, m.maxLag)
	var da []float64
	if grad {
		da = make([]float64, m.n)
		st.dhd, st.dhk, st.dhs = make([]float64, m.n), make([]float64, m.n), make([]float64, m.n)
	}
	adstock(m.spend[c], w, dw, a, da)
	for t := range a {
		h, dha, dhk, dhs := hillGrad(a[t]/m.xScale[c], k, s)
		st.h[t] = h
		st.hSum += h
		if grad {
			st.dhd[t] = float64(dha * (da[t] / m.xScale[c]))
			st.dhk[t], st.dhs[t] = dhk, dhs
		}
	}
	return st
}

// LogDensityGrad is log p(θ | data) up to a constant — the Normal
// likelihood on the scaled outcome, every prior and every transform's
// log-Jacobian — with its gradient written into grad. A θ whose density
// or gradient is not finite (an ROI channel whose curve is 0 in every
// row, an overflow far out on the unconstrained scale) is −Inf.
func (m *bayesModel) LogDensityGrad(theta, grad []float64) float64 {
	lp := m.logDensity(theta, grad)
	if !finite(lp) {
		return math.Inf(-1)
	}
	for _, g := range grad {
		if !finite(g) {
			return math.Inf(-1)
		}
	}
	return lp
}

func (m *bayesModel) logDensity(theta, grad []float64) float64 {
	dim := m.Dim()
	coords := make([]coordinate, dim)
	lp := 0.0
	for i := range dim {
		coords[i] = m.mapCoordinate(i, theta[i])
		lp += coords[i].lp
	}
	if !finite(lp) {
		return math.Inf(-1)
	}
	chans := make([]channelState, m.nc)
	for c := range m.nc {
		st := m.curve(c, coords[m.iDecay(c)].y, coords[m.iK(c)].y, coords[m.iS(c)].y, true)
		e := coords[m.iEffect(c)].y
		if m.isBeta[c] {
			st.beta = e
		} else {
			if !(st.hSum > 0) {
				return math.Inf(-1)
			}
			st.beta = float64(e*m.spendShare[c]) / st.hSum
		}
		chans[c] = st
	}

	// Residuals r_t = ỹ_t − μ_t and the likelihood.
	alpha, sigma := coords[m.iIntercept()].y, coords[m.iSigma()].y
	r := make([]float64, m.n)
	ssr := 0.0
	for t := range m.n {
		mu := alpha
		for j := range m.nz {
			mu += float64(coords[m.iControl(j)].y * m.zs[j][t])
		}
		for c := range m.nc {
			mu += float64(chans[c].beta * chans[c].h[t])
		}
		r[t] = m.ys[t] - mu
		ssr += float64(r[t] * r[t])
	}
	s2 := float64(sigma * sigma)
	lp += -float64(float64(m.n)*detmath.Log(sigma)) - float64(0.5*ssr)/s2

	// ∂L/∂μ_t = r_t/σ²; each likelihood partial is chained through its
	// coordinate's dvalue/dθ and added to the prior's.
	g := make([]float64, m.n)
	for t := range r {
		g[t] = r[t] / s2
	}
	for i := range grad {
		grad[i] = coords[i].dl
	}
	add := func(i int, dLdy float64) { grad[i] += float64(dLdy * coords[i].dy) }

	sumG := 0.0
	for _, v := range g {
		sumG += v
	}
	add(m.iIntercept(), sumG)
	for j := range m.nz {
		d := 0.0
		for t, v := range g {
			d += float64(v * m.zs[j][t])
		}
		add(m.iControl(j), d)
	}
	for c, st := range chans {
		gh := 0.0 // G_c = Σ_t g_t·h_t
		for t, v := range g {
			gh += float64(v * st.h[t])
		}
		// In ROI mode β = ROI·share/Σh, so a curve parameter φ moves μ_t
		// by β·(∂h_t/∂φ − h_t·Σ∂h/∂φ / Σh): its partial is
		// β·Σ_t (g_t − G/Σh)·∂h_t/∂φ. In β mode the shift is 0.
		shift := 0.0
		if m.isBeta[c] {
			add(m.iEffect(c), gh)
		} else {
			shift = gh / st.hSum
			add(m.iEffect(c), float64(gh*st.beta)/coords[m.iEffect(c)].y)
		}
		var dd, dk, ds float64
		for t, v := range g {
			u := v - shift
			dd += float64(u * st.dhd[t])
			dk += float64(u * st.dhk[t])
			ds += float64(u * st.dhs[t])
		}
		add(m.iDecay(c), float64(st.beta*dd))
		add(m.iK(c), float64(st.beta*dk))
		add(m.iS(c), float64(st.beta*ds))
	}
	// ∂L/∂σ = −n/σ + SSR/σ³.
	add(m.iSigma(), -float64(m.n)/sigma+ssr/float64(s2*sigma))
	return lp
}

// constrain maps θ to original units, in θ's layout: the intercept and
// each control's coefficient on the raw outcome and raw control, the
// decay, k in raw adstock units, s, the ROI (ROI mode) or β on the raw
// outcome (β mode), and σ on the raw outcome. It is safe for concurrent
// use (an mcmc.Options.Constrain).
func (m *bayesModel) constrain(theta, out []float64) {
	v := make([]float64, m.Dim())
	for i := range v {
		v[i] = m.priors[i].transform().Forward(theta[i]).Y
	}
	icpt := v[m.iIntercept()]
	for j := range m.nz {
		g := v[m.iControl(j)]
		icpt -= float64(g*m.zMean[j]) / m.zScale[j]
		out[m.iControl(j)] = float64(m.yScale*g) / m.zScale[j]
	}
	out[m.iIntercept()] = float64(m.yScale * icpt)
	for c := range m.nc {
		out[m.iDecay(c)] = v[m.iDecay(c)]
		out[m.iK(c)] = float64(v[m.iK(c)] * m.xScale[c])
		out[m.iS(c)] = v[m.iS(c)]
		e := v[m.iEffect(c)]
		if m.isBeta[c] {
			e = float64(m.yScale * e)
		}
		out[m.iEffect(c)] = e
	}
	out[m.iSigma()] = float64(m.yScale * v[m.iSigma()])
}

// channelEffects returns, per channel at θ, β on the raw outcome
// (contribution_t = β·hill(raw adstock_t, k_raw, s)) and the ROI
// (Σ_t contribution_t / Σ_t spend_t), whichever of the two the channel
// samples. A ROI channel's β is NaN when its curve is 0 in every row.
func (m *bayesModel) channelEffects(theta []float64) (beta, roi []float64) {
	beta, roi = make([]float64, m.nc), make([]float64, m.nc)
	val := func(i int) float64 { return m.priors[i].transform().Forward(theta[i]).Y }
	for c := range m.nc {
		st := m.curve(c, val(m.iDecay(c)), val(m.iK(c)), val(m.iS(c)), false)
		e := val(m.iEffect(c))
		if m.isBeta[c] {
			beta[c] = float64(m.yScale * e)
			roi[c] = float64(e*st.hSum) / m.spendShare[c]
			continue
		}
		roi[c] = e
		if st.hSum > 0 {
			beta[c] = float64(m.yScale*float64(e*m.spendShare[c])) / st.hSum
		} else {
			beta[c] = math.NaN()
		}
	}
	return beta, roi
}

// toTheta maps constrained scaled values onto θ through each prior's
// inverse transform.
func (m *bayesModel) toTheta(v []float64) []float64 {
	theta := make([]float64, len(v))
	for i, y := range v {
		theta[i] = m.priors[i].transform().Inverse(y)
	}
	return theta
}

// defaultInit is the fixed, data-derived start: the intercept at the
// scaled outcome's mean, the controls at 0, each decay at its prior's
// center, k at the mean positive scaled adstock there (clamped inside a
// truncnormal's bounds), s and the effect at their prior's center, σ at
// the scaled outcome's standard deviation. A coordinate whose value
// falls outside its prior's support starts at the prior's center.
func (m *bayesModel) defaultInit() []float64 {
	v := make([]float64, m.Dim())
	mean := 0.0
	for _, y := range m.ys {
		mean += y
	}
	mean /= float64(m.n)
	ss := 0.0
	for _, y := range m.ys {
		d := y - mean
		ss += float64(d * d)
	}
	v[m.iIntercept()] = mean
	v[m.iSigma()] = math.Sqrt(ss / float64(m.n))
	for c := range m.nc {
		d := m.priors[m.iDecay(c)].center()
		v[m.iDecay(c)] = d
		w, _ := geometricWeights(d, m.maxLag)
		a := make([]float64, m.n)
		adstock(m.spend[c], w, nil, a, nil)
		sum, cnt := 0.0, 0
		for _, x := range a {
			if x > 0 {
				sum += x / m.xScale[c]
				cnt++
			}
		}
		v[m.iK(c)] = sum / float64(cnt)
		v[m.iS(c)] = m.priors[m.iS(c)].center()
		v[m.iEffect(c)] = m.priors[m.iEffect(c)].center()
	}
	theta := m.toTheta(v)
	for i, x := range theta {
		if lp, _ := m.priors[i].logDensity(v[i]); !finite(x) || !finite(lp) {
			theta[i] = m.priors[i].transform().Inverse(m.priors[i].center())
		}
	}
	return theta
}

// mapBound boxes the MAP search on the unconstrained scale: far enough
// out that no posterior mode of scaled data sits past it (e^20 on a log
// coordinate), near enough that no evaluation overflows.
const mapBound = 20

// mapMaxIter is the MAP search's SLSQP iteration cap (mapStart's
// maxIter).
const mapMaxIter = 500

// mapStart is the sampler's warm start: the posterior mode on the
// unconstrained scale (the density NUTS samples, Jacobians included),
// found by optimize.Minimize from defaultInit inside ±mapBound in at
// most maxIter iterations (mapMaxIter in a fit). It is
// deterministic: the same data and priors give the same bits. A search
// that errors, stops short or ends below the start's density falls back
// to defaultInit, reported by ok = false.
func (m *bayesModel) mapStart(maxIter int) (theta []float64, ok bool) {
	x0 := m.defaultInit()
	dim := m.Dim()
	lower, upper := make([]float64, dim), make([]float64, dim)
	for i := range lower {
		lower[i], upper[i] = -mapBound, mapBound
		x0[i] = min(max(x0[i], -mapBound), mapBound)
	}
	scratch := make([]float64, dim)
	lp0 := m.LogDensityGrad(x0, scratch)
	// A −Inf density is a large finite objective, so SLSQP's line
	// search backs off it rather than meeting an infinity.
	const wall = 1e100
	f := func(x []float64) float64 {
		lp := m.LogDensityGrad(x, make([]float64, dim))
		if !finite(lp) {
			return wall
		}
		return -lp
	}
	g := func(x []float64) []float64 {
		gr := make([]float64, dim)
		if !finite(m.LogDensityGrad(x, gr)) {
			clear(gr)
			return gr
		}
		for i := range gr {
			gr[i] = -gr[i]
		}
		return gr
	}
	res, err := optimize.Minimize(f, g, x0, optimize.MinimizeOptions{Lower: lower, Upper: upper, MaxIter: maxIter})
	if err != nil || res == nil {
		return x0, false
	}
	if lp := m.LogDensityGrad(res.X, scratch); !finite(lp) || (finite(lp0) && lp < lp0) {
		return x0, false
	}
	return res.X, true
}

var _ mcmc.Model = (*bayesModel)(nil)
