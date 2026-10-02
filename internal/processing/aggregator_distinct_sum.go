package processing

import (
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// AGG_DISTINCT_SUM — sum a value field ONCE PER DISTINCT KEY.
//
// The Aggregation's own Field names the value summed; Params.distinct_by
// names the key field. A key observed on N records contributes its value
// once, not N times. The motivating shape is a survey weight repeated
// across every row one respondent produced: AGG_SUM over that column
// multiplies the respondent's weight by their row count, while
// AGG_DISTINCT_SUM keyed on the respondent id returns the weighted
// respondent base.
//
// FIRST VALUE WINS. When one key carries conflicting values across its
// records, the FIRST value observed is summed and every later value for
// that key is ignored. That rule is a deliberate contract rather than an
// implementation accident: the alternatives (last-wins, max, refuse) each
// make a different silent claim about which row is authoritative, and
// first-wins is the only one that is stable under the streaming path,
// where "later" rows may not have arrived yet. Insertion order is the
// record order the aggregator is driven in; under MergeOnline it is the
// receiver's records followed by the argument's, and the orchestrator
// merges shard partials in deterministic index order, so receiver-wins
// IS first-wins across the whole input.
//
// Insertion order is retained (`order`) rather than derived from the map
// so the scalar sum and the merge fold are both deterministic — a float64
// sum accumulated in Go map iteration order would vary in the last ULP
// between runs of an identical request.
//
// Components() emits {sum, distinct_count}: the same scalar the value
// channel carries, plus the number of distinct keys that contributed. One
// operator therefore yields both figures from ONE scan, so a consumer
// rendering both can never have them come from different passes over the
// data.
//
// Streamable (per-key state, one map insert per row) and mergeable (the
// per-key maps union). Its ComponentsMergeability is Partial, matching
// AGG_DISTINCT_COUNT: the fold is associative but not constant-space.
type distinctSumParams struct {
	DistinctBy string `json:"distinct_by"`
}

type distinctSumAggregator struct {
	distinctBy string

	// firstValue maps each distinct key to the FIRST value observed for
	// it; order records the keys in observation order so the sum and the
	// merge are both order-stable.
	firstValue map[float64]float64
	order      []float64

	frozenSum           float64
	frozenDistinctCount int
	frozenFinalized     bool
}

func newDistinctSumAggregator(agg *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	var params distinctSumParams
	if agg != nil && len(agg.Params) > 0 {
		if err := json.Unmarshal(agg.Params, &params); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"invalid distinct_sum params: "+err.Error())
		}
	}
	// Refused, never defaulted: falling back to the aggregation's own
	// Field would silently turn the operator into a distinct-VALUE sum,
	// which returns a plausible number for a question nobody asked.
	if params.DistinctBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"AGG_DISTINCT_SUM requires Params.distinct_by")
	}
	return &distinctSumAggregator{distinctBy: params.DistinctBy}, nil
}

func (a *distinctSumAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.firstValue, a.order = nil, nil
	for _, r := range records {
		a.fold(r, field)
	}
	a.freeze()
	return a.frozenSum, nil
}

func (a *distinctSumAggregator) UpdateRow(r *Record, field string) error {
	a.fold(r, field)
	return nil
}

// fold registers one record. A row missing either half of the
// (key, value) pair contributes nothing AND registers no key, so a later
// row carrying the same key with a real value still counts — the null
// row neither claims the key nor blocks it.
func (a *distinctSumAggregator) fold(r *Record, field string) {
	k, ok := r.NumericValue(a.distinctBy)
	if !ok {
		return
	}
	v, ok := r.NumericValue(field)
	if !ok {
		return
	}
	if _, seen := a.firstValue[k]; seen {
		// First value wins — a later value for a known key is ignored.
		return
	}
	if a.firstValue == nil {
		a.firstValue = make(map[float64]float64)
	}
	a.firstValue[k] = v
	a.order = append(a.order, k)
}

func (a *distinctSumAggregator) Finalize() (float64, error) {
	a.freeze()
	out := a.frozenSum
	a.firstValue, a.order = nil, nil
	return out, nil
}

// freeze stamps the components mirrors from the live per-key state.
// Called from both Aggregate and Finalize so Components() returns the
// same map on either path even after the streaming Finalize-reset drops
// the live map. The sum is accumulated by walking `order`, not the map,
// so an identical request produces an identical float64 every run.
func (a *distinctSumAggregator) freeze() {
	sum := 0.0
	for _, k := range a.order {
		sum += a.firstValue[k]
	}
	a.frozenSum = sum
	a.frozenDistinctCount = len(a.order)
	a.frozenFinalized = true
}

// Components returns {sum, distinct_count} — the distinct-keyed sum the
// scalar channel carries, plus the number of distinct keys behind it.
// Both keys are emitted unconditionally (zeros before any input) so the
// operator's runtime key set always equals its declared ComponentSchema;
// callers distinguish "ran, saw nothing" from "never ran" through the
// orchestrator's universal floor (n), which this map deliberately does
// not re-emit.
func (a *distinctSumAggregator) Components() (map[string]any, error) {
	return map[string]any{
		"sum":            a.frozenSum,
		"distinct_count": a.frozenDistinctCount,
	}, nil
}

// MergeOnline unions the per-key maps. A key present in BOTH partials
// keeps the RECEIVER's value: the orchestrator folds shard partials in
// deterministic index order, so receiver-wins is exactly the
// first-value-wins rule applied across the whole input.
func (a *distinctSumAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*distinctSumAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_DISTINCT_SUM")
	}
	for _, k := range b.order {
		if _, seen := a.firstValue[k]; seen {
			continue
		}
		if a.firstValue == nil {
			a.firstValue = make(map[float64]float64, len(b.order))
		}
		a.firstValue[k] = b.firstValue[k]
		a.order = append(a.order, k)
	}
	return nil
}

// Compile-time interface locks — catches drift at build time for
// AGG_DISTINCT_SUM across all four aggregator surfaces.
var (
	_ Aggregator          = (*distinctSumAggregator)(nil)
	_ OnlineAggregator    = (*distinctSumAggregator)(nil)
	_ MergeableAggregator = (*distinctSumAggregator)(nil)
	_ MetaAggregator      = (*distinctSumAggregator)(nil)
)
