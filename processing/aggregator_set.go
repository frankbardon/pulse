package processing

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Set-typed aggregators — AGG_SET_UNION, AGG_SET_INTERSECTION,
// AGG_SET_FREQUENCY, AGG_SET_CARDINALITY_SUM, AGG_SET_CARDINALITY_AVG,
// AGG_SET_DISTINCT_VALUES.
//
// Every one of them holds encoding.SetMask — the single in-memory set
// representation for every rung from set_u8 to set_u256 — and reads it
// through the single accessor, Record.SetMaskValue. There is ONE body
// per operator across all widths: no uint64 arm, no wide arm, no
// narrowing bridge. Bit i is set when dictionary entry i is selected.
// Bitwise OR (union) and AND (intersection) are associative +
// commutative at 256 bits exactly as they were at 64, so the
// orchestrator's per-shard and per-segment parallel reducers work for
// free. AGG_SET_INTERSECTION's margin is recompute-only (AND across
// cells ≠ AND across all rows in general); the others are summable.
//
// SetMask is a fixed [4]uint64 VALUE — it never allocates, copies by
// assignment, and aliases nothing — so an accumulating aggregator holds
// its own mask across a multi-record fold with no defensive copy and no
// reuse contract.
//
// UNION / INTERSECTION / FREQUENCY also satisfy RichAggregator: the
// scalar float64 they return through Aggregate / Finalize is a sensible
// fallback (popcount for the masks, max-bin count for frequency), and
// Rich() supplies the typed value (resolved labels, label→count map)
// that the processor / streaming path / crosstab cell builder routes
// into Response.Data and CrosstabResult.Matrix cells. Both payloads are
// dictionary-bounded, never 64-bounded, so a 206-member set resolves
// every selected member.

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

// setMaskComponentWords projects a SetMask into the wire form the
// mask_union / mask_intersection component keys carry: a []uint64 of
// exactly encoding.SetMaskWords words, LOW WORD FIRST (words[0] is bits
// 0–63), matching SetMask's documented word order.
//
// The length is fixed rather than trimmed to the rung's width so the
// key has one shape at every width — a set_u8 column and a set_u256
// column emit the same four-word array, and a consumer indexes
// words[bit/64] without first asking how wide the column was. A single
// uint64 could not carry a mask past bit 63: emitting its low word
// would have made every wide-set union quietly report a subset of the
// selections it actually saw, with popcount and labels beside it
// telling a different story.
func setMaskComponentWords(m encoding.SetMask) []uint64 {
	w := m.Words()
	return w[:]
}

// Bitwise OR across rows. Result = mask of every bit set in any row.
// Rich value = resolved labels sorted by bit order (ascending).

type setUnionAggregator struct {
	dict *encoding.Dictionary
	mask encoding.SetMask

	// frozen* mirror the post-Aggregate / post-Finalize state so
	// Components() works on both buffered and streaming code paths.
	// The set Finalize methods do not reset live state today, but the
	// frozen-mirror pattern keeps the components map stable across any
	// future Finalize-reset.
	frozenMask      encoding.SetMask
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
	a.mask = encoding.SetMask{}
	for _, r := range records {
		m, ok := r.SetMaskValue(field)
		if !ok {
			continue
		}
		a.mask = a.mask.Union(m)
	}
	a.frozenMask = a.mask
	a.frozenHasResult = true
	return float64(a.mask.PopCount()), nil
}

func (a *setUnionAggregator) UpdateRow(r *Record, field string) error {
	m, ok := r.SetMaskValue(field)
	if !ok {
		return nil
	}
	a.mask = a.mask.Union(m)
	return nil
}

func (a *setUnionAggregator) Finalize() (float64, error) {
	out := float64(a.mask.PopCount())
	a.frozenMask = a.mask
	a.frozenHasResult = true
	return out, nil
}

func (a *setUnionAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*setUnionAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_SET_UNION")
	}
	a.mask = a.mask.Union(b.mask)
	return nil
}

func (a *setUnionAggregator) Rich() (any, error) {
	return a.mask.Labels(a.dict), nil
}

// Components returns {mask_union, popcount, labels} — the union mask
// as its four constituent words, its popcount, and the decoded
// dictionary labels (same path as Rich()). Reads the frozen mirror
// stamped by Aggregate / Finalize so any future streaming
// Finalize-reset does not erase the values before Components() runs.
// Empty input (no rows seen) collapses to the zero mask, popcount 0 and
// an empty label slice — matches Rich()'s concrete-empty contract.
func (a *setUnionAggregator) Components() (map[string]any, error) {
	m := a.frozenMask
	if !a.frozenHasResult {
		m = encoding.SetMask{}
	}
	return map[string]any{
		"mask_union": setMaskComponentWords(m),
		"popcount":   m.PopCount(),
		"labels":     m.Labels(a.dict),
	}, nil
}

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
	mask encoding.SetMask
	seen bool

	// frozen* mirror the post-Aggregate / post-Finalize state. Mirrors
	// `seen` separately so Components() can distinguish "no rows seen"
	// (empty intersection) from "rows seen, intersection collapsed to
	// 0 mask" — both render as popcount 0 but the components map
	// reflects the raw mask in either case.
	frozenMask      encoding.SetMask
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
	a.mask = encoding.SetMask{}
	a.seen = false
	for _, r := range records {
		m, ok := r.SetMaskValue(field)
		if !ok {
			continue
		}
		if !a.seen {
			a.mask = m
			a.seen = true
			continue
		}
		a.mask = a.mask.Intersect(m)
	}
	a.frozenMask = a.mask
	a.frozenSeen = a.seen
	a.frozenHasResult = true
	return float64(a.mask.PopCount()), nil
}

func (a *setIntersectionAggregator) UpdateRow(r *Record, field string) error {
	m, ok := r.SetMaskValue(field)
	if !ok {
		return nil
	}
	if !a.seen {
		a.mask = m
		a.seen = true
		return nil
	}
	a.mask = a.mask.Intersect(m)
	return nil
}

func (a *setIntersectionAggregator) Finalize() (float64, error) {
	out := float64(a.mask.PopCount())
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
	a.mask = a.mask.Intersect(b.mask)
	return nil
}

func (a *setIntersectionAggregator) Rich() (any, error) {
	if !a.seen {
		return []string{}, nil
	}
	return a.mask.Labels(a.dict), nil
}

// Components returns {mask_intersection, popcount, labels} — the
// intersection mask as its four constituent words, its popcount, and
// the decoded dictionary labels (same path as Rich()). Empty input (no
// non-null row contributed) collapses to the zero mask, popcount 0 and
// an empty label slice — matches Rich()'s concrete-empty contract.
func (a *setIntersectionAggregator) Components() (map[string]any, error) {
	m := a.frozenMask
	if !a.frozenHasResult || !a.frozenSeen {
		m = encoding.SetMask{}
	}
	return map[string]any{
		"mask_intersection": setMaskComponentWords(m),
		"popcount":          m.PopCount(),
		"labels":            m.Labels(a.dict),
	}, nil
}

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
	frozenPerLabel       map[string]int
	frozenTotalLabelObs  int
	frozenDistinctLabels int
	frozenHasResult      bool
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
	// The counter slice is PRE-SIZED to the dictionary, capped at the
	// set address space (encoding.SetMaskBits = 256) rather than at 64.
	// This is a sizing decision only, not a correctness one: foldMask
	// grows the slice on demand and MergeOnline reconciles
	// differently-grown slices, so a 64-cap here still counts member
	// 205 correctly — it just reallocates on the first row that selects
	// past the cap. Falsifying the cap alone does not move any
	// assertion; the bit walk in foldMask is where correctness lives.
	if width > encoding.SetMaskBits {
		width = encoding.SetMaskBits
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
		m, ok := r.SetMaskValue(field)
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
	m, ok := r.SetMaskValue(field)
	if !ok {
		return nil
	}
	a.foldMask(m)
	return nil
}

// foldMask increments the per-bit counter for every selected member.
// The walk is encoding.SetMask.NextBit, which covers the whole 256-bit
// address space, so a member at bit 205 is counted exactly like a
// member at bit 5.
func (a *setFrequencyAggregator) foldMask(m encoding.SetMask) {
	for i, ok := m.NextBit(0); ok; i, ok = m.NextBit(i + 1) {
		if i >= len(a.counts) {
			grown := make([]uint64, i+1)
			copy(grown, a.counts)
			a.counts = grown
		}
		a.counts[i]++
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

// Sum of popcounts across contributing rows. Scalar; summable; no rich
// value. Popcount is SetMask.PopCount — the sum over all four words —
// so a row selecting more than 64 members contributes its true
// cardinality, not the population of its low word.

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
		m, ok := r.SetMaskValue(field)
		if !ok {
			continue
		}
		a.total += uint64(m.PopCount())
	}
	a.frozenTotal = a.total
	a.frozenHasResult = true
	return float64(a.total), nil
}

func (a *setCardinalitySumAggregator) UpdateRow(r *Record, field string) error {
	m, ok := r.SetMaskValue(field)
	if !ok {
		return nil
	}
	a.total += uint64(m.PopCount())
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
		m, ok := r.SetMaskValue(field)
		if !ok {
			continue
		}
		a.total += uint64(m.PopCount())
		a.n++
	}
	a.freeze()
	if a.n == 0 {
		return 0, nil
	}
	return float64(a.total) / float64(a.n), nil
}

func (a *setCardinalityAvgAggregator) UpdateRow(r *Record, field string) error {
	m, ok := r.SetMaskValue(field)
	if !ok {
		return nil
	}
	a.total += uint64(m.PopCount())
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

// Distinct exact mask values seen — treats each (possibly empty)
// combination as atomic. Mergeable via set union of seen masks.
//
// The seen map is keyed by encoding.SetMask itself: a [4]uint64 array
// in a struct is comparable, so two rows count as the same combination
// only when all 256 bits agree. Keying on a narrowed uint64 would have
// collapsed every pair of combinations differing only above bit 63 into
// one bucket.

type setDistinctValuesAggregator struct {
	dict *encoding.Dictionary
	seen map[encoding.SetMask]struct{}

	// frozen* mirror the post-Aggregate / post-Finalize state. The
	// declared schema surfaces the bitwise-OR union of every distinct
	// mask seen (alongside its popcount + decoded labels) — a strict
	// superset of "any bit any row contributed". The frozen union is
	// stamped at the freeze point so Components() returns a stable
	// snapshot even if a future Finalize-reset wipes `seen`.
	frozenMaskUnion encoding.SetMask
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
	a.seen = make(map[encoding.SetMask]struct{})
	for _, r := range records {
		m, ok := r.SetMaskValue(field)
		if !ok {
			continue
		}
		a.seen[m] = struct{}{}
	}
	a.freeze()
	return float64(len(a.seen)), nil
}

func (a *setDistinctValuesAggregator) UpdateRow(r *Record, field string) error {
	m, ok := r.SetMaskValue(field)
	if !ok {
		return nil
	}
	if a.seen == nil {
		a.seen = make(map[encoding.SetMask]struct{})
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
	var union encoding.SetMask
	for m := range a.seen {
		union = union.Union(m)
	}
	a.frozenMaskUnion = union
}

// Components returns {mask_union, popcount, labels} — the bitwise-OR
// union of every distinct mask seen (as its four constituent words),
// its popcount, and the decoded dictionary labels. Reads the frozen
// mirror so streaming Finalize-reset does not erase the value. Empty
// input (no rows seen) collapses to the zero mask, popcount 0 and an
// empty label slice.
func (a *setDistinctValuesAggregator) Components() (map[string]any, error) {
	m := a.frozenMaskUnion
	if !a.frozenHasResult {
		m = encoding.SetMask{}
	}
	return map[string]any{
		"mask_union": setMaskComponentWords(m),
		"popcount":   m.PopCount(),
		"labels":     m.Labels(a.dict),
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
		a.seen = make(map[encoding.SetMask]struct{}, len(b.seen))
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

// rejectSetFieldForNumericAggregator refuses an aggregator bound to a
// set column when that aggregator reads Record.NumericValue. AGG_
// FREQUENCY / AGG_MODE / AGG_DISTINCT_COUNT collect their input through
// the orchestrator's valueAggregator shortcut, i.e. a []float64 of
// "non-null numbers", and a set column has no such number: the decoder
// echoes the mask into the numeric map as the LOW 64 BITS for set_u128
// / set_u256 (encoding.decodeFixed) and as a float64 that has already
// lost precision above 2^53 for set_u64.
//
// Both untreated outcomes are SILENT. Before NumericValue refused set
// fields these three tallied the echo — a "modal value" that was really
// a bitmask reinterpreted as a quantity. After the refusal they collect
// nothing and report 0 distinct values over a fully answered column.
// Refusing at construction is the only outcome a caller can see.
//
// This matches what they now DECLARE: descriptor/capabilities_
// aggregators.go gives each of them AcceptsTypes: nonSetFieldTypes.
// AGG_COUNT / AGG_NULL_COUNT are deliberately NOT here — they ask
// presence, not value, through FieldPresent (processing/record_
// presence.go), and they keep every set rung in their declaration.
//
// A nil schema (registry probe construction) has nothing to check.
func rejectSetFieldForNumericAggregator(agg *types.Aggregation, schema *encoding.Schema) error {
	if schema == nil || agg == nil || agg.Field == "" {
		return nil
	}
	f := schema.Field(agg.Field)
	if f == nil || !f.Type.IsSet() {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		string(agg.Type)+": field "+agg.Field+" is a set column ("+f.Type.String()+
			"); its numeric value is the low 64 bits of the membership bitmask, not a quantity. Use AGG_SET_FREQUENCY for per-member counts, AGG_SET_DISTINCT_VALUES for distinct combinations, or AGG_COUNT for answered rows.",
		map[string]any{
			"field":      agg.Field,
			"type":       f.Type.String(),
			"aggregator": string(agg.Type),
			"alternates": []string{
				string(types.AGG_SET_FREQUENCY),
				string(types.AGG_SET_DISTINCT_VALUES),
				string(types.AGG_COUNT),
			},
		})
}
