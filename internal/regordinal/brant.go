package regordinal

import (
	"math"

	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// brant.go is the Brant (1990) test of the proportional-odds assumption,
// the brant package's algorithm: the K − 1 binary logits of 1{y > m}
// (each with an intercept, prior weights w*) are fitted to their fixed
// point; their slopes β*_m are stacked, with the covariance
//
//	Var(β*_m)       = (X̃ᵀW_mX̃)⁻¹ without the intercept row / column
//	Cov(β*_m, β*_l) = (X̃ᵀW_mX̃)⁻¹ (X̃ᵀW_mlX̃) (X̃ᵀW_lX̃)⁻¹, same rows,
//
// W_m = w*·π_m(1 − π_m), W_ml = w*·π_l(1 − π_m) for m < l, and
// Cov(β*_l, β*_m) its transpose. The omnibus statistic is the Wald χ² of
// β*_1 = β*_m for every m (df (K − 2)·p); each predictor's is the same
// over its own slopes (df K − 2). The brant package (0.3.0) stores the
// (m, l) block itself, untransposed, at (l, m): that moves only the
// omnibus statistic (scripts/reference/gen_ordinal.R reproduces it and
// keeps the symmetric form, which is a covariance).

// brantResult is the test: the omnibus statistic and each predictor's,
// in predictor order. A figure the fits cannot give (a binary fit that
// does not converge, a singular block) is NaN — null on the wire; the
// ordinal fit stands.
type brantResult struct {
	stat, df, p float64
	terms       []brantTerm
}

type brantTerm struct {
	stat, df, p float64
}

// binaryLogit is one 1{y > m} logit at its fixed point: its
// coefficients (intercept first) and fitted probabilities.
type binaryLogit struct {
	beta []float64
	pi   []float64
}

// brantMaxIter / brantStepTol: the binary fits' Newton (= IRLS on the
// canonical link) cap and stop rule.
const (
	brantMaxIter = 100
	brantStepTol = 1e-13
)

// fitBinary fits the logit of z (0/1 per row) on [1, x] with prior
// weights w by Newton–Raphson from β = 0, to the fixed point; false
// when it does not converge (separation) or the information is
// singular.
func (d *design) fitBinary(z []bool) (binaryLogit, bool) {
	q := d.p + 1
	beta := make([]float64, q)
	pi := make([]float64, len(z))
	row := make([]float64, q)
	for it := 0; it < brantMaxIter; it++ {
		grad := make([]float64, q)
		info := make([][]float64, q)
		for j := range info {
			info[j] = make([]float64, q)
		}
		for i := range z {
			d.augmented(i, row)
			eta := 0.0
			for j, v := range row {
				eta += float64(v * beta[j])
			}
			F, f, _ := logistic(eta)
			pi[i] = F
			y := 0.0
			if z[i] {
				y = 1
			}
			w := d.w[i]
			r := float64(w * (y - F))
			wf := float64(w * f)
			for j, v := range row {
				grad[j] += float64(r * v)
				wv := float64(wf * v)
				for k := 0; k <= j; k++ {
					info[j][k] += float64(wv * row[k])
				}
			}
		}
		sym, _ := linalg.NewSymFromRows(info)
		step, err := linalg.SolveSPD(sym, linalg.NewVec(grad))
		if err != nil {
			return binaryLogit{}, false
		}
		rel := 0.0
		for j, v := range step.Slice() {
			beta[j] += v
			if r := math.Abs(v) / math.Max(math.Abs(beta[j]), 1); r > rel {
				rel = r
			}
		}
		if rel <= brantStepTol {
			for i := range z {
				d.augmented(i, row)
				eta := 0.0
				for j, v := range row {
					eta += float64(v * beta[j])
				}
				pi[i], _, _ = logistic(eta)
			}
			return binaryLogit{beta: beta, pi: pi}, true
		}
	}
	return binaryLogit{}, false
}

// augmented writes [1, x_i] into row.
func (d *design) augmented(i int, row []float64) {
	row[0] = 1
	copy(row[1:], d.x[i])
}

// weightedCross is X̃ᵀ diag(c) X̃ over the rows.
func (d *design) weightedCross(c []float64) [][]float64 {
	q := d.p + 1
	out := make([][]float64, q)
	for j := range out {
		out[j] = make([]float64, q)
	}
	row := make([]float64, q)
	for i := range c {
		d.augmented(i, row)
		for j, v := range row {
			cv := float64(c[i] * v)
			for k := 0; k <= j; k++ {
				out[j][k] += float64(cv * row[k])
			}
		}
	}
	for j := range out {
		for k := j + 1; k < q; k++ {
			out[j][k] = out[k][j]
		}
	}
	return out
}

// matMul is a·b for square row-major matrices.
func matMul(a, b [][]float64) [][]float64 {
	n := len(a)
	out := make([][]float64, n)
	for i := range out {
		out[i] = make([]float64, n)
		for j := range out[i] {
			s := 0.0
			for k := 0; k < n; k++ {
				s += float64(a[i][k] * b[k][j])
			}
			out[i][j] = s
		}
	}
	return out
}

// brant runs the test over the design (link logit). k ≥ 3.
func (d *design) brant() brantResult {
	p, J := d.p, d.k
	nan := math.NaN()
	res := brantResult{stat: nan, df: float64((J - 2) * p), p: nan, terms: make([]brantTerm, p)}
	for k := range res.terms {
		res.terms[k] = brantTerm{stat: nan, df: float64(J - 2), p: nan}
	}
	fits := make([]binaryLogit, J-1)
	inv := make([][][]float64, J-1)
	n := len(d.y)
	for m := 0; m < J-1; m++ {
		z := make([]bool, n)
		for i, y := range d.y {
			z[i] = y > m
		}
		f, ok := d.fitBinary(z)
		if !ok {
			return res
		}
		fits[m] = f
		c := make([]float64, n)
		for i := range c {
			c[i] = float64(d.w[i] * (f.pi[i] - float64(f.pi[i]*f.pi[i])))
		}
		sym, _ := linalg.NewSymFromRows(d.weightedCross(c))
		iv, err := linalg.InverseSPD(sym)
		if err != nil {
			return res
		}
		inv[m] = iv.ToRows()
	}
	// v(m, a, l, b) is Cov(β*_{m,a}, β*_{l,b}), slopes a, b (0-based,
	// the intercept skipped).
	cross := map[[2]int][][]float64{}
	for m := 0; m < J-1; m++ {
		for l := m + 1; l < J-1; l++ {
			c := make([]float64, n)
			for i := range c {
				pm, pl := fits[m].pi[i], fits[l].pi[i]
				c[i] = float64(d.w[i] * (pl - float64(pm*pl)))
			}
			cross[[2]int{m, l}] = matMul(matMul(inv[m], d.weightedCross(c)), inv[l])
		}
	}
	v := func(m, a, l, b int) float64 {
		switch {
		case m == l:
			return inv[m][a+1][b+1]
		case m < l:
			return cross[[2]int{m, l}][a+1][b+1]
		default:
			return cross[[2]int{l, m}][b+1][a+1]
		}
	}
	slope := func(m, a int) float64 { return fits[m].beta[a+1] }
	// wald is dᵀ M⁻¹ d for the differences β*_{0,a} − β*_{i+1,a} over
	// the (i, a) pairs of idx.
	wald := func(idx [][2]int) float64 {
		r := len(idx)
		dv := make([]float64, r)
		mrows := make([][]float64, r)
		for x, ia := range idx {
			i, a := ia[0], ia[1]
			dv[x] = slope(0, a) - slope(i+1, a)
			mrows[x] = make([]float64, r)
			for y, jb := range idx {
				j, b := jb[0], jb[1]
				mrows[x][y] = v(0, a, 0, b) - v(0, a, j+1, b) - v(i+1, a, 0, b) + v(i+1, a, j+1, b)
			}
		}
		sym, err := linalg.NewSymFromRows(mrows)
		if err != nil {
			return nan
		}
		sol, err := linalg.SolveSPD(sym, linalg.NewVec(dv))
		if err != nil {
			return nan
		}
		s := 0.0
		for x, u := range sol.Slice() {
			s += float64(dv[x] * u)
		}
		return s
	}
	var all [][2]int
	for i := 0; i < J-2; i++ {
		for a := 0; a < p; a++ {
			all = append(all, [2]int{i, a})
		}
	}
	res.stat = wald(all)
	res.p = chiSquareP(res.stat, res.df)
	for a := 0; a < p; a++ {
		var idx [][2]int
		for i := 0; i < J-2; i++ {
			idx = append(idx, [2]int{i, a})
		}
		st := wald(idx)
		res.terms[a] = brantTerm{stat: st, df: float64(J - 2), p: chiSquareP(st, float64(J-2))}
	}
	return res
}

// chiSquareP is the χ² upper tail, NaN for an undefined statistic.
func chiSquareP(x, df float64) float64 {
	if math.IsNaN(x) {
		return math.NaN()
	}
	return statdist.ChiSquareSurvival(x, df)
}

// twoSidedNormalP is 2·Φ(−|z|) = erfc(|z|/√2).
func twoSidedNormalP(z float64) float64 {
	if math.IsNaN(z) {
		return math.NaN()
	}
	return detmath.Erfc(math.Abs(z) / math.Sqrt2)
}
