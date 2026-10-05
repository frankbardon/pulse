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
//
// Frequency-weighted (ClassFrequencyOnly), each row stands for w
// identical rows: weighted mid-ranks, R_i = Σ w·rank, n_i and N read
// Σw and the tie correction the expanded tie sizes — exactly the
// unweighted test on the expanded rows. Details keep the raw `n` /
// `n_total` and add `sum_weights` shaped like `n`.
type kruskalWallisRow struct {
	spec    *types.Test
	schema  *encoding.Schema
	field   string
	splitBy string
	alpha   float64

	values map[string]*rankSample
	order  []string

	testWeight
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
		if f := schema.Field(spec.Field); f != nil && (f.Type.IsCategorical()) {
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
		spec:       spec,
		schema:     schema,
		field:      spec.Field,
		splitBy:    spec.SplitBy,
		alpha:      alpha,
		values:     make(map[string]*rankSample),
		testWeight: newTestWeight(spec),
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
	w, ok := k.rowWeight(record)
	if !ok {
		return nil
	}
	s, exists := k.values[key]
	if !exists {
		s = &rankSample{}
		k.values[key] = s
		k.order = append(k.order, key)
	}
	s.add(v, w)
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
	samples := make([]*rankSample, len(keys))
	for i, key := range keys {
		samples[i] = k.values[key]
	}
	combined, spans := concatRankSamples(samples)
	N := combined.n()
	if N < len(keys)*2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_KRUSKAL_WALLIS requires ≥ 2 observations per group, got total %d across %d groups", N, len(keys)),
			map[string]any{"n_total": N, "groups": len(keys), "min_per_group": 2})
	}
	ranks, ties := weightedMidRanks(combined.values, combined.weights)
	rankSums := make([]float64, len(keys))
	ns := make([]int, len(keys))
	sumWs := make([]float64, len(keys))
	sumWSqs := make([]float64, len(keys))
	var h float64
	for i, span := range spans {
		ns[i] = span[1] - span[0]
		sumWs[i], sumWSqs[i] = samples[i].sumW, samples[i].sumWSq
		var sum float64
		for j := span[0]; j < span[1]; j++ {
			sum += combined.weights[j] * ranks[j]
		}
		rankSums[i] = sum
		// n_i is Σw_i (the group's row count unweighted).
		h += sum * sum / sumWs[i]
	}
	Nf := combined.sumW
	h = 12/(Nf*(Nf+1))*h - 3*(Nf+1)
	// Tie correction.
	denom := 1 - tieCorrectionW(ties)/(Nf*Nf*Nf-Nf)
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
	// ε² uses the tie-corrected H; when every value is tied the
	// correction is undefined (denom ≤ 0) and the key is omitted.
	if denom > 0 {
		setEffectSize(res.Details, "epsilon_squared", epsilonSquared(h, Nf))
	}
	k.noteGroupSums(res.Details, sumWs, sumWSqs)
	if tiesDominateW(ties, Nf) {
		res.Warnings = append(res.Warnings, string(errors.PULSE_TEST_TIES_DOMINATE)+
			": ≥ 50% of values are tied; asymptotic p-value is unreliable")
	}
	return res, nil
}

func (k *kruskalWallisRow) reset() {
	k.values = make(map[string]*rankSample)
	k.order = nil
}
