package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// brownForsytheRow implements TEST_BROWN_FORSYTHE as a buffered row test:
// homogeneity-of-variance check by running one-way ANOVA on the absolute
// deviation from each group's median.
//
// Algorithm:
//  1. Buffer values per group.
//  2. For each group, compute median, then z_ij = |x_ij − median|.
//  3. Run standard one-way ANOVA on z values across groups.
//
// p-value via the existing fSurvival on (k−1, N−k) degrees of freedom.
// Median-based residuals make the test robust against non-normality —
// the conventional preferred variant over Levene's mean-based residuals.
//
// Frequency-weighted (ClassFrequencyOnly), each row stands for w
// identical rows: each group's median is the weighted median under the
// frequency rule shared with AGG_MEDIAN (Hmisc wtd.quantile, normwt =
// FALSE — the median of the expanded rows), each |x − median| carries
// its row's weight into a weighted Welford bucket, and the ANOVA reads
// N* = Σw (summariseWeightedANOVA) — exactly the unweighted test on the
// expanded rows. Details keep the raw `n` and add `sum_weights` shaped
// like it.
type brownForsytheRow struct {
	spec    *types.Test
	schema  *encoding.Schema
	field   string
	splitBy string
	alpha   float64

	values map[string]*rankSample
	order  []string

	testWeight
}

func newBrownForsytheRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_BROWN_FORSYTHE requires field")
	}
	if spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_BROWN_FORSYTHE requires split_by")
	}
	alpha := spec.Alpha
	if alpha == 0 {
		alpha = 0.05
	}
	if alpha <= 0 || alpha >= 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INVALID_ALPHA,
			fmt.Sprintf("alpha %g not in (0, 1)", spec.Alpha),
			map[string]any{"alpha": spec.Alpha})
	}
	if schema != nil {
		if f := schema.Field(spec.Field); f != nil && (f.Type.IsCategorical()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
				fmt.Sprintf("TEST_BROWN_FORSYTHE field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.SplitBy); f != nil && !f.Type.IsCategorical() {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("TEST_BROWN_FORSYTHE split_by %q must be categorical, got %s", spec.SplitBy, f.Type.String()),
				map[string]any{"split_by": spec.SplitBy, "field_type": f.Type.String()})
		}
	}
	return &brownForsytheRow{
		spec:    spec,
		schema:  schema,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		alpha:   alpha,
		values:  make(map[string]*rankSample),

		testWeight: newTestWeight(spec),
	}, nil
}

func (b *brownForsytheRow) UpdateRow(record *Record) error {
	v, ok := record.NumericValue(b.field)
	if !ok {
		return nil
	}
	key, ok := record.StringValue(b.splitBy)
	if !ok {
		return nil
	}
	w, ok := b.rowWeight(record)
	if !ok {
		return nil
	}
	s, exists := b.values[key]
	if !exists {
		s = &rankSample{}
		b.values[key] = s
		b.order = append(b.order, key)
	}
	s.add(v, w)
	return nil
}

func (b *brownForsytheRow) Finalize() (*types.TestResult, error) {
	defer b.reset()
	keys := append([]string(nil), b.order...)
	sort.Strings(keys)
	k := len(keys)
	if k < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_BROWN_FORSYTHE requires ≥ 2 groups, got %d", k),
			map[string]any{"groups": keys, "min_required": 2})
	}
	// Compute median per group, then |dev| → folded into Welford buckets
	// with each row's weight (1 unweighted).
	medians := make([]float64, k)
	devBuckets := make(map[string]*weighting.Welford, k)
	devOrder := make([]string, 0, k)
	buckets := make([]*weighting.Welford, 0, k)
	for i, key := range keys {
		s := b.values[key]
		if s.n() < 2 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
				fmt.Sprintf("TEST_BROWN_FORSYTHE requires n ≥ 2 per group; %q has %d", key, s.n()),
				map[string]any{"group": key, "n": s.n(), "min_required": 2})
		}
		vs, ws := sortedWeighted(s)
		medians[i] = weightedMedianSorted(vs, ws)
		bk := &weighting.Welford{}
		for j, v := range vs {
			bk.Add(math.Abs(v-medians[i]), ws[j])
		}
		devBuckets[key] = bk
		devOrder = append(devOrder, key)
		buckets = append(buckets, bk)
	}
	stats := summariseWeightedANOVA(devOrder, devBuckets, b.basis)
	dfB := float64(k - 1)
	// N* − k: N − k unweighted, Σw − k under frequency.
	dfW := stats.NStar - float64(k)
	if dfW <= 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			"TEST_BROWN_FORSYTHE: zero within-group degrees of freedom",
			map[string]any{"k": k, "n": stats.N})
	}
	msB := stats.SSB / dfB
	msW := stats.SSW / dfW
	var F, p float64
	if msW == 0 {
		F = 0
		p = 0
	} else {
		F = msB / msW
		p = fSurvival(F, dfB, dfW)
	}
	res := &types.TestResult{
		Label:      testLabel(b.spec),
		Type:       types.TEST_BROWN_FORSYTHE,
		Variant:    "median",
		Statistic:  F,
		DF:         dfB,
		PValue:     p,
		Alpha:      b.alpha,
		RejectNull: p < b.alpha,
		Details: map[string]any{
			"groups":        keys,
			"n":             stats.Ns,
			"group_medians": medians,
			"abs_dev_means": stats.Means,
			"ss_between":    stats.SSB,
			"ss_within":     stats.SSW,
			"df_between":    dfB,
			"df_within":     dfW,
		},
	}
	b.noteGroups(res.Details, buckets)
	return res, nil
}

func (b *brownForsytheRow) reset() {
	b.values = make(map[string]*rankSample)
	b.order = nil
}

// weightedMedianSorted is the median of sorted values carrying row
// weights ws (nil: every row weighs 1) under the frequency rule of the
// weighted AGG_MEDIAN (weightedQuantileAggregator.median): W = Σw,
// rank h = 0.5·(W − 1), the 0-based order statistic x₍ₖ₎ the first
// value whose cumulative weight reaches k + 1 — with integer weights
// the median of the expanded rows. Unit weights give median(sorted)
// bit for bit (the half case is the same mean of the two middles).
func weightedMedianSorted(sorted, ws []float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if ws == nil {
		return median(sorted)
	}
	cum := make([]float64, len(sorted))
	total := 0.0
	for i, w := range ws {
		total += w
		cum[i] = total
	}
	snapCumWeights(cum)
	at := func(k int) float64 { return sorted[quantileOrderIndex(cum, k)] }
	rank := 0.5 * (total - 1)
	low := int(math.Floor(rank))
	high := int(math.Ceil(rank))
	lo, hi := at(low), at(high)
	switch {
	case low == high:
		return lo
	case rank-float64(low) == 0.5:
		return 0.5 * (lo + hi)
	default:
		return lo + (rank-float64(low))*(hi-lo)
	}
}

// median returns the median of a *sorted* slice. Caller must sort.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return 0.5 * (sorted[n/2-1] + sorted[n/2])
}
