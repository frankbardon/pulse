package processing

import (
	"fmt"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// kruskalWallisRow implements TEST_KRUSKAL_WALLIS as a buffered row test:
// nonparametric k-group alternative to TEST_ANOVA_F.
//
// Algorithm:
//  1. Buffer values per group.
//  2. Mid-rank the combined value set (ties → average rank).
//  3. R_i = Σ ranks in group i, n_i = |group i|.
//  4. H = (12/(N(N+1))) · Σ (R_i² / n_i) − 3(N+1).
//  5. Tie-correct: H_c = H / (1 − Σ(t³−t) / (N³−N)).
//  6. p = chiSquareSurvival(H_c, k−1).
type kruskalWallisRow struct {
	spec    *types.Test
	schema  *encoding.Schema
	field   string
	splitBy string
	alpha   float64

	values map[string][]float64
	order  []string
}

func newKruskalWallisRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_KRUSKAL_WALLIS requires field")
	}
	if spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_KRUSKAL_WALLIS requires split_by")
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
		if f := schema.Field(spec.Field); f != nil && (f.Type.IsCategorical() || f.Type.IsGeo()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
				fmt.Sprintf("TEST_KRUSKAL_WALLIS field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.SplitBy); f != nil && !f.Type.IsCategorical() {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("TEST_KRUSKAL_WALLIS split_by %q must be categorical, got %s", spec.SplitBy, f.Type.String()),
				map[string]any{"split_by": spec.SplitBy, "field_type": f.Type.String()})
		}
	}
	return &kruskalWallisRow{
		spec:    spec,
		schema:  schema,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		alpha:   alpha,
		values:  make(map[string][]float64),
	}, nil
}

func (k *kruskalWallisRow) UpdateRow(record *Record) error {
	v, ok := record.NumericValue(k.field)
	if !ok {
		return nil
	}
	key, ok := record.StringValue(k.splitBy)
	if !ok {
		return nil
	}
	if _, exists := k.values[key]; !exists {
		k.order = append(k.order, key)
	}
	k.values[key] = append(k.values[key], v)
	return nil
}

func (k *kruskalWallisRow) Finalize() (*types.TestResult, error) {
	defer k.reset()
	keys := append([]string(nil), k.order...)
	sort.Strings(keys)
	if len(keys) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_KRUSKAL_WALLIS requires k ≥ 2 groups, got %d", len(keys)),
			map[string]any{"groups": keys, "min_required": 2})
	}
	// Concatenate and assign mid-ranks across the full set, then sum per
	// group by walking the originating bucket offsets.
	type bucket struct {
		start, end int
	}
	buckets := make([]bucket, len(keys))
	var combined []float64
	for i, key := range keys {
		buckets[i].start = len(combined)
		combined = append(combined, k.values[key]...)
		buckets[i].end = len(combined)
	}
	N := len(combined)
	if N < len(keys)*2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_KRUSKAL_WALLIS requires ≥ 2 observations per group, got total %d across %d groups", N, len(keys)),
			map[string]any{"n_total": N, "groups": len(keys), "min_per_group": 2})
	}
	ranks, ties := midRanks(combined)
	rankSums := make([]float64, len(keys))
	ns := make([]int, len(keys))
	var h float64
	for i, b := range buckets {
		ns[i] = b.end - b.start
		var sum float64
		for j := b.start; j < b.end; j++ {
			sum += ranks[j]
		}
		rankSums[i] = sum
		h += sum * sum / float64(ns[i])
	}
	Nf := float64(N)
	h = 12/(Nf*(Nf+1))*h - 3*(Nf+1)
	// Tie correction.
	denom := 1 - tieCorrection(ties)/(Nf*Nf*Nf-Nf)
	if denom > 0 {
		h /= denom
	}
	df := float64(len(keys) - 1)
	p := chiSquareSurvival(h, df)
	res := &types.TestResult{
		Label:      testLabel(k.spec),
		Type:       types.TEST_KRUSKAL_WALLIS,
		Variant:    "asymptotic",
		Statistic:  h,
		DF:         df,
		PValue:     p,
		Alpha:      k.alpha,
		RejectNull: p < k.alpha,
		Details: map[string]any{
			"groups":     keys,
			"n":          ns,
			"rank_sums":  rankSums,
			"n_total":    N,
			"tie_factor": denom,
		},
	}
	if tiesDominate(ties, N) {
		res.Warnings = append(res.Warnings, string(errors.PULSE_TEST_TIES_DOMINATE)+
			": ≥ 50% of values are tied; asymptotic p-value is unreliable")
	}
	return res, nil
}

func (k *kruskalWallisRow) reset() {
	k.values = make(map[string][]float64)
	k.order = nil
}
