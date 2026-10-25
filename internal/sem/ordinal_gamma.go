package sem

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/internal/detmath"
	"github.com/frankbardon/pulse/linalg"
)

// The asymptotic covariance (Gamma) of the two-step statistics — the
// weight matrix of DWLS and the meat of WLSMV's robust standard errors
// and scaled-shifted test — as lavaan's muthen1984 builds it (WLS.W, the
// default numeric_bread = "observed" of sem() / cfa()):
//
//   - INNER = Σ w·s·sᵀ over the casewise scores s (Scores; lavaan's
//     inner2 for frequency weights);
//   - A11, the univariate information: per variable, the INNER block of
//     an ordered column's thresholds, and for a numeric column the
//     observed information of its mean and ML variance (Σw / σ², and
//     Σw / 2σ⁴; lav_m84_a11_numeric);
//   - A21 / A22, the correlation rows: an ordered × ordered pair's
//     Σ w·(ρ-score)·(threshold score) and Σ w·(ρ-score)², a pair touching
//     a numeric column the observed second derivatives of its bivariate
//     log-likelihood (lav_m84_pair_obs_deriv);
//   - WLS.W = B⁻¹·INNER·B⁻ᵀ with B = [A11 0; A21 A22] (lav_m84_b_inv),
//     then H·WLS.W·Hᵀ, H the correlation → covariance map of a pair
//     touching a numeric column, and a numeric column's mean row and
//     column negated (WLS.obs carries −mean).
//
// NACOV — lavaan's Gamma — is N·WLS.W (N = Σw). Like the rest of the
// two-step numerics the file is FMA-free and calls only detmath and
// math.Sqrt (and linalg.InverseSPD, a reference kernel), so Gamma is a
// function of the data alone on every architecture.

// ReasonSingular is an OrdinalError reason: the asymptotic covariance is
// undefined — a univariate information block or a correlation's
// information does not invert, or a figure is not finite (a correlation
// at ±1).
const ReasonSingular = "singular"

// Gamma is one group's asymptotic covariance of the two-step statistics
// and its pieces, every matrix in ScoreLayout coordinates (WLS.obs order:
// thresholds and numeric means, numeric variances, then the pairs).
type Gamma struct {
	// Inner is Σ w·s·sᵀ (Size × Size).
	Inner [][]float64
	// A11 is the univariate information (Cor × Cor, block diagonal by
	// variable); A21 the correlation rows' cross information (pairs ×
	// Cor); A22 the correlations' own information (one per pair).
	A11 [][]float64
	A21 [][]float64
	A22 []float64
	// WLSW is lavaan's WLS.W: the asymptotic covariance of WLSObs
	// (Size × Size). NACOV is N·WLSW.
	WLSW [][]float64
	// N is the total weight Σw.
	N float64
}

// NACOV returns N·WLS.W, lavaan's NACOV (a fresh matrix).
func (g *Gamma) NACOV() [][]float64 {
	out := make([][]float64, len(g.WLSW))
	for i, row := range g.WLSW {
		out[i] = make([]float64, len(row))
		for j, v := range row {
			out[i][j] = float64(g.N * v)
		}
	}
	return out
}

// WLSObs returns the sample statistics in lavaan's WLS.obs layout
// (ScoreLayout order): each ordered column's thresholds and a numeric
// column's NEGATED mean, the numeric variances, then every pair's
// covariance — the correlation times the standard deviations, so an
// ordered × ordered pair's is its polychoric correlation as is.
func (s *TwoStepStats) WLSObs() []float64 {
	l := s.Layout()
	out := make([]float64, l.Size)
	for v, kind := range s.Kinds {
		if kind == KindOrdered {
			copy(out[l.Th[v]:], s.Thresholds[v])
			continue
		}
		out[l.Th[v]] = -s.Mean[v]
		out[l.VarIdx[v]] = s.Var[v]
	}
	for k, p := range s.Pairs {
		out[l.Cor+k] = s.pairCov(k, p.Rho)
	}
	return out
}

// pairCov is pair k's covariance at correlation rho: rho times the
// numeric columns' standard deviations (an ordered column's is 1).
func (s *TwoStepStats) pairCov(k int, rho float64) float64 {
	p := s.Pairs[k]
	c := rho
	if s.Kinds[p.I] == KindNumeric {
		c = float64(c * math.Sqrt(s.Var[p.I]))
	}
	if s.Kinds[p.J] == KindNumeric {
		c = float64(c * math.Sqrt(s.Var[p.J]))
	}
	return c
}

// Gamma computes the asymptotic covariance of s from in, the rows (and
// weights) s was computed from. A univariate block or a correlation
// information that does not invert, or a non-finite figure, is an
// OrdinalError with ReasonSingular.
func (s *TwoStepStats) Gamma(in TwoStepInput) (*Gamma, error) {
	l := s.Layout()
	if len(in.Rows) != s.Rows || len(in.Kinds) != len(s.Kinds) {
		return nil, &OrdinalError{Var: -1, Group: -1, Reason: ReasonInvalidInput,
			Msg: fmt.Sprintf("gamma: %d rows of %d columns, the statistics were computed from %d of %d", len(in.Rows), len(in.Kinds), s.Rows, len(s.Kinds))}
	}
	size, nu, np := l.Size, l.Cor, len(s.Pairs)
	g := &Gamma{Inner: square(size), A11: square(nu), A21: make([][]float64, np), A22: make([]float64, np), N: s.N}
	for k := range g.A21 {
		g.A21[k] = make([]float64, nu)
	}
	sc := make([]float64, size)
	for r, row := range in.Rows {
		w := rowWeight(in.Weights, r)
		if w == 0 {
			continue
		}
		if err := s.Scores(row, sc); err != nil {
			return nil, &OrdinalError{Var: -1, Group: -1, Reason: ReasonInvalidInput, Msg: err.Error()}
		}
		for i, si := range sc {
			if si == 0 {
				continue
			}
			ws := float64(w * si)
			gi := g.Inner[i]
			for j, sj := range sc {
				gi[j] += float64(ws * sj)
			}
		}
		for k, p := range s.Pairs {
			if p.Kind != PairPolychoric {
				continue
			}
			ps := s.PairScores(k, row)
			wr := float64(w * ps.Rho)
			for m, d := range ps.ThI {
				g.A21[k][l.Th[p.I]+m] += float64(wr * d)
			}
			for m, d := range ps.ThJ {
				g.A21[k][l.Th[p.J]+m] += float64(wr * d)
			}
			g.A22[k] += float64(wr * ps.Rho)
		}
	}
	// A11: per variable.
	for v, kind := range s.Kinds {
		if kind == KindOrdered {
			for a := l.Th[v]; a < l.Th[v]+l.NTh[v]; a++ {
				for b := l.Th[v]; b < l.Th[v]+l.NTh[v]; b++ {
					g.A11[a][b] = g.Inner[a][b]
				}
			}
			continue
		}
		ev := s.Var[v]
		g.A11[l.Th[v]][l.Th[v]] = s.N / ev
		g.A11[l.VarIdx[v]][l.VarIdx[v]] = s.N / float64(float64(2*ev)*ev)
	}
	// A21 / A22 of the pairs touching a numeric column: the observed
	// second derivatives.
	for k, p := range s.Pairs {
		if p.Kind == PairPolychoric {
			continue
		}
		od := s.pairObsDeriv(in, k)
		g.A22[k] = od.dRho
		for m, d := range od.thI {
			g.A21[k][l.Th[p.I]+m] = d
		}
		for m, d := range od.thJ {
			g.A21[k][l.Th[p.J]+m] = d
		}
		if s.Kinds[p.I] == KindNumeric {
			g.A21[k][l.VarIdx[p.I]] = od.varI
		}
		if s.Kinds[p.J] == KindNumeric {
			g.A21[k][l.VarIdx[p.J]] = od.varJ
		}
	}
	a11inv, err := s.a11Inverse(g.A11, l)
	if err != nil {
		return nil, err
	}
	for k, d := range g.A22 {
		if !(d != 0) || math.IsNaN(d) || math.IsInf(d, 0) {
			return nil, &OrdinalError{Var: -1, Group: -1, Reason: ReasonSingular,
				Msg: fmt.Sprintf("gamma: the information of correlation %d (columns %d, %d) is %v", k, s.Pairs[k].I, s.Pairs[k].J, d)}
		}
	}
	// B⁻¹ = [A11⁻¹ 0; −A22⁻¹·A21·A11⁻¹ A22⁻¹] (lav_m84_b_inv).
	binv := square(size)
	for a := 0; a < nu; a++ {
		copy(binv[a][:nu], a11inv[a])
	}
	for k := 0; k < np; k++ {
		row := binv[nu+k]
		for c := 0; c < nu; c++ {
			t := 0.0
			for m := 0; m < nu; m++ {
				t += float64(g.A21[k][m] * a11inv[m][c])
			}
			row[c] = -t / g.A22[k]
		}
		row[nu+k] = 1 / g.A22[k]
	}
	w := sandwich(binv, g.Inner)
	if h := s.covarianceMap(l); h != nil {
		w = sandwich(h, w)
	}
	for v, kind := range s.Kinds {
		if kind != KindNumeric {
			continue
		}
		i := l.Th[v]
		for j := range w {
			if j != i {
				w[i][j], w[j][i] = -w[i][j], -w[j][i]
			}
		}
	}
	for _, row := range w {
		for _, v := range row {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, &OrdinalError{Var: -1, Group: -1, Reason: ReasonSingular,
					Msg: "gamma: the asymptotic covariance is not finite (a correlation at ±1, or a degenerate table)"}
			}
		}
	}
	g.WLSW = w
	return g, nil
}

// a11Inverse inverts the block-diagonal A11 block by block: an ordered
// column's threshold information (symmetric positive definite: every
// category holds weight), a numeric column's diagonal mean / variance
// pair.
func (s *TwoStepStats) a11Inverse(a11 [][]float64, l ScoreLayout) ([][]float64, error) {
	out := square(l.Cor)
	for v, kind := range s.Kinds {
		if kind == KindNumeric {
			out[l.Th[v]][l.Th[v]] = 1 / a11[l.Th[v]][l.Th[v]]
			out[l.VarIdx[v]][l.VarIdx[v]] = 1 / a11[l.VarIdx[v]][l.VarIdx[v]]
			continue
		}
		n := l.NTh[v]
		blk := make([][]float64, n)
		for a := range blk {
			blk[a] = a11[l.Th[v]+a][l.Th[v] : l.Th[v]+n]
		}
		sym, err := linalg.NewSymFromRows(blk)
		if err == nil {
			sym, err = linalg.InverseSPD(sym)
		}
		if err != nil {
			return nil, &OrdinalError{Var: v, Group: -1, Reason: ReasonSingular,
				Msg: "gamma: the threshold information does not invert: " + err.Error()}
		}
		for a := 0; a < n; a++ {
			for b := 0; b < n; b++ {
				out[l.Th[v]+a][l.Th[v]+b] = sym.At(a, b)
			}
		}
	}
	return out, nil
}

// covarianceMap is lavaan's H (muthen1984's h21 / h22): the Jacobian of
// the WLS.obs statistics in the step-1 / step-2 ones — identity, except
// that a pair touching a numeric column maps its correlation to a
// covariance (ρ·σᵢ·σⱼ). Nil when every column is ordered (H = I).
func (s *TwoStepStats) covarianceMap(l ScoreLayout) [][]float64 {
	numeric := false
	for _, k := range s.Kinds {
		numeric = numeric || k == KindNumeric
	}
	if !numeric {
		return nil
	}
	h := square(l.Size)
	for i := range h {
		h[i][i] = 1
	}
	for k, p := range s.Pairs {
		row := h[l.Cor+k]
		ni, nj := s.Kinds[p.I] == KindNumeric, s.Kinds[p.J] == KindNumeric
		si, sj := math.Sqrt(s.Var[p.I]), math.Sqrt(s.Var[p.J])
		switch {
		case ni && nj:
			row[l.VarIdx[p.I]] = float64(sj*p.Rho) / float64(2*si)
			row[l.VarIdx[p.J]] = float64(si*p.Rho) / float64(2*sj)
			row[l.Cor+k] = float64(si * sj)
		case ni:
			row[l.VarIdx[p.I]] = p.Rho / float64(2*si)
			row[l.Cor+k] = si
		case nj:
			row[l.VarIdx[p.J]] = p.Rho / float64(2*sj)
			row[l.Cor+k] = sj
		}
	}
	return h
}

// obsDeriv is a pair's observed second derivatives (lav_m84_pair_obs_deriv):
// −∂²ℓ/∂ρ² and the cross derivatives ∂²ℓ/∂ρ∂(column statistics), each
// column's thresholds (a numeric column: its mean) and a numeric
// column's variance, with lavaan's signs.
type obsDeriv struct {
	dRho       float64
	thI, thJ   []float64
	varI, varJ float64
}

// pairObsDeriv is lav_m84_pair_obs_deriv for pair k (polyserial or
// Pearson): the bivariate log-likelihood's observed derivatives at the
// estimates, weighted, summed in row order.
func (s *TwoStepStats) pairObsDeriv(in TwoStepInput, k int) obsDeriv {
	p := s.Pairs[k]
	if p.Kind == PairPearson {
		return s.pearsonObsDeriv(in, p)
	}
	x, o := p.I, p.J
	if s.Kinds[x] == KindOrdered {
		x, o = o, x
	}
	ps := s.polyserialPair(in, x, o)
	_, hess := ps.score(p.Rho)
	dMean, dVar, dTh := ps.crossDerivs(p.Rho)
	out := obsDeriv{dRho: -hess}
	if x == p.I {
		out.thI, out.varI, out.thJ = []float64{dMean}, dVar, dTh
	} else {
		out.thJ, out.varJ, out.thI = []float64{dMean}, dVar, dTh
	}
	return out
}

// crossDerivs is lav_bvmix_cor_hessian_uni_cache without covariates:
// ∂²ℓ/∂ρ∂μ, ∂²ℓ/∂ρ∂σ² of the numeric column and ∂²ℓ/∂ρ∂τ of the
// ordered one's thresholds, at rho (a row whose numeric density
// underflows dropped, as lavaan drops it).
func (ps *polyserialData) crossDerivs(rho float64) (dMean, dVar float64, dTh []float64) {
	r := math.Sqrt(1 - float64(rho*rho))
	r3 := float64(float64(r*r) * r)
	dTh = make([]float64, len(ps.th))
	var sz, szz float64
	for i, z := range ps.z {
		if stdNormPDF(z) == 0 {
			continue
		}
		c := ps.cat[i]
		up, lo := ordinalLimits(ps.th, c)
		a := (up - float64(rho*z)) / r
		b := (lo - float64(rho*z)) / r
		pa, pb := stdNormPDF(a), stdNormPDF(b)
		pr := stdNormCDF(a) - stdNormCDF(b)
		if pr < polyserialFloor {
			pr = polyserialFloor
		}
		ga := float64(up*rho) - z
		gb := float64(lo*rho) - z
		g := float64(pa*ga) - float64(pb*gb)
		dgz := float64((rho/r)*(float64(float64(a*pa)*ga)-float64(float64(b*pb)*gb))) - (pa - pb)
		dpz := -float64((rho / r) * (pa - pb))
		dgta := -float64(float64(a*pa)*ga)/r + float64(pa*rho)
		dpta := pa / r
		dgtb := float64(float64(b*pb)*gb)/r - float64(pb*rho)
		dptb := -pb / r
		p2r3 := float64(float64(pr*pr) * r3)
		dz := (float64(dgz*pr) - float64(g*dpz)) / p2r3
		dta := (float64(dgta*pr) - float64(g*dpta)) / p2r3
		dtb := (float64(dgtb*pr) - float64(g*dptb)) / p2r3
		w := rowWeight(ps.w, i)
		sz += float64(w * dz)
		szz += float64(float64(w*z) * dz)
		if c < len(ps.th) {
			dTh[c] -= float64(w * dta)
		}
		if c > 0 {
			dTh[c-1] -= float64(w * dtb)
		}
	}
	return sz / ps.sd, szz / float64(2*float64(ps.sd*ps.sd)), dTh
}

// polyserialPair is polyserial's fixed pieces for numeric column x and
// ordered column o.
func (s *TwoStepStats) polyserialPair(in TwoStepInput, x, o int) *polyserialData {
	ps := &polyserialData{th: s.Thresholds[o], mean: s.Mean[x], sd: math.Sqrt(s.Var[x])}
	ps.z = make([]float64, len(in.Rows))
	ps.cat = make([]int, len(in.Rows))
	ps.w = in.Weights
	for r, row := range in.Rows {
		ps.z[r] = (row[x] - ps.mean) / ps.sd
		ps.cat[r] = int(row[o])
	}
	return ps
}

// pearsonObsDeriv is lav_bvreg_hessian_cache and
// lav_bvreg_cor_hessian_uni_cache without covariates for numeric pair
// p (y1 = column I, y2 = column J).
func (s *TwoStepStats) pearsonObsDeriv(in TwoStepInput, p PairEstimate) obsDeriv {
	rho := p.Rho
	m1, m2, v1, v2 := s.Mean[p.I], s.Mean[p.J], s.Var[p.I], s.Var[p.J]
	sd1, sd2 := math.Sqrt(v1), math.Sqrt(v2)
	s12 := float64(sd1 * sd2)
	r := 1 - float64(rho*rho)
	r2 := float64(r * r)
	r3 := float64(r2 * r)
	rho2 := float64(rho * rho)
	var h, b1, b2, e1, e2 float64
	for i, row := range in.Rows {
		y1c, y2c := row[p.I]-m1, row[p.J]-m2
		t1 := float64(y1c*y2c) / s12
		t2 := float64(y1c*y1c)/v1 - float64(float64(2*rho)*t1) + float64(y2c*y2c)/v2
		if detmath.Exp(-float64(0.5*(t2/r))) == 0 {
			continue // the bivariate density underflows: lavaan's lik ≤ 0 row
		}
		w := rowWeight(in.Weights, i)
		hr := 1/r - float64(float64(2*rho2)*t2)/r3 + float64(float64(2*rho2)*(1-t2/r))/r2 + float64(float64(4*rho)*t1)/r2 - t2/r2
		d1 := (y2c/s12 - float64((rho/r)*(float64(2*y1c)/v1-float64(float64(2*rho)*y2c)/s12))) / r
		d2 := (y1c/s12 - float64((rho/r)*(float64(2*y2c)/v2-float64(float64(2*rho)*y1c)/s12))) / r
		de1 := (-t1/float64(2*v1) - float64((rho/r)*(-float64(y1c*y1c)/float64(v1*v1)+float64(rho*t1)/v1))) / r
		de2 := (-t1/float64(2*v2) - float64((rho/r)*(-float64(y2c*y2c)/float64(v2*v2)+float64(rho*t1)/v2))) / r
		h += float64(w * hr)
		b1 += float64(w * d1)
		b2 += float64(w * d2)
		e1 += float64(w * de1)
		e2 += float64(w * de2)
	}
	return obsDeriv{dRho: -h, thI: []float64{b1}, thJ: []float64{b2}, varI: -e1, varJ: -e2}
}

// square is an n × n zero matrix.
func square(n int) [][]float64 {
	out := make([][]float64, n)
	for i := range out {
		out[i] = make([]float64, n)
	}
	return out
}

// sandwich is a·m·aᵀ for a square a and a symmetric m, both n × n,
// running sums over the inner index ascending (FMA-free); the result is
// symmetric by construction (the upper triangle mirrored).
func sandwich(a, m [][]float64) [][]float64 {
	n := len(a)
	am := square(n)
	for i := 0; i < n; i++ {
		for k := 0; k < n; k++ {
			aik := a[i][k]
			if aik == 0 {
				continue
			}
			for j := 0; j < n; j++ {
				am[i][j] += float64(aik * m[k][j])
			}
		}
	}
	out := square(n)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			v := 0.0
			for k := 0; k < n; k++ {
				v += float64(am[i][k] * a[j][k])
			}
			out[i][j], out[j][i] = v, v
		}
	}
	return out
}
