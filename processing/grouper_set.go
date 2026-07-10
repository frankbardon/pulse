package processing

import (
	"math/bits"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Set-typed groupers — GROUP_SET_VALUE (atomic mask → one bucket per
// unique combination) and GROUP_SET_PER_ELEMENT (per-bit fan-out: one
// row → N buckets, one per selected element).
//
// GROUP_SET_VALUE is a normal single-key StreamingGrouper. PER_ELEMENT
// implements MultiKeyStreamingGrouper so the streaming orchestrator
// can fan a single record into multiple per-key buckets without
// buffering.

// resolveSetGrouperDict returns the field's dictionary, validating
// the field exists and is a set type. Construction with nil schema is
// tolerated for registry tests; runtime callers always pass a schema.
func resolveSetGrouperDict(grp *types.Group, schema *encoding.Schema) (*encoding.Dictionary, error) {
	if schema == nil {
		return nil, nil
	}
	if grp.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(grp.Type)+" requires field")
	}
	f := schema.Field(grp.Field)
	if f == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(grp.Type)+": unknown field "+grp.Field)
	}
	if !f.Type.IsSet() {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(grp.Type)+": field "+grp.Field+" is not a set type")
	}
	return f.Dictionary, nil
}

// Atomic mask = bucket. Key stringified as sorted label list
// (pipe-delimited). Empty mask = "" key, matching how other groupers
// stringify the zero value.

// setValueBucketStat carries the per-bucket aggregates Components()
// emits for GROUP_SET_VALUE: the raw mask value and the row count.
// labels are derived from mask at emission time via resolveMaskLabels
// (the same path the buffered Group() / streaming KeyFor route uses)
// so we never duplicate the dictionary-decode logic.
type setValueBucketStat struct {
	mask  uint64
	count int
}

type setValueGrouper struct {
	field   string
	dict    *encoding.Dictionary
	include *includeFilter

	// liveBuckets mirrors the post-Group / post-KeyForRow state so
	// Components() can emit per-bucket mask + count + decoded labels
	// without re-scanning the record set. Keyed by the same bucket
	// string the grouper emits via KeyFor so the buffered + streaming
	// code paths share a single source of truth. KeyFor itself does
	// NOT touch the counter (fused crosstab path) — see categoryGrouper
	// for the rationale.
	liveBuckets map[string]setValueBucketStat
	// nEmptyMask counts rows whose set field carried the empty-mask
	// value (0). The empty mask is a legitimate bucket key ("" after
	// label resolution), distinct from null/missing per
	// ErrGrouperKeyNull semantics. Surfaced verbatim on Components().
	nEmptyMask int
}

func newSetValueGrouper(grp *types.Group, schema *encoding.Schema) (Grouper, error) {
	dict, err := resolveSetGrouperDict(grp, schema)
	if err != nil {
		return nil, err
	}
	return &setValueGrouper{
		field:   grp.Field,
		dict:    dict,
		include: buildIncludeFilter(grp.Include),
	}, nil
}

// trackSetValueRow folds a single (mask, bucket key) observation into
// the set-value grouper's live components state. Called once per row-
// producing observation by both Group() (buffered) and KeyForRow
// (streaming) so the two code paths fill liveBuckets / nEmptyMask
// identically. The bucket key carries no mask signal (sorted labels
// joined by "|"), so we stash the raw mask alongside the count.
func (g *setValueGrouper) trackSetValueRow(mask uint64, key string) {
	if g.liveBuckets == nil {
		g.liveBuckets = make(map[string]setValueBucketStat)
	}
	stat := g.liveBuckets[key]
	stat.mask = mask
	stat.count++
	g.liveBuckets[key] = stat
	if mask == 0 {
		g.nEmptyMask++
	}
}

// KeyFor implements StreamableGrouper.KeyFor. The bound field's mask is
// decoded against the dictionary into sorted, pipe-joined labels —
// identical shape to the buffered Group() path. An empty mask is a
// valid bucket key of "" and does NOT trigger ErrGrouperKeyNull; only a
// null / missing field does.
func (g *setValueGrouper) KeyFor(r *Record) (string, error) {
	m, ok := r.SetValue(g.field)
	if !ok {
		return "", ErrGrouperKeyNull
	}
	labels := resolveMaskLabels(m, g.dict)
	sort.Strings(labels)
	key := strings.Join(labels, "|")
	if !includeAccepts(g.include, key) {
		return "", ErrGrouperKeyNull
	}
	return key, nil
}

// KeyForRow folds a single observation into the live components state
// after delegating bucket-key derivation to KeyFor. Mirrors the
// categoryGrouper pattern so MetaGrouper.Components() can emit per-
// bucket mask + count + decoded labels on the streaming path. KeyFor
// does the include-filter / null check; only filter-passing rows
// reach trackSetValueRow.
func (g *setValueGrouper) KeyForRow(r *Record, _ string) (string, bool, error) {
	key, ok, err := keyForOrSkip(g, r)
	if err != nil || !ok {
		return key, ok, err
	}
	m, ok2 := r.SetValue(g.field)
	if !ok2 {
		return key, ok, nil
	}
	g.trackSetValueRow(m, key)
	return key, ok, nil
}

func (g *setValueGrouper) Group(records []*Record, _ string) (map[string][]*Record, error) {
	groups := make(map[string][]*Record)
	// Reset live state so re-use of the grouper instance does not
	// surface stale buckets / nEmptyMask. Group() is the buffered
	// terminal pass; the streaming path drives KeyForRow exclusively.
	g.liveBuckets = nil
	g.nEmptyMask = 0
	for _, r := range records {
		key, ok, err := keyForOrSkip(g, r)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		m, mok := r.SetValue(g.field)
		if !mok {
			// keyForOrSkip already cleared null/skip cases; guard the
			// cast anyway so the tracker stays safe under contract drift.
			continue
		}
		g.trackSetValueRow(m, key)
		groups[key] = append(groups[key], r)
	}
	return groups, nil
}

// Components implements MetaGrouper. Returns the per-grouper schema
// declared in descriptor/capabilities_groupers.go for GROUP_SET_VALUE:
// {n_empty_mask, buckets: [{key, mask, count, labels}]}.
//
//   - n_empty_mask: rows whose mask was 0 (legitimate bucket key,
//     distinct from null/missing per ErrGrouperKeyNull semantics).
//   - buckets[].key: bucket key the grouper emits (sorted labels
//     joined by "|", empty string for the zero-mask bucket).
//   - buckets[].mask: raw set mask shared by every row in the bucket.
//   - buckets[].count: row count for that mask.
//   - buckets[].labels: dictionary-decoded labels for the bits set in
//     mask, sorted in ascending bit order (same path as resolveMaskLabels,
//     i.e. same path Rich() uses on the set aggregators).
//
// Reads g.liveBuckets / g.nEmptyMask, populated by Group() (buffered)
// or KeyForRow (streaming). Returns an empty buckets slice (not nil)
// when no rows were observed so the emitted shape stays consistent
// across cohorts. Buckets are sorted by key for deterministic output.
// The universal floor ({total_n, n_null}) is filled by the orchestrator
// — Components() returns operator-specific keys only.
func (g *setValueGrouper) Components() (map[string]any, error) {
	keys := make([]string, 0, len(g.liveBuckets))
	for k := range g.liveBuckets {
		keys = append(keys, k)
	}
	// Include order when an Include list is active (composite keys like
	// "MC|VISA"), otherwise sort.Strings (byte-identity for no-include).
	keys = orderKeysByInclude(g.include, keys)

	buckets := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		stat := g.liveBuckets[k]
		buckets = append(buckets, map[string]any{
			"key":    k,
			"mask":   stat.mask,
			"count":  stat.count,
			"labels": resolveMaskLabels(stat.mask, g.dict),
		})
	}
	return map[string]any{
		"n_empty_mask": g.nEmptyMask,
		"buckets":      buckets,
	}, nil
}

// IncludeFilter implements IncludeOrdered.
func (g *setValueGrouper) IncludeFilter() *includeFilter { return g.include }

// Per-bit fan-out. One row → one bucket per set bit. Empty-mask rows
// contribute to zero buckets (consistent with skipping nulls). The
// same record pointer is shared across all destination buckets — the
// buffered Group() path appends the pointer N times; the streaming
// path calls KeysForRow and the orchestrator drives UpdateRow per
// resulting bucket.

// setPerElementBucketStat carries the per-label aggregates Components()
// emits for GROUP_SET_PER_ELEMENT: the resolved label string, the
// observation count, and the bit position (== dictionary index) the
// label occupies in the bound field's dictionary.
type setPerElementBucketStat struct {
	label     string
	count     int
	dictIndex int
}

type setPerElementGrouper struct {
	dict    *encoding.Dictionary
	include *includeFilter

	// liveBuckets mirrors the post-Group / post-KeysForRow state per
	// label so Components() can emit per-label counts + dict indices
	// without re-scanning the record set. Keyed by the same label
	// string the grouper emits via KeysForRow so the buffered +
	// streaming code paths share a single source of truth.
	//
	// Note: this is a multi-key grouper — a single row contributes to
	// one bucket per selected bit, so sum(buckets[].count) may exceed
	// the orchestrator's TotalN (which counts each row once). The
	// total_label_observations key surfaces that sum verbatim.
	liveBuckets map[string]setPerElementBucketStat
}

func newSetPerElementGrouper(grp *types.Group, schema *encoding.Schema) (Grouper, error) {
	dict, err := resolveSetGrouperDict(grp, schema)
	if err != nil {
		return nil, err
	}
	return &setPerElementGrouper{
		dict:    dict,
		include: buildIncludeFilter(grp.Include),
	}, nil
}

// resolveMaskLabelsWithIndices walks the bits of mask and returns
// dictionary labels in ascending bit order plus the bit position
// (== dictionary index) each label occupies. Mirrors resolveMaskLabels
// (aggregator_set.go) but additionally returns the parallel index
// slice so trackSetPerElementRow can stash the dict index without a
// second dictionary lookup. Labels and indices are byte-equal to the
// resolveMaskLabels output by construction (same bit walk, same
// empty-label skip).
func resolveMaskLabelsWithIndices(mask uint64, dict *encoding.Dictionary) ([]string, []int) {
	if dict == nil {
		return []string{}, []int{}
	}
	dictLen := dict.Count()
	pop := bits.OnesCount64(mask)
	labels := make([]string, 0, pop)
	indices := make([]int, 0, pop)
	for i := 0; i < dictLen && i < 64; i++ {
		if mask&(uint64(1)<<uint(i)) == 0 {
			continue
		}
		label := dict.Resolve(uint32(i))
		if label == "" {
			continue
		}
		labels = append(labels, label)
		indices = append(indices, i)
	}
	return labels, indices
}

// trackSetPerElementRow folds a single (label, dict_index) observation
// into the per-element grouper's live components state. Called once
// per selected bit by both Group() (buffered) and KeysForRow
// (streaming) so the two code paths fill liveBuckets identically. The
// first-write wins on label / dictIndex (every subsequent observation
// for the same label would produce identical strings + index by
// construction, so first-write is exact).
func (g *setPerElementGrouper) trackSetPerElementRow(label string, dictIndex int) {
	if g.liveBuckets == nil {
		g.liveBuckets = make(map[string]setPerElementBucketStat)
	}
	stat, exists := g.liveBuckets[label]
	if !exists {
		stat = setPerElementBucketStat{label: label, dictIndex: dictIndex}
	}
	stat.count++
	g.liveBuckets[label] = stat
}

func (g *setPerElementGrouper) KeysForRow(r *Record, field string) ([]string, bool, error) {
	m, ok := r.SetValue(field)
	if !ok {
		return nil, false, nil
	}
	labels, indices := resolveMaskLabelsWithIndices(m, g.dict)
	if g.include.active() {
		keptLabels := labels[:0]
		keptIndices := indices[:0]
		for i, l := range labels {
			if _, ok := g.include.index(l); ok {
				keptLabels = append(keptLabels, l)
				keptIndices = append(keptIndices, indices[i])
			}
		}
		labels = keptLabels
		indices = keptIndices
	}
	if len(labels) == 0 {
		return nil, false, nil
	}
	for i, l := range labels {
		g.trackSetPerElementRow(l, indices[i])
	}
	return labels, true, nil
}

func (g *setPerElementGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	groups := make(map[string][]*Record)
	// Reset live state so re-use of the grouper instance does not
	// surface stale per-label counts.
	g.liveBuckets = nil
	for _, r := range records {
		labels, ok, err := g.KeysForRow(r, field)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, label := range labels {
			groups[label] = append(groups[label], r)
		}
	}
	return groups, nil
}

// Components implements MetaGrouper. Returns the per-grouper schema
// declared in descriptor/capabilities_groupers.go for
// GROUP_SET_PER_ELEMENT:
// {total_label_observations, buckets: [{key, label, count, dict_index}]}.
//
//   - total_label_observations: sum of buckets[].count across the
//     partition. MAY EXCEED the orchestrator's TotalN because each
//     row's set fans into one bucket per selected label — a row with
//     popcount 3 contributes 3 to this sum but 1 to TotalN. Empty
//     masks (popcount 0) contribute 0 here and 0 to TotalN.
//   - buckets[].key: bucket key (the label string itself, identical
//     to buckets[].label, kept for parity with single-key groupers).
//   - buckets[].label: resolved dictionary label for the bit.
//   - buckets[].count: observation count for that label.
//   - buckets[].dict_index: 0-indexed bit position of the label in
//     the bound field's dictionary (== dictionary order).
//
// Reads g.liveBuckets, populated by Group() (buffered) or KeysForRow
// (streaming). Returns an empty buckets slice (not nil) when no rows
// contribute. Buckets are sorted by dict_index ascending so the
// emitted ordering tracks dictionary order, matching the bit-walk
// every set-family operator already uses. The universal floor
// ({total_n, n_null}) is filled by the orchestrator — Components()
// returns operator-specific keys only.
func (g *setPerElementGrouper) Components() (map[string]any, error) {
	stats := make([]setPerElementBucketStat, 0, len(g.liveBuckets))
	for _, stat := range g.liveBuckets {
		stats = append(stats, stat)
	}
	if g.include.active() {
		// Include active — emit per-label buckets in include order (labels
		// are the include entries). Non-members are already filtered at
		// KeysForRow, so every present label is a member; the defensive
		// tail in orderKeysByInclude would only fire under contract drift.
		labels := make([]string, len(stats))
		byLabel := make(map[string]setPerElementBucketStat, len(stats))
		for i, stat := range stats {
			labels[i] = stat.label
			byLabel[stat.label] = stat
		}
		labels = orderKeysByInclude(g.include, labels)
		ordered := make([]setPerElementBucketStat, 0, len(labels))
		for _, l := range labels {
			ordered = append(ordered, byLabel[l])
		}
		stats = ordered
	} else {
		// No include — sort by dict_index ascending so emission order
		// matches the bit-walk every set-family operator (Rich, AGG_SET_*)
		// emits in. Byte-identity for the no-include path.
		sort.SliceStable(stats, func(i, j int) bool {
			return stats[i].dictIndex < stats[j].dictIndex
		})
	}

	totalObs := 0
	buckets := make([]map[string]any, 0, len(stats))
	for _, stat := range stats {
		totalObs += stat.count
		buckets = append(buckets, map[string]any{
			"key":        stat.label,
			"label":      stat.label,
			"count":      stat.count,
			"dict_index": stat.dictIndex,
		})
	}
	return map[string]any{
		"total_label_observations": totalObs,
		"buckets":                  buckets,
	}, nil
}

// IncludeFilter implements IncludeOrdered.
func (g *setPerElementGrouper) IncludeFilter() *includeFilter { return g.include }

// Compile-time interface locks. Catch interface drift at build time
// and keep the MetaGrouper wiring grep-discoverable.
var (
	_ MetaGrouper = (*setValueGrouper)(nil)
	_ MetaGrouper = (*setPerElementGrouper)(nil)

	_ IncludeOrdered = (*setValueGrouper)(nil)
	_ IncludeOrdered = (*setPerElementGrouper)(nil)
)
