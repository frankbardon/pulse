package sem

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/internal/detmath"
)

// Two-step ordinal sample statistics: the first stage of lavaan's
// DWLS / WLSMV estimators (lavaan's muthen1984 → lav_samp_step1 /
// lav_samp_step2, which lavCor(..., ordered = ...) also reports).
//
//  1. Univariate: an ordered variable's thresholds are the normal
//     quantiles of its cumulative (weighted) category proportions; a
//     numeric variable's mean and ML variance (divisor N).
//  2. Bivariate, every pair with the step-1 figures held fixed: the ML
//     polychoric correlation (ordered × ordered), the two-step ML
//     polyserial correlation (numeric × ordered) and the Pearson
//     correlation (numeric × numeric), each bounded as lavaan bounds it.
//
// Rows are complete (listwise, the caller drops incomplete rows first)
// and Weights are FREQUENCY weights: a row of weight w counts as w
// copies, N = Σw, so an integer weight is bit-for-bit a replicated row
// up to summation order. Everything here is FMA-free (see normal.go).

// OrdinalKind is a two-step variable's measurement level.
type OrdinalKind uint8

const (
	// KindNumeric is a continuous variable (a mean and a variance).
	KindNumeric OrdinalKind = iota
	// KindOrdered is an ordinal variable: integer category codes
	// 0..Levels-1, read as a cut normal latent response.
	KindOrdered
)

// PairKind is a two-step correlation's estimator.
type PairKind uint8

const (
	// PairPearson is numeric × numeric: the ML (Pearson) correlation.
	PairPearson PairKind = iota
	// PairPolyserial is numeric × ordered: two-step ML polyserial.
	PairPolyserial
	// PairPolychoric is ordered × ordered: two-step ML polychoric.
	PairPolychoric
)

func (k PairKind) String() string {
	switch k {
	case PairPearson:
		return "pearson"
	case PairPolyserial:
		return "polyserial"
	case PairPolychoric:
		return "polychoric"
	}
	return fmt.Sprintf("PairKind(%d)", uint8(k))
}

// Bounds of the bivariate estimates (lavaan's nlminb boxes): the
// polychoric and Pearson searches stay in ±0.999, the polyserial in
// ±0.995. A maximum outside the box is reported on its edge
// (PairEstimate.Bound).
const (
	PolychoricBound = 0.999
	PolyserialBound = 0.995
)

// cellFloor is .Machine$double.eps^(2/3), lavaan's floor on a polychoric
// cell probability (lav_bvord_noexo_pi).
const cellFloor = 0x1.428a2f98d7292p-35

// polyserialFloor is .Machine$double.eps, lavaan's floor on a polyserial
// conditional probability (lav_bvmix_lik_cache).
const polyserialFloor = 0x1p-52

// ordinalSentinel is lavaan's ±100 stand-in for an open threshold
// (lav_uvord_o12): the bottom category's lower limit and the top
// category's upper.
const ordinalSentinel = 100

// Reasons of an OrdinalError.
const (
	// ReasonNoData: no row, or a total weight that is not positive.
	ReasonNoData = "no_data"
	// ReasonInvalidInput: a ragged row, a non-finite value, a code
	// outside 0..Levels-1, a negative or non-finite weight, or a kind /
	// level spec that does not describe the columns.
	ReasonInvalidInput = "invalid_input"
	// ReasonConstant: a variable without spread — an ordered one with a
	// single category (Levels 1, or every weight on one code), a numeric
	// one with zero variance.
	ReasonConstant = "constant"
	// ReasonEmptyCategory: an ordered variable with a declared category
	// no row (no positive weight) falls in — the lavaan refusal "some
	// categories of variable are empty in group g", reached when a
	// category observed in the pooled data is missing from one group.
	ReasonEmptyCategory = "empty_category"
)

// OrdinalError is a refused two-step input: Var is the column at fault
// (-1 for the whole input), Group the group index (-1 ungrouped), Reason
// one of the Reason* codes, Freq an ordered column's weighted category
// frequencies when they are the reason.
type OrdinalError struct {
	Var, Group int
	Reason     string
	Freq       []float64
	Msg        string
}

func (e *OrdinalError) Error() string {
	where := ""
	if e.Group >= 0 {
		where = fmt.Sprintf(" in group %d", e.Group)
	}
	if e.Var >= 0 {
		return fmt.Sprintf("two-step statistics: variable %d%s: %s", e.Var, where, e.Msg)
	}
	return fmt.Sprintf("two-step statistics%s: %s", where, e.Msg)
}

// TwoStepInput is one group's data for TwoStep.
type TwoStepInput struct {
	// Kinds is each column's measurement level.
	Kinds []OrdinalKind
	// Levels is each ordered column's category count (codes
	// 0..Levels-1; ignored for a numeric column). Every declared
	// category must hold weight (ReasonEmptyCategory).
	Levels []int
	// Rows are the complete observations, one value per column; an
	// ordered column holds its integer code.
	Rows [][]float64
	// Weights are per-row frequency weights; nil weighs every row 1.
	Weights []float64
}

// PairEstimate is one bivariate two-step correlation. Pairs are in
// lavaan's layout: column-major below the diagonal, (1,0), (2,0), …,
// (p-1,0), (2,1), …, so I > J and the k-th pair is lavaan's k-th
// correlation (its WLS.obs order).
type PairEstimate struct {
	I, J int
	Kind PairKind
	Rho  float64
	// Bound reports an estimate pinned on its box edge (±0.999 /
	// ±0.995): the likelihood still rose toward ±1.
	Bound bool
	// ZeroCells reports a polychoric table with an empty cell.
	ZeroCells bool
	// Closed reports a closed-form polychoric (a 2 × 2 table with
	// proportional margins, two empty cells, or both thresholds 0;
	// lavaan's lav_bvord_cor_twostep_fit shortcuts).
	Closed bool
}

// TwoStepStats is one group's two-step sample statistics.
type TwoStepStats struct {
	// N is the total weight Σw; Rows the row count.
	N    float64
	Rows int
	// Kinds and Levels echo the input.
	Kinds  []OrdinalKind
	Levels []int
	// Thresholds are each ordered column's Levels-1 thresholds (nil for
	// a numeric column); Freq its weighted category frequencies.
	Thresholds [][]float64
	Freq       [][]float64
	// Mean and Var are a numeric column's mean and ML variance (Σw
	// divisor); an ordered column's latent response is standardised (0
	// and 1).
	Mean, Var []float64
	// Cor is the full symmetric correlation matrix (unit diagonal).
	Cor [][]float64
	// Pairs are the bivariate estimates in lavaan's order.
	Pairs []PairEstimate
}

// TwoStep computes one group's thresholds, means, variances and
// pairwise correlations.
func TwoStep(in TwoStepInput) (*TwoStepStats, error) {
	return twoStep(in, -1)
}

// TwoStepGroups computes the statistics per group: group[i] in
// 0..levels-1 is row i's group, and every group shares in's Kinds and
// Levels, so a category the pooled data holds but one group lacks is
// that group's ReasonEmptyCategory refusal (as lavaan refuses it).
func TwoStepGroups(in TwoStepInput, group []int, levels int) ([]*TwoStepStats, error) {
	if len(group) != len(in.Rows) || levels < 1 {
		return nil, &OrdinalError{Var: -1, Group: -1, Reason: ReasonInvalidInput,
			Msg: "the group index must name one group in 0..levels-1 per row"}
	}
	parts := make([]TwoStepInput, levels)
	for g := range parts {
		parts[g] = TwoStepInput{Kinds: in.Kinds, Levels: in.Levels}
		if in.Weights != nil {
			parts[g].Weights = []float64{}
		}
	}
	for i, g := range group {
		if g < 0 || g >= levels {
			return nil, &OrdinalError{Var: -1, Group: -1, Reason: ReasonInvalidInput,
				Msg: fmt.Sprintf("row %d names group %d outside 0..%d", i, g, levels-1)}
		}
		parts[g].Rows = append(parts[g].Rows, in.Rows[i])
		if in.Weights != nil {
			parts[g].Weights = append(parts[g].Weights, in.Weights[i])
		}
	}
	out := make([]*TwoStepStats, levels)
	for g := range parts {
		s, err := twoStep(parts[g], g)
		if err != nil {
			return nil, err
		}
		out[g] = s
	}
	return out, nil
}

func twoStep(in TwoStepInput, group int) (*TwoStepStats, error) {
	p := len(in.Kinds)
	refuse := func(v int, reason, msg string, freq []float64) error {
		return &OrdinalError{Var: v, Group: group, Reason: reason, Freq: freq, Msg: msg}
	}
	if p < 1 || len(in.Levels) != p {
		return nil, refuse(-1, ReasonInvalidInput, "Kinds and Levels must describe every column", nil)
	}
	if len(in.Rows) == 0 {
		return nil, refuse(-1, ReasonNoData, "no complete rows", nil)
	}
	if in.Weights != nil && len(in.Weights) != len(in.Rows) {
		return nil, refuse(-1, ReasonInvalidInput, "one weight per row is required", nil)
	}
	n := 0.0
	for i, row := range in.Rows {
		if len(row) != p {
			return nil, refuse(-1, ReasonInvalidInput, fmt.Sprintf("row %d has %d values, want %d", i, len(row), p), nil)
		}
		w := rowWeight(in.Weights, i)
		if math.IsNaN(w) || math.IsInf(w, 0) || w < 0 {
			return nil, refuse(-1, ReasonInvalidInput, fmt.Sprintf("row %d has weight %v", i, w), nil)
		}
		n += w
	}
	if !(n > 0) {
		return nil, refuse(-1, ReasonNoData, "the total weight is not positive", nil)
	}
	s := &TwoStepStats{
		N: n, Rows: len(in.Rows),
		Kinds: append([]OrdinalKind(nil), in.Kinds...), Levels: append([]int(nil), in.Levels...),
		Thresholds: make([][]float64, p), Freq: make([][]float64, p),
		Mean: make([]float64, p), Var: make([]float64, p),
	}
	for v := 0; v < p; v++ {
		switch in.Kinds[v] {
		case KindOrdered:
			k := in.Levels[v]
			if k < 1 {
				return nil, refuse(v, ReasonInvalidInput, fmt.Sprintf("an ordered column needs at least one level, has %d", k), nil)
			}
			freq := make([]float64, k)
			for i, row := range in.Rows {
				c := row[v]
				if c != math.Trunc(c) || c < 0 || c >= float64(k) {
					return nil, refuse(v, ReasonInvalidInput, fmt.Sprintf("row %d has code %v outside 0..%d", i, c, k-1), nil)
				}
				freq[int(c)] += rowWeight(in.Weights, i)
			}
			s.Freq[v] = freq
			if k == 1 {
				return nil, refuse(v, ReasonConstant, "an ordered column with a single category has no thresholds", freq)
			}
			held := 0
			for _, f := range freq {
				if f > 0 {
					held++
				}
			}
			if held < 2 {
				return nil, refuse(v, ReasonConstant, "every row falls in one category", freq)
			}
			for c, f := range freq {
				if !(f > 0) {
					return nil, refuse(v, ReasonEmptyCategory, fmt.Sprintf("category %d of %d holds no row", c, k), freq)
				}
			}
			s.Thresholds[v] = thresholdsFromFreq(freq)
			s.Var[v] = 1
		case KindNumeric:
			sum := 0.0
			for i, row := range in.Rows {
				y := row[v]
				if math.IsNaN(y) || math.IsInf(y, 0) {
					return nil, refuse(v, ReasonInvalidInput, fmt.Sprintf("row %d has value %v", i, y), nil)
				}
				sum += float64(rowWeight(in.Weights, i) * y)
			}
			mean := sum / n
			ss := 0.0
			for i, row := range in.Rows {
				d := row[v] - mean
				ss += float64(float64(rowWeight(in.Weights, i)*d) * d)
			}
			s.Mean[v], s.Var[v] = mean, ss/n
			if !(s.Var[v] > 0) {
				return nil, refuse(v, ReasonConstant, "a numeric column without variance", nil)
			}
		default:
			return nil, refuse(v, ReasonInvalidInput, fmt.Sprintf("unknown kind %d", in.Kinds[v]), nil)
		}
	}
	s.Cor = make([][]float64, p)
	for i := range s.Cor {
		s.Cor[i] = make([]float64, p)
		s.Cor[i][i] = 1
	}
	for j := 0; j < p-1; j++ {
		for i := j + 1; i < p; i++ {
			est := s.pair(in, i, j)
			s.Pairs = append(s.Pairs, est)
			s.Cor[i][j], s.Cor[j][i] = est.Rho, est.Rho
		}
	}
	return s, nil
}

func rowWeight(w []float64, i int) float64 {
	if w == nil {
		return 1
	}
	return w[i]
}

// thresholdsFromFreq is lav_uvord_th: qnorm of the cumulative
// proportions (the exact univariate ML thresholds), cumulated left to
// right as R's cumsum does.
func thresholdsFromFreq(freq []float64) []float64 {
	total := 0.0
	for _, f := range freq {
		total += f
	}
	th := make([]float64, len(freq)-1)
	cum := 0.0
	for c := range th {
		cum += freq[c] / total
		th[c] = stdNormQuantile(cum)
	}
	return th
}

// pair estimates column pair (i, j), i > j. lavaan fits the
// ordered × ordered pair with the earlier column as y1; the
// numeric × ordered pair with the numeric column as y1 wherever it
// sits.
func (s *TwoStepStats) pair(in TwoStepInput, i, j int) PairEstimate {
	ki, kj := s.Kinds[i], s.Kinds[j]
	est := PairEstimate{I: i, J: j}
	switch {
	case ki == KindNumeric && kj == KindNumeric:
		est.Kind = PairPearson
		est.Rho = s.pearson(in, i, j)
	case ki == KindOrdered && kj == KindOrdered:
		est.Kind = PairPolychoric
		s.polychoric(in, j, i, &est)
	case ki == KindNumeric:
		est.Kind = PairPolyserial
		s.polyserial(in, i, j, &est)
	default:
		est.Kind = PairPolyserial
		s.polyserial(in, j, i, &est)
	}
	return est
}

// pearson is the ML correlation of two numeric columns given their
// step-1 means and variances (lav_bvreg_cor_twostep_fit's closed form on
// complete data).
func (s *TwoStepStats) pearson(in TwoStepInput, a, b int) float64 {
	cp := 0.0
	for r, row := range in.Rows {
		cp += float64(float64(rowWeight(in.Weights, r)*(row[a]-s.Mean[a])) * (row[b] - s.Mean[b]))
	}
	return (cp / s.N) / float64(math.Sqrt(s.Var[a])*math.Sqrt(s.Var[b]))
}

// pearsonStart is cor(y1, y2) of the raw columns, lavaan's starting
// value (weighted as frequency copies); 0 when a column is constant or
// the value is not inside (-1, 1).
func pearsonStart(in TwoStepInput, a, b int, n float64) float64 {
	ma, mb := 0.0, 0.0
	for r, row := range in.Rows {
		w := rowWeight(in.Weights, r)
		ma += float64(w * row[a])
		mb += float64(w * row[b])
	}
	ma, mb = ma/n, mb/n
	var saa, sbb, sab float64
	for r, row := range in.Rows {
		w := rowWeight(in.Weights, r)
		da, db := row[a]-ma, row[b]-mb
		saa += float64(float64(w*da) * da)
		sbb += float64(float64(w*db) * db)
		sab += float64(float64(w*da) * db)
	}
	if !(saa > 0) || !(sbb > 0) {
		return 0
	}
	r := sab / float64(math.Sqrt(saa)*math.Sqrt(sbb))
	if math.IsNaN(r) || math.Abs(r) >= 1 {
		return 0
	}
	return r
}

// scoreFn evaluates a bivariate log-likelihood's first and second
// derivatives in ρ.
type scoreFn func(rho float64) (g, h float64)

// solveRho maximises a bivariate log-likelihood over [-bound, bound] by
// a safeguarded Newton search on its score g(ρ) = 0 (Newton steps kept
// inside the sign bracket, bisection otherwise), started at start. A
// score that does not change sign across the box puts the maximum on the
// edge it rises toward (bound reported). Deterministic: the iterates are
// a function of the inputs.
func solveRho(f scoreFn, start, bound float64) (rho float64, onBound bool) {
	lo, hi := -bound, bound
	glo, _ := f(lo)
	ghi, _ := f(hi)
	switch {
	case ghi >= 0 && glo >= 0:
		if ghi == 0 && glo > 0 {
			return hi, false
		}
		return hi, ghi > 0
	case glo <= 0 && ghi <= 0:
		if glo == 0 {
			return lo, false
		}
		return lo, true
	case glo < 0 && ghi > 0:
		// a minimum inside: take the better edge is undefined here —
		// lavaan's local search from start ascends to one edge; follow
		// the start's slope.
		if gs, _ := f(start); gs >= 0 {
			return hi, true
		}
		return lo, true
	}
	// glo > 0 > ghi: a root inside.
	x := start
	if !(x > lo && x < hi) {
		x = 0
	}
	for iter := 0; iter < 200; iter++ {
		g, h := f(x)
		if g == 0 {
			return x, false
		}
		if g > 0 {
			lo = x
		} else {
			hi = x
		}
		next := math.NaN()
		if h < 0 && !math.IsInf(h, 0) {
			next = x - g/h
		}
		if !(next > lo && next < hi) {
			next = lo + float64((hi-lo)/2)
		}
		if next == x || hi-lo <= float64(float64(4*0x1p-52)*math.Max(1, math.Abs(x))) {
			return next, false
		}
		if math.Abs(next-x) <= float64(0x1p-52*math.Max(math.Abs(x), 1e-3)) {
			return next, false
		}
		x = next
	}
	return x, false
}

// polychoric fits ordered columns y1 (a) and y2 (b): lavaan's
// lav_bvord_cor_twostep_fit without covariates — the weighted table, the
// 2 × 2 shortcuts and zero-cell continuity rule (zero.add c(0.5, 0),
// zero.keep.margins TRUE), then ML over ±0.999.
func (s *TwoStepStats) polychoric(in TwoStepInput, a, b int, est *PairEstimate) {
	t1, t2 := s.Thresholds[a], s.Thresholds[b]
	nr, nc := len(t1)+1, len(t2)+1
	freq := make([][]float64, nr)
	for r := range freq {
		freq[r] = make([]float64, nc)
	}
	for r, row := range in.Rows {
		freq[int(row[a])][int(row[b])] += rowWeight(in.Weights, r)
	}
	zeros := 0
	for r := range freq {
		for c := range freq[r] {
			if freq[r][c] == 0 {
				zeros++
			}
		}
	}
	est.ZeroCells = zeros > 0
	if nr == 2 && nc == 2 {
		if zeros == 2 {
			// two empty cells: perfect (anti-)association.
			est.Closed = true
			if freq[0][0] > 0 {
				est.Rho = 1
			} else {
				est.Rho = -1
			}
			return
		}
		if zeros == 1 {
			// one empty cell: add 0.5 to it and its diagonal partner,
			// take 0.5 from the other two (margins kept).
			const add = 0.5
			if freq[0][0] == 0 || freq[1][1] == 0 {
				freq[0][0] += add
				freq[1][1] += add
				freq[1][0] -= add
				freq[0][1] -= add
			} else {
				freq[0][0] -= add
				freq[1][1] -= add
				freq[1][0] += add
				freq[0][1] += add
			}
		}
		if float64(freq[0][0]*freq[1][1]) == float64(freq[0][1]*freq[1][0]) {
			est.Closed = true
			est.Rho = 0
			return
		}
		if t1[0] == 0 && t2[0] == 0 {
			est.Closed = true
			total := freq[0][0] + freq[1][0] + freq[0][1] + freq[1][1]
			est.Rho = -detmath.Cos(float64(2*math.Pi*freq[0][0]) / total)
			return
		}
	}
	tab := newPolyTable(t1, t2, freq)
	start := pearsonStart(in, a, b, s.N)
	est.Rho, est.Bound = solveRho(tab.score, start, PolychoricBound)
}

// polyTable is a polychoric pair's fixed step-1 pieces: thresholds and
// the (adjusted) weighted frequency table.
type polyTable struct {
	t1, t2 []float64
	freq   [][]float64
	// p1, p2 are Φ at each column's thresholds.
	p1, p2 []float64
}

func newPolyTable(t1, t2 []float64, freq [][]float64) *polyTable {
	pt := &polyTable{t1: t1, t2: t2, freq: freq, p1: make([]float64, len(t1)), p2: make([]float64, len(t2))}
	for i, t := range t1 {
		pt.p1[i] = stdNormCDF(t)
	}
	for j, t := range t2 {
		pt.p2[j] = stdNormCDF(t)
	}
	return pt
}

// cellProbs is lav_bvord_noexo_pi: the (nth1+1) × (nth2+1) cell
// probabilities at rho, each floored at cellFloor.
func (pt *polyTable) cellProbs(rho float64) [][]float64 {
	n1, n2 := len(pt.t1), len(pt.t2)
	pi := make([][]float64, n1+1)
	if rho == 0 {
		for r := range pi {
			pi[r] = make([]float64, n2+1)
			rp := cumDiff(pt.p1, r)
			for c := range pi[r] {
				pi[r][c] = float64(rp * cumDiff(pt.p2, c))
			}
		}
		return pi
	}
	// bi is the padded grid: row 0 / column 0 zero, the last row Φ(τ2),
	// the last column Φ(τ1) (1 at the corner), the inner grid
	// Φ₂(τ1_r, τ2_c; ρ).
	bi := make([][]float64, n1+2)
	for r := range bi {
		bi[r] = make([]float64, n2+2)
	}
	for r := 1; r <= n1; r++ {
		for c := 1; c <= n2; c++ {
			bi[r][c] = BivariateNormalCDF(pt.t1[r-1], pt.t2[c-1], rho)
		}
		bi[r][n2+1] = pt.p1[r-1]
	}
	for c := 1; c <= n2; c++ {
		bi[n1+1][c] = pt.p2[c-1]
	}
	bi[n1+1][n2+1] = 1
	for r := range pi {
		pi[r] = make([]float64, n2+1)
		for c := range pi[r] {
			v := bi[r+1][c+1] - bi[r+1][c] - bi[r][c+1] + bi[r][c]
			if v < cellFloor {
				v = cellFloor
			}
			pi[r][c] = v
		}
	}
	return pi
}

// cumDiff is a category's marginal probability from Φ at the thresholds.
func cumDiff(p []float64, c int) float64 {
	upper, lower := 1.0, 0.0
	if c < len(p) {
		upper = p[c]
	}
	if c > 0 {
		lower = p[c-1]
	}
	return upper - lower
}

// cornerSum is lav_bvord_corner_sum over x (nth1 × nth2): cell (r, c) is
// x[r][c] − x[r−1][c] − x[r][c−1] + x[r−1][c−1], an out-of-range index
// contributing 0.
func cornerSum(x [][]float64, n1, n2 int) [][]float64 {
	at := func(r, c int) float64 {
		if r < 0 || c < 0 || r >= n1 || c >= n2 {
			return 0
		}
		return x[r][c]
	}
	out := make([][]float64, n1+1)
	for r := range out {
		out[r] = make([]float64, n2+1)
		for c := range out[r] {
			out[r][c] = at(r, c) - at(r-1, c) - at(r, c-1) + at(r-1, c-1)
		}
	}
	return out
}

// derivs returns ∂π/∂ρ (phi) and ∂²π/∂ρ² (gnorm) of every cell at rho,
// lavaan's lav_bvord_noexo_phi_cache / _gnorm_cache.
func (pt *polyTable) derivs(rho float64) (phi, gnorm [][]float64) {
	n1, n2 := len(pt.t1), len(pt.t2)
	d := make([][]float64, n1)
	g := make([][]float64, n1)
	for r := range d {
		d[r] = make([]float64, n2)
		g[r] = make([]float64, n2)
		for c := range d[r] {
			d[r][c] = bvnDensity(pt.t1[r], pt.t2[c], rho)
			g[r][c] = float64(d[r][c] * bvnGuv(pt.t1[r], pt.t2[c], rho))
		}
	}
	return cornerSum(d, n1, n2), cornerSum(g, n1, n2)
}

// score is the polychoric log-likelihood's ρ-derivative and its second
// derivative (lav_bvord_grad_cache / lav_bvord_hessian_cache), cells
// visited column-major as R's sum() visits a matrix.
func (pt *polyTable) score(rho float64) (grad, hess float64) {
	pi := pt.cellProbs(rho)
	phi, gnorm := pt.derivs(rho)
	var h1, h2 float64
	for c := range pi[0] {
		for r := range pi {
			f := pt.freq[r][c]
			grad += float64(f*phi[r][c]) / pi[r][c]
			h1 += float64(f*gnorm[r][c]) / pi[r][c]
			h2 += float64(float64(f*phi[r][c])*phi[r][c]) / float64(pi[r][c]*pi[r][c])
		}
	}
	return grad, h1 - h2
}

// polyserial fits numeric column y1 (x) with ordered column y2 (o):
// lavaan's lav_bvmix_cor_twostep_fit without covariates — the ordinal's
// thresholds and the numeric's mean and ML variance fixed, ML over
// ±0.995.
func (s *TwoStepStats) polyserial(in TwoStepInput, x, o int, est *PairEstimate) {
	ps := &polyserialData{th: s.Thresholds[o], mean: s.Mean[x], sd: math.Sqrt(s.Var[x])}
	ps.z = make([]float64, len(in.Rows))
	ps.cat = make([]int, len(in.Rows))
	ps.w = in.Weights
	for r, row := range in.Rows {
		ps.z[r] = (row[x] - ps.mean) / ps.sd
		ps.cat[r] = int(row[o])
	}
	est.Rho, est.Bound = solveRho(ps.score, s.polyserialStart(in, x, o), PolyserialBound)
}

// polyserialStart is lav_bvmix_init_cache's start: cor(y1, y2) ·
// sd(y2) / Σ φ(τ), halved past |0.9|.
func (s *TwoStepStats) polyserialStart(in TwoStepInput, x, o int) float64 {
	c := pearsonStart(in, x, o, s.N)
	mo := 0.0
	for r, row := range in.Rows {
		mo += float64(rowWeight(in.Weights, r) * row[o])
	}
	mo /= s.N
	vo := 0.0
	for r, row := range in.Rows {
		d := row[o] - mo
		vo += float64(float64(rowWeight(in.Weights, r)*d) * d)
	}
	den := 0.0
	for _, t := range s.Thresholds[o] {
		den += stdNormPDF(t)
	}
	start := float64(c*math.Sqrt(vo/s.N)) / den
	switch {
	case math.IsNaN(start) || math.IsInf(start, 0):
		return 0
	case math.Abs(start) > 0.9:
		return start / 2
	}
	return start
}

// polyserialData is a polyserial pair's fixed pieces.
type polyserialData struct {
	th       []float64
	mean, sd float64
	z        []float64
	cat      []int
	w        []float64
}

// limits is a category's (upper, lower) threshold with lavaan's ±100
// sentinels at the open ends.
func ordinalLimits(th []float64, c int) (upper, lower float64) {
	upper, lower = ordinalSentinel, -ordinalSentinel
	if c < len(th) {
		upper = th[c]
	}
	if c > 0 {
		lower = th[c-1]
	}
	return upper, lower
}

// score is lav_bvmix_grad_cache / lav_bvmix_hessian_cache: the
// polyserial log-likelihood's ρ-derivative and second derivative,
// weighted, summed in row order.
func (ps *polyserialData) score(rho float64) (grad, hess float64) {
	r := math.Sqrt(1 - float64(rho*rho))
	r2 := float64(r * r)
	r3 := float64(r2 * r)
	for i, z := range ps.z {
		if stdNormPDF(z) == 0 {
			// lavaan drops a row whose likelihood underflows (lik > 0
			// fails): the numeric density at z.
			continue
		}
		up, lo := ordinalLimits(ps.th, ps.cat[i])
		ts1 := (up - float64(rho*z)) / r
		ts2 := (lo - float64(rho*z)) / r
		py := stdNormCDF(ts1) - stdNormCDF(ts2)
		if py < polyserialFloor {
			py = polyserialFloor
		}
		yz1, yz2 := stdNormPDF(ts1), stdNormPDF(ts2)
		inv := 1 / float64(py*r3)
		d1 := float64(up*rho) - z
		d2 := float64(lo*rho) - z
		dx := float64(inv * (float64(yz1*d1) - float64(yz2*d2)))
		t1 := z - float64(rho*ts1)/r
		t2 := z - float64(rho*ts2)/r
		a := float64(yz1 * (float64(d1*(float64(3*rho)/r2+float64(ts1*t1)/r)) + up + float64(float64(dx*r2)*t1)))
		b := float64(yz2 * (float64(d2*(float64(3*rho)/r2+float64(ts2*t2)/r)) + lo + float64(float64(dx*r2)*t2)))
		w := rowWeight(ps.w, i)
		grad += float64(w * dx)
		hess += float64(w * float64((a-b)*inv))
	}
	return grad, hess
}
