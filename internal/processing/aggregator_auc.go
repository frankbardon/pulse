package processing

import (
	"cmp"
	"math"
	"slices"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/aucspec"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
)

// AGG_AUC — the area under the ROC curve of a score (the slot's Field)
// against a binary outcome (params.outcome; params.positive names the
// event value, default 1). Model-workbench E2-S1 (#342).
//
//	AUC  = Σ_i Σ_j w_i·w_j·ψ(x_i, y_j) / (Σw_pos · Σw_neg)
//	ψ(x, y) = 1 if x > y, ½ if x == y, 0 otherwise
//
// over positives x_i and negatives y_j: the exact Mann–Whitney rank
// sum with ties counted as half. Unweighted every w is 1, so it is the
// share of (positive, negative) pairs the score orders correctly; on a
// weighted slot it is the weighted probability a positive outranks a
// negative. Gini = 2·AUC − 1.
//
// DeLong (unweighted only): the placement values V10_i (a positive's
// share of negatives it outranks) and V01_j (a negative's share of
// positives that outrank it) give var = S10/m + S01/n with S the sample
// variances (n − 1 denominators); se = √var, the 95% interval
// AUC ± z·se clipped to [0, 1] — pROC's ci.auc(method = "delong"). It
// is null when either class has fewer than two rows (pROC's NA). Under
// a weight se / ci_lower / ci_upper are null and se_note carries
// PULSE_AUC_DELONG_WEIGHTED: no weighted DeLong is defensible.
//
// A row contributes when the score and the outcome both answer and,
// weighted, the weight is valid and > 0. A null score is the universal
// floor's n_null (the floor keys on Field, as for every aggregator); a
// null outcome under a present score is n_null_outcome. An outcome
// value other than 0 / 1 (NaN included) fails the run
// (PROCESSING_AUC_OUTCOME_NOT_BINARY). A NaN score is a value, not a
// null: it makes every figure NaN (null on the wire). No positive or
// no negative ⇒ NaN.
//
// Buffered: the figure needs every scored row sorted, so the operator
// is neither streamable nor mergeable (ComponentsMergeability None —
// a stream sees it at terminal flush). Both classes are sorted by
// (score, weight) before any sum, so the result does not depend on
// record order: identical across DecodeWorkers / ShardWorkers.

// aucConfLevel is the DeLong interval's fixed confidence level.
const aucConfLevel = 0.95

// aucPoint is one scored row of one class.
type aucPoint struct{ s, w float64 }

type aucAggregator struct {
	score, outcome string
	positive       float64
	weight         *types.WeightSpec

	frozen map[string]any
}

func newAUCAggregator(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
	var params aucspec.Params
	paramread.Declare(paramread.Aggregator, string(agg.Type), &params)
	if len(agg.Params) > 0 {
		if err := paramread.Decode(paramread.Aggregator, string(agg.Type), agg.Params, &params); err != nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				aucspec.Name+": invalid params: "+err.Error(),
				map[string]any{"aggregation": aucspec.Name})
		}
	}
	if err := aucspec.ConfigError(agg.Field, params, schema); err != nil {
		return nil, err
	}
	a := &aucAggregator{score: agg.Field, outcome: params.Outcome, positive: params.PositiveValue()}
	if w := slotWeight(agg); w != nil {
		ew := effectiveWeight(w)
		a.weight = &ew
	}
	return a, nil
}

// Aggregate collects the scored rows of each class and resolves the
// figure. An outcome value other than 0 / 1 is the run's refusal.
func (a *aucAggregator) Aggregate(records []*Record, _ string) (float64, error) {
	var pos, neg []aucPoint
	nullOutcome := 0
	nanScore := false
	for _, r := range records {
		s, ok := r.NumericValue(a.score)
		if !ok {
			continue
		}
		o, ok := r.NumericValue(a.outcome)
		if !ok {
			nullOutcome++
			continue
		}
		if o != 0 && o != 1 {
			return math.NaN(), aucspec.NotBinary(a.outcome, o)
		}
		w := 1.0
		if a.weight != nil {
			vw, ok := validRowWeight(r, a.weight)
			if !ok {
				continue
			}
			w = vw
		}
		if math.IsNaN(s) {
			nanScore = true
		}
		if o == a.positive {
			pos = append(pos, aucPoint{s, w})
		} else {
			neg = append(neg, aucPoint{s, w})
		}
	}
	res := computeAUC(pos, neg, a.weight == nil, nanScore)
	a.frozen = res.components(len(pos), len(neg), nullOutcome, a.weight != nil)
	return res.auc, nil
}

// aucResult is one resolved AUC with its DeLong figures.
type aucResult struct {
	auc, se, lo, hi float64
}

func (r aucResult) components(nPos, nNeg, nullOutcome int, weighted bool) map[string]any {
	out := map[string]any{
		"auc":            r.auc,
		"gini":           2*r.auc - 1,
		"n_pos":          nPos,
		"n_neg":          nNeg,
		"n_null_outcome": nullOutcome,
		"se":             r.se,
		"ci_lower":       r.lo,
		"ci_upper":       r.hi,
		"conf_level":     aucConfLevel,
	}
	if weighted {
		out["se_note"] = string(errors.PULSE_AUC_DELONG_WEIGHTED)
	}
	return out
}

// cmpAUCPoint orders by score, then weight: a total order on the
// non-NaN points, so the sums below never depend on record order.
func cmpAUCPoint(x, y aucPoint) int {
	if c := cmp.Compare(x.s, y.s); c != 0 {
		return c
	}
	return cmp.Compare(x.w, y.w)
}

// placements returns, for each point of a (sorted), the weight of b
// (sorted) strictly below its score and the weight of b at its score.
func placements(a, b []aucPoint) (below, equal []float64) {
	below = make([]float64, len(a))
	equal = make([]float64, len(a))
	j := 0
	var wBelow float64
	for i, p := range a {
		for j < len(b) && b[j].s < p.s {
			wBelow += b[j].w
			j++
		}
		var wEq float64
		for k := j; k < len(b) && b[k].s == p.s; k++ {
			wEq += b[k].w
		}
		below[i], equal[i] = wBelow, wEq
	}
	return below, equal
}

// computeAUC resolves the AUC (and, unweighted, its DeLong se and
// interval) from the two classes. delong is false on a weighted slot.
func computeAUC(pos, neg []aucPoint, delong, nanScore bool) aucResult {
	nan := math.NaN()
	res := aucResult{auc: nan, se: nan, lo: nan, hi: nan}
	if len(pos) == 0 || len(neg) == 0 || nanScore {
		return res
	}
	slices.SortFunc(pos, cmpAUCPoint)
	slices.SortFunc(neg, cmpAUCPoint)
	var wPos, wNeg float64
	for _, p := range pos {
		wPos += p.w
	}
	for _, q := range neg {
		wNeg += q.w
	}
	below, equal := placements(pos, neg)
	var num float64
	for i, p := range pos {
		// Explicit product barriers: no FMA contraction on any arch.
		num += float64(p.w * (below[i] + float64(0.5*equal[i])))
	}
	res.auc = num / float64(wPos*wNeg)
	if !delong || len(pos) < 2 || len(neg) < 2 {
		return res
	}
	m, n := float64(len(pos)), float64(len(neg))
	theta := res.auc
	var s10 float64
	for i := range pos {
		v := (below[i]+float64(0.5*equal[i]))/n - theta
		s10 += float64(v * v)
	}
	s10 /= m - 1
	// V01_j: the share of positives above a negative, plus half the ties.
	nb, ne := placements(neg, pos)
	var s01 float64
	for j := range neg {
		v := (m-nb[j]-float64(0.5*ne[j]))/m - theta
		s01 += float64(v * v)
	}
	s01 /= n - 1
	res.se = math.Sqrt(s10/m + s01/n)
	z := statdist.NormalQuantile(1 - (1-aucConfLevel)/2)
	res.lo = math.Max(0, theta-float64(z*res.se))
	res.hi = math.Min(1, theta+float64(z*res.se))
	return res
}

// Components returns the frozen figures: auc, gini, n_pos, n_neg,
// n_null_outcome, se, ci_lower, ci_upper, conf_level and, weighted,
// se_note. Before any Aggregate it reports the empty state.
func (a *aucAggregator) Components() (map[string]any, error) {
	if a.frozen == nil {
		nan := math.NaN()
		return aucResult{auc: nan, se: nan, lo: nan, hi: nan}.components(0, 0, 0, a.weight != nil), nil
	}
	return a.frozen, nil
}

// Compile-time interface lock.
var _ MetaAggregator = (*aucAggregator)(nil)
