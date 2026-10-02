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
// rank of values[i]).
func midRanks(values []float64) ([]float64, []int) {
	n := len(values)
	if n == 0 {
		return nil, nil
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		return values[idx[i]] < values[idx[j]]
	})
	ranks := make([]float64, n)
	var ties []int
	i := 0
	for i < n {
		j := i
		for j+1 < n && values[idx[j+1]] == values[idx[i]] {
			j++
		}
		// Positions are 1-based: i+1 .. j+1. Their average:
		avg := float64((i+1)+(j+1)) / 2.0
		for k := i; k <= j; k++ {
			ranks[idx[k]] = avg
		}
		if j > i {
			ties = append(ties, j-i+1)
		}
		i = j + 1
	}
	return ranks, ties
}

// tieCorrection returns Σ (t³ − t) over tie-group sizes. Used as the
// numerator correction in Mann-Whitney / Kruskal-Wallis variance
// formulas under ties.
func tieCorrection(ties []int) float64 {
	sum := 0.0
	for _, t := range ties {
		tf := float64(t)
		sum += tf*tf*tf - tf
	}
	return sum
}

// tiesDominate reports whether tied observations make up at least half
// of n. Triggers PULSE_TEST_TIES_DOMINATE warnings.
func tiesDominate(ties []int, n int) bool {
	if n == 0 {
		return false
	}
	count := 0
	for _, t := range ties {
		count += t
	}
	return count*2 >= n
}
