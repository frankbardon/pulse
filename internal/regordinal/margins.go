package regordinal

import (
	"math"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// margins.go is REG_ORDINAL's marginal effects (RegressionSpec.Margins,
// extend.MarginsRegression; U68 E4-S1, #652): the effect of each
// predictor on each level's probability
//
//	P_k(η) = F(ζ_k − η) − F(ζ_{k−1} − η),   η = xᵀβ, ζ_0 = −∞, ζ_K = +∞
//	∂P_k/∂x_j = −β_j·D_k,   D_k = f(ζ_k − η) − f(ζ_{k−1} − η)
//
// (f = F′, f′ its derivative; the link kernel the probability scorer
// evaluates — links.go — so a level's effect is the derivative of the
// very probability ATTR_REG_PREDICT scores). Type ame averages it over
// the fitted rows with their prior weights w* (frequency: the rep()
// expansion's; probability: w·n_eff/Σw, a constant multiple, so the
// average is the w-weighted one); type mem evaluates it at the weighted
// predictor means x̄ (marginaleffects slopes(newdata = "mean"), at the
// weighted point). The delta-method gradient in θ = (β, ζ):
//
//	∂/∂β_m   = −δ_jm·D_k + β_j·(f′(ζ_k − η) − f′(ζ_{k−1} − η))·x_m
//	∂/∂ζ_k   = −β_j·f′(ζ_k − η)        (k < K)
//	∂/∂ζ_{k−1} = +β_j·f′(ζ_{k−1} − η)  (k > 1)
//
// averaged like the effect (x_m → x̄_m under mem), Var = J·V·Jᵀ on the
// fit's own covariance V (classical, or the hc0 / hc1 / cluster sandwich
// its standard errors read). The K effects of a term sum to 0.
//
// scale link is the effect on each cumulative link g(P(Y ≤ k)) = ζ_k − η:
// −β_j for every level but the last (whose cumulative probability is 1),
// with the coefficient's standard error. Rows are summed serially in
// record order and every product is barriered, so the effects are
// bit-identical across worker counts and architectures.

// fitState is one successful fit, kept for Margins: the design, θ̂, the
// coefficient covariance the standard errors read, and the level order.
type fitState struct {
	d      *design
	theta  []float64
	vcov   [][]float64
	levels []string
}

// Margins reports the slot's marginal effects over the last fit
// (extend.MarginsRegression): one per level and term, keyed by level.
func (m *model) Margins(req extend.MarginsRequest) (extend.MarginsOutput, error) {
	st := m.fitted
	if st == nil {
		return extend.MarginsOutput{}, perr.NewCodedErrorWithDetails(perr.PROCESSING_INTERNAL,
			string(Name)+": margins requested before a successful fit", nil)
	}
	d := st.d
	col := make(map[string]int, d.p)
	for j, name := range m.spec.Predictors {
		col[name] = j
	}
	var out extend.MarginsOutput
	if req.Type == types.MarginsMEM {
		out.At = make(map[string]float64, d.p)
		for j, v := range d.weightedMeans() {
			out.At[m.spec.Predictors[j]] = v
		}
	}
	if req.Scale == types.MarginsScaleLink {
		out.Outcomes = append([]string(nil), st.levels[:d.k-1]...)
		for _, lv := range out.Outcomes {
			for _, t := range req.Terms {
				j := col[t]
				out.Effects = append(out.Effects, extend.MarginalEffect{Outcome: lv, Term: t,
					Estimate: -st.theta[j], StdErr: extend.Figure(math.Sqrt(st.vcov[j][j]))})
			}
		}
		return out, nil
	}
	g := d.thresholdDerivatives(m.link, st.theta, req.Type == types.MarginsMEM)
	out.Outcomes = append([]string(nil), st.levels...)
	q := d.q()
	jac := make([]float64, q)
	for k, lv := range out.Outcomes {
		// D_k and the β-gradient's link part, from the level's upper
		// (threshold k) and lower (threshold k − 1) cut points.
		dk := 0.0
		gx := make([]float64, d.p)
		if k < d.k-1 {
			dk += g.f[k]
			for c := range gx {
				gx[c] += g.fpx[k][c]
			}
		}
		if k > 0 {
			dk -= g.f[k-1]
			for c := range gx {
				gx[c] -= g.fpx[k-1][c]
			}
		}
		for _, t := range req.Terms {
			j := col[t]
			bj := st.theta[j]
			clear(jac)
			for c := range gx {
				jac[c] = float64(bj * gx[c])
			}
			jac[j] -= dk
			if k < d.k-1 {
				jac[d.p+k] = -float64(bj * g.fp[k])
			}
			if k > 0 {
				jac[d.p+k-1] = float64(bj * g.fp[k-1])
			}
			out.Effects = append(out.Effects, extend.MarginalEffect{Outcome: lv, Term: t,
				Estimate: -float64(bj * dk), StdErr: extend.Figure(math.Sqrt(quadForm(st.vcov, jac)))})
		}
	}
	return out, nil
}

// thresholdTerms are the per-threshold averages the effects read: for
// each cut point t (0..K−2), f(ζ_t − η) and f′(ζ_t − η) and
// f′(ζ_t − η)·x_m — averaged over the fitted rows with their prior
// weights (ame), or evaluated once at the weighted means (mem).
type thresholdTerms struct {
	f, fp []float64
	fpx   [][]float64
}

// thresholdDerivatives is the thresholdTerms of θ under link l.
func (d *design) thresholdDerivatives(l link, theta []float64, atMeans bool) thresholdTerms {
	nt := d.k - 1
	g := thresholdTerms{f: make([]float64, nt), fp: make([]float64, nt), fpx: make([][]float64, nt)}
	for t := range g.fpx {
		g.fpx[t] = make([]float64, d.p)
	}
	add := func(x []float64, w float64) {
		eta := 0.0
		for j, v := range x {
			eta += float64(v * theta[j])
		}
		for t := 0; t < nt; t++ {
			_, f, fp := l.eval(theta[d.p+t] - eta)
			g.f[t] += float64(w * f)
			wfp := float64(w * fp)
			g.fp[t] += wfp
			for c, v := range x {
				g.fpx[t][c] += float64(wfp * v)
			}
		}
	}
	if atMeans {
		add(d.weightedMeans(), 1)
		return g
	}
	sw := 0.0
	for i := range d.y {
		add(d.x[i], d.w[i])
		sw += d.w[i]
	}
	for t := 0; t < nt; t++ {
		g.f[t] /= sw
		g.fp[t] /= sw
		for c := range g.fpx[t] {
			g.fpx[t][c] /= sw
		}
	}
	return g
}

// weightedMeans is Σw*·x_i/Σw* per predictor (the prior weights; their
// constant probability scale cancels).
func (d *design) weightedMeans() []float64 {
	xbar := make([]float64, d.p)
	sw := 0.0
	for i, x := range d.x {
		for j, v := range x {
			xbar[j] += float64(d.w[i] * v)
		}
		sw += d.w[i]
	}
	for j := range xbar {
		xbar[j] /= sw
	}
	return xbar
}

// quadForm is jᵀ·V·j, clamped at 0.
func quadForm(v [][]float64, j []float64) float64 {
	s := 0.0
	for a := range j {
		row := 0.0
		for b := range j {
			row += float64(v[a][b] * j[b])
		}
		s += float64(j[a] * row)
	}
	if s < 0 {
		return 0
	}
	return s
}
