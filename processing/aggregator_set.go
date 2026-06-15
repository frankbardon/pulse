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

	// frozen* mirror the post-Aggregate / post-Finalize state so
	// Components() works on both buffered and streaming code paths.
	// The set Finalize methods do not reset live state today, but the
	// frozen-mirror pattern matches E1-S5..S9 — the components map
	// stays stable across any future Finalize-reset.
	frozenMask      uint64
	frozenHasResult bool
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
	a.frozenMask = a.mask
	a.frozenHasResult = true
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
	a.frozenMask = a.mask
	a.frozenHasResult = true
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

// Components returns {mask_union, popcount, labels} — the union mask
// from the frozen mirror, its popcount, and the decoded dictionary
// labels (same path as Rich()). Reads the frozen mirror stamped by
// Aggregate / Finalize so any future streaming Finalize-reset on
// (mask) does not erase the values before Components() runs. Empty
// input (no rows seen) collapses to {0, 0, []string{}} — matches
// Rich()'s concrete-empty contract.
func (a *setUnionAggregator) Components() (map[string]any, error) {
	if !a.frozenHasResult {
		return map[string]any{
			"mask_union": uint64(0),
			"popcount":   0,
			"labels":     []string{},
		}, nil
	}
	return map[string]any{
		"mask_union": a.frozenMask,
		"popcount":   bits.OnesCount64(a.frozenMask),
		"labels":     resolveMaskLabels(a.frozenMask, a.dict),
	}, nil
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

	// frozen* mirror the post-Aggregate / post-Finalize state. Mirrors
	// `seen` separately so Components() can distinguish "no rows seen"
	// (empty intersection) from "rows seen, intersection collapsed to
	// 0 mask" — both render as popcount 0 but the components map
	// reflects the raw mask in either case.
	frozenMask      uint64
	frozenSeen      bool
	frozenHasResult bool
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
	a.frozenMask = a.mask
	a.frozenSeen = a.seen
	a.frozenHasResult = true
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
	a.frozenMask = a.mask
	a.frozenSeen = a.seen
	a.frozenHasResult = true
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

// Components returns {mask_intersection, popcount, labels} — the
// intersection mask from the frozen mirror, its popcount, and the
// decoded dictionary labels (same path as Rich()). Empty input (no
// non-null row contributed) collapses to {0, 0, []string{}} — matches
// Rich()'s concrete-empty contract.
func (a *setIntersectionAggregator) Components() (map[string]any, error) {
	if !a.frozenHasResult || !a.frozenSeen {
		return map[string]any{
			"mask_intersection": uint64(0),
			"popcount":          0,
			"labels":            []string{},
		}, nil
	}
	return map[string]any{
		"mask_intersection": a.frozenMask,
		"popcount":          bits.OnesCount64(a.frozenMask),
		"labels":            resolveMaskLabels(a.frozenMask, a.dict),
	}, nil
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

	// frozen* mirror the post-Aggregate / post-Finalize state. The
	// per-label count map and aggregate counters are stamped at the
	// freeze point so Components() returns a stable snapshot even if
	// a future Finalize-reset clears `counts`.
	frozenPerLabel        map[string]int
	frozenTotalLabelObs   int
	frozenDistinctLabels  int
	frozenHasResult       bool
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
	out := a.maxCount()
	a.freeze()
	return out, nil
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
	out := a.maxCount()
	a.freeze()
	return out, nil
}

// freeze stamps the per-label count map plus total-observations /
// distinct-labels counters from the live `counts` slice. Reuses the
// same dictionary-resolve path Rich() uses — zero-count bits and bits
// past the dictionary tail are dropped consistently. Called from both
// Aggregate and Finalize so Components() returns a stable snapshot on
// either path even after a future Finalize-reset.
func (a *setFrequencyAggregator) freeze() {
	a.frozenHasResult = true
	perLabel := make(map[string]int, len(a.counts))
	total := 0
	if a.dict == nil {
		a.frozenPerLabel = perLabel
		a.frozenTotalLabelObs = 0
		a.frozenDistinctLabels = 0
		return
	}
	dictLen := a.dict.Count()
	for i, c := range a.counts {
		if c == 0 {
			continue
		}
		total += int(c)
		if i >= dictLen {
			continue
		}
		label := a.dict.Resolve(uint32(i))
		if label == "" {
			continue
		}
		perLabel[label] = int(c)
	}
	a.frozenPerLabel = perLabel
	a.frozenTotalLabelObs = total
	a.frozenDistinctLabels = len(perLabel)
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

// Components returns {total_label_observations, distinct_labels,
// per_label_count} — the total bits-set across contributing rows, the
// cardinality of distinct labels observed, and the per-label row-count
// map (same dictionary-resolve path Rich() uses). Mergeability=Partial
// — the per-label map merges across chunks but costs allocation, so the
// orchestrator may stage the merge at terminal flush. Empty input (no
// rows seen) collapses to {0, 0, map[string]int{}} so callers always
// see a populated map shape.
func (a *setFrequencyAggregator) Components() (map[string]any, error) {
	if !a.frozenHasResult {
		return map[string]any{
			"total_label_observations": 0,
			"distinct_labels":          0,
			"per_label_count":          map[string]int{},
		}, nil
	}
	perLabel := a.frozenPerLabel
	if perLabel == nil {
		perLabel = map[string]int{}
	}
	return map[string]any{
		"total_label_observations": a.frozenTotalLabelObs,
		"distinct_labels":          a.frozenDistinctLabels,
		"per_label_count":          perLabel,
	}, nil
}

// --- AGG_SET_CARDINALITY_SUM ---------------------------------------
//
// Sum of popcounts across contributing rows. Scalar; summable; no rich
// value.

type setCardinalitySumAggregator struct {
	total uint64

	frozenTotal     uint64
	frozenHasResult bool
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
	a.frozenTotal = a.total
	a.frozenHasResult = true
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
	a.frozenTotal = a.total
	a.frozenHasResult = true
	return float64(a.total), nil
}

// Components returns {sum_cardinality} — the running sum of popcounts
// across contributing rows. Reads the frozen mirror so streaming
// Finalize-reset (none today) does not erase the value. Empty input
// (no rows seen) emits {0} so the components map always carries the
// declared key.
func (a *setCardinalitySumAggregator) Components() (map[string]any, error) {
	if !a.frozenHasResult {
		return map[string]any{
			"sum_cardinality": 0,
		}, nil
	}
	return map[string]any{
		"sum_cardinality": int(a.frozenTotal),
	}, nil
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

	frozenTotal     uint64
	frozenN         uint64
	frozenAvg       float64
	frozenHasResult bool
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
	a.freeze()
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
	a.freeze()
	if a.n == 0 {
		return 0, nil
	}
	return float64(a.total) / float64(a.n), nil
}

// freeze stamps the running totals + avg into the frozen mirror so
// Components() works on both buffered and streaming code paths. Called
// from both Aggregate and Finalize so the components map is stable
// regardless of which path produced the value.
func (a *setCardinalityAvgAggregator) freeze() {
	a.frozenHasResult = true
	a.frozenTotal = a.total
	a.frozenN = a.n
	if a.n == 0 {
		a.frozenAvg = 0
		return
	}
	a.frozenAvg = float64(a.total) / float64(a.n)
}

// Components returns {sum_cardinality, avg_cardinality} — the running
// popcount sum and its average over contributing rows. Reads the
// frozen mirror so streaming Finalize-reset does not erase the values.
// Empty input (no rows seen) emits {0, 0.0} so the components map
// always carries the declared keys.
func (a *setCardinalityAvgAggregator) Components() (map[string]any, error) {
	if !a.frozenHasResult {
		return map[string]any{
			"sum_cardinality": 0,
			"avg_cardinality": 0.0,
		}, nil
	}
	return map[string]any{
		"sum_cardinality": int(a.frozenTotal),
		"avg_cardinality": a.frozenAvg,
	}, nil
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
	dict *encoding.Dictionary
	seen map[uint64]struct{}

	// frozen* mirror the post-Aggregate / post-Finalize state. The
	// declared schema surfaces the bitwise-OR union of every distinct
	// mask seen (alongside its popcount + decoded labels) — a strict
	// superset of "any bit any row contributed". The frozen union is
	// stamped at the freeze point so Components() returns a stable
	// snapshot even if a future Finalize-reset wipes `seen`.
	frozenMaskUnion uint64
	frozenHasResult bool
}

func newSetDistinctValuesAggregator(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
	dict, err := setAggDict(agg, schema)
	if err != nil {
		return nil, err
	}
	return &setDistinctValuesAggregator{dict: dict}, nil
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
	a.freeze()
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
	a.freeze()
	return out, nil
}

// freeze stamps the bitwise-OR union of every distinct mask seen into
// the frozen mirror. Used by Components() to surface the union mask +
// popcount + decoded labels per the declared component schema. Called
// from both Aggregate and Finalize so the components map is stable
// across either path.
func (a *setDistinctValuesAggregator) freeze() {
	a.frozenHasResult = true
	var union uint64
	for m := range a.seen {
		union |= m
	}
	a.frozenMaskUnion = union
}

// Components returns {mask_union, popcount, labels} — the bitwise-OR
// union of every distinct mask seen, its popcount, and the decoded
// dictionary labels (same path as resolveMaskLabels). Reads the
// frozen mirror so streaming Finalize-reset does not erase the value.
// Empty input (no rows seen) collapses to {0, 0, []string{}}.
func (a *setDistinctValuesAggregator) Components() (map[string]any, error) {
	if !a.frozenHasResult {
		return map[string]any{
			"mask_union": uint64(0),
			"popcount":   0,
			"labels":     []string{},
		}, nil
	}
	return map[string]any{
		"mask_union": a.frozenMaskUnion,
		"popcount":   bits.OnesCount64(a.frozenMaskUnion),
		"labels":     resolveMaskLabels(a.frozenMaskUnion, a.dict),
	}, nil
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

// Compile-time interface locks — catches MetaAggregator drift at build
// time for the six set aggregators. Keeps the wiring grep-discoverable
// alongside the existing locks in aggregator.go / aggregator_cohort.go
// / aggregator_welford.go.
var (
	_ MetaAggregator = (*setUnionAggregator)(nil)
	_ MetaAggregator = (*setIntersectionAggregator)(nil)
	_ MetaAggregator = (*setFrequencyAggregator)(nil)
	_ MetaAggregator = (*setCardinalitySumAggregator)(nil)
	_ MetaAggregator = (*setCardinalityAvgAggregator)(nil)
	_ MetaAggregator = (*setDistinctValuesAggregator)(nil)
)

