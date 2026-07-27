package processing

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// formatRoundedNumeric formats a float64 in the same shape every
// numeric-key grouper uses today — integer-valued floats render via
// strconv.FormatInt, fractional values via FormatFloat with -1
// precision. Lifted out of categoryGrouper / roundedGrouper /
// rangeGrouper so the KeyFor and Group paths share a single
// canonicalisation site.
func formatRoundedNumeric(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// groupKeyResult collects the per-record bucket key plus a "used"
// flag used by every streamable grouper's Group implementation. The
// underlying KeyFor returns an ErrGrouperKeyNull sentinel for null
// rows; we translate that to used=false so Group continues to skip
// them silently (matching its long-standing contract).
func keyForOrSkip(g StreamableGrouper, r *Record) (string, bool, error) {
	key, err := g.KeyFor(r)
	if err != nil {
		if isGrouperKeyNull(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return key, true, nil
}

// isGrouperKeyNull recognises the ErrGrouperKeyNull sentinel without
// pulling stderrors into every grouper file.
func isGrouperKeyNull(err error) bool {
	return err == ErrGrouperKeyNull
}

// includeFilter is the ordered replacement for the order-discarding
// membership map that Group.Include used to compile down to. pos maps
// each accepted key to its FIRST occurrence index in the Group.Include
// slice — later duplicates are ignored, so a key buckets once, at its
// first position. A nil *includeFilter means "accept-all", preserving
// byte-identity against the pre-Include bucket set.
type includeFilter struct {
	pos map[string]int
}

// buildIncludeFilter materialises Group.Include into an ordered filter.
// Empty / nil Include returns nil — callers MUST treat nil as
// "accept-all" to preserve byte-identity against the pre-Include
// bucket set. Duplicate values keep their first-occurrence index.
func buildIncludeFilter(include []string) *includeFilter {
	if len(include) == 0 {
		return nil
	}
	pos := make(map[string]int, len(include))
	for i, v := range include {
		if _, seen := pos[v]; !seen {
			pos[v] = i
		}
	}
	return &includeFilter{pos: pos}
}

// active reports whether the filter constrains membership. A nil filter
// (accept-all) is inactive.
func (f *includeFilter) active() bool {
	return f != nil && len(f.pos) > 0
}

// index returns the include-order position of key and whether key is a
// member. A nil / inactive filter reports (0, false) — callers use
// active() to decide whether membership matters.
func (f *includeFilter) index(key string) (int, bool) {
	if f == nil {
		return 0, false
	}
	i, ok := f.pos[key]
	return i, ok
}

// includeAccepts reports whether key passes the include filter. nil
// filter accepts every key; non-nil rejects keys outside the set.
func includeAccepts(filter *includeFilter, key string) bool {
	if !filter.active() {
		return true
	}
	_, ok := filter.pos[key]
	return ok
}

// IncludeOrdered is the optional sibling of Grouper for groupers that
// carry a Group.Include inclusion list and can surface it as an ordered
// filter. GROUP_CATEGORY / GROUP_SET_VALUE / GROUP_SET_PER_ELEMENT
// implement it; every other built-in grouper (and any extension grouper
// that does not opt in) is treated as accept-all by includeFilterOf.
type IncludeOrdered interface {
	Grouper
	// IncludeFilter returns the grouper's ordered include filter, or nil
	// when no Include list was supplied (accept-all).
	IncludeFilter() *includeFilter
}

// includeFilterOf extracts the ordered include filter from a grouper,
// returning nil for groupers that don't implement IncludeOrdered
// (extension groupers fall through to sort.Strings via orderKeysByInclude).
func includeFilterOf(g Grouper) *includeFilter {
	if io, ok := g.(IncludeOrdered); ok {
		return io.IncludeFilter()
	}
	return nil
}

// orderKeysByInclude returns keys ordered for bucket emission. A nil /
// inactive filter funnels through sort.Strings — the byte-identity
// guarantee for the no-include path. An active filter sorts present
// member keys by their include index (first-occurrence order); any
// non-member straggler (which should not occur once KeyFor filters
// non-members, but is guarded defensively) sorts alphabetically after
// all members. keys is not mutated — a fresh slice is returned.
func orderKeysByInclude(f *includeFilter, keys []string) []string {
	out := make([]string, len(keys))
	copy(out, keys)
	if !f.active() {
		sort.Strings(out)
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		ii, iok := f.index(out[i])
		ji, jok := f.index(out[j])
		switch {
		case iok && jok:
			return ii < ji
		case iok != jok:
			// members sort before non-member stragglers
			return iok
		default:
			// both non-members: defensive alphabetical tail
			return out[i] < out[j]
		}
	})
	return out
}

// streamableGroup is the shared Group() implementation for streamable
// groupers — pass-1 computes per-record keys via KeyFor, pass-2 fills
// pre-sized per-key slices. Lifted so categoryGrouper / roundedGrouper /
// rangeGrouper / dateGrouper share the exact same partition shape and
// no key-format logic is duplicated.
func streamableGroup(g StreamableGrouper, records []*Record) (map[string][]*Record, error) {
	keys := make([]string, len(records))
	used := make([]bool, len(records))
	counts := make(map[string]int)
	for i, r := range records {
		key, ok, err := keyForOrSkip(g, r)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		keys[i] = key
		used[i] = true
		counts[key]++
	}
	groups := make(map[string][]*Record, len(counts))
	for k, n := range counts {
		groups[k] = make([]*Record, 0, n)
	}
	for i, r := range records {
		if !used[i] {
			continue
		}
		groups[keys[i]] = append(groups[keys[i]], r)
	}
	return groups, nil
}

type categoryGrouper struct {
	schema  *encoding.Schema
	field   string
	include *includeFilter

	// liveBuckets mirrors the post-Group / post-KeyForRow state so
	// Components() can emit per-bucket counts without re-scanning the
	// record set. Group() populates the full map from streamableGroup's
	// per-key bucket slices; KeyForRow increments per filter-passing
	// record on the streaming path. KeyFor itself does NOT touch the
	// counter (the fused crosstab path drives KeyFor directly and does
	// not feed Response.Components.Groupers today) — this keeps the
	// equivalence test in grouper_keyfor_test.go from double-counting
	// rows that flow through both Group() and bare KeyFor.
	liveBuckets map[string]int
}

func newCategoryGrouper(grp *types.Group, schema *encoding.Schema) (Grouper, error) {
	return &categoryGrouper{
		schema:  schema,
		field:   grp.Field,
		include: buildIncludeFilter(grp.Include),
	}, nil
}

// KeyFor implements StreamableGrouper.KeyFor. Categorical fields resolve
// the wire-format dictionary index to its label; non-categorical numeric
// fields stringify the value via formatRoundedNumeric so the output
// matches the historical Group() partition key byte-for-byte.
func (g *categoryGrouper) KeyFor(r *Record) (string, error) {
	v, ok := r.NumericValue(g.field)
	if !ok {
		return "", ErrGrouperKeyNull
	}
	var key string
	if g.schema != nil {
		if f := g.schema.Field(g.field); f != nil && f.Type.IsCategorical() && f.Dictionary != nil {
			key = f.Dictionary.Resolve(uint32(v))
			if key == "" {
				key = strconv.FormatUint(uint64(uint32(v)), 10)
			}
		}
	}
	if key == "" {
		key = formatRoundedNumeric(v)
	}
	if !includeAccepts(g.include, key) {
		return "", ErrGrouperKeyNull
	}
	return key, nil
}

// KeyForRow delegates to KeyFor — single source of truth for the
// category bucket key format. The field argument is accepted for
// StreamingGrouper compatibility; runtime callers always pass
// grp.Field which matches g.field by construction. Bumps the
// per-bucket live counter on success so MetaGrouper.Components()
// has a populated map after the streaming iteration terminates.
func (g *categoryGrouper) KeyForRow(r *Record, _ string) (string, bool, error) {
	key, ok, err := keyForOrSkip(g, r)
	if err != nil || !ok {
		return key, ok, err
	}
	if g.liveBuckets == nil {
		g.liveBuckets = make(map[string]int)
	}
	g.liveBuckets[key]++
	return key, ok, nil
}

func (g *categoryGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	groups, err := streamableGroup(g, records)
	if err != nil {
		return nil, err
	}
	// Mirror the bucket population into liveBuckets so Components()
	// can emit per-bucket counts on the buffered path. Allocates
	// exactly len(groups) entries — no oversize, no resizing.
	g.liveBuckets = make(map[string]int, len(groups))
	for k, v := range groups {
		g.liveBuckets[k] = len(v)
	}
	return groups, nil
}

// Components implements MetaGrouper. Returns the per-grouper schema
// declared in descriptor/capabilities_groupers.go for GROUP_CATEGORY:
// {dict_size, buckets: [{key, label, count}]}. dict_size is the
// cardinality of the schema's categorical dictionary on the partition
// field (zero for non-categorical fields). buckets are sorted by key
// for deterministic emission; key and label match the bucket
// identity, which IS the dictionary label for categorical fields and
// the stringified numeric value for non-categorical inputs.
//
// Reads g.liveBuckets, populated by Group() (buffered) or KeyForRow
// (streaming). Returns an empty buckets slice (not nil) when no rows
// were observed so the emitted shape stays consistent across cohorts.
// The universal floor ({total_n, n_null}) is filled by the orchestrator
// — Components() returns operator-specific keys only.
func (g *categoryGrouper) Components() (map[string]any, error) {
	dictSize := 0
	if g.schema != nil {
		if f := g.schema.Field(g.field); f != nil && f.Type.IsCategorical() && f.Dictionary != nil {
			dictSize = f.Dictionary.Count()
		}
	}

	// Order bucket keys for deterministic emission — include order when
	// an Include list is active, otherwise sort.Strings (byte-identity
	// for the no-include path). An empty live map collapses to an empty
	// buckets slice — not nil, since downstream JSON consumers prefer a
	// concrete `[]` over `null`.
	keys := make([]string, 0, len(g.liveBuckets))
	for k := range g.liveBuckets {
		keys = append(keys, k)
	}
	keys = orderKeysByInclude(g.include, keys)

	buckets := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		buckets = append(buckets, map[string]any{
			"key":   k,
			"label": k,
			"count": g.liveBuckets[k],
		})
	}
	return map[string]any{
		"dict_size": dictSize,
		"buckets":   buckets,
	}, nil
}

// IncludeFilter implements IncludeOrdered.
func (g *categoryGrouper) IncludeFilter() *includeFilter { return g.include }

// roundedBucketStat tracks the per-bucket aggregates Components()
// emits for GROUP_ROUNDED: the rounded scalar value (low) and the
// row count. The "high" boundary collapses to the same scalar for
// GROUP_ROUNDED (rounded values are points on the value axis, not
// half-open ranges — the Components contract still emits {low,
// high} for caller parity with GROUP_RANGE).
type roundedBucketStat struct {
	low   float64
	count int
}

type roundedGrouper struct {
	field    string
	interval float64

	// liveBuckets mirrors the post-Group / post-KeyForRow state so
	// Components() can emit per-bucket counts + rounded edges without
	// re-scanning the record set. Keyed by the same bucket string the
	// grouper emits via KeyFor so the buffered + streaming code paths
	// share a single source of truth.
	liveBuckets map[string]roundedBucketStat
}

func newRoundedGrouper(grp *types.Group, _ *encoding.Schema) (Grouper, error) {
	if grp.Interval <= 0 {
		grp.Interval = 1 // default to 1
	}
	return &roundedGrouper{field: grp.Field, interval: grp.Interval}, nil
}

// KeyFor implements StreamableGrouper.KeyFor. Floor-rounds the value to
// the configured interval and emits the stringified rounded value via
// formatRoundedNumeric — identical to today's Group() partition key.
func (g *roundedGrouper) KeyFor(r *Record) (string, error) {
	v, ok := r.NumericValue(g.field)
	if !ok {
		return "", ErrGrouperKeyNull
	}
	rounded := math.Floor(v/g.interval) * g.interval
	return formatRoundedNumeric(rounded), nil
}

// KeyForRow folds a single observation into the live components state
// after delegating bucket-key derivation to KeyFor. Mirrors the
// categoryGrouper / dateGrouper pattern so MetaGrouper.Components()
// can emit per-bucket counts on the streaming path.
func (g *roundedGrouper) KeyForRow(r *Record, _ string) (string, bool, error) {
	key, ok, err := keyForOrSkip(g, r)
	if err != nil || !ok {
		return key, ok, err
	}
	v, ok2 := r.NumericValue(g.field)
	if !ok2 {
		return key, ok, nil
	}
	if g.liveBuckets == nil {
		g.liveBuckets = make(map[string]roundedBucketStat)
	}
	rounded := math.Floor(v/g.interval) * g.interval
	stat := g.liveBuckets[key]
	stat.low = rounded
	stat.count++
	g.liveBuckets[key] = stat
	return key, ok, nil
}

func (g *roundedGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	groups, err := streamableGroup(g, records)
	if err != nil {
		return nil, err
	}
	// Mirror the bucket population into liveBuckets so Components()
	// can emit per-bucket counts + rounded edges on the buffered path.
	// Re-derive count from len(slice) and pull the rounded scalar from
	// the first row of each bucket (every row in a bucket rounds to
	// the same value by construction).
	g.liveBuckets = make(map[string]roundedBucketStat, len(groups))
	for key, bucket := range groups {
		if len(bucket) == 0 {
			continue
		}
		for _, r := range bucket {
			v, ok := r.NumericValue(field)
			if !ok {
				continue
			}
			rounded := math.Floor(v/g.interval) * g.interval
			g.liveBuckets[key] = roundedBucketStat{low: rounded, count: len(bucket)}
			break
		}
	}
	return groups, nil
}

// Components implements MetaGrouper. Returns the per-grouper schema
// declared in descriptor/capabilities_groupers.go for GROUP_ROUNDED:
// {precision, edges: [...], buckets: [{key, low, high, count}]}.
// precision is the rounding increment (Group.Interval). edges are the
// sorted rounded scalars, one per bucket. Per-bucket low == high ==
// the rounded scalar — rounded values are points on the value axis,
// not half-open ranges; {low, high} stays in the emitted shape for
// caller parity with GROUP_RANGE.
//
// Reads g.liveBuckets, populated by Group() (buffered) or KeyForRow
// (streaming). Returns an empty buckets / edges slice (not nil) when
// no rows were observed so the emitted shape stays consistent across
// cohorts. The universal floor ({total_n, n_null}) is filled by the
// orchestrator.
func (g *roundedGrouper) Components() (map[string]any, error) {
	keys := make([]string, 0, len(g.liveBuckets))
	for k := range g.liveBuckets {
		keys = append(keys, k)
	}
	// Sort by the bucket's rounded scalar (numerically), not by string
	// key — keeps edges ascending and matches the GROUP_RANGE shape.
	sort.SliceStable(keys, func(i, j int) bool {
		return g.liveBuckets[keys[i]].low < g.liveBuckets[keys[j]].low
	})

	buckets := make([]map[string]any, 0, len(keys))
	edges := make([]float64, 0, len(keys))
	for _, k := range keys {
		stat := g.liveBuckets[k]
		buckets = append(buckets, map[string]any{
			"key":   k,
			"low":   stat.low,
			"high":  stat.low,
			"count": stat.count,
		})
		edges = append(edges, stat.low)
	}
	return map[string]any{
		"precision": g.interval,
		"edges":     edges,
		"buckets":   buckets,
	}, nil
}

// rangeBucketStat tracks the per-bucket aggregates Components() emits
// for GROUP_RANGE: the half-open [low, high) boundaries and the row
// count. low is the floor-rounded bucket start; high is low+interval
// (subject to the same FP drift the KeyFor partition key exhibits).
type rangeBucketStat struct {
	low   float64
	high  float64
	count int
}

type rangeGrouper struct {
	field    string
	interval float64

	// liveBuckets mirrors the post-Group / post-KeyForRow state so
	// Components() can emit per-bucket counts + edges without re-
	// scanning the record set. Keyed by the same bucket string the
	// grouper emits via KeyFor so the buffered + streaming code paths
	// share a single source of truth.
	liveBuckets map[string]rangeBucketStat
	// rangeMin / rangeMax / liveRangeSet capture the smallest /
	// largest non-null value observed. The grouper has no configured
	// range bounds today, so these mirror the data: every observed
	// value falls inside [rangeMin, rangeMax] by definition, and
	// underflow_count / overflow_count stay at zero.
	rangeMin     float64
	rangeMax     float64
	liveRangeSet bool
	// underflowCount / overflowCount stay at zero on the current
	// rangeGrouper (auto-detected bounds), but the schema requires
	// the keys. Tracked as fields so a future configured-range
	// extension can populate them without touching Components().
	underflowCount int
	overflowCount  int
}

func newRangeGrouper(grp *types.Group, _ *encoding.Schema) (Grouper, error) {
	if grp.Interval <= 0 {
		grp.Interval = 1 // default to 1
	}
	return &rangeGrouper{field: grp.Field, interval: grp.Interval}, nil
}

// KeyFor implements StreamableGrouper.KeyFor. Emits a "lo-hi" range
// string covering the half-open bin [low, low+interval). low+interval
// can drift due to FP error on non-power-of-two intervals; the format
// matches the historical Group() output exactly because both call into
// formatRoundedNumeric on identical inputs.
func (g *rangeGrouper) KeyFor(r *Record) (string, error) {
	v, ok := r.NumericValue(g.field)
	if !ok {
		return "", ErrGrouperKeyNull
	}
	low := math.Floor(v/g.interval) * g.interval
	high := low + g.interval
	return formatRoundedNumeric(low) + "-" + formatRoundedNumeric(high), nil
}

// KeyForRow folds a single observation into the live components state
// after delegating bucket-key derivation to KeyFor. Mirrors the
// categoryGrouper / dateGrouper pattern so MetaGrouper.Components()
// can emit per-bucket counts + range_min/max on the streaming path.
func (g *rangeGrouper) KeyForRow(r *Record, _ string) (string, bool, error) {
	key, ok, err := keyForOrSkip(g, r)
	if err != nil || !ok {
		return key, ok, err
	}
	v, ok2 := r.NumericValue(g.field)
	if !ok2 {
		return key, ok, nil
	}
	g.trackRangeRow(v, key)
	return key, ok, nil
}

// trackRangeRow folds a single (value, bucket key) observation into the
// range grouper's live components state. Called once per row-producing
// observation by both Group() (buffered) and KeyForRow (streaming) so
// the two code paths fill liveBuckets / rangeMin / rangeMax identically.
func (g *rangeGrouper) trackRangeRow(v float64, key string) {
	if g.liveBuckets == nil {
		g.liveBuckets = make(map[string]rangeBucketStat)
	}
	stat, exists := g.liveBuckets[key]
	if !exists {
		low := math.Floor(v/g.interval) * g.interval
		stat = rangeBucketStat{low: low, high: low + g.interval}
	}
	stat.count++
	g.liveBuckets[key] = stat
	if !g.liveRangeSet || v < g.rangeMin {
		g.rangeMin = v
	}
	if !g.liveRangeSet || v > g.rangeMax {
		g.rangeMax = v
	}
	g.liveRangeSet = true
}

func (g *rangeGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	groups, err := streamableGroup(g, records)
	if err != nil {
		return nil, err
	}
	// Mirror the bucket population into liveBuckets. Group()'s
	// streamableGroup path discards the per-key counts after building
	// the per-key slices, so we walk each bucket's rows once to derive
	// the bucket boundaries (every row in a bucket shares the same
	// floor-rounded low by construction) and feed range_min / range_max.
	g.liveBuckets = make(map[string]rangeBucketStat, len(groups))
	g.liveRangeSet = false
	for key, bucket := range groups {
		if len(bucket) == 0 {
			continue
		}
		for _, r := range bucket {
			v, ok := r.NumericValue(field)
			if !ok {
				continue
			}
			g.trackRangeRow(v, key)
		}
	}
	return groups, nil
}

// Components implements MetaGrouper. Returns the per-grouper schema
// declared in descriptor/capabilities_groupers.go for GROUP_RANGE:
// {interval, range_min, range_max, n_buckets, edges: [...], buckets:
// [{key, low, high, count}], underflow_count, overflow_count}.
// interval mirrors the configured Group.Interval. range_min /
// range_max are the smallest / largest non-null values observed (the
// rangeGrouper has no configured range bounds today; embedders that
// add a configured-range extension fill the overflow / underflow
// counters via trackRangeRow's existing fields).
//
// edges are the sorted unique bucket boundaries — sorted ascending
// lows plus the top bucket's high — giving callers a complete bin
// edge list (n_buckets + 1 entries when buckets are non-adjacent,
// fewer when the inner highs collapse onto neighbouring lows).
//
// Reads g.liveBuckets, populated by Group() (buffered) or KeyForRow
// (streaming). Returns empty slices (not nil) when no rows were
// observed so the emitted shape stays consistent across cohorts. The
// universal floor ({total_n, n_null}) is filled by the orchestrator.
func (g *rangeGrouper) Components() (map[string]any, error) {
	keys := make([]string, 0, len(g.liveBuckets))
	for k := range g.liveBuckets {
		keys = append(keys, k)
	}
	// Sort by bucket's low edge (numerically) so the emitted buckets +
	// edges list ascend monotonically regardless of the lexical key
	// shape (negative bins like "-20--10" must order before "-10-0").
	sort.SliceStable(keys, func(i, j int) bool {
		return g.liveBuckets[keys[i]].low < g.liveBuckets[keys[j]].low
	})

	buckets := make([]map[string]any, 0, len(keys))
	edges := make([]float64, 0, len(keys)+1)
	for i, k := range keys {
		stat := g.liveBuckets[k]
		buckets = append(buckets, map[string]any{
			"key":   k,
			"low":   stat.low,
			"high":  stat.high,
			"count": stat.count,
		})
		edges = append(edges, stat.low)
		// Append the top-bucket high to close the edge list. Interior
		// highs collapse onto the next bucket's low by construction
		// (low_{i+1} = low_i + interval = high_i) so emitting the
		// single trailing high keeps the edge list deduplicated and
		// monotonically ascending.
		if i == len(keys)-1 {
			edges = append(edges, stat.high)
		}
	}

	rangeMin := 0.0
	rangeMax := 0.0
	if g.liveRangeSet {
		rangeMin = g.rangeMin
		rangeMax = g.rangeMax
	}

	return map[string]any{
		"interval":        g.interval,
		"range_min":       rangeMin,
		"range_max":       rangeMax,
		"n_buckets":       len(buckets),
		"edges":           edges,
		"buckets":         buckets,
		"underflow_count": g.underflowCount,
		"overflow_count":  g.overflowCount,
	}, nil
}

// quantileBucketStat tracks the per-bucket aggregates Components()
// emits for GROUP_QUANTILE: the [low, high] value range and the row
// count. Bracket bounds are inclusive at both ends in the strict-
// streaming sense (the lowest value in the bucket is "low", the
// highest is "high"); adjacent buckets share an edge by construction.
type quantileBucketStat struct {
	low   float64
	high  float64
	count int
}

type quantileGrouper struct {
	buckets int

	// frozen* mirrors stamp the post-Group state so Components() can
	// emit operator-specific keys without re-scanning the record set.
	// GROUP_QUANTILE is non-mergeable (needs a sorted view of the full
	// input) — streaming chunks MUST NOT emit components per the
	// per-chunk omission contract. The runtime's terminal buffered
	// flush is the only call site that observes a populated frozen
	// state; calls before Group() collapse to (nil, nil) per the
	// universal-floor convention.
	frozenFinalized bool
	frozenBuckets   []quantileBucketStat
	frozenKeys      []string
	frozenEdges     []float64
}

func newQuantileGrouper(grp *types.Group, _ *encoding.Schema) (Grouper, error) {
	buckets := int(grp.Interval)
	if buckets <= 0 {
		buckets = 4
	}
	return &quantileGrouper{buckets: buckets}, nil
}

// quantileMethod returns the interpolation method label emitted on
// Components(). Kept in sync with AGG_PERCENTILE's percentileMethodLinear
// so a single rename re-aligns both surfaces.
func quantileMethod() string {
	return percentileMethodLinear
}

type indexedValue struct {
	record *Record
	value  float64
}

// quantileGrouper deliberately does NOT implement StreamableGrouper.
// Bucket assignment depends on a sorted view of the full record set
// (each row's rank is set by every other row's value), so per-record
// KeyFor would lie. The fused crosstab path detects the missing
// interface via type assertion and falls back to the buffered
// PartitionByAxis route.

func (g *quantileGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	groups := make(map[string][]*Record)

	// Reset frozen state on every Group() call so re-use of the
	// grouper instance does not surface stale buckets / edges.
	g.frozenFinalized = true
	g.frozenBuckets = nil
	g.frozenKeys = nil
	g.frozenEdges = nil

	// Collect non-null (record, value) pairs.
	items := make([]indexedValue, 0, len(records))
	for _, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			continue
		}
		items = append(items, indexedValue{record: r, value: v})
	}

	n := len(items)
	if n == 0 {
		return groups, nil
	}

	// Sort by value.
	sort.Slice(items, func(i, j int) bool {
		return items[i].value < items[j].value
	})

	// Determine key prefix.
	prefix := "B"
	switch g.buckets {
	case 4:
		prefix = "Q"
	case 10:
		prefix = "D"
	case 100:
		prefix = "P"
	}

	// Pass 1: count records per bucket.
	bucketCounts := make([]int, g.buckets)
	for rank := range n {
		bucket := rank * g.buckets / n
		if bucket >= g.buckets {
			bucket = g.buckets - 1
		}
		bucketCounts[bucket]++
	}

	// Pre-allocate per-key slices to known final size.
	bucketKeys := make([]string, g.buckets)
	for b := 0; b < g.buckets; b++ {
		if bucketCounts[b] == 0 {
			continue
		}
		key := prefix + strconv.Itoa(b+1)
		bucketKeys[b] = key
		groups[key] = make([]*Record, 0, bucketCounts[b])
	}

	// Track per-bucket low/high in rank order. Items are sorted, so
	// the first item to land in a bucket sets the low; the last sets
	// the high. Stash on a parallel slice indexed by bucket id.
	stats := make([]quantileBucketStat, g.buckets)
	statsSet := make([]bool, g.buckets)

	// Pass 2: assign each item to its pre-sized bucket, stamping
	// the per-bucket {low, high} as we go.
	for rank, item := range items {
		bucket := rank * g.buckets / n
		if bucket >= g.buckets {
			bucket = g.buckets - 1
		}
		key := bucketKeys[bucket]
		groups[key] = append(groups[key], item.record)
		if !statsSet[bucket] {
			stats[bucket].low = item.value
			statsSet[bucket] = true
		}
		stats[bucket].high = item.value
		stats[bucket].count++
	}

	// Stamp frozen mirrors in bucket-id order so Components() emits
	// the buckets sorted by ascending value. Empty buckets are
	// dropped — the grouper never partitions a row into them.
	keys := make([]string, 0, g.buckets)
	bucketStats := make([]quantileBucketStat, 0, g.buckets)
	edges := make([]float64, 0, g.buckets+1)
	for b := 0; b < g.buckets; b++ {
		if !statsSet[b] {
			continue
		}
		keys = append(keys, bucketKeys[b])
		bucketStats = append(bucketStats, stats[b])
		edges = append(edges, stats[b].low)
	}
	// Close the edge list with the top bucket's high so callers have
	// the complete cutpoint sequence. Mirrors GROUP_RANGE's emission.
	if len(bucketStats) > 0 {
		edges = append(edges, bucketStats[len(bucketStats)-1].high)
	}
	g.frozenKeys = keys
	g.frozenBuckets = bucketStats
	g.frozenEdges = edges

	return groups, nil
}

// Components implements MetaGrouper. Returns the per-grouper schema
// declared in descriptor/capabilities_groupers.go for GROUP_QUANTILE:
// {n_quantiles, method, edges: [...], buckets: [{key, low, high,
// count}]}. n_quantiles mirrors the configured bucket count
// (Group.Interval, defaulting to 4 = quartiles). method mirrors
// AGG_PERCENTILE's interpolation enum (currently "linear" only) so
// embedders can re-use one method-string-table across both operators.
// edges are the sorted unique cutpoints separating adjacent quantile
// buckets — ascending bucket lows plus the top bucket's high.
//
// ComponentsMergeability=none — quantile cutoffs depend on a sorted
// view of the full input. The streaming path omits per-chunk
// components; the buffered exit emits them. Returns (nil, nil) when
// Group() has not yet been called
// (no frozen state) so the orchestrator's universal-floor pass still
// emits TotalN / NNull.
func (g *quantileGrouper) Components() (map[string]any, error) {
	if !g.frozenFinalized {
		return nil, nil
	}
	buckets := make([]map[string]any, 0, len(g.frozenBuckets))
	for i, stat := range g.frozenBuckets {
		buckets = append(buckets, map[string]any{
			"key":   g.frozenKeys[i],
			"low":   stat.low,
			"high":  stat.high,
			"count": stat.count,
		})
	}
	edges := g.frozenEdges
	if edges == nil {
		edges = []float64{}
	}
	return map[string]any{
		"n_quantiles": g.buckets,
		"method":      quantileMethod(),
		"edges":       edges,
		"buckets":     buckets,
	}, nil
}

type dateGroupParams struct {
	Component    string `json:"component"`
	FiscalOffset *int   `json:"fiscal_offset,omitempty"`
}

var validDateGroupComponents = map[string]bool{
	"year": true, "quarter": true, "month": true,
	"week": true, "day": true, "day_of_week": true,
}

// fiscalComponents lists the only components that meaningfully accept a
// fiscal_offset. Month/week/day/day_of_week buckets do not shift under a
// fiscal calendar, so combining them with fiscal_offset is rejected at
// construction time.
var fiscalComponents = map[string]bool{
	"year": true, "quarter": true,
}

type dateGrouper struct {
	field        string
	component    string
	fiscalOffset int // 0 = calendar; non-zero = FY starts at month (((offset%12)+12)%12)+1

	// liveBuckets mirrors the post-Group / post-KeyForRow state per
	// bucket key. count tracks rows assigned to the bucket; periodStart
	// / periodEnd are the canonical ISO-8601 boundaries derived from
	// the first row observed in that bucket (every row in the bucket
	// shares the same boundaries by construction). KeyFor itself does
	// NOT touch the live map (fused crosstab path) — see categoryGrouper
	// for the rationale.
	liveBuckets map[string]dateBucketStat
	// rangeMinDay / rangeMaxDay carry the earliest / latest days-
	// since-epoch value observed across all rows. Used to populate
	// {range_start, range_end} on Components() in the same ISO-8601
	// formatter family that produces per-bucket period_start /
	// period_end. liveRangeSet flips to true on the first non-null row
	// so an empty-input grouper emits empty strings rather than the
	// zero-day "1970-01-01" default.
	rangeMinDay  int64
	rangeMaxDay  int64
	liveRangeSet bool
}

// dateBucketStat carries the per-bucket aggregates Components() emits
// for GROUP_DATE: row count plus ISO-8601 period boundaries. The
// boundary strings are computed from the first row to land in the
// bucket (every subsequent row in the same bucket would produce
// identical strings by construction, so first-write is exact).
type dateBucketStat struct {
	count       int
	periodStart string
	periodEnd   string
}

// dateRangeBoundaries returns ISO-8601 (YYYY-MM-DD) strings for the
// start and end of the calendar period containing t under the
// configured granularity. Day collapses to a single date; week
// returns Monday..Sunday of the ISO week; month / quarter / year
// return the first day and last day of their span. day_of_week and
// the fiscal variants do not have a contiguous calendar range, so
// they return empty strings — Components() emits them verbatim.
func (g *dateGrouper) dateRangeBoundaries(t time.Time) (string, string) {
	if g.fiscalOffset != 0 {
		// Fiscal-offset year / quarter spans rotate off the calendar
		// year. The Components contract sticks to calendar ISO-8601
		// boundaries; embedders reading FY-prefixed bucket keys can
		// reconstruct the span from the key + offset. Return empty
		// strings rather than guess the wrong calendar boundary.
		return "", ""
	}
	switch g.component {
	case "day":
		s := t.Format("2006-01-02")
		return s, s
	case "week":
		// ISO week starts Monday. Go's time.Weekday() returns Sunday=0,
		// Monday=1, ..., Saturday=6. Shift so Monday=0, Sunday=6, then
		// subtract to land on Monday.
		wd := int(t.Weekday())
		// Normalize so Monday=0 ... Sunday=6.
		mondayOffset := (wd + 6) % 7
		monday := t.AddDate(0, 0, -mondayOffset)
		sunday := monday.AddDate(0, 0, 6)
		return monday.Format("2006-01-02"), sunday.Format("2006-01-02")
	case "month":
		first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		last := first.AddDate(0, 1, -1)
		return first.Format("2006-01-02"), last.Format("2006-01-02")
	case "quarter":
		qStartMonth := time.Month(((int(t.Month())-1)/3)*3 + 1)
		first := time.Date(t.Year(), qStartMonth, 1, 0, 0, 0, 0, time.UTC)
		last := first.AddDate(0, 3, -1)
		return first.Format("2006-01-02"), last.Format("2006-01-02")
	case "year":
		first := time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
		last := time.Date(t.Year(), time.December, 31, 0, 0, 0, 0, time.UTC)
		return first.Format("2006-01-02"), last.Format("2006-01-02")
	}
	// day_of_week and any unknown component — no calendar span.
	return "", ""
}

// trackDateRow folds a single (record day, bucket key) observation
// into the date grouper's live components state. Called once per
// row-producing observation by both Group() (buffered) and KeyForRow
// (streaming) so the two code paths fill liveBuckets identically.
func (g *dateGrouper) trackDateRow(dayUnix int64, key string) {
	if g.liveBuckets == nil {
		g.liveBuckets = make(map[string]dateBucketStat)
	}
	stat, exists := g.liveBuckets[key]
	if !exists {
		t := time.Unix(dayUnix*86400, 0).UTC()
		ps, pe := g.dateRangeBoundaries(t)
		stat = dateBucketStat{periodStart: ps, periodEnd: pe}
	}
	stat.count++
	g.liveBuckets[key] = stat
	if !g.liveRangeSet || dayUnix < g.rangeMinDay {
		g.rangeMinDay = dayUnix
	}
	if !g.liveRangeSet || dayUnix > g.rangeMaxDay {
		g.rangeMaxDay = dayUnix
	}
	g.liveRangeSet = true
}

func newDateGrouper(grp *types.Group, _ *encoding.Schema) (Grouper, error) {
	component := "month"
	fiscalOffset := 0
	if len(grp.Params) > 0 {
		var params dateGroupParams
		if err := json.Unmarshal(grp.Params, &params); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("invalid GROUP_DATE params: %v", err))
		}
		if params.Component != "" {
			component = params.Component
		}
		if params.FiscalOffset != nil {
			fiscalOffset = *params.FiscalOffset
		}
	}

	if !validDateGroupComponents[component] {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("invalid date group component %q: must be one of year, quarter, month, week, day, day_of_week", component))
	}

	if fiscalOffset < -11 || fiscalOffset > 11 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("invalid GROUP_DATE fiscal_offset %d: must be in range [-11, 11]", fiscalOffset))
	}
	if fiscalOffset != 0 && !fiscalComponents[component] {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("GROUP_DATE fiscal_offset only applies to component=year or component=quarter, got %q", component))
	}

	return &dateGrouper{field: grp.Field, component: component, fiscalOffset: fiscalOffset}, nil
}

// fiscalYearQuarter returns the fiscal year (end-year convention) and the
// 1-indexed fiscal quarter for a calendar date under a given fiscal offset.
// Offset normalisation: start_month_1idx = ((offset%12)+12)%12 + 1, so
// offset=3 and offset=-9 both yield start_month=April; offset=9 and
// offset=-3 both yield start_month=October.
func fiscalYearQuarter(t time.Time, offset int) (int, int) {
	startMonth := ((offset%12)+12)%12 + 1
	calYear := t.Year()
	calMonth := int(t.Month())
	fy := calYear
	if startMonth != 1 && calMonth >= startMonth {
		fy = calYear + 1
	}
	elapsed := (calMonth - startMonth + 12) % 12
	fq := elapsed/3 + 1
	return fy, fq
}

// formatDateKey is the shared bucket-key formatter for dateGrouper. Both
// the buffered Group() path and the streaming KeyFor / KeyForRow paths
// call into it so the date / fiscal_offset formatting lives in one
// place. Returns the canonical bucket key for the configured component.
func (g *dateGrouper) formatDateKey(t time.Time) string {
	switch g.component {
	case "year":
		if g.fiscalOffset != 0 {
			fy, _ := fiscalYearQuarter(t, g.fiscalOffset)
			return fmt.Sprintf("FY%d", fy)
		}
		return fmt.Sprintf("%d", t.Year())
	case "quarter":
		if g.fiscalOffset != 0 {
			fy, fq := fiscalYearQuarter(t, g.fiscalOffset)
			return fmt.Sprintf("FY%d-Q%d", fy, fq)
		}
		return fmt.Sprintf("%d-Q%d", t.Year(), (int(t.Month())-1)/3+1)
	case "month":
		return t.Format("2006-01")
	case "week":
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	case "day":
		return t.Format("2006-01-02")
	case "day_of_week":
		return t.Weekday().String()
	}
	// Construction rejects unknown components, so this is unreachable
	// in normal use. Return empty string to keep the partition stable
	// if the contract ever drifts.
	return ""
}

// KeyFor implements StreamableGrouper.KeyFor. Per-record date
// classification is fully local — the same calendar conversion the
// buffered Group() path runs, lifted into formatDateKey. GROUP_DATE
// itself remains marked non-streamable for the Process orchestrator
// because grouped streaming Process is not wired through today; the
// crosstab fused path consumes KeyFor directly.
func (g *dateGrouper) KeyFor(r *Record) (string, error) {
	v, ok := r.NumericValue(g.field)
	if !ok {
		return "", ErrGrouperKeyNull
	}
	t := time.Unix(int64(v)*86400, 0).UTC()
	return g.formatDateKey(t), nil
}

// KeyForRow is the StreamingGrouper-shape adapter on top of KeyFor.
// Allows downstream code that consults StreamingGrouper to drive
// dateGrouper per record without buffering. Folds the row into the
// live components state so MetaGrouper.Components() can emit per-
// bucket counts + period boundaries after the streaming iteration
// terminates.
func (g *dateGrouper) KeyForRow(r *Record, _ string) (string, bool, error) {
	key, ok, err := keyForOrSkip(g, r)
	if err != nil || !ok {
		return key, ok, err
	}
	v, ok2 := r.NumericValue(g.field)
	if !ok2 {
		// keyForOrSkip already returned a usable key, so the field
		// value is present — guard the cast anyway to keep the
		// tracker safe under contract drift.
		return key, ok, nil
	}
	g.trackDateRow(int64(v), key)
	return key, ok, nil
}

func (g *dateGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	groups, err := streamableGroup(g, records)
	if err != nil {
		return nil, err
	}
	// Mirror the bucket population into liveBuckets. Group()'s
	// streamableGroup path discards the per-key counts after building
	// the per-key slices, so we re-derive count from len(slice) and
	// re-extract the per-bucket period boundaries from the first row
	// of each bucket (every row in a bucket shares the same boundaries
	// by construction).
	g.liveBuckets = make(map[string]dateBucketStat, len(groups))
	g.liveRangeSet = false
	for key, bucket := range groups {
		if len(bucket) == 0 {
			continue
		}
		// Resolve the first row's day to derive the bucket boundaries
		// and feed range_start / range_end. Subsequent rows in the
		// same bucket update range_min/max if they widen the span.
		for _, r := range bucket {
			v, ok := r.NumericValue(field)
			if !ok {
				continue
			}
			g.trackDateRow(int64(v), key)
		}
	}
	return groups, nil
}

// Components implements MetaGrouper. Returns the per-grouper schema
// declared in descriptor/capabilities_groupers.go for GROUP_DATE:
// {granularity, range_start, range_end, n_buckets, buckets:
// [{key, period_start, period_end, count}]}. Granularity mirrors the
// configured component; range_start / range_end are the earliest /
// latest period boundary observed across all rows (ISO-8601
// YYYY-MM-DD); per-bucket period_start / period_end describe the
// calendar span the bucket key represents.
//
// Reads g.liveBuckets, populated by Group() (buffered) or KeyForRow
// (streaming). Returns an empty buckets slice (not nil) when no rows
// were observed so the emitted shape stays consistent across cohorts.
// Fiscal-offset variants emit empty period_start / period_end / range
// strings — embedders can reconstruct the calendar span from the
// FY-prefixed key plus the configured offset. The universal floor
// ({total_n, n_null}) is filled by the orchestrator.
func (g *dateGrouper) Components() (map[string]any, error) {
	keys := make([]string, 0, len(g.liveBuckets))
	for k := range g.liveBuckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	buckets := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		stat := g.liveBuckets[k]
		buckets = append(buckets, map[string]any{
			"key":          k,
			"period_start": stat.periodStart,
			"period_end":   stat.periodEnd,
			"count":        stat.count,
		})
	}

	var rangeStart, rangeEnd string
	if g.liveRangeSet {
		startT := time.Unix(g.rangeMinDay*86400, 0).UTC()
		endT := time.Unix(g.rangeMaxDay*86400, 0).UTC()
		rs, _ := g.dateRangeBoundaries(startT)
		_, re := g.dateRangeBoundaries(endT)
		rangeStart = rs
		rangeEnd = re
	}

	return map[string]any{
		"granularity": g.component,
		"range_start": rangeStart,
		"range_end":   rangeEnd,
		"n_buckets":   len(buckets),
		"buckets":     buckets,
	}, nil
}

// dateRangesGroupParams is the wire shape of GROUP_DATE_RANGES params.
// Ranges is the inline labeled-range source (the only source wired in
// this story; a named Table reference arrives in a later story and is
// accepted but ignored here). UnmatchedLabel overrides the default
// "unmatched" bucket label for rows that fall outside every range.
type dateRangesGroupParams struct {
	Ranges         []DateRangeSpec `json:"ranges"`
	UnmatchedLabel string          `json:"unmatched_label,omitempty"`
	// Table is reserved for the named range-table source (later story).
	// It is decoded so an inline+table mix does not fail json.Unmarshal,
	// but has no effect on the inline path.
	Table string `json:"table,omitempty"`
}

const defaultUnmatchedRangeLabel = "unmatched"

// dateRangesGrouper buckets each date row by a validated labeled
// date-range set (the E1-S1 shared model). The bucket key is the
// matching range's label; rows outside every range land in the
// configured unmatched bucket. A per-record range→label lookup is
// fully row-local, so the grouper is streamable + mergeable.
type dateRangesGrouper struct {
	field          string
	set            *DateRangeSet
	order          []string // supplied range labels, in author order
	unmatchedLabel string

	// liveBuckets mirrors the post-Group / post-KeyForRow per-bucket row
	// counts so Components() can emit counts without re-scanning. KeyFor
	// itself does NOT touch it (fused crosstab path) — mirrors the
	// categoryGrouper rationale.
	liveBuckets map[string]int
}

func newDateRangesGrouper(grp *types.Group, schema *encoding.Schema) (Grouper, error) {
	// Field must be a date-typed column. Validate against the schema when
	// present (the runtime always supplies one); a nil schema (e.g. a
	// probe with no field) skips the check.
	if schema != nil {
		if f := schema.Field(grp.Field); f != nil && f.Type != encoding.FieldTypeDate {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("GROUP_DATE_RANGES requires a date field, got %q on field %q", f.Type, grp.Field))
		}
	}

	var params dateRangesGroupParams
	if len(grp.Params) > 0 {
		if err := json.Unmarshal(grp.Params, &params); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("invalid GROUP_DATE_RANGES params: %v", err))
		}
	}

	// Inline ranges only in this story. CompileDateRanges emits
	// PULSE_RANGE_EMPTY when Ranges is absent/empty and the four range
	// validation coded errors otherwise.
	set, err := CompileDateRanges(params.Ranges)
	if err != nil {
		return nil, err
	}

	unmatched := defaultUnmatchedRangeLabel
	if params.UnmatchedLabel != "" {
		unmatched = params.UnmatchedLabel
	}

	// Preserve the caller's supplied label order for bucket emission —
	// CompileDateRanges sorts by lower bound internally, so we cannot use
	// set.Labels(). Reject a collision between the unmatched label and a
	// supplied range label: it would silently merge out-of-range rows
	// into a real range bucket.
	order := make([]string, 0, len(params.Ranges))
	for i := range params.Ranges {
		label := params.Ranges[i].Label
		if label == unmatched {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("GROUP_DATE_RANGES unmatched_label %q collides with a range label", unmatched))
		}
		order = append(order, label)
	}

	return &dateRangesGrouper{
		field:          grp.Field,
		set:            set,
		order:          order,
		unmatchedLabel: unmatched,
	}, nil
}

// KeyFor implements StreamableGrouper.KeyFor. Resolves the record's day
// integer against the range set; a match returns the range label, a
// miss returns the unmatched bucket label. Null/missing dates return
// the ErrGrouperKeyNull sentinel.
func (g *dateRangesGrouper) KeyFor(r *Record) (string, error) {
	v, ok := r.NumericValue(g.field)
	if !ok {
		return "", ErrGrouperKeyNull
	}
	if label, matched := g.set.Match(uint32(v)); matched {
		return label, nil
	}
	return g.unmatchedLabel, nil
}

// KeyForRow is the StreamingGrouper adapter on top of KeyFor. Folds the
// row into the live components state so MetaGrouper.Components() has a
// populated per-bucket map after the streaming iteration terminates.
func (g *dateRangesGrouper) KeyForRow(r *Record, _ string) (string, bool, error) {
	key, ok, err := keyForOrSkip(g, r)
	if err != nil || !ok {
		return key, ok, err
	}
	if g.liveBuckets == nil {
		g.liveBuckets = make(map[string]int)
	}
	g.liveBuckets[key]++
	return key, ok, nil
}

func (g *dateRangesGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	groups, err := streamableGroup(g, records)
	if err != nil {
		return nil, err
	}
	// Mirror the bucket population into liveBuckets so Components() can
	// emit per-bucket counts on the buffered path.
	g.liveBuckets = make(map[string]int, len(groups))
	for k, v := range groups {
		g.liveBuckets[k] = len(v)
	}
	return groups, nil
}

// Components implements MetaGrouper. Returns the per-grouper schema
// declared in descriptor/capabilities_groupers.go for GROUP_DATE_RANGES:
// {n_ranges, unmatched_label, buckets: [{key, label, count}]}. Buckets
// emit in supplied range order (author order), followed by the
// unmatched bucket when any out-of-range rows were observed. Every
// configured range emits a bucket even at zero count so the customer's
// labels are always present. The universal floor ({total_n, n_null}) is
// filled by the orchestrator — Components() returns operator keys only.
func (g *dateRangesGrouper) Components() (map[string]any, error) {
	buckets := make([]map[string]any, 0, len(g.order)+1)
	for _, label := range g.order {
		buckets = append(buckets, map[string]any{
			"key":   label,
			"label": label,
			"count": g.liveBuckets[label],
		})
	}
	if n := g.liveBuckets[g.unmatchedLabel]; n > 0 {
		buckets = append(buckets, map[string]any{
			"key":   g.unmatchedLabel,
			"label": g.unmatchedLabel,
			"count": n,
		})
	}
	return map[string]any{
		"n_ranges":        len(g.order),
		"unmatched_label": g.unmatchedLabel,
		"buckets":         buckets,
	}, nil
}

// Compile-time interface locks. Catch interface drift at build time
// and keep the MetaGrouper wiring grep-discoverable.
var (
	_ MetaGrouper = (*categoryGrouper)(nil)
	_ MetaGrouper = (*dateGrouper)(nil)
	_ MetaGrouper = (*dateRangesGrouper)(nil)
	_ MetaGrouper = (*rangeGrouper)(nil)
	_ MetaGrouper = (*roundedGrouper)(nil)
	_ MetaGrouper = (*quantileGrouper)(nil)

	_ StreamableGrouper = (*dateRangesGrouper)(nil)
	_ StreamingGrouper  = (*dateRangesGrouper)(nil)

	// IncludeOrdered locks — only the include-capable groupers surface
	// an ordered filter; every other grouper falls through to sort.Strings
	// via includeFilterOf returning nil.
	_ IncludeOrdered = (*categoryGrouper)(nil)
)
