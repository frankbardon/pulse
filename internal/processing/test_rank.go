package processing

import "sort"

// Rank machinery shared by the nonparametric TEST_* operators
// (Mann-Whitney U, Wilcoxon signed-rank, Kruskal-Wallis, Spearman ρ,
// Kendall τ). All rankings are mid-rank: ties receive the average of
// the positions they would occupy if separated.

// midRanks returns one rank (1-based) per input value with average
// ranks assigned to runs of equal values. The second return is the
// list of tie-group sizes — used by the tie-correction terms in the
// nonparametric variance formulas.
//
// Stable: input order is preserved in the output (ranks[i] is the
// rank of values[i]). It is weightedMidRanks at unit weights.
func midRanks(values []float64) ([]float64, []int) {
	ranks, wties := weightedMidRanks(values, nil)
	var ties []int
	for _, t := range wties {
		ties = append(ties, int(t))
	}
	return ranks, ties
}

// weightedMidRanks is the frequency-weighted mid-rank: values[i]
// stands for weights[i] identical rows (nil weights: every row weighs
// 1). A run of equal values with total weight W, preceded by
// cumulative weight C in sorted order, takes the rank C + (W+1)/2 —
// the mid-rank the run's rows would share on the physically expanded
// sample, so every rank statistic built on it equals the unweighted
// statistic on the expansion. The second return lists the tie-group
// WEIGHTS (W of each run with W > 1, in sorted order): the tie sizes of
// the expansion, where a single row of weight w is itself a tie of
// size w.
//
// Unit weights reproduce the unweighted ranks bit for bit: C and W are
// then small integers and C + (W+1)/2 is the exact half-integer
// ((i+1)+(j+1))/2. Callers pass only positive weights.
func weightedMidRanks(values, weights []float64) ([]float64, []float64) {
	n := len(values)
	if n == 0 {
		return nil, nil
	}
	weight := func(i int) float64 {
		if weights == nil {
			return 1
		}
		return weights[i]
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		return values[idx[i]] < values[idx[j]]
	})
	ranks := make([]float64, n)
	var ties []float64
	var cum float64
	i := 0
	for i < n {
		j := i
		w := weight(idx[i])
		for j+1 < n && values[idx[j+1]] == values[idx[i]] {
			j++
			w += weight(idx[j])
		}
		r := cum + (w+1)/2
		for k := i; k <= j; k++ {
			ranks[idx[k]] = r
		}
		if w > 1 {
			ties = append(ties, w)
		}
		cum += w
		i = j + 1
	}
	return ranks, ties
}

// tieCorrection returns Σ (t³ − t) over tie-group sizes. Used as the
// numerator correction in Mann-Whitney / Kruskal-Wallis variance
// formulas under ties.
func tieCorrection(ties []int) float64 {
	return tieCorrectionW(intsToFloats(ties))
}

// tieCorrectionW is tieCorrection over weighted tie-group sizes (the
// second return of weightedMidRanks).
func tieCorrectionW(ties []float64) float64 {
	sum := 0.0
	for _, t := range ties {
		sum += t*t*t - t
	}
	return sum
}

// tiesDominate reports whether tied observations make up at least half
// of n. Triggers PULSE_TEST_TIES_DOMINATE warnings.
func tiesDominate(ties []int, n int) bool {
	return tiesDominateW(intsToFloats(ties), float64(n))
}

// tiesDominateW is tiesDominate over weighted tie-group sizes and a
// weighted total n (Σw).
func tiesDominateW(ties []float64, n float64) bool {
	if n == 0 {
		return false
	}
	count := 0.0
	for _, t := range ties {
		count += t
	}
	return count*2 >= n
}

func intsToFloats(xs []int) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = float64(x)
	}
	return out
}

// rankSample buffers one sample for a buffered rank test: each value
// with the weight it carries (exactly 1 unweighted — testWeight.
// rowWeight) plus the running Σw and Σw². A frequency weight w makes a
// row stand for w identical rows (weightedMidRanks).
type rankSample struct {
	values, weights []float64
	sumW, sumWSq    float64
}

func (s *rankSample) add(v, w float64) {
	s.values = append(s.values, v)
	s.weights = append(s.weights, w)
	s.sumW += w
	s.sumWSq += w * w
}

// n is the raw row count (the `n` a test reports).
func (s *rankSample) n() int { return len(s.values) }

// concatRankSamples joins samples in order into one rankSample (sums
// folded in the same order) and returns the [start, end) offsets of
// each, for ranking a combined set and summing ranks back per sample.
func concatRankSamples(samples []*rankSample) (*rankSample, [][2]int) {
	all := &rankSample{}
	spans := make([][2]int, len(samples))
	for i, s := range samples {
		spans[i][0] = len(all.values)
		for j, v := range s.values {
			all.add(v, s.weights[j])
		}
		spans[i][1] = len(all.values)
	}
	return all, spans
}

// tieSizes renders weighted tie-group sizes for a test's details: the
// int sizes unweighted (the pre-weighting Go type), Σw sizes weighted.
// Both marshal identically at unit weights.
func tieSizes(ties []float64, weighted bool) any {
	if weighted {
		return ties
	}
	if ties == nil {
		return []int(nil)
	}
	out := make([]int, len(ties))
	for i, t := range ties {
		out[i] = int(t)
	}
	return out
}
