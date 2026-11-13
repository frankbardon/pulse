package regmmm

import (
	"fmt"
	"math"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/mcmc"
)

// bayes_geo_model.go is REG_MMM_BAYES's geo-hierarchical model
// (params.geo): one posterior over G markets' series that partially
// pools each channel's effect and every adstock-kernel parameter across
// the geos, non-centred. Like bayes_model.go it reads no request.
//
// Per geo g and week t (each geo's rows in time order; the scaling is the
// whole panel's, so a figure means the same in every geo):
//
//	ỹ_gt = y_gt / max|y|
//	ã_gct = adstock(spend_gc, φ_gc)_t / max(spend_c)
//	h_gct = curve(ã_gct, q_c)                      (curves shared)
//	z̃_gjt = (z_gjt − mean z_j) / max|z_j − mean z_j|
//	ỹ_gt ~ Normal(α_g + Σ_j γ_j·z̃_gjt + Σ_c β_gc·h_gct, σ)
//
// with an intercept per geo (unpooled: the geos' levels differ), shared
// control coefficients γ, shared curve parameters q_c and one σ. Each
// POOLED class — the channel's effect e (ROI by default, β under a beta
// prior) and each kernel parameter φ_i (decay; decay and peak; shape and
// scale) — is, per channel c and geo g, on its prior's unconstrained
// scale,
//
//	e_gc = T(μ_c + τ_c·η_gc),   η_gc ~ Normal(0, 1),   τ_c ~ HalfNormal(geoTauSigma),
//
// T the class prior's transform (bayes_prior.go): μ_c is sampled exactly
// as the one-market model samples the parameter — the channel's prior,
// with its transform's log-Jacobian — so T(μ_c) is the population value,
// reported under the term's own name; τ_c is the geos' spread on that
// scale (log ROI, logit decay, …). The non-centred form keeps the
// τ → 0 funnel out of the sampler's way. A geo's value is a
// deterministic function of μ, τ and η, so it owes no Jacobian.
//
// In ROI mode β_gc = ROI_gc · Σ_t spend_gct / (max|y| · Σ_t h_gct), the
// one-market rule per geo.
//
// θ is
//
//	[α_1…α_G, γ_1…γ_J, μ(φ1)_1…μ(φ1)_C, …, q1_1…q1_C, …, μ(e)_1…μ(e)_C, σ,
//	 τ(e)_1…τ(e)_C, τ(φ1)_1…τ(φ1)_C, …, η(e)_11…η(e)_1G, …, η(φ1)_11…]
//
// — the one-market layout with α per geo, then the scales, then the η's
// (class, channel, geo order). Every product feeding an add is
// float64()-barriered, transcendentals are detmath's and every sum runs
// in index order, so the density's bits are the same on every
// architecture.

// geoTauSigma is every pooling scale's HalfNormal prior σ, on the
// pooled parameter's unconstrained scale: median 0.34 — a typical geo
// within ±40% of the population ROI (log scale) — with 95% of the mass
// below 1.
const geoTauSigma = 0.5

// geoSeries is one geo's fitted rows on the panel's scale.
type geoSeries struct {
	n          int
	ys         []float64   // scaled outcome
	spend      [][]float64 // raw spend per channel
	zs         [][]float64 // centred, scaled controls
	spendShare []float64   // per channel: Σ_t spend_gct / yScale
	spendTotal []float64   // per channel: Σ_t spend_gct
}

// bayesGeoModel is one geo panel's posterior. Build it with
// newBayesGeoModel; it is read-only afterwards.
type bayesGeoModel struct {
	// shape carries the panel's shared facts — channel, control, kernel
	// and curve counts, the kernel and curve, maxLag, the scaling and
	// each channel's β mode — and holds no rows: curveOn runs a geo's
	// spend through it.
	shape  *bayesModel
	ng, nk int // geos; pooled classes (the effect, then each kernel parameter)
	geos   []geoSeries
	nObs   int
	priors []prior // per θ coordinate
}

// geoPanel is newBayesGeoModel's input: per geo its outcome, each
// channel's raw spend and each control, all of one length per geo, in
// time order; the channel and geo names label its refusals.
type geoPanel struct {
	y        [][]float64
	spend    [][][]float64 // [geo][channel][t]
	controls [][][]float64 // [geo][control][t]
	geos     []string
	channels []string
}

// newBayesGeoModel builds the geo panel's posterior. The refusals: rows
// not above the parameter count (PROCESSING_REGRESSION_INSUFFICIENT_DATA);
// a negative or non-finite value (PROCESSING_CONFIG); an outcome that is
// zero everywhere, a channel that never spends in some geo, or a
// constant control (PROCESSING_REGRESSION_RANK_DEFICIENT).
func newBayesGeoModel(in geoPanel, maxLag int, pr bayesPriors) (*bayesGeoModel, error) {
	ng := len(in.y)
	if ng == 0 || len(in.spend) != ng || len(in.controls) != ng {
		return nil, bayesConfigError("REG_MMM_BAYES: the geo panel needs at least one geo, each with its spend and controls", nil)
	}
	nc, nz := len(in.spend[0]), len(in.controls[0])
	if nc == 0 {
		return nil, bayesConfigError("REG_MMM_BAYES needs at least one channel", nil)
	}
	if maxLag < 1 {
		return nil, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: max_lag must be ≥ 1 (got %d)", maxLag), map[string]any{"max_lag": maxLag})
	}
	if len(pr.Channels) != nc {
		return nil, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: %d channel priors for %d channels", len(pr.Channels), nc), nil)
	}
	kernel := pr.Kernel
	if kernel == nil {
		kernel = geometricKernel{}
	}
	curve := pr.Curve
	if curve == nil {
		curve = hillCurveModel{}
	}
	shape := &bayesModel{nc: nc, nz: nz, maxLag: maxLag, kernel: kernel, np: len(kernel.Params()), sat: curve, nq: len(curve.Params()),
		xScale: make([]float64, nc), zMean: make([]float64, nz), zScale: make([]float64, nz), isBeta: make([]bool, nc)}
	m := &bayesGeoModel{shape: shape, ng: ng, nk: 1 + shape.np, geos: make([]geoSeries, ng)}
	geoName := func(g int) string {
		if g < len(in.geos) {
			return in.geos[g]
		}
		return fmt.Sprint(g)
	}
	chName := func(c int) string {
		if c < len(in.channels) {
			return in.channels[c]
		}
		return fmt.Sprint(c)
	}
	for g := range ng {
		m.nObs += len(in.y[g])
	}
	if dim := m.Dim(); m.nObs <= dim {
		return nil, perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			fmt.Sprintf("REG_MMM_BAYES needs more than %d rows over the %d-geo panel (its parameter count: an intercept per geo, the controls, %d·channels shared terms, sigma, and per pooled term a scale and a value per geo), got %d",
				dim, ng, shape.np+shape.nq+1, m.nObs),
			map[string]any{"n": m.nObs, "required": dim + 1, "geos": ng})
	}
	for g := range ng {
		if len(in.spend[g]) != nc || len(in.controls[g]) != nz {
			return nil, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: geo %s has %d channels and %d controls, the panel %d and %d", geoName(g), len(in.spend[g]), len(in.controls[g]), nc, nz), nil)
		}
		for _, v := range in.y[g] {
			if !finite(v) {
				return nil, bayesConfigError("REG_MMM_BAYES: the outcome has a non-finite value", map[string]any{"geo": geoName(g)})
			}
			shape.yScale = max(shape.yScale, math.Abs(v))
		}
	}
	if !(shape.yScale > 0) {
		return nil, perr.NewCodedError(perr.PROCESSING_REGRESSION_RANK_DEFICIENT, "REG_MMM_BAYES: the outcome is zero in every row")
	}
	for g := range ng {
		n := len(in.y[g])
		gs := geoSeries{n: n, ys: make([]float64, n), spend: in.spend[g], zs: make([][]float64, nz),
			spendShare: make([]float64, nc), spendTotal: make([]float64, nc)}
		for t, v := range in.y[g] {
			gs.ys[t] = v / shape.yScale
		}
		for c, x := range in.spend[g] {
			if len(x) != n {
				return nil, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: channel %s has %d rows in geo %s, the outcome %d", chName(c), len(x), geoName(g), n), nil)
			}
			for _, v := range x {
				if !(v >= 0) || math.IsInf(v, 0) {
					return nil, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: channel %s has a negative or non-finite spend (%v) in geo %s", chName(c), v, geoName(g)),
						map[string]any{"channel": chName(c), "geo": geoName(g)})
				}
				shape.xScale[c] = max(shape.xScale[c], v)
				gs.spendTotal[c] += v
			}
		}
		m.geos[g] = gs
	}
	for g := range m.geos {
		gs := &m.geos[g]
		for c := range nc {
			if !(gs.spendTotal[c] > 0) {
				// The geo carries no information on the channel: its
				// effect there would be its prior alone.
				return nil, perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_RANK_DEFICIENT,
					fmt.Sprintf("REG_MMM_BAYES: channel %s never spends in geo %s, so its effect there is unidentified — drop the geo or the channel", chName(c), geoName(g)),
					map[string]any{"channel": chName(c), "geo": geoName(g)})
			}
			gs.spendShare[c] = gs.spendTotal[c] / shape.yScale
		}
	}
	for j := range nz {
		mean := 0.0
		for g := range ng {
			z := in.controls[g][j]
			if len(z) != m.geos[g].n {
				return nil, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: control %d has %d rows in geo %s, the outcome %d", j, len(z), geoName(g), m.geos[g].n), nil)
			}
			for _, v := range z {
				if !finite(v) {
					return nil, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: control %d has a non-finite value", j), map[string]any{"control": j})
				}
				mean += v
			}
		}
		mean /= float64(m.nObs)
		scale := 0.0
		for g := range ng {
			for _, v := range in.controls[g][j] {
				scale = max(scale, math.Abs(v-mean))
			}
		}
		if !(scale > 0) {
			return nil, perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_RANK_DEFICIENT,
				fmt.Sprintf("REG_MMM_BAYES: control %d is constant", j), map[string]any{"control": j})
		}
		shape.zMean[j], shape.zScale[j] = mean, scale
		for g := range ng {
			zs := make([]float64, m.geos[g].n)
			for t, v := range in.controls[g][j] {
				zs[t] = (v - mean) / scale
			}
			m.geos[g].zs[j] = zs
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
	for g := range ng {
		if err := set(m.iIntercept(g), pr.Intercept, supportReal, "intercept"); err != nil {
			return nil, err
		}
	}
	for j := range nz {
		if err := set(m.iControl(j), pr.Control, supportReal, "control"); err != nil {
			return nil, err
		}
	}
	for c, cp := range pr.Channels {
		shape.isBeta[c] = cp.Beta
		for i, kp := range kernel.Params() {
			if err := set(m.iAdstock(i, c), *kp.prior(&cp), kp.sup, fmt.Sprintf("channel %d %s", c, kp.key)); err != nil {
				return nil, err
			}
		}
		for q, cq := range curve.Params() {
			if err := set(m.iCurve(q, c), *cq.prior(&cp), supportPositive, fmt.Sprintf("channel %d %s", c, cq.key)); err != nil {
				return nil, err
			}
		}
		if err := set(m.iEffect(c), cp.Effect, supportPositive, fmt.Sprintf("channel %d effect", c)); err != nil {
			return nil, err
		}
	}
	if err := set(m.iSigma(), pr.Sigma, supportPositive, "sigma"); err != nil {
		return nil, err
	}
	tau, eta := halfNormalPrior(geoTauSigma), normalPrior(0, 1)
	for k := range m.nk {
		for c := range nc {
			if err := set(m.iTau(k, c), tau, supportPositive, "tau"); err != nil {
				return nil, err
			}
			for g := range ng {
				if err := set(m.iEta(k, c, g), eta, supportReal, "eta"); err != nil {
					return nil, err
				}
			}
		}
	}
	return m, nil
}

// The θ layout (see the file comment). A pooled class k is the effect
// (k = 0) or kernel parameter k − 1; iMu is its population coordinate.
func (m *bayesGeoModel) iIntercept(g int) int { return g }
func (m *bayesGeoModel) iControl(j int) int   { return m.ng + j }
func (m *bayesGeoModel) iAdstock(p, c int) int {
	return m.ng + m.shape.nz + p*m.shape.nc + c
}
func (m *bayesGeoModel) iCurve(q, c int) int {
	return m.ng + m.shape.nz + (m.shape.np+q)*m.shape.nc + c
}
func (m *bayesGeoModel) iEffect(c int) int {
	return m.ng + m.shape.nz + (m.shape.np+m.shape.nq)*m.shape.nc + c
}
func (m *bayesGeoModel) iSigma() int {
	return m.ng + m.shape.nz + (m.shape.np+m.shape.nq+1)*m.shape.nc
}
func (m *bayesGeoModel) iMu(k, c int) int {
	if k == 0 {
		return m.iEffect(c)
	}
	return m.iAdstock(k-1, c)
}
func (m *bayesGeoModel) iTau(k, c int) int { return m.iSigma() + 1 + k*m.shape.nc + c }
func (m *bayesGeoModel) iEta(k, c, g int) int {
	return m.iSigma() + 1 + m.nk*m.shape.nc + (k*m.shape.nc+c)*m.ng + g
}

// Dim is G + controls + (np + nq + 1)·channels + 1 + (np + 1)·channels·(G + 1).
func (m *bayesGeoModel) Dim() int {
	return m.iSigma() + 1 + m.nk*m.shape.nc*(1+m.ng)
}

// mapCoordinate is coordinate i's value, prior and Jacobian
// (bayesModel.mapCoordinate's rule).
func (m *bayesGeoModel) mapCoordinate(i int, x float64) coordinate {
	p := m.priors[i]
	f := p.transform().Forward(x)
	lp, dlp := p.logDensity(f.Y)
	return coordinate{y: f.Y, dy: f.DyDx, lp: lp + f.LogJac, dl: float64(dlp*f.DyDx) + f.DLogJac}
}

// pooled is one geo's value of a pooled class: T(μ + τ·η) and its
// derivative in μ + τ·η.
type pooled struct{ y, dy float64 }

// pooledValues is every pooled class's per-geo value at θ, [k][c][g];
// tau(k, c) is the class's constrained scale.
func (m *bayesGeoModel) pooledValues(theta []float64, tau func(k, c int) float64) [][][]pooled {
	out := make([][][]pooled, m.nk)
	for k := range out {
		out[k] = make([][]pooled, m.shape.nc)
		for c := range out[k] {
			out[k][c] = make([]pooled, m.ng)
			mu, tr, tk := theta[m.iMu(k, c)], m.priors[m.iMu(k, c)].transform(), tau(k, c)
			for g := range m.ng {
				f := tr.Forward(mu + float64(tk*theta[m.iEta(k, c, g)]))
				out[k][c][g] = pooled{y: f.Y, dy: f.DyDx}
			}
		}
	}
	return out
}

// geoKernel is geo g's channel-c kernel parameters from the pooled
// values (Params order).
func (m *bayesGeoModel) geoKernel(pv [][][]pooled, c, g int) []float64 {
	ad := make([]float64, m.shape.np)
	for i := range ad {
		ad[i] = pv[1+i][c][g].y
	}
	return ad
}

// LogDensityGrad is the panel's log p(θ | data) up to a constant with
// its gradient (bayesModel.LogDensityGrad's contract).
func (m *bayesGeoModel) LogDensityGrad(theta, grad []float64) float64 {
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

func (m *bayesGeoModel) logDensity(theta, grad []float64) float64 {
	s := m.shape
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
	for i := range grad {
		grad[i] = coords[i].dl
	}
	add := func(i int, dLdy float64) { grad[i] += float64(dLdy * coords[i].dy) }

	pv := m.pooledValues(theta, func(k, c int) float64 { return coords[m.iTau(k, c)].y })
	// dv is ∂L/∂(pooled value), [k][c][g].
	dv := make([][][]float64, m.nk)
	for k := range dv {
		dv[k] = make([][]float64, s.nc)
		for c := range dv[k] {
			dv[k][c] = make([]float64, m.ng)
		}
	}
	qs := make([][]float64, s.nc)
	for c := range qs {
		qs[c] = make([]float64, s.nq)
		for i := range qs[c] {
			qs[c][i] = coords[m.iCurve(i, c)].y
		}
	}
	sigma := coords[m.iSigma()].y
	s2 := float64(sigma * sigma)
	ssr := 0.0
	dControl := make([]float64, s.nz)
	dCurve := make([][]float64, s.nc)
	for c := range dCurve {
		dCurve[c] = make([]float64, s.nq)
	}
	for g := range m.ng {
		gs := &m.geos[g]
		chans := make([]channelState, s.nc)
		for c := range s.nc {
			st := s.curveOn(gs.spend[c], c, m.geoKernel(pv, c, g), qs[c], true)
			e := pv[0][c][g].y
			if s.isBeta[c] {
				st.beta = e
			} else {
				if !(st.hSum > 0) {
					return math.Inf(-1)
				}
				st.beta = float64(e*gs.spendShare[c]) / st.hSum
			}
			chans[c] = st
		}
		alpha := coords[m.iIntercept(g)].y
		r := make([]float64, gs.n)
		for t := range gs.n {
			mu := alpha
			for j := range s.nz {
				mu += float64(coords[m.iControl(j)].y * gs.zs[j][t])
			}
			for c := range s.nc {
				mu += float64(chans[c].beta * chans[c].h[t])
			}
			r[t] = gs.ys[t] - mu
			ssr += float64(r[t] * r[t])
		}
		gt := make([]float64, gs.n)
		sumG := 0.0
		for t := range r {
			gt[t] = r[t] / s2
			sumG += gt[t]
		}
		add(m.iIntercept(g), sumG)
		for j := range s.nz {
			d := 0.0
			for t, v := range gt {
				d += float64(v * gs.zs[j][t])
			}
			dControl[j] += d
		}
		for c, st := range chans {
			gh := 0.0
			for t, v := range gt {
				gh += float64(v * st.h[t])
			}
			// The one-market partials (bayesModel.logDensity), per geo.
			shift := 0.0
			if s.isBeta[c] {
				dv[0][c][g] += gh
			} else {
				shift = gh / st.hSum
				dv[0][c][g] += float64(gh*st.beta) / pv[0][c][g].y
			}
			dp, dq := make([]float64, s.np), make([]float64, s.nq)
			for t, v := range gt {
				u := v - shift
				for i := range dp {
					dp[i] += float64(u * st.dhp[i][t])
				}
				for i := range dq {
					dq[i] += float64(u * st.dhq[i][t])
				}
			}
			for i, d := range dp {
				dv[1+i][c][g] += float64(st.beta * d)
			}
			for i, d := range dq {
				dCurve[c][i] += float64(st.beta * d)
			}
		}
	}
	lp += -float64(float64(m.nObs)*detmath.Log(sigma)) - float64(0.5*ssr)/s2
	for j, d := range dControl {
		add(m.iControl(j), d)
	}
	for c := range dCurve {
		for i, d := range dCurve[c] {
			add(m.iCurve(i, c), d)
		}
	}
	add(m.iSigma(), -float64(m.nObs)/sigma+ssr/float64(s2*sigma))
	// The pooled values' chain rule: u = μ + τ·η, so ∂u/∂μ = 1,
	// ∂u/∂τ = η and ∂u/∂η = τ (τ through its own transform).
	for k := range dv {
		for c := range dv[k] {
			ti := m.iTau(k, c)
			mi := m.iMu(k, c)
			for g, d := range dv[k][c] {
				du := float64(d * pv[k][c][g].dy)
				ei := m.iEta(k, c, g)
				grad[mi] += du
				grad[ti] += float64(float64(du*theta[ei]) * coords[ti].dy)
				grad[ei] += float64(du * coords[ti].y)
			}
		}
	}
	return lp
}

// geoDraw is one draw of the panel in original units: the coefficient
// row (bayesRegression.geoCoefNames' order), per channel its pooled and
// per-geo ROI, marginal ROI and contribution (entries c·(G + 1): the
// pooled figure, then each geo's), each pooled class's population value
// μ (the effect in its sampled units: ROI, or β on the raw outcome) per
// channel ([k][c]).
type geoDraw struct {
	row                []float64
	roi, mroi, contrib []float64
	mu                 [][]float64
}

// draw maps θ onto a geoDraw.
func (m *bayesGeoModel) draw(theta []float64) geoDraw {
	s := m.shape
	v := make([]float64, m.Dim())
	for i := range v {
		v[i] = m.priors[i].transform().Forward(theta[i]).Y
	}
	pv := m.pooledValues(theta, func(k, c int) float64 { return v[m.iTau(k, c)] })
	qs := make([][]float64, s.nc)
	for c := range qs {
		qs[c] = make([]float64, s.nq)
		for i := range qs[c] {
			qs[c][i] = v[m.iCurve(i, c)]
		}
	}
	entries := s.nc * (1 + m.ng)
	d := geoDraw{roi: make([]float64, entries), mroi: make([]float64, entries), contrib: make([]float64, entries), mu: make([][]float64, m.nk)}
	betaGeo := make([][]float64, s.nc)
	betaPop := make([]float64, s.nc)
	for c := range s.nc {
		betaGeo[c] = make([]float64, m.ng)
		base := c * (1 + m.ng)
		spendAll, contribAll, mroiAll := 0.0, 0.0, 0.0
		for g := range m.ng {
			gs := &m.geos[g]
			st := s.curveOn(gs.spend[c], c, m.geoKernel(pv, c, g), qs[c], false)
			e := pv[0][c][g].y
			b := e // β on the scaled outcome
			var roi float64
			if s.isBeta[c] {
				betaGeo[c][g] = float64(s.yScale * e)
				roi = float64(e*st.hSum) / gs.spendShare[c]
			} else {
				roi = e
				b = float64(e*gs.spendShare[c]) / st.hSum
				betaGeo[c][g] = float64(s.yScale*float64(e*gs.spendShare[c])) / st.hSum
			}
			mroi := float64(b*s.sat.Elasticity(st.a, st.h, qs[c])) / gs.spendShare[c]
			contrib := float64(roi * gs.spendTotal[c])
			d.roi[base+1+g], d.mroi[base+1+g], d.contrib[base+1+g] = roi, mroi, contrib
			spendAll += gs.spendTotal[c]
			contribAll += contrib
			mroiAll += float64(mroi * gs.spendTotal[c])
		}
		// The all-geo figures: the panel's summed contribution over its
		// summed spend, and the spend-weighted marginal ROI.
		d.roi[base], d.mroi[base], d.contrib[base] = contribAll/spendAll, mroiAll/spendAll, contribAll
		// The population β: the effect at its population value μ over
		// the whole panel — in ROI mode the β that μ's ROI gives at the
		// population kernel across every geo's spend.
		mu := v[m.iEffect(c)]
		if s.isBeta[c] {
			betaPop[c] = float64(s.yScale * mu)
		} else {
			ad := make([]float64, s.np)
			for i := range ad {
				ad[i] = v[m.iAdstock(i, c)]
			}
			hTot := 0.0
			for g := range m.ng {
				hTot += s.curveOn(m.geos[g].spend[c], c, ad, qs[c], false).hSum
			}
			if hTot > 0 {
				betaPop[c] = float64(s.yScale*float64(mu*(spendAll/s.yScale))) / hTot
			} else {
				betaPop[c] = math.NaN()
			}
		}
	}
	for k := range m.nk {
		d.mu[k] = make([]float64, s.nc)
		for c := range s.nc {
			mu := v[m.iMu(k, c)]
			if k == 0 && s.isBeta[c] {
				mu = float64(s.yScale * mu)
			}
			d.mu[k][c] = mu
		}
	}

	row := make([]float64, 0, m.Dim())
	for g := range m.ng {
		icpt := v[m.iIntercept(g)]
		for j := range s.nz {
			icpt -= float64(v[m.iControl(j)]*s.zMean[j]) / s.zScale[j]
		}
		row = append(row, float64(s.yScale*icpt))
	}
	for c := range s.nc {
		row = append(row, betaPop[c])
		row = append(row, betaGeo[c]...)
	}
	for j := range s.nz {
		row = append(row, float64(s.yScale*v[m.iControl(j)])/s.zScale[j])
	}
	for i := range s.np {
		for c := range s.nc {
			row = append(row, v[m.iAdstock(i, c)])
			for g := range m.ng {
				row = append(row, pv[1+i][c][g].y)
			}
		}
	}
	for i, cq := range s.sat.Params() {
		for c := range s.nc {
			row = append(row, cq.raw(v[m.iCurve(i, c)], s.xScale[c]))
		}
	}
	row = append(row, float64(s.yScale*v[m.iSigma()]))
	for k := range m.nk {
		for c := range s.nc {
			row = append(row, v[m.iTau(k, c)])
		}
	}
	d.row = row
	return d
}

// predictGeo is geo g's model mean outcome at θ, in original units, for
// rows from..n−1 of its full series (spend and controls the full
// columns, the fitted rows first): bayesModel.predict's rule with geo
// g's parameters, its fitted rows' β and the panel's scaling.
func (m *bayesGeoModel) predictGeo(theta []float64, g int, spend, controls [][]float64, from int) []float64 {
	s := m.shape
	n := len(spend[0])
	v := make([]float64, m.Dim())
	for i := range v {
		v[i] = m.priors[i].transform().Forward(theta[i]).Y
	}
	pv := m.pooledValues(theta, func(k, c int) float64 { return v[m.iTau(k, c)] })
	mu := make([]float64, n-from)
	for i := range mu {
		mu[i] = v[m.iIntercept(g)]
	}
	for j := range s.nz {
		gm := v[m.iControl(j)]
		for t := from; t < n; t++ {
			mu[t-from] += float64(gm * ((controls[j][t] - s.zMean[j]) / s.zScale[j]))
		}
	}
	gs := &m.geos[g]
	for c := range s.nc {
		ad := m.geoKernel(pv, c, g)
		q := make([]float64, s.nq)
		for i := range q {
			q[i] = v[m.iCurve(i, c)]
		}
		b := pv[0][c][g].y
		if !s.isBeta[c] {
			st := s.curveOn(gs.spend[c], c, ad, q, false)
			if st.hSum > 0 {
				b = float64(b*gs.spendShare[c]) / st.hSum
			} else {
				b = math.NaN()
			}
		}
		w, _ := s.kernel.Weights(ad, s.maxLag, false)
		a := make([]float64, n)
		adstock(spend[c], w, nil, a, nil)
		for t := from; t < n; t++ {
			h, _ := s.sat.Eval(a[t]/s.xScale[c], q, nil)
			mu[t-from] += float64(b * h)
		}
	}
	for i := range mu {
		mu[i] = float64(s.yScale * mu[i])
	}
	return mu
}

// defaultInit is the panel's fixed, data-derived start
// (bayesModel.defaultInit's rule): each geo's intercept at its scaled
// outcome's mean, the controls at 0, each population term at its prior's
// center, each curve parameter from the mean positive scaled adstock
// over the whole panel at the population kernel, σ at the pooled
// within-geo standard deviation, every scale at its prior's center and
// every η at 0 (each geo at the population value).
func (m *bayesGeoModel) defaultInit() []float64 {
	s := m.shape
	v := make([]float64, m.Dim())
	ss := 0.0
	for g := range m.ng {
		gs := &m.geos[g]
		mean := 0.0
		for _, y := range gs.ys {
			mean += y
		}
		mean /= float64(gs.n)
		for _, y := range gs.ys {
			d := y - mean
			ss += float64(d * d)
		}
		v[m.iIntercept(g)] = mean
	}
	v[m.iSigma()] = math.Sqrt(ss / float64(m.nObs))
	for c := range s.nc {
		ad := make([]float64, s.np)
		for i := range ad {
			v[m.iAdstock(i, c)] = m.priors[m.iAdstock(i, c)].center()
			ad[i] = v[m.iAdstock(i, c)]
		}
		w, _ := s.kernel.Weights(ad, s.maxLag, false)
		sum, cnt := 0.0, 0
		for g := range m.ng {
			x := m.geos[g].spend[c]
			a := make([]float64, len(x))
			adstock(x, w, nil, a, nil)
			for _, y := range a {
				if y > 0 {
					sum += y / s.xScale[c]
					cnt++
				}
			}
		}
		meanA := sum / float64(cnt)
		for i, cq := range s.sat.Params() {
			if x, ok := cq.init(meanA); ok {
				v[m.iCurve(i, c)] = x
			} else {
				v[m.iCurve(i, c)] = m.priors[m.iCurve(i, c)].center()
			}
		}
		v[m.iEffect(c)] = m.priors[m.iEffect(c)].center()
		for k := range m.nk {
			v[m.iTau(k, c)] = m.priors[m.iTau(k, c)].center()
		}
	}
	theta := make([]float64, len(v))
	for i, y := range v {
		theta[i] = m.priors[i].transform().Inverse(y)
	}
	for i, x := range theta {
		if lp, _ := m.priors[i].logDensity(v[i]); !finite(x) || !finite(lp) {
			theta[i] = m.priors[i].transform().Inverse(m.priors[i].center())
		}
	}
	return theta
}

// mapStart is the panel's MAP warm start (mapSearch from defaultInit).
func (m *bayesGeoModel) mapStart(maxIter int) (theta []float64, ok bool) {
	return mapSearch(m.Dim(), m.LogDensityGrad, m.defaultInit(), maxIter)
}

var _ mcmc.Model = (*bayesGeoModel)(nil)
