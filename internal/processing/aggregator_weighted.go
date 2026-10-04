package processing

import (
	"math"

	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Weighted forms of the core aggregators — AGG_COUNT, AGG_SUM,
// AGG_AVERAGE (and its alias AGG_WEIGHTED_MEAN), AGG_VARIANCE,
// AGG_STDDEV, AGG_WELFORD. A factory returns one of these instead of
// the unweighted type when the slot carries an applied weight
// (slotWeight: the stamped per-slot weight, see StampWeights); with no
// weight the unweighted type is returned untouched, so an unweighted
// request runs exactly the code it ran before weighting existed.
//
// Operation-order rule (.claude/reference/weighting.md): every formula
// here reduces to the unweighted type's operation sequence when w = 1,
// so an all-ones weight column reproduces the unweighted figure bit for
// bit on every path:
//
//	streaming / Welford:  Σw += w; δ = x − mean; mean += (w·δ)/Σw;
//	                      m2 += w·δ·(x − mean)
//	                      (unweighted: n++; mean += δ/n; m2 += δ·δ₂)
//	buffered two-pass:    mean = Σwx/Σw; m2 = Σ w·d·d  (d = x − mean)
//	merge (Chan):         mean = a + δ·w_b/W; m2 = m2_a + m2_b + δ·δ·w_a·w_b/W
//
// Σw over a column of ones is an exact integer in float64, so every
// division by Σw is the division by n the unweighted type performs.
//
// Row handling: a row whose value is absent contributes nothing (as
// unweighted). A row whose weight is invalid (weighting.Classify) is
// excluded — never coerced — and counted by the orchestrator's floor
// (WeightFloor) and response tally (WeightRowTally), not here. A zero
// weight is valid and contributes nothing: the row is skipped so a
// leading zero cannot divide by a zero Σw.

// slotWeight returns the weight applied to an aggregation slot, or nil
// when the slot is unweighted. After StampWeights the slot's own
// `weight` IS the resolved weight (set = applied, null = none).
func slotWeight(agg *types.Aggregation) *types.WeightSpec {
	if agg == nil {
		return nil
	}
	return agg.Weight.Spec()
}

// weightedState is the weighted accumulator's sufficient statistics.
type weightedState struct {
	rows   int64   // rows that contributed with w > 0
	sumW   float64 // Σw
	sumWX  float64 // Σw·x (unused by AGG_COUNT)
	sumWSq float64 // Σw²
	mean   float64 // running (streaming) or two-pass (buffered variance) mean
	m2     float64 // Σw(x − mean)²
}

// weightedAggregator is the shared weighted accumulator. op selects
// the scalar and the components shape. The live state folds rows and
// merges; Finalize freezes it into frozen and resets the live state
// (the unweighted types' Finalize-reset contract), so Components / Rich
// read the frozen copy and a reused instance starts clean.
type weightedAggregator struct {
	op     types.AggregationType
	weight types.WeightSpec

	weightedState
	frozen weightedState
}

func newWeightedAggregator(op types.AggregationType, spec *types.WeightSpec) *weightedAggregator {
	w := *spec
	w.Kind = spec.EffectiveKind()
	return &weightedAggregator{op: op, weight: w}
}

// weightedMeanAggregator is AGG_WEIGHTED_MEAN's name for the shared
// accumulator — the operator is an alias of weighted AGG_AVERAGE that
// keeps its own type name and components shape.
type weightedMeanAggregator = weightedAggregator

// rowWeight returns the row's weight and whether it contributes: a
// valid weight greater than zero.
func (a *weightedAggregator) rowWeight(r *Record) (float64, bool) {
	w, ok := r.NumericValue(a.weight.Field)
	if weighting.Classify(w, ok, a.weight.Kind) != weighting.Valid || w == 0 {
		return 0, false
	}
	return w, true
}

func (a *weightedAggregator) reset() {
	a.weightedState = weightedState{}
}

// fold is the streaming recurrence over one contributing (x, w) pair.
func (a *weightedAggregator) fold(x, w float64) {
	a.rows++
	a.sumW += w
	a.sumWX += w * x
	a.sumWSq += w * w
	delta := x - a.mean
	a.mean += (w * delta) / a.sumW
	a.m2 += w * delta * (x - a.mean)
}

// UpdateRow folds one record. AGG_COUNT asks for presence (any field
// type, sets included); every other weighted operator for a numeric
// value.
func (a *weightedAggregator) UpdateRow(r *Record, field string) error {
	if a.op == types.AGG_COUNT {
		if !FieldPresent(r, field) {
			return nil
		}
		if w, ok := a.rowWeight(r); ok {
			a.rows++
			a.sumW += w
			a.sumWSq += w * w
		}
		return nil
	}
	x, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	if w, ok := a.rowWeight(r); ok {
		a.fold(x, w)
	}
	return nil
}

// Aggregate is the buffered path. AGG_VARIANCE / AGG_STDDEV run the
// two-pass form their unweighted twins run (mean first, then the
// weighted squared deviations); every other operator folds the records
// in order through the streaming recurrence, which is what its
// unweighted twin does on the buffered path too.
func (a *weightedAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.reset()
	if a.op != types.AGG_VARIANCE && a.op != types.AGG_STDDEV {
		for _, r := range records {
			if err := a.UpdateRow(r, field); err != nil {
				return 0, err
			}
		}
		return a.Finalize()
	}
	type pair struct{ x, w float64 }
	pairs := make([]pair, 0, len(records))
	for _, r := range records {
		x, ok := r.NumericValue(field)
		if !ok {
			continue
		}
		w, ok := a.rowWeight(r)
		if !ok {
			continue
		}
		pairs = append(pairs, pair{x, w})
		a.rows++
		a.sumW += w
		a.sumWX += w * x
		a.sumWSq += w * w
	}
	if a.sumW == 0 {
		return a.Finalize()
	}
	a.mean = a.sumWX / a.sumW
	for _, p := range pairs {
		d := p.x - a.mean
		a.m2 += p.w * d * d
	}
	return a.Finalize()
}

// Finalize returns the scalar, freezes the state for Components /
// Rich and resets the live state.
func (a *weightedAggregator) Finalize() (float64, error) {
	a.frozen = a.weightedState
	a.reset()
	return a.frozen.scalar(a.op), nil
}

// scalar is the operator's figure over st.
func (st *weightedState) scalar(op types.AggregationType) float64 {
	switch op {
	case types.AGG_COUNT:
		return st.sumW
	case types.AGG_SUM:
		return st.sumWX
	case types.AGG_VARIANCE:
		if st.sumW == 0 {
			return 0
		}
		return st.m2 / st.sumW
	case types.AGG_STDDEV:
		if st.sumW == 0 {
			return 0
		}
		return math.Sqrt(st.m2 / st.sumW)
	case types.AGG_WELFORD:
		if st.rows == 0 {
			return math.NaN()
		}
		return st.mean
	}
	// AGG_AVERAGE, AGG_WEIGHTED_MEAN: Σwx/Σw, the exact weighted mean
	// (not the recurrence's running mean).
	if st.sumW == 0 {
		return 0
	}
	return st.sumWX / st.sumW
}

// sampleVariance is the frequency-weights sample variance
// m2 / (Σw − 1) — the unweighted n−1 form at w = 1; 0 when Σw ≤ 1.
func (st *weightedState) sampleVariance() float64 {
	return weightedVariance(st.m2, st.sumW)
}

// MergeOnline folds another partial of the same slot: the sums add and
// the moments combine through the weighted Chan-Welford merge.
func (a *weightedAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*weightedAggregator)
	if !ok || b.op != a.op {
		return mergeTypeMismatch(string(a.op))
	}
	mergeWelfordWeighted(&a.sumW, &a.mean, &a.m2, b.sumW, b.mean, b.m2)
	a.rows += b.rows
	a.sumWX += b.sumWX
	a.sumWSq += b.sumWSq
	return nil
}

// mergeWelfordWeighted is mergeWelford over weight totals: the
// Chan-Welford parallel reduction of two (Σw, mean, M2) partials. With
// unit weights it is mergeWelford's exact operation sequence. Σw is
// updated in place, so callers merge their other sums separately.
func mergeWelfordWeighted(wA, meanA, m2A *float64, wB, meanB, m2B float64) {
	if wB == 0 {
		return
	}
	if *wA == 0 {
		*wA, *meanA, *m2A = wB, meanB, m2B
		return
	}
	total := *wA + wB
	delta := meanB - *meanA
	newMean := *meanA + delta*wB/total
	newM2 := *m2A + m2B + delta*delta*(*wA)*wB/total
	*wA = total
	*meanA = newMean
	*m2A = newM2
}

// Rich returns AGG_WELFORD's triple (N = contributing rows; Σw rides
// the floor's sum_weights); nil for every other operator.
func (a *weightedAggregator) Rich() (any, error) {
	st := &a.frozen
	if a.op != types.AGG_WELFORD || st.rows == 0 {
		return nil, nil
	}
	return WelfordTriple{Mean: st.mean, Variance: st.sampleVariance(), N: uint64(st.rows)}, nil
}

// Components mirrors each operator's unweighted map (identical keys
// and, at w = 1, identical values) — AGG_AVERAGE adds the weighted
// moments AGG_WEIGHTED_MEAN has always emitted.
func (a *weightedAggregator) Components() (map[string]any, error) {
	st := &a.frozen
	switch a.op {
	case types.AGG_COUNT:
		return nil, nil
	case types.AGG_SUM:
		return map[string]any{"sum": st.sumWX}, nil
	case types.AGG_VARIANCE, types.AGG_STDDEV:
		if st.sumW == 0 {
			out := map[string]any{"mean": 0.0, "m2": 0.0, "variance": 0.0}
			if a.op == types.AGG_STDDEV {
				out["stddev"] = 0.0
			}
			return out, nil
		}
		variance := st.m2 / st.sumW
		out := map[string]any{"mean": st.mean, "m2": st.m2, "variance": variance}
		if a.op == types.AGG_STDDEV {
			out["stddev"] = math.Sqrt(variance)
		}
		return out, nil
	case types.AGG_WELFORD:
		if st.rows == 0 {
			return nil, nil
		}
		variance := st.sampleVariance()
		return map[string]any{"mean": st.mean, "m2": st.m2, "variance": variance, "stddev": math.Sqrt(variance)}, nil
	}
	weightedMean := 0.0
	if st.sumW != 0 {
		weightedMean = st.sumWX / st.sumW
	}
	out := map[string]any{
		"sum_weighted":      st.sumWX,
		"weighted_mean":     weightedMean,
		"m2_weighted":       st.m2,
		"sum_weights_sq":    st.sumWSq,
		"weighted_variance": st.sampleVariance(),
	}
	if a.op == types.AGG_WEIGHTED_MEAN {
		out["sum_weights"] = st.sumW
		out["n_eff"] = kishNEff(st.sumW, st.sumWSq)
	} else {
		out["sum"] = st.sumWX
	}
	return out, nil
}

// Compile-time interface locks.
var (
	_ MergeableAggregator = (*weightedAggregator)(nil)
	_ MetaAggregator      = (*weightedAggregator)(nil)
	_ RichAggregator      = (*weightedAggregator)(nil)
)
