package synth

import (
	"math"
	"sort"
	"testing"
)

// TestCollapseCategoricalNumeric_FoldsInSortedKeyOrder is the direct
// ordering assertion behind E2-S4. The top-K collapse inside
// computeConditionalCategoricalNumericPairs folds every out-of-top-K
// category into ONE shared otherCategoryLabel accumulator, so its
// sum/sumSq are a pairwise float64 sum of many addends. Go randomizes
// map iteration order and float addition is not associative, so a fold
// that walks the raw map directly produces a different sumSq per
// process — which the cancellation-prone (sumSq - mean*sum) variance
// below amplifies into a visible drift in the emitted Std.
//
// A tolerance-based assertion would let exactly that bug back in, so
// this test asserts BIT-equality against the sorted-key fold computed
// independently here: not "close enough", but "this specific
// deterministic answer". The fixture is deliberately built so ordering
// cannot fail to matter — one accumulator carries sumSq 1e16 while
// forty carry 1.0, and 1.0 is below half an ulp at 1e16, so folding
// the small addends into the large one first LOSES them entirely while
// folding them together first does not. Pre-fix, this test fails on
// essentially every run rather than flakily.
func TestCollapseCategoricalNumeric_FoldsInSortedKeyOrder(t *testing.T) {
	const numField = "score"
	const catField = "region"

	raw := map[string]*condCatNumAcc{
		"z_big": {count: 1, sum: 1e8, sumSq: 1e16},
	}
	for i := 0; i < 40; i++ {
		raw[smallKey(i)] = &condCatNumAcc{count: 1, sum: 1.0, sumSq: 1.0}
	}

	accs := map[string]map[string]map[string]*condCatNumAcc{
		catField: {numField: raw},
	}
	// Empty allowed set: every raw key is out-of-top-K, so every one of
	// them folds into the single shared "other" accumulator. That is
	// the multi-source bucket the bug lives in; an in-top-K key has
	// exactly one source and was never affected.
	topK := map[string]map[string]bool{catField: {}}

	var warnings []string
	pairs := computeConditionalCategoricalNumericPairs(
		[]string{catField}, []string{numField}, accs, topK, &warnings)
	if len(pairs) != 1 {
		t.Fatalf("pairs = %d, want 1", len(pairs))
	}
	if len(pairs[0].Categories) != 1 {
		t.Fatalf("categories = %d, want 1 (all raw values collapse to %q)", len(pairs[0].Categories), otherCategoryLabel)
	}
	got := pairs[0].Categories[0]
	if got.Category != otherCategoryLabel {
		t.Fatalf("category = %q, want %q", got.Category, otherCategoryLabel)
	}

	wantMean, wantStd, wantN := sortedFoldMoments(raw)
	if got.N != wantN {
		t.Errorf("N = %d, want %d", got.N, wantN)
	}
	if math.Float64bits(got.Mean) != math.Float64bits(wantMean) {
		t.Errorf("Mean = %v (bits %#x), want the sorted-key fold %v (bits %#x)",
			got.Mean, math.Float64bits(got.Mean), wantMean, math.Float64bits(wantMean))
	}
	if math.Float64bits(got.Std) != math.Float64bits(wantStd) {
		t.Errorf("Std = %v (bits %#x), want the sorted-key fold %v (bits %#x)",
			got.Std, math.Float64bits(got.Std), wantStd, math.Float64bits(wantStd))
	}
}

// TestCollapseCategoricalNumeric_RepeatedFoldsAreBitIdentical is the
// belt to the sorted-fold assertion's braces: it re-runs the same
// collapse many times inside one process, so the map-iteration
// randomization Go applies per range statement is exercised repeatedly
// rather than sampled once. A single-run comparison is flaky-NEGATIVE
// on the buggy tree (one lucky iteration order passes); 500 runs over
// a 41-key map are not.
func TestCollapseCategoricalNumeric_RepeatedFoldsAreBitIdentical(t *testing.T) {
	const numField = "score"
	const catField = "region"

	build := func() map[string]map[string]map[string]*condCatNumAcc {
		// Magnitudes mirror the cohort the bug was found on: values
		// around 12,690, so sumSq lands near 1e11 against a variance
		// near 1e8 and the subtraction cancels ~3 significant digits.
		raw := make(map[string]*condCatNumAcc, 64)
		for i := 0; i < 64; i++ {
			v := 12690.0 + float64(float64(i)*0.37)
			raw[smallKey(i)] = &condCatNumAcc{count: 3, sum: 3 * v, sumSq: 3 * v * v}
		}
		return map[string]map[string]map[string]*condCatNumAcc{catField: {numField: raw}}
	}
	topK := map[string]map[string]bool{catField: {}}

	var wantMean, wantStd uint64
	for run := 0; run < 500; run++ {
		var warnings []string
		pairs := computeConditionalCategoricalNumericPairs(
			[]string{catField}, []string{numField}, build(), topK, &warnings)
		if len(pairs) != 1 || len(pairs[0].Categories) != 1 {
			t.Fatalf("run %d: unexpected shape %+v", run, pairs)
		}
		c := pairs[0].Categories[0]
		mb, sb := math.Float64bits(c.Mean), math.Float64bits(c.Std)
		if run == 0 {
			wantMean, wantStd = mb, sb
			continue
		}
		if mb != wantMean || sb != wantStd {
			t.Fatalf("run %d drifted: mean bits %#x want %#x, std bits %#x want %#x (mean %v, std %v)",
				run, mb, wantMean, sb, wantStd, c.Mean, c.Std)
		}
	}
}

// smallKey returns a fixed-width key so lexical order and insertion
// index agree, keeping the expected fold order legible.
func smallKey(i int) string {
	const digits = "0123456789"
	return "a" + string([]byte{digits[i/10], digits[i%10]})
}

// sortedFoldMoments recomputes the collapse's expected output the way
// the fix must: fold every accumulator into one bucket in sorted key
// order, then apply the same mean/variance formulas the production
// path uses. Deliberately duplicates those formulas rather than calling
// into them — the point is to pin the ORDER, and a shared helper would
// pin nothing.
//
// The duplication must be FAITHFUL, which includes the float64() fusion
// barrier on the product (see synth/moments.go). The comparison above is
// bit-exact, so a fused reference against a barriered production would
// fail on arm64 for a reason that has nothing to do with fold order.
func sortedFoldMoments(raw map[string]*condCatNumAcc) (mean, std float64, n int) {
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sum, sumSq float64
	for _, k := range keys {
		acc := raw[k]
		n += acc.count
		sum += acc.sum
		sumSq += acc.sumSq
	}
	mean = sum / float64(n)
	var variance float64
	if n > 1 {
		variance = (sumSq - float64(mean*sum)) / float64(n-1)
	}
	if variance < 0 {
		variance = 0
	}
	return mean, math.Sqrt(variance), n
}
