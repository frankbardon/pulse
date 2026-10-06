package linalg

import (
	"math"
	"strconv"
)

// CoMomentMode selects how a CoMoment treats a row with a missing value
// (a NaN in x).
type CoMomentMode int

const (
	// Listwise (the zero value, so the default) drops a row with ANY
	// missing value; every figure is over the same complete rows.
	Listwise CoMomentMode = iota
	// Pairwise keeps, for every pair (i, j), its own count, weight sum,
	// means and co-moments over the rows where BOTH x[i] and x[j] are
	// present. Variable i's mean and variance read the pair (i, i).
	Pairwise
)

// String returns "listwise", "pairwise" or "unknown".
func (m CoMomentMode) String() string {
	switch m {
	case Listwise:
		return "listwise"
	case Pairwise:
		return "pairwise"
	}
	return "unknown"
}

// pairMoment is one pair's running state: raw row count, weight sum,
// the two means and the weighted co-moments Σw(xi−mi)², Σw(xj−mj)²,
// Σw(xi−mi)(xj−mj) over the rows the pair admits.
type pairMoment struct {
	n             int64
	w             float64
	mi, mj        float64
	mii, mjj, cij float64
}

// CoMoment is a weighted, mergeable accumulator of the means and the
// covariance (co-moment) matrix of p variables — the one streaming
// first/second-moment primitive the engine, synth and embedders share.
//
// # Update
//
// Add folds one row by the weighted Welford–West recurrence. Merge folds
// another accumulator by the exact Chan–Golub–LeVeque combine,
//
//	W = Wa + Wb,  δ = mean_b − mean_a,
//	mean = mean_a + δ·Wb/W,
//	M2 = M2a + M2b + δδᵀ·Wa·Wb/W,
//
// per pair in Pairwise mode. Both are REFERENCE kernels: pure Go and
// FMA-free (every product feeding an addition is an explicit
// float64(a*b) conversion), so the same sequence of Add and Merge calls
// gives the same bits on every architecture. A fixed merge tree over
// fixed blocks is therefore reproducible bit for bit, whatever runs it.
//
// The first row to carry mass sets the mean to x exactly, and a later
// row equal to the mean adds exactly zero, so a constant column's
// variance is exactly 0. With unit weights the recurrence is the
// classic unweighted Welford one.
//
// # Missing values and weights
//
// A NaN in x is a missing value: Listwise skips the row, Pairwise skips
// it per pair. A row the mode admits nowhere (Listwise: any NaN;
// Pairwise: every value NaN) is not counted and its weight is never
// judged. Otherwise the weight is validated as Pulse's weighting
// contract does: a NaN, ±Inf or negative w skips the row and increments
// NWeightInvalid; w = 0 is valid — the row counts toward N (and the
// pair counts) but adds no mass to W, the means or the co-moments; a
// positive finite w is used as given.
//
// # Tolerance
//
// Floating-point addition is not associative, so merging the pieces of
// a split is not bit-identical to one serial pass. It agrees within
// 1e-10 relative to the figure's scale: |Δmean_i| ≤ 1e-10·(|mean_i| +
// sd_i + 1), |ΔCov_ij| ≤ 1e-10·sd_i·sd_j, |ΔCorr_ij| ≤ 1e-10, W and NEff
// to 1e-10 relative. Counts (N, NWeightInvalid, PairN) are exact. Merge
// is associative to the same tolerance, and merging with an
// accumulator that carries no mass (empty, or zero-weight rows only)
// leaves every moment bit-identical on either side.
//
// A CoMoment is not safe for concurrent use; give each worker its own
// and Merge them.
type CoMoment struct {
	p        int
	mode     CoMomentMode
	n        int64 // rows counted (see N)
	nInvalid int64
	w, w2    float64

	// Listwise: one shared count/weight (n, w above), the means and the
	// packed upper-triangle co-moment matrix.
	mean []float64
	m2   []float64

	// Pairwise: one state per packed upper-triangle pair (i ≤ j).
	pairs []pairMoment
}

// NewCoMoment returns an empty accumulator over p variables. A negative
// p or an unknown mode is PULSE_MATRIX_SHAPE_MISMATCH.
func NewCoMoment(p int, mode CoMomentMode) (*CoMoment, error) {
	if p < 0 {
		return nil, shapeError("co-moment dimension must be non-negative", map[string]any{"p": p})
	}
	c := &CoMoment{p: p, mode: mode}
	size := p * (p + 1) / 2
	switch mode {
	case Listwise:
		c.mean = make([]float64, p)
		c.m2 = make([]float64, size)
	case Pairwise:
		c.pairs = make([]pairMoment, size)
	default:
		return nil, shapeError("unknown co-moment mode", map[string]any{"mode": int(mode)})
	}
	return c, nil
}

// P returns the number of variables.
func (c *CoMoment) P() int { return c.p }

// Mode returns the missing-value mode.
func (c *CoMoment) Mode() CoMomentMode { return c.mode }

// index maps (i, j) to the packed upper-triangle offset (the Sym layout).
func (c *CoMoment) index(i, j int) int {
	if i > j {
		i, j = j, i
	}
	return i*c.p - i*(i-1)/2 + (j - i)
}

func validWeight(w float64) bool {
	return !math.IsNaN(w) && !math.IsInf(w, 0) && w >= 0
}

// Add folds one row. len(x) must equal P(); anything else is a
// programming error and panics, like an out-of-range index. See the type
// comment for missing values and weights.
func (c *CoMoment) Add(x []float64, w float64) {
	if len(x) != c.p {
		panic("linalg: CoMoment.Add row length " + strconv.Itoa(len(x)) +
			" does not equal p = " + strconv.Itoa(c.p))
	}
	if c.mode == Pairwise {
		c.addPairwise(x, w)
		return
	}
	for _, v := range x {
		if math.IsNaN(v) {
			return
		}
	}
	if !validWeight(w) {
		c.nInvalid++
		return
	}
	c.n++
	if w == 0 {
		return
	}
	wOld := c.w
	c.w += w
	c.w2 += float64(w * w)
	if wOld == 0 {
		copy(c.mean, x)
		return
	}
	// wd[i] = w·(x_i − mean_i) against the OLD mean; then the means move
	// and the co-moments take wd[i]·(x_j − mean_j) against the NEW mean.
	wd := make([]float64, c.p)
	for i, v := range x {
		wd[i] = float64(w * (v - c.mean[i]))
	}
	for i := range c.mean {
		c.mean[i] += wd[i] / c.w
	}
	k := 0
	for i := 0; i < c.p; i++ {
		for j := i; j < c.p; j++ {
			c.m2[k] += float64(wd[i] * (x[j] - c.mean[j]))
			k++
		}
	}
}

func (c *CoMoment) addPairwise(x []float64, w float64) {
	present := false
	for _, v := range x {
		if !math.IsNaN(v) {
			present = true
			break
		}
	}
	if !present {
		return
	}
	if !validWeight(w) {
		c.nInvalid++
		return
	}
	c.n++
	if w != 0 {
		c.w += w
		c.w2 += float64(w * w)
	}
	k := 0
	for i := 0; i < c.p; i++ {
		for j := i; j < c.p; j++ {
			if !math.IsNaN(x[i]) && !math.IsNaN(x[j]) {
				c.pairs[k].add(x[i], x[j], w)
			}
			k++
		}
	}
}

// add is the per-pair Welford–West step, the same operation order as
// the Listwise path.
func (s *pairMoment) add(xi, xj, w float64) {
	s.n++
	if w == 0 {
		return
	}
	wOld := s.w
	s.w += w
	if wOld == 0 {
		s.mi, s.mj = xi, xj
		return
	}
	wdi := float64(w * (xi - s.mi))
	wdj := float64(w * (xj - s.mj))
	s.mi += wdi / s.w
	s.mj += wdj / s.w
	s.mii += float64(wdi * (xi - s.mi))
	s.mjj += float64(wdj * (xj - s.mj))
	s.cij += float64(wdi * (xj - s.mj))
}

// merge is the per-pair Chan–Golub–LeVeque combine.
func (s *pairMoment) merge(o pairMoment) {
	s.n += o.n
	if o.w == 0 {
		return
	}
	if s.w == 0 {
		n := s.n
		*s = o
		s.n = n
		return
	}
	wa, wb := s.w, o.w
	w := wa + wb
	di := o.mi - s.mi
	dj := o.mj - s.mj
	f := float64(wa * wb)
	s.mi += float64(di*wb) / w
	s.mj += float64(dj*wb) / w
	s.mii = s.mii + o.mii + float64(float64(di*di)*f)/w
	s.mjj = s.mjj + o.mjj + float64(float64(dj*dj)*f)/w
	s.cij = s.cij + o.cij + float64(float64(di*dj)*f)/w
	s.w = w
}

// Merge folds other into the receiver (the receiver is MUTATED; other is
// not) by the exact Chan–Golub–LeVeque combine — so a merge tree is a
// sequence of left.Merge(right) calls. Counts and NWeightInvalid add. A
// nil other, or one whose P or Mode differs, is
// PULSE_MATRIX_SHAPE_MISMATCH and leaves the receiver untouched.
// c.Merge(c) is allowed and doubles the mass: every element of other
// is read before the receiver's copy of it is written.
func (c *CoMoment) Merge(other *CoMoment) error {
	if other == nil {
		return nilOperand("co-moment")
	}
	if other.p != c.p || other.mode != c.mode {
		return shapeError("co-moment accumulators differ in dimension or mode",
			map[string]any{"p": c.p, "other_p": other.p,
				"mode": c.mode.String(), "other_mode": other.mode.String()})
	}
	c.n += other.n
	c.nInvalid += other.nInvalid
	if c.mode == Pairwise {
		c.w += other.w
		c.w2 += other.w2
		for k := range c.pairs {
			c.pairs[k].merge(other.pairs[k])
		}
		return nil
	}
	if other.w == 0 {
		return nil
	}
	if c.w == 0 {
		c.w, c.w2 = other.w, other.w2
		copy(c.mean, other.mean)
		copy(c.m2, other.m2)
		return nil
	}
	wa, wb := c.w, other.w
	w := wa + wb
	f := float64(wa * wb)
	delta := make([]float64, c.p)
	for i := range delta {
		delta[i] = other.mean[i] - c.mean[i]
	}
	for i := range c.mean {
		c.mean[i] += float64(delta[i]*wb) / w
	}
	k := 0
	for i := 0; i < c.p; i++ {
		for j := i; j < c.p; j++ {
			c.m2[k] = c.m2[k] + other.m2[k] + float64(float64(delta[i]*delta[j])*f)/w
			k++
		}
	}
	c.w = w
	c.w2 += other.w2
	return nil
}

// Clone returns an independent deep copy.
func (c *CoMoment) Clone() *CoMoment {
	out := *c
	out.mean = append([]float64(nil), c.mean...)
	out.m2 = append([]float64(nil), c.m2...)
	out.pairs = append([]pairMoment(nil), c.pairs...)
	return &out
}

// N returns the rows counted: Listwise, the complete rows with a valid
// weight (zero included); Pairwise, the rows with at least one present
// value and a valid weight. Rows skipped for an invalid weight are
// NWeightInvalid, never N.
func (c *CoMoment) N() int64 { return c.n }

// W returns Σw over the rows N counts.
func (c *CoMoment) W() float64 { return c.w }

// NWeightInvalid returns the rows skipped for a NaN, ±Inf or negative
// weight (among rows the mode would otherwise have admitted).
func (c *CoMoment) NWeightInvalid() int64 { return c.nInvalid }

// NEff returns Kish's effective sample size (Σw)²/Σw² over the rows N
// counts — N itself under unit weights — and 0 when no row carries mass.
func (c *CoMoment) NEff() float64 {
	if c.w2 == 0 {
		return 0
	}
	return c.w * c.w / c.w2
}

// PairN returns the rows counted for the pair (i, j) — N for every pair
// in Listwise mode. It panics when i or j is out of range.
func (c *CoMoment) PairN(i, j int) int64 {
	c.check(i, j)
	if c.mode == Pairwise {
		return c.pairs[c.index(i, j)].n
	}
	return c.n
}

// PairW returns Σw for the pair (i, j) — W for every pair in Listwise
// mode. It panics when i or j is out of range.
func (c *CoMoment) PairW(i, j int) float64 {
	c.check(i, j)
	if c.mode == Pairwise {
		return c.pairs[c.index(i, j)].w
	}
	return c.w
}

func (c *CoMoment) check(i, j int) {
	if i < 0 || i >= c.p || j < 0 || j >= c.p {
		panic("linalg: CoMoment index (" + strconv.Itoa(i) + ", " + strconv.Itoa(j) +
			") out of range for p = " + strconv.Itoa(c.p))
	}
}

// Mean returns the weighted means (Pairwise: variable i over the rows
// where x[i] is present). A variable with no mass has a NaN mean.
func (c *CoMoment) Mean() *Vec {
	out := &Vec{data: make([]float64, c.p)}
	for i := range out.data {
		w, m := c.w, 0.0
		if c.mode == Pairwise {
			s := c.pairs[c.index(i, i)]
			w, m = s.w, s.mi
		} else {
			m = c.mean[i]
		}
		if w == 0 {
			m = math.NaN()
		}
		out.data[i] = m
	}
	return out
}

// comoment returns the pair (i, j)'s weight sum, cross co-moment and the
// two own co-moments over the same rows.
func (c *CoMoment) comoment(i, j int) (w, cij, mii, mjj float64) {
	if c.mode == Pairwise {
		s := c.pairs[c.index(i, j)]
		if i > j {
			return s.w, s.cij, s.mjj, s.mii
		}
		return s.w, s.cij, s.mii, s.mjj
	}
	return c.w, c.m2[c.index(i, j)], c.m2[c.index(i, i)], c.m2[c.index(j, j)]
}

// Cov returns the covariance matrix M2/(W − ddof) — per pair in
// Pairwise mode. ddof = 0 is the weighted population covariance;
// ddof = 1 the frequency-weight sample covariance (the unweighted sample
// covariance under unit weights). An entry whose W − ddof ≤ 0 is NaN.
// Probability-weight corrections are the caller's: NEff is reported
// beside it.
func (c *CoMoment) Cov(ddof int) *Sym {
	out := &Sym{n: c.p, data: make([]float64, c.p*(c.p+1)/2)}
	for i := 0; i < c.p; i++ {
		for j := i; j < c.p; j++ {
			w, cij, _, _ := c.comoment(i, j)
			d := w - float64(ddof)
			v := math.NaN()
			if d > 0 {
				v = cij / d
			}
			out.data[out.index(i, j)] = v
		}
	}
	return out
}

// corrDenominator is √(mii·mjj), or √mii·√mjj when the product is not a
// normal float64 (overflow to +Inf, underflow toward 0). mii, mjj > 0.
func corrDenominator(mii, mjj float64) float64 {
	if prod := mii * mjj; prod >= minNormal && !math.IsInf(prod, 1) {
		return math.Sqrt(prod)
	}
	return float64(math.Sqrt(mii) * math.Sqrt(mjj))
}

// minNormal is the smallest positive normal float64 (2⁻¹⁰²²).
const minNormal = 0x1p-1022

// Corr returns the correlation matrix C_ij/√(M2_ii·M2_jj) — each pair's
// own co-moments in Pairwise mode — clamped to [−1, 1] against rounding.
// The one-root form is TEST_PEARSON_R's arithmetic, so a single block of
// unit-weight rows correlates to the same bits as that test; when the
// product M2_ii·M2_jj leaves the normal range (overflows to +Inf or
// falls below the smallest normal float64) the entry takes the two-root
// form C_ij/(√M2_ii·√M2_jj) instead, so no spread overflows or
// underflows.
// The diagonal is exactly 1 for a variable with spread. A variable with
// zero spread (a constant column, a single massed row, no rows) has NO
// defined correlation: every entry touching it, its own diagonal
// included, is NaN — never 0.
func (c *CoMoment) Corr() *Sym {
	out := &Sym{n: c.p, data: make([]float64, c.p*(c.p+1)/2)}
	for i := 0; i < c.p; i++ {
		for j := i; j < c.p; j++ {
			_, cij, mii, mjj := c.comoment(i, j)
			v := math.NaN()
			switch {
			case !(mii > 0) || !(mjj > 0):
			case i == j:
				v = 1
			default:
				v = cij / corrDenominator(mii, mjj)
				v = math.Max(-1, math.Min(1, v))
			}
			out.data[out.index(i, j)] = v
		}
	}
	return out
}
