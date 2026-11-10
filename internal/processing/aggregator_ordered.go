package processing

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/orderedcat"
	"github.com/frankbardon/pulse/types"
)

// Ordered-categorical AGG_MIN / AGG_MAX / AGG_MEDIAN / AGG_PERCENTILE
// (U66 E9-S2, #718; the rule and its refusals: internal/orderedcat).
// params.ordered / params.levels read a categorical field's level
// RANKS; the figure is the chosen level's dictionary index (a code, as
// AGG_MODE over a categorical). Each operator keeps its class: MIN / MAX
// stream and merge (the extreme rank seen), MEDIAN / PERCENTILE stay
// buffered (R quantile type 1 over the per-rank totals). A row whose
// label params.levels does not list is dropped and counted
// (n_unlisted).

// orderedFactory wraps an order-statistic factory with the ordered rule:
// a slot whose params request the ordered reading is resolved and built
// here (or refused, with predict's code); every other slot goes to the
// numeric reader exactly as before.
func orderedFactory(f AggregatorFactory) AggregatorFactory {
	num := numericReader(f)
	return func(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
		if agg != nil {
			spec, cerr := orderedcat.Resolve(agg.Type, agg.Field, agg.Params, schema)
			if cerr != nil {
				return nil, cerr
			}
			if spec != nil {
				return newOrderedAggregator(agg, spec)
			}
		}
		return num(agg, schema)
	}
}

// orderedDecimalRefusal is the ordered rule on the decimal arm (AGG_MIN
// / AGG_MAX over a decimal128 field, which never reaches a factory):
// a slot requesting the ordered reading is refused there exactly as
// predict refuses it; nil otherwise.
func orderedDecimalRefusal(agg *types.Aggregation, schema *encoding.Schema) error {
	if agg == nil {
		return nil
	}
	if _, cerr := orderedcat.Resolve(agg.Type, agg.Field, agg.Params, schema); cerr != nil {
		return cerr
	}
	return nil
}

func newOrderedAggregator(agg *types.Aggregation, spec *orderedcat.Spec) (Aggregator, error) {
	base := orderedAggregator{op: agg.Type, spec: spec}
	switch agg.Type {
	case types.AGG_MIN, types.AGG_MAX:
		return &orderedExtremeAggregator{orderedAggregator: base, best: -1}, nil
	case types.AGG_MEDIAN:
		base.p = 0.5
	case types.AGG_PERCENTILE:
		p, err := percentileParam(agg)
		if err != nil {
			return nil, err
		}
		base.percentile = p
		base.p = float64(p / 100)
	}
	if w := slotWeight(agg); w != nil {
		ew := effectiveWeight(w)
		base.weight = &ew
	}
	return &base, nil
}

// orderedAggregator is the buffered ordered form (MEDIAN / PERCENTILE,
// and the buffered pass of MIN / MAX): per-rank totals — row counts, or
// Σw over the rows whose weight contributes on a weighted slot.
type orderedAggregator struct {
	op         types.AggregationType
	spec       *orderedcat.Spec
	p          float64
	percentile float64
	weight     *types.WeightSpec

	frozenFinalized bool
	frozenN         int
	frozenRank      int
	frozenValue     float64
	frozenUnlisted  int
}

// rankOfRow reads r's level rank: ok false for a null (or absent) value
// or an unlisted label (unlisted true); an index outside the field's
// dictionary is the corrupt-record refusal (ENCODING_INVALID).
func (a *orderedAggregator) rankOfRow(r *Record, field string) (rank int, ok, unlisted bool, err error) {
	v, present := r.NumericValue(field)
	if !present {
		return 0, false, false, nil
	}
	if err := encx.CategoricalIndexFault(a.spec.SchemaField(), v); err != nil {
		return 0, false, false, err
	}
	if v < 0 || int(v) >= len(a.spec.Rank) {
		// A dictionary-less field: no level is listed.
		return 0, false, true, nil
	}
	rank, listed := a.spec.RankOf(v)
	if !listed {
		return 0, false, true, nil
	}
	return rank, true, false, nil
}

// pick is the chosen rank over the per-rank totals: the lowest / highest
// observed rank for MIN / MAX, R's type-1 quantile otherwise.
func (a *orderedAggregator) pick(totals []float64) int {
	switch a.op {
	case types.AGG_MIN:
		return orderedcat.QuantileRank(totals, 0, false)
	case types.AGG_MAX:
		for r := len(totals) - 1; r >= 0; r-- {
			if totals[r] > 0 {
				return r
			}
		}
		return -1
	}
	return orderedcat.QuantileRank(totals, a.p, a.weight != nil)
}

func (a *orderedAggregator) Aggregate(records []*Record, field string) (float64, error) {
	totals := make([]float64, len(a.spec.Code))
	n, unlisted := 0, 0
	for _, r := range records {
		rank, ok, un, err := a.rankOfRow(r, field)
		if err != nil {
			return 0, err
		}
		if un {
			unlisted++
		}
		if !ok {
			continue
		}
		w := 1.0
		if a.weight != nil {
			var valid bool
			if w, valid = validRowWeight(r, a.weight); !valid {
				continue
			}
		}
		totals[rank] += w
		n++
	}
	return a.freeze(n, unlisted, a.pick(totals)), nil
}

// freeze stamps the frozen mirrors Components reads and returns the
// figure: the chosen rank's dictionary index, NaN (JSON null) when no
// row was admitted.
func (a *orderedAggregator) freeze(n, unlisted, rank int) float64 {
	a.frozenFinalized = true
	a.frozenN = n
	a.frozenUnlisted = unlisted
	a.frozenRank = rank
	if rank < 0 {
		a.frozenValue = math.NaN()
	} else {
		a.frozenValue = float64(a.spec.Code[rank])
	}
	return a.frozenValue
}

// Components keeps each operator's keys, read over ranks, plus
// n_unlisted: MIN {min}, MAX {max} (null when no row was admitted);
// MEDIAN {position_low, position_high, median} and PERCENTILE {p,
// position, lower, upper, method "type1", value}, where a position is
// the chosen level's 0-based rank and lower = upper = value its code —
// nil (or n_unlisted alone) when no row was admitted.
func (a *orderedAggregator) Components() (map[string]any, error) {
	switch a.op {
	case types.AGG_MIN:
		return map[string]any{"min": a.frozenValue, "n_unlisted": a.frozenUnlisted}, nil
	case types.AGG_MAX:
		return map[string]any{"max": a.frozenValue, "n_unlisted": a.frozenUnlisted}, nil
	}
	if !a.frozenFinalized || a.frozenN == 0 {
		if a.frozenUnlisted > 0 {
			return map[string]any{"n_unlisted": a.frozenUnlisted}, nil
		}
		return nil, nil
	}
	if a.op == types.AGG_MEDIAN {
		return map[string]any{
			"position_low":  a.frozenRank,
			"position_high": a.frozenRank,
			"median":        a.frozenValue,
			"n_unlisted":    a.frozenUnlisted,
		}, nil
	}
	return map[string]any{
		"p":          a.percentile,
		"position":   a.frozenRank,
		"lower":      a.frozenValue,
		"upper":      a.frozenValue,
		"method":     orderedMethod,
		"value":      a.frozenValue,
		"n_unlisted": a.frozenUnlisted,
	}, nil
}

// orderedMethod is an ordered percentile's components method: R's
// quantile type 1 (inverse of the empirical distribution, no
// interpolation).
const orderedMethod = "type1"

// orderedExtremeAggregator is ordered MIN / MAX: online and mergeable
// like the plain operators — the extreme rank seen so far.
type orderedExtremeAggregator struct {
	orderedAggregator
	best     int
	n        int
	unlisted int
}

func (a *orderedExtremeAggregator) better(rank int) bool {
	if a.best < 0 {
		return true
	}
	if a.op == types.AGG_MIN {
		return rank < a.best
	}
	return rank > a.best
}

func (a *orderedExtremeAggregator) UpdateRow(r *Record, field string) error {
	rank, ok, un, err := a.rankOfRow(r, field)
	if err != nil {
		return err
	}
	if un {
		a.unlisted++
	}
	if !ok {
		return nil
	}
	a.n++
	if a.better(rank) {
		a.best = rank
	}
	return nil
}

func (a *orderedExtremeAggregator) Finalize() (float64, error) {
	out := a.freeze(a.n, a.unlisted, a.best)
	a.best, a.n, a.unlisted = -1, 0, 0
	return out, nil
}

func (a *orderedExtremeAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*orderedExtremeAggregator)
	if !ok || b.op != a.op {
		return mergeTypeMismatch(string(a.op))
	}
	a.n += b.n
	a.unlisted += b.unlisted
	if b.best >= 0 && a.better(b.best) {
		a.best = b.best
	}
	return nil
}

var (
	_ MetaAggregator      = (*orderedAggregator)(nil)
	_ MetaAggregator      = (*orderedExtremeAggregator)(nil)
	_ MergeableAggregator = (*orderedExtremeAggregator)(nil)
)
