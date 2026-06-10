package processing

import (
	"math/bits"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Set-typed aggregators — AGG_SET_UNION, AGG_SET_INTERSECTION,
// AGG_SET_FREQUENCY, AGG_SET_CARDINALITY_SUM, AGG_SET_CARDINALITY_AVG,
// AGG_SET_DISTINCT_VALUES.
//
// Set values arrive through Record.SetValue (uint64 mask). Bit i is set
// when dictionary entry i is selected. Bitwise OR (union) and AND
// (intersection) are associative + commutative, so the orchestrator's
// per-shard parallel reducer works for free. AGG_SET_INTERSECTION'S
// margin is recompute-only (AND across cells ≠ AND across all rows in
// general); the others are summable.
//
// UNION / INTERSECTION / FREQUENCY also satisfy RichAggregator: the
// scalar float64 they return through Aggregate / Finalize is a sensible
// fallback (popcount for the masks, max-bin count for frequency), and
// Rich() supplies the typed value (resolved labels, label→count map)
// that the processor / streaming path / crosstab cell builder routes
// into Response.Data and CrosstabResult.Matrix cells.

// setAggDict resolves the dictionary for the field this aggregator
// targets. Every set aggregator constructs once at factory time and
// holds the pointer so Aggregate / UpdateRow stay allocation-free.
func setAggDict(agg *types.Aggregation, schema *encoding.Schema) (*encoding.Dictionary, error) {
	// Construction with nil schema is tolerated for the registry
	// streamability test. Runtime callers always pass a schema, and the
	// validation below catches type errors before Aggregate ever runs.
	if schema == nil {
		return nil, nil
	}
	if agg.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(agg.Type)+" requires field")
	}
	f := schema.Field(agg.Field)
	if f == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(agg.Type)+": unknown field "+agg.Field)
	}
	if !f.Type.IsSet() {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(agg.Type)+": field "+agg.Field+" is not a set type")
	}
	return f.Dictionary, nil
}

// resolveMaskLabels walks the bits of mask and returns dictionary
// labels in ascending bit order. Mirrors resolveSetLabels in record.go
// (kept private to that file). Empty mask returns an empty slice (not
// nil) so JSON emits `[]` rather than `null`.
func resolveMaskLabels(mask uint64, dict *encoding.Dictionary) []string {
	if dict == nil {
		return []string{}
	}
	dictLen := dict.Count()
	out := make([]string, 0, bits.OnesCount64(mask))
	for i := 0; i < dictLen && i < 64; i++ {
		if mask&(uint64(1)<<uint(i)) == 0 {
			continue
		}
		label := dict.Resolve(uint32(i))
		if label == "" {
			continue
		}
		out = append(out, label)
	}
	return out
}

// --- AGG_SET_UNION -------------------------------------------------
//
// Bitwise OR across rows. Result = mask of every bit set in any row.
// Rich value = resolved labels sorted by bit order (ascending).

type setUnionAggregator struct {
	dict *encoding.Dictionary
	mask uint64
}

func newSetUnionAggregator(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
	dict, err := setAggDict(agg, schema)
	if err != nil {
		return nil, err
	}
	return &setUnionAggregator{dict: dict}, nil
}

func (a *setUnionAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.mask = 0
	for _, r := range records {
		m, ok := r.SetValue(field)
		if !ok {
			continue
		}
		a.mask |= m
	}
	return float64(bits.OnesCount64(a.mask)), nil
}

func (a *setUnionAggregator) UpdateRow(r *Record, field string) error {
	m, ok := r.SetValue(field)
	if !ok {
		return nil
	}
	a.mask |= m
	return nil
}

func (a *setUnionAggregator) Finalize() (float64, error) {
	out := float64(bits.OnesCount64(a.mask))
	return out, nil
}

func (a *setUnionAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*setUnionAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_SET_UNION")
	}
	a.mask |= b.mask
	return nil
}

func (a *setUnionAggregator) Rich() (any, error) {
	return resolveMaskLabels(a.mask, a.dict), nil
}

// --- AGG_SET_INTERSECTION ------------------------------------------
//
// Bitwise AND across rows. Result = mask of bits set in every
// contributing row. seen tracks whether any non-null row arrived;
// empty input returns the zero mask.
//
// MarginReducibility = MarginRecompute. AND across cells is NOT the
// same as AND across all rows in general (a bit absent from any single
// cell drops out of that cell, but may still be present in every row
// of a different cell), so the crosstab path recomputes from raw rows.

type setIntersectionAggregator struct {
	dict *encoding.Dictionary
	mask uint64
	seen bool
}

func newSetIntersectionAggregator(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
	dict, err := setAggDict(agg, schema)
	if err != nil {
		return nil, err
	}
	return &setIntersectionAggregator{dict: dict}, nil
}

func (a *setIntersectionAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.mask = 0
	a.seen = false
	for _, r := range records {
		m, ok := r.SetValue(field)
		if !ok {
			continue
		}
		if !a.seen {
			a.mask = m
			a.seen = true
			continue
		}
		a.mask &= m
	}
	return float64(bits.OnesCount64(a.mask)), nil
}

func (a *setIntersectionAggregator) UpdateRow(r *Record, field string) error {
	m, ok := r.SetValue(field)
	if !ok {
		return nil
	}
	if !a.seen {
		a.mask = m
		a.seen = true
		return nil
	}
	a.mask &= m
	return nil
}

func (a *setIntersectionAggregator) Finalize() (float64, error) {
	out := float64(bits.OnesCount64(a.mask))
	return out, nil
}

func (a *setIntersectionAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*setIntersectionAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_SET_INTERSECTION")
	}
	if !b.seen {
		return nil
	}
	if !a.seen {
		a.mask = b.mask
		a.seen = true
		return nil
	}
	a.mask &= b.mask
	return nil
}

func (a *setIntersectionAggregator) Rich() (any, error) {
	if !a.seen {
		return []string{}, nil
	}
	return resolveMaskLabels(a.mask, a.dict), nil
}

// --- AGG_SET_FREQUENCY ---------------------------------------------
//
// Per-bit count: how many rows had bit i set. Rich value = map from
// resolved label to row count, omitting labels with zero count.
// Scalar fallback = max-bin count (highest single-label frequency),
// mirroring AGG_FREQUENCY's float64 contract.

type setFrequencyAggregator struct {
	dict   *encoding.Dictionary
	counts []uint64
}

func newSetFrequencyAggregator(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
	dict, err := setAggDict(agg, schema)
	if err != nil {
		return nil, err
	}
	width := 0
	if dict != nil {
		width = dict.Count()
	}
	if width > 64 {
		width = 64
	}
	return &setFrequencyAggregator{
		dict:   dict,
		counts: make([]uint64, width),
	}, nil
}

func (a *setFrequencyAggregator) Aggregate(records []*Record, field string) (float64, error) {
	for i := range a.counts {
		a.counts[i] = 0
	}
	for _, r := range records {
		m, ok := r.SetValue(field)
		if !ok {
			continue
		}
		a.foldMask(m)
	}
	return a.maxCount(), nil
}

func (a *setFrequencyAggregator) UpdateRow(r *Record, field string) error {
	m, ok := r.SetValue(field)
	if !ok {
		return nil
	}
	a.foldMask(m)
	return nil
}

func (a *setFrequencyAggregator) foldMask(m uint64) {
	for m != 0 {
		i := bits.TrailingZeros64(m)
		if i >= len(a.counts) {
			grown := make([]uint64, i+1)
			copy(grown, a.counts)
			a.counts = grown
		}
		a.counts[i]++
		m &= m - 1
	}
}

func (a *setFrequencyAggregator) maxCount() float64 {
	max := uint64(0)
	for _, c := range a.counts {
		if c > max {
			max = c
		}
	}
	return float64(max)
}

func (a *setFrequencyAggregator) Finalize() (float64, error) {
	return a.maxCount(), nil
}

func (a *setFrequencyAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*setFrequencyAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_SET_FREQUENCY")
	}
	if len(b.counts) > len(a.counts) {
		grown := make([]uint64, len(b.counts))
		copy(grown, a.counts)
		a.counts = grown
	}
	for i, c := range b.counts {
		a.counts[i] += c
	}
	return nil
}

func (a *setFrequencyAggregator) Rich() (any, error) {
	out := make(map[string]int, len(a.counts))
	if a.dict == nil {
		return out, nil
	}
	dictLen := a.dict.Count()
	for i, c := range a.counts {
		if c == 0 {
			continue
		}
		if i >= dictLen {
			continue
		}
		label := a.dict.Resolve(uint32(i))
		if label == "" {
			continue
		}
		out[label] = int(c)
	}
	return out, nil
}

// --- AGG_SET_CARDINALITY_SUM ---------------------------------------
//
// Sum of popcounts across contributing rows. Scalar; summable; no rich
// value.

type setCardinalitySumAggregator struct {
	total uint64
}

func newSetCardinalitySumAggregator(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
	if _, err := setAggDict(agg, schema); err != nil {
		return nil, err
	}
	return &setCardinalitySumAggregator{}, nil
}

func (a *setCardinalitySumAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.total = 0
	for _, r := range records {
		m, ok := r.SetValue(field)
		if !ok {
			continue
		}
		a.total += uint64(bits.OnesCount64(m))
	}
	return float64(a.total), nil
}

func (a *setCardinalitySumAggregator) UpdateRow(r *Record, field string) error {
	m, ok := r.SetValue(field)
	if !ok {
		return nil
	}
	a.total += uint64(bits.OnesCount64(m))
	return nil
}

func (a *setCardinalitySumAggregator) Finalize() (float64, error) {
	return float64(a.total), nil
}

func (a *setCardinalitySumAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*setCardinalitySumAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_SET_CARDINALITY_SUM")
	}
	a.total += b.total
	return nil
}

// --- AGG_SET_CARDINALITY_AVG ---------------------------------------
//
// Average popcount per contributing row. Merge via summed totals and
// counts (mean-reducible margin). Empty input returns 0.

type setCardinalityAvgAggregator struct {
	total uint64
	n     uint64
}

func newSetCardinalityAvgAggregator(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
	if _, err := setAggDict(agg, schema); err != nil {
		return nil, err
	}
	return &setCardinalityAvgAggregator{}, nil
}

func (a *setCardinalityAvgAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.total = 0
	a.n = 0
	for _, r := range records {
		m, ok := r.SetValue(field)
		if !ok {
			continue
		}
		a.total += uint64(bits.OnesCount64(m))
		a.n++
	}
	if a.n == 0 {
		return 0, nil
	}
	return float64(a.total) / float64(a.n), nil
}

func (a *setCardinalityAvgAggregator) UpdateRow(r *Record, field string) error {
	m, ok := r.SetValue(field)
	if !ok {
		return nil
	}
	a.total += uint64(bits.OnesCount64(m))
	a.n++
	return nil
}

func (a *setCardinalityAvgAggregator) Finalize() (float64, error) {
	if a.n == 0 {
		return 0, nil
	}
	return float64(a.total) / float64(a.n), nil
}

func (a *setCardinalityAvgAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*setCardinalityAvgAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_SET_CARDINALITY_AVG")
	}
	a.total += b.total
	a.n += b.n
	return nil
}

// --- AGG_SET_DISTINCT_VALUES ---------------------------------------
//
// Distinct exact mask values seen — treats each (possibly empty)
// combination as atomic. Mergeable via set union of seen masks.

type setDistinctValuesAggregator struct {
	seen map[uint64]struct{}
}

func newSetDistinctValuesAggregator(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
	if _, err := setAggDict(agg, schema); err != nil {
		return nil, err
	}
	return &setDistinctValuesAggregator{}, nil
}

func (a *setDistinctValuesAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.seen = make(map[uint64]struct{})
	for _, r := range records {
		m, ok := r.SetValue(field)
		if !ok {
			continue
		}
		a.seen[m] = struct{}{}
	}
	return float64(len(a.seen)), nil
}

func (a *setDistinctValuesAggregator) UpdateRow(r *Record, field string) error {
	m, ok := r.SetValue(field)
	if !ok {
		return nil
	}
	if a.seen == nil {
		a.seen = make(map[uint64]struct{})
	}
	a.seen[m] = struct{}{}
	return nil
}

func (a *setDistinctValuesAggregator) Finalize() (float64, error) {
	out := float64(len(a.seen))
	return out, nil
}

func (a *setDistinctValuesAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*setDistinctValuesAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_SET_DISTINCT_VALUES")
	}
	if len(b.seen) == 0 {
		return nil
	}
	if a.seen == nil {
		a.seen = make(map[uint64]struct{}, len(b.seen))
	}
	for m := range b.seen {
		a.seen[m] = struct{}{}
	}
	return nil
}

