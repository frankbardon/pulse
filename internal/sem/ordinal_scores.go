package sem

import (
	"fmt"
	"math"
)

// Casewise scores of the two-step statistics: the per-row derivatives
// of each univariate and bivariate log-likelihood at the step-1 / step-2
// estimates, the raw material of the asymptotic covariance (Gamma) of
// the thresholds, means, variances and correlations (lavaan's
// muthen1984: its SC matrix, INNER = Σ w·s·sᵀ and the A21 cross
// products). Every score here is UNWEIGHTED — one copy of the row; a
// frequency-weighted caller multiplies by the row's weight (lavaan's
// SC holds w·s, its INNER for frequency weights Σ (w·s)(w·s)ᵀ / w).

// ScoreLayout is the column layout of Scores, lavaan's SC: the threshold
// block (each ordered column's Levels-1 thresholds, a numeric column's
// mean, in column order), then each numeric column's variance, then the
// correlations in Pairs order.
type ScoreLayout struct {
	// Th[v] is column v's first threshold (or mean) index; NTh[v] its
	// count (Levels-1, or 1 for a numeric column).
	Th, NTh []int
	// VarIdx[v] is a numeric column's variance index (-1 for ordered).
	VarIdx []int
	// Cor is the first correlation index; Size the total width.
	Cor, Size int
}

// Layout returns the Scores layout of s.
func (s *TwoStepStats) Layout() ScoreLayout {
	p := len(s.Kinds)
	l := ScoreLayout{Th: make([]int, p), NTh: make([]int, p), VarIdx: make([]int, p)}
	at := 0
	for v := 0; v < p; v++ {
		l.Th[v] = at
		if s.Kinds[v] == KindOrdered {
			l.NTh[v] = s.Levels[v] - 1
		} else {
			l.NTh[v] = 1
		}
		at += l.NTh[v]
	}
	for v := 0; v < p; v++ {
		l.VarIdx[v] = -1
		if s.Kinds[v] == KindNumeric {
			l.VarIdx[v] = at
			at++
		}
	}
	l.Cor = at
	l.Size = at + len(s.Pairs)
	return l
}

// Scores writes row's casewise score vector (ScoreLayout order; the
// univariate blocks are each column's own score, the correlation block
// each pair's ρ-score) into out, which must hold Layout().Size values.
func (s *TwoStepStats) Scores(row, out []float64) error {
	return s.scores(row, out, nil)
}

// pairScoreCache memoises the polychoric pairs' casewise scores by
// (pair, cell): a polychoric row's scores are a function of its cell
// alone, so Gamma reads each cell's once — the same figures, bit for
// bit, as computing them row by row.
type pairScoreCache map[[3]int]PairScore

// pairScores is PairScores through cache (nil: none).
func (s *TwoStepStats) pairScores(k int, row []float64, cache pairScoreCache) PairScore {
	pe := s.Pairs[k]
	if cache == nil || pe.Kind != PairPolychoric {
		return s.PairScores(k, row)
	}
	key := [3]int{k, int(row[pe.I]), int(row[pe.J])}
	if ps, ok := cache[key]; ok {
		return ps
	}
	ps := s.PairScores(k, row)
	cache[key] = ps
	return ps
}

func (s *TwoStepStats) scores(row, out []float64, cache pairScoreCache) error {
	l := s.Layout()
	if len(out) != l.Size || len(row) != len(s.Kinds) {
		return fmt.Errorf("two-step scores: row has %d values (want %d), out %d (want %d)", len(row), len(s.Kinds), len(out), l.Size)
	}
	for i := range out {
		out[i] = 0
	}
	for v, kind := range s.Kinds {
		if kind == KindOrdered {
			ThresholdScores(s.Thresholds[v], int(row[v]), out[l.Th[v]:l.Th[v]+l.NTh[v]])
			continue
		}
		res := row[v] - s.Mean[v]
		ev := s.Var[v]
		out[l.Th[v]] = res / ev
		out[l.VarIdx[v]] = -1/float64(2*ev) + float64(float64(1/float64(2*float64(ev*ev))*res)*res)
	}
	for k := range s.Pairs {
		ps := s.pairScores(k, row, cache)
		out[l.Cor+k] = ps.Rho
	}
	return nil
}

// ThresholdScores writes the casewise threshold scores of one row in
// category c (lav_uvord_sc: ∂ log π_c / ∂τ_k, two non-zero entries —
// the category's upper threshold +φ(τ_c)/π_c, its lower −φ(τ_{c−1})/π_c)
// into out (len(th) values).
func ThresholdScores(th []float64, c int, out []float64) {
	for i := range out {
		out[i] = 0
	}
	z1, z2 := ordinalLimits(th, c)
	pi := stdNormCDF(z1) - stdNormCDF(z2)
	if z2 > 1 {
		pi = stdNormCDF(-z2) - stdNormCDF(-z1)
	}
	inv := 1 / pi
	if c < len(th) {
		out[c] = float64(inv * stdNormPDF(z1))
	}
	if c > 0 {
		out[c-1] = -float64(inv * stdNormPDF(z2))
	}
}

// PairScore is one row's casewise scores of a bivariate log-likelihood
// (lavaan's lav_m84_pair_sc): Rho is ∂/∂ρ; ThI / ThJ are ∂/∂ the
// thresholds of column I / J (a numeric column: its mean, one value);
// VarI / VarJ are ∂/∂ a numeric column's variance (NaN for an ordered
// one).
type PairScore struct {
	Rho        float64
	ThI, ThJ   []float64
	VarI, VarJ float64
}

// PairScores returns row's casewise scores for Pairs[k].
func (s *TwoStepStats) PairScores(k int, row []float64) PairScore {
	pe := s.Pairs[k]
	i, j := pe.I, pe.J
	out := PairScore{VarI: math.NaN(), VarJ: math.NaN()}
	switch pe.Kind {
	case PairPolychoric:
		// lav_bvord_cor_sc with y1 = column I, y2 = column J.
		out.Rho, out.ThI, out.ThJ = polychoricCaseScore(s.Thresholds[i], s.Thresholds[j], int(row[i]), int(row[j]), pe.Rho)
	case PairPolyserial:
		x, o := i, j
		if s.Kinds[i] == KindOrdered {
			x, o = j, i
		}
		mu, va, th, rho := polyserialCaseScore(s.Mean[x], s.Var[x], s.Thresholds[o], row[x], int(row[o]), pe.Rho)
		out.Rho = rho
		if x == i {
			out.ThI, out.VarI, out.ThJ = []float64{mu}, va, th
		} else {
			out.ThJ, out.VarJ, out.ThI = []float64{mu}, va, th
		}
	case PairPearson:
		mi, vi, mj, vj, rho := pearsonCaseScore(s.Mean[i], s.Var[i], s.Mean[j], s.Var[j], row[i], row[j], pe.Rho)
		out.Rho, out.ThI, out.VarI, out.ThJ, out.VarJ = rho, []float64{mi}, vi, []float64{mj}, vj
	}
	return out
}

// polychoricCaseScore is lav_bvord_cor_sc_cache without covariates for
// a row in cell (c1, c2): the ρ-score φ-corner-sum / π, and the
// threshold scores of both columns.
func polychoricCaseScore(t1, t2 []float64, c1, c2 int, rho float64) (dRho float64, d1, d2 []float64) {
	pt := newPolyTable(t1, t2, nil)
	lik := pt.cellProbs(rho)[c1][c2]
	phi, _ := pt.derivs(rho)
	dRho = phi[c1][c2] / lik
	r := math.Sqrt(1 - float64(rho*rho))
	u1, l1 := ordinalLimits(t1, c1)
	u2, l2 := ordinalLimits(t2, c2)
	// side is the ∂π/∂(limit) of one column at limit a, the other
	// column's (upper, lower) limits (b1, b2).
	side := func(a, b1, b2 float64) float64 {
		if r == 0 {
			return float64(stdNormPDF(a) * 0.5)
		}
		return float64(stdNormPDF(a)*stdNormCDF((b1-float64(rho*a))/r)) - float64(stdNormPDF(a)*stdNormCDF((b2-float64(rho*a))/r))
	}
	d1 = make([]float64, len(t1))
	if c1 < len(t1) {
		d1[c1] = side(u1, u2, l2) / lik
	}
	if c1 > 0 {
		d1[c1-1] = -side(l1, u2, l2) / lik
	}
	d2 = make([]float64, len(t2))
	if c2 < len(t2) {
		d2[c2] = side(u2, u1, l1) / lik
	}
	if c2 > 0 {
		d2[c2-1] = -side(l2, u1, l1) / lik
	}
	return dRho, d1, d2
}

// polyserialCaseScore is lav_bvmix_cor_sc_cache without covariates
// (sigma_correction FALSE) for numeric value y and ordered category c.
func polyserialCaseScore(mean, variance float64, th []float64, y float64, c int, rho float64) (dMu, dVar float64, dTh []float64, dRho float64) {
	sd := math.Sqrt(variance)
	z := (y - mean) / sd
	r := math.Sqrt(1 - float64(rho*rho))
	up, lo := ordinalLimits(th, c)
	ts1 := (up - float64(rho*z)) / r
	ts2 := (lo - float64(rho*z)) / r
	yz1, yz2 := stdNormPDF(ts1), stdNormPDF(ts2)
	py := stdNormCDF(ts1) - stdNormCDF(ts2)
	if py < polyserialFloor {
		py = polyserialFloor
	}
	inv := 1 / py
	dz := yz1 - yz2
	dMu = float64((1 / sd) * (z + float64(float64(inv*(rho/r))*dz)))
	dVar = float64((1 / float64(2*variance)) * (float64(z*z) - 1 + float64(float64(float64(inv*rho)*z)/r*dz)))
	dTh = make([]float64, len(th))
	if c < len(th) {
		dTh[c] = float64(float64(yz1*(1/r)) * inv)
	}
	if c > 0 {
		dTh[c-1] = -float64(float64(yz2*(1/r)) * inv)
	}
	tj := float64(yz1 * (float64(up*rho) - z))
	tj1 := float64(yz2 * (float64(lo*rho) - z))
	dRho = float64(float64(inv*(1/float64(float64(r*r)*r))) * (tj - tj1))
	return dMu, dVar, dTh, dRho
}

// pearsonCaseScore is lav_bvreg_cor_sc_cache without covariates for the
// pair (y1, y2).
func pearsonCaseScore(m1, v1, m2, v2, y1, y2, rho float64) (dMu1, dVar1, dMu2, dVar2, dRho float64) {
	r := 1 - float64(rho*rho)
	y1c, y2c := y1-m1, y2-m2
	sd1, sd2 := math.Sqrt(v1), math.Sqrt(v2)
	s12 := float64(sd1 * sd2)
	dMu1 = (float64(2*y1c)/v1 - float64(float64(2*rho)*y2c)/s12) / float64(2*r)
	dMu2 = -(float64(float64(2*rho)*y1c)/s12 - float64(2*y2c)/v2) / float64(2*r)
	dVar1 = -(0.5/v1 - (float64(y1c*y1c)/float64(v1*v1)-float64(float64(rho*y1c)*y2c)/float64(float64(v1*sd1)*sd2))/float64(2*r))
	dVar2 = -(0.5/v2 + (float64(float64(rho*y1c)*y2c)/float64(float64(v2*sd1)*sd2)-float64(y2c*y2c)/float64(v2*v2))/float64(2*r))
	q := float64(y1c*y1c)/v1 - float64(float64(float64(2*rho)*y1c)*y2c)/s12 + float64(y2c*y2c)/v2
	dRho = rho/r + (float64(y1c*y2c)/float64(s12*r) - float64(q*rho)/float64(r*r))
	return dMu1, dVar1, dMu2, dVar2, dRho
}
