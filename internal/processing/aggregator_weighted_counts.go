package processing

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/reprosum"
	"github.com/frankbardon/pulse/types"
)

// Weighted forms of the counting aggregators (weighting-descriptive
// E2-S2; contract .claude/reference/weighting.md "Aggregator
// classification"):
//
//   - AGG_FREQUENCY — match_count = Σw of the matching rows, share =
//     Σw_match / Σw over the value-present rows;
//   - AGG_SET_FREQUENCY — Σw per selected member (a float map);
//   - AGG_SET_CARDINALITY_SUM — Σw·card;
//   - AGG_SET_CARDINALITY_AVG — Σw·card / Σw.
//
// (AGG_RATIO's weighted form, Σw·num / Σw·den, lives on ratioAggregator
// itself — its state is already two float sums.)
//
// Each mirrors its unweighted twin's execution shape exactly —
// streamable, mergeable, and with the same Finalize / freeze contract —
// and every figure reduces to the twin's operation sequence at w = 1:
// a sum of unit weights is the exact integer count, and w·x is x, so an
// all-ones weight column answers bit for bit. The integer counters of
// the unweighted twins become float64 here; at w = 1 they are integral,
// so the wire form is byte-identical. None implements valueAggregator:
// the weights live on the records. Row handling is validRowWeight's:
// an invalid or zero weight contributes nothing (counted by the
// orchestrator's WeightFloor / WeightRowTally, never coerced).
//
// Every Σ here is a binned reproducible sum (internal/reprosum; a
// product goes through AddWeighted, its FMA barrier), merged as binned
// state, so the figures are bit-identical on every arm, worker count
// and shard split (root TestBitExact_*).

// --- AGG_FREQUENCY ----------------------------------------------------

type weightedFrequencyAggregator struct {
	key       float64
	matchable bool
	weight    types.WeightSpec

	sumW   reprosum.Sum // Σw over value-present contributing rows
	matchW reprosum.Sum // Σw over the matching rows

	frozenFinalized bool
	frozenSumW      float64
	frozenMatchW    float64
}

func newWeightedFrequencyAggregator(key float64, matchable bool, spec *types.WeightSpec) *weightedFrequencyAggregator {
	return &weightedFrequencyAggregator{key: key, matchable: matchable, weight: effectiveWeight(spec)}
}

func (a *weightedFrequencyAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	w, ok := validRowWeight(r, &a.weight)
	if !ok {
		return nil
	}
	a.sumW.Add(w)
	if a.matchable && v == a.key {
		a.matchW.Add(w)
	}
	return nil
}

func (a *weightedFrequencyAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.sumW, a.matchW = reprosum.Sum{}, reprosum.Sum{}
	for _, r := range records {
		if err := a.UpdateRow(r, field); err != nil {
			return 0, err
		}
	}
	return a.Finalize()
}

func (a *weightedFrequencyAggregator) Finalize() (float64, error) {
	a.frozenFinalized = true
	a.frozenSumW, a.frozenMatchW = a.sumW.Float64(), a.matchW.Float64()
	a.sumW, a.matchW = reprosum.Sum{}, reprosum.Sum{}
	return a.frozenMatchW, nil
}

func (a *weightedFrequencyAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*weightedFrequencyAggregator)
	if !ok {
		return mergeTypeMismatch(string(types.AGG_FREQUENCY))
	}
	a.sumW.Merge(&b.sumW)
	a.matchW.Merge(&b.matchW)
	return nil
}

// Components mirrors the unweighted {match_count, share}: match_count
// is the matching Σw (a float), share its fraction of Σw — omitted when
// Σw is 0 (a share of nothing is undefined, not zero).
func (a *weightedFrequencyAggregator) Components() (map[string]any, error) {
	if !a.frozenFinalized {
		return nil, nil
	}
	out := map[string]any{"match_count": a.frozenMatchW}
	if a.frozenSumW > 0 {
		out["share"] = a.frozenMatchW / a.frozenSumW
	}
	return out, nil
}

// --- AGG_SET_FREQUENCY ------------------------------------------------

// weightedSetFrequencyAggregator keeps a binned Σw per member bit
// (56 bytes per member: a set_u256 slot holds 256 of them per group). Rich is the
// label→Σw map (map[string]float64); the scalar fallback the largest
// single-member Σw. A member selected only on zero-weight rows has
// Σw = 0 and is dropped like an unseen member.
type weightedSetFrequencyAggregator struct {
	dict   *encoding.Dictionary
	weight types.WeightSpec
	sums   []reprosum.Sum

	frozenPerLabel       map[string]float64
	frozenTotalLabelObs  float64
	frozenDistinctLabels int
	frozenHasResult      bool
}

func newWeightedSetFrequencyAggregator(dict *encoding.Dictionary, width int, spec *types.WeightSpec) *weightedSetFrequencyAggregator {
	return &weightedSetFrequencyAggregator{dict: dict, weight: effectiveWeight(spec), sums: make([]reprosum.Sum, width)}
}

func (a *weightedSetFrequencyAggregator) Aggregate(records []*Record, field string) (float64, error) {
	clear(a.sums)
	for _, r := range records {
		if err := a.UpdateRow(r, field); err != nil {
			return 0, err
		}
	}
	out := a.maxSum()
	a.freeze()
	return out, nil
}

func (a *weightedSetFrequencyAggregator) UpdateRow(r *Record, field string) error {
	m, ok := r.SetMaskValue(field)
	if !ok {
		return nil
	}
	w, ok := validRowWeight(r, &a.weight)
	if !ok {
		return nil
	}
	for i, ok := m.NextBit(0); ok; i, ok = m.NextBit(i + 1) {
		if i >= len(a.sums) {
			grown := make([]reprosum.Sum, i+1)
			copy(grown, a.sums)
			a.sums = grown
		}
		a.sums[i].Add(w)
	}
	return nil
}

func (a *weightedSetFrequencyAggregator) maxSum() float64 {
	max := 0.0
	for i := range a.sums {
		if s := a.sums[i].Float64(); s > max {
			max = s
		}
	}
	return max
}

func (a *weightedSetFrequencyAggregator) Finalize() (float64, error) {
	out := a.maxSum()
	a.freeze()
	return out, nil
}

// perLabel resolves the live sums to the label→Σw map — the unweighted
// Rich's dictionary walk: zero sums and bits past the dictionary tail
// are dropped. total is Σ over every bit's sum (the tail included, as
// unweighted), itself a binned sum of the per-bit sums.
func (a *weightedSetFrequencyAggregator) perLabel() (map[string]float64, float64) {
	out := make(map[string]float64, len(a.sums))
	if a.dict == nil {
		return out, 0
	}
	var total reprosum.Sum
	dictLen := a.dict.Count()
	for i := range a.sums {
		s := a.sums[i].Float64()
		if s == 0 {
			continue
		}
		total.Add(s)
		if i >= dictLen {
			continue
		}
		if label := a.dict.Resolve(uint32(i)); label != "" {
			out[label] = s
		}
	}
	return out, total.Float64()
}

func (a *weightedSetFrequencyAggregator) freeze() {
	a.frozenHasResult = true
	a.frozenPerLabel, a.frozenTotalLabelObs = a.perLabel()
	a.frozenDistinctLabels = len(a.frozenPerLabel)
}

func (a *weightedSetFrequencyAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*weightedSetFrequencyAggregator)
	if !ok {
		return mergeTypeMismatch(string(types.AGG_SET_FREQUENCY))
	}
	if len(b.sums) > len(a.sums) {
		grown := make([]reprosum.Sum, len(b.sums))
		copy(grown, a.sums)
		a.sums = grown
	}
	for i := range b.sums {
		a.sums[i].Merge(&b.sums[i])
	}
	return nil
}

// Rich is the label→Σw map (float64 values; integral, and so
// wire-identical to the unweighted map[string]int, at w = 1).
func (a *weightedSetFrequencyAggregator) Rich() (any, error) {
	out, _ := a.perLabel()
	return out, nil
}

// Components mirrors the unweighted {total_label_observations,
// distinct_labels, per_label_count}: the observation total and the
// per-label figures are Σw (floats); distinct_labels stays a count.
func (a *weightedSetFrequencyAggregator) Components() (map[string]any, error) {
	if !a.frozenHasResult {
		return map[string]any{
			"total_label_observations": 0,
			"distinct_labels":          0,
			"per_label_count":          map[string]float64{},
		}, nil
	}
	perLabel := a.frozenPerLabel
	if perLabel == nil {
		perLabel = map[string]float64{}
	}
	return map[string]any{
		"total_label_observations": a.frozenTotalLabelObs,
		"distinct_labels":          a.frozenDistinctLabels,
		"per_label_count":          perLabel,
	}, nil
}

// --- AGG_SET_CARDINALITY_SUM / AGG_SET_CARDINALITY_AVG -----------------

// weightedSetCardinalityAggregator is Σw·card (SUM) and Σw·card / Σw
// (AVG; NaN — JSON null — when Σw is 0). Like the unweighted twins, Finalize freezes
// without resetting and Aggregate starts clean.
type weightedSetCardinalityAggregator struct {
	op     types.AggregationType
	weight types.WeightSpec
	total  reprosum.Sum // Σw·card
	sumW   reprosum.Sum // Σw

	frozenTotal     float64
	frozenSumW      float64
	frozenHasResult bool
}

func newWeightedSetCardinalityAggregator(op types.AggregationType, spec *types.WeightSpec) *weightedSetCardinalityAggregator {
	return &weightedSetCardinalityAggregator{op: op, weight: effectiveWeight(spec)}
}

func (a *weightedSetCardinalityAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.total, a.sumW = reprosum.Sum{}, reprosum.Sum{}
	for _, r := range records {
		if err := a.UpdateRow(r, field); err != nil {
			return 0, err
		}
	}
	return a.Finalize()
}

func (a *weightedSetCardinalityAggregator) UpdateRow(r *Record, field string) error {
	m, ok := r.SetMaskValue(field)
	if !ok {
		return nil
	}
	w, ok := validRowWeight(r, &a.weight)
	if !ok {
		return nil
	}
	a.total.AddWeighted(w, float64(m.PopCount()))
	a.sumW.Add(w)
	return nil
}

func (a *weightedSetCardinalityAggregator) Finalize() (float64, error) {
	a.frozenHasResult = true
	a.frozenTotal, a.frozenSumW = a.total.Float64(), a.sumW.Float64()
	return a.scalar(), nil
}

func (a *weightedSetCardinalityAggregator) scalar() float64 {
	if a.op == types.AGG_SET_CARDINALITY_SUM {
		return a.frozenTotal
	}
	if a.frozenSumW == 0 {
		return math.NaN()
	}
	return a.frozenTotal / a.frozenSumW
}

func (a *weightedSetCardinalityAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*weightedSetCardinalityAggregator)
	if !ok || b.op != a.op {
		return mergeTypeMismatch(string(a.op))
	}
	a.total.Merge(&b.total)
	a.sumW.Merge(&b.sumW)
	return nil
}

// Components mirrors the unweighted maps; sum_cardinality is Σw·card
// (a float).
func (a *weightedSetCardinalityAggregator) Components() (map[string]any, error) {
	if !a.frozenHasResult {
		out := map[string]any{"sum_cardinality": 0}
		if a.op == types.AGG_SET_CARDINALITY_AVG {
			out["avg_cardinality"] = 0.0
		}
		return out, nil
	}
	out := map[string]any{"sum_cardinality": a.frozenTotal}
	if a.op == types.AGG_SET_CARDINALITY_AVG {
		out["avg_cardinality"] = a.scalar()
	}
	return out, nil
}

// Compile-time interface locks: the weighted twins expose exactly the
// execution interfaces their unweighted twins do.
var (
	_ MergeableAggregator = (*weightedFrequencyAggregator)(nil)
	_ MetaAggregator      = (*weightedFrequencyAggregator)(nil)
	_ MergeableAggregator = (*weightedSetFrequencyAggregator)(nil)
	_ MetaAggregator      = (*weightedSetFrequencyAggregator)(nil)
	_ RichAggregator      = (*weightedSetFrequencyAggregator)(nil)
	_ MergeableAggregator = (*weightedSetCardinalityAggregator)(nil)
	_ MetaAggregator      = (*weightedSetCardinalityAggregator)(nil)
)
