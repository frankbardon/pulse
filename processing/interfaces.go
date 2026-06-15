package processing

import (
	stderrors "errors"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// ErrGrouperKeyNull is the sentinel error returned by
// StreamableGrouper.KeyFor when the record's grouped field is null,
// missing, or otherwise has no defined bucket key. Callers in the fused
// crosstab path (and any future per-record bucket router) check it via
// errors.Is to decide whether to skip the record.
//
// The sentinel is distinct from an empty-string key — set-typed
// groupers (GROUP_SET_VALUE with an empty mask) legitimately emit "" as
// a valid bucket key, so an in-band string sentinel is impossible.
//
// Exported so embedder-supplied StreamableGrouper extensions can
// return the same null signal the built-in groupers do; the fused
// crosstab router accepts the sentinel from any source.
var ErrGrouperKeyNull = stderrors.New("processing: grouper key null")

// Aggregator computes a single aggregate value from a set of records.
type Aggregator interface {
	// Aggregate computes the aggregation over the given records for the named field.
	Aggregate(records []*Record, field string) (float64, error)
}

// AggregatorFactory creates an Aggregator from a type specification.
type AggregatorFactory func(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error)

// OnlineAggregator is the optional sibling of Aggregator that supports
// single-pass streaming computation without materializing the full record
// set. Aggregators that can produce their result via O(1) (or O(unique))
// state per row implement this interface so the orchestrator can stream
// the iterator directly when every aggregation in a request is online.
//
// Implementations MUST handle null values internally: callers invoke
// UpdateRow once per record (after filters), and the aggregator decides
// whether the row contributes (e.g., COUNT counts every row, SUM skips
// nulls on its target field).
//
// Finalize is called once after the iterator is exhausted; it returns
// the aggregated value. Implementations MUST be safe to call Finalize
// without any UpdateRow calls (i.e., empty input case).
type OnlineAggregator interface {
	// UpdateRow folds a single record into the running state. The field
	// argument is the field the aggregator operates on; for COUNT this
	// can be ignored (COUNT counts rows, not values).
	UpdateRow(record *Record, field string) error
	// Finalize returns the final aggregated value and resets internal
	// state. It must be safe to call exactly once after streaming.
	Finalize() (float64, error)
}

// AttributeComputer computes derived attribute values for each record.
type AttributeComputer interface {
	// Compute calculates derived values for all records, returning a value per record.
	// The returned slice has one entry per record. A nil second return means no null handling.
	Compute(records []*Record, field string) ([]float64, error)
}

// RowLocalAttribute is the optional sibling of AttributeComputer for
// attributes whose value depends only on the current row (no first-pass
// population stats needed). Streaming paths drive RowLocalAttribute.Row
// inline instead of buffering the full record set.
//
// FORMULA and DATE_PART implement this interface; ZSCORE / TSCORE /
// NORMALIZED implement TwoPassAttribute (a superset). PERCENTILE does
// NOT implement either — it needs a sorted view of every value, which
// forces the buffered path.
type RowLocalAttribute interface {
	// Row computes this attribute's value for a single record and field.
	// For a pure RowLocalAttribute, no PrePass call is needed; for a
	// TwoPassAttribute, Row must be called only after Finalize.
	Row(record *Record, field string) (float64, error)
}

// TwoPassAttribute is the streaming-friendly path for attributes that
// need population statistics (ZSCORE / TSCORE need mean+stddev,
// NORMALIZED needs min+max). The orchestrator drives PrePass over every
// filter-passing record, then Finalize locks the global stats, then Row
// emits per-record values during a second iter pass.
//
// Mirrors feature.StreamingComputer.PrePass+Finalize+EmitRow so the
// streaming infrastructure (iter.Reset(), staged passes) is uniform.
//
// Implementations MUST be safe to call PrePass repeatedly; Finalize
// exactly once between PrePass and Row; and Row only after Finalize.
// State is per-instance — callers construct a fresh instance per Process
// call via the AttributeFactory.
type TwoPassAttribute interface {
	RowLocalAttribute
	// PrePass folds a single record's contribution into the running
	// state used to compute population statistics.
	PrePass(record *Record, field string) error
	// Finalize closes the PrePass phase. After Finalize, Row may be
	// called for each record (typically during iter pass 2).
	Finalize() error
}

// AttributeFactory creates an AttributeComputer from a type specification.
type AttributeFactory func(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error)

// FilterFunc evaluates whether a record passes a filter.
type FilterFunc func(record *Record) (bool, error)

// FiltererBuilder constructs a FilterFunc from a filter specification.
type FiltererBuilder interface {
	// Build creates a filter function from the filter specification.
	Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error)
}

// FiltererFactory creates a FiltererBuilder from a type specification.
type FiltererFactory func() FiltererBuilder

// Grouper partitions records into named groups.
type Grouper interface {
	// Group partitions the records by the specified field, returning a map of group key to records.
	Group(records []*Record, field string) (map[string][]*Record, error)
}

// StreamingGrouper is the optional sibling of Grouper for groupers that
// can derive a partition key from a single record without seeing the
// full record set. CATEGORY / RANGE / ROUNDED implement this interface;
// QUANTILE and DATE require finalize-time work over the full input.
//
// Implementations MUST be safe to call repeatedly; the streaming path
// invokes KeyForRow once per filter-passing record and uses the key to
// index a per-group online aggregator bucket.
type StreamingGrouper interface {
	// KeyForRow returns the group key string for record's value of field.
	// ok=false signals the row should be skipped (e.g. null value);
	// ok=true means the key is valid for bucketing.
	KeyForRow(record *Record, field string) (key string, ok bool, err error)
}

// StreamableGrouper is the field-bound sibling of StreamingGrouper used
// by the fused crosstab path. The grouper instance carries its target
// field (extracted from the *types.Group at factory time), so the
// per-record hot path can compute a bucket key with a single method
// call and no extra argument plumbing — important for tight decode
// loops that route each record into the correct cell accumulator.
//
// Semantics mirror StreamingGrouper.KeyForRow without the explicit
// field argument and without the ok bool. Null / missing values are
// signalled by returning ErrGrouperKeyNull instead of a non-nil
// (key, nil) pair — empty-string is a legitimate bucket key for set
// groupers, so an in-band string sentinel is not safe.
//
// Streamable groupers (CATEGORY, RANGE, ROUNDED, DATE, SET_VALUE) all
// implement this interface; non-streamable groupers (QUANTILE) and
// multi-key streaming groupers (SET_PER_ELEMENT) do NOT — callers must
// use interface assertion to discriminate.
type StreamableGrouper interface {
	Grouper
	// KeyFor returns the bucket key for record on the grouper's bound
	// field. Returns (key, nil) on success. Returns ("", ErrGrouperKeyNull)
	// when the record's field is null, missing, or otherwise has no
	// defined bucket (e.g. an empty mask on SET_VALUE remains a valid
	// key of ""; only an absent/null mask triggers the sentinel).
	KeyFor(record *Record) (string, error)
}

// MultiKeyStreamingGrouper is the optional sibling of StreamingGrouper
// for groupers that fan a single record into multiple per-key buckets
// — GROUP_SET_PER_ELEMENT is the canonical implementer: a row whose
// set field selected N labels contributes to N buckets, one per label.
//
// The streaming orchestrator prefers this interface when present and
// drives UpdateRow once per resulting key; groupers that implement
// only StreamingGrouper continue to follow the single-key path.
//
// Implementations MUST be safe to call repeatedly; ok=false signals
// the row should be skipped (e.g. null or empty set); ok=true means
// at least one key was returned. An empty key slice with ok=true is
// not a valid response — return ok=false instead.
type MultiKeyStreamingGrouper interface {
	KeysForRow(record *Record, field string) (keys []string, ok bool, err error)
}

// GrouperFactory creates a Grouper from a type specification.
type GrouperFactory func(grp *types.Group, schema *encoding.Schema) (Grouper, error)

// MergeableAggregator is the optional sibling of OnlineAggregator for
// aggregators whose running state is associative+commutative (or
// associative under a parallel-friendly recurrence). Implementations
// fold another instance's state into the receiver without rescanning
// any rows, enabling per-shard parallel execution: each worker
// computes its partial state from its shard, the orchestrator merges
// partials in shard insertion order, then Finalize() emits the
// aggregate.
//
// Implementations MUST be safe to call MergeOnline repeatedly. The
// receiver absorbs other's state; other is left in an unspecified
// state and should not be reused. Returning a non-nil error indicates
// the two instances were not constructed from compatible specs (a
// programming bug — the orchestrator only merges aggregators
// constructed from the same Aggregation spec).
//
// MergeOnline preserves the mathematical contract of Finalize:
//   - COUNT / SUM / NULL_COUNT: sum the counters.
//   - MIN / MAX: pick the extremum, accounting for the "seen" flag.
//   - MEAN (Welford): combine (n, mean, M2) via the Chan-Welford
//     parallel formula so the merged mean equals the single-pass mean
//     to within ULP on well-conditioned inputs.
//   - FREQUENCY: union the per-value count maps; Finalize then picks
//     the max as it does in the serial path.
//
// Aggregators whose state is fundamentally non-mergeable
// (percentile/median/zscore — they require a sorted view) MUST NOT
// implement this interface; the parallel orchestrator falls through
// to the serial shard iterator for such requests.
//
// Components contract under merge. MergeOnline composes per-chunk
// state with another mergeable aggregator's state. After MergeOnline
// returns, the receiver's scalar (Aggregate/Finalize) output AND any
// MetaAggregator.Components() output reflect the merged state —
// there is no separate MergeComponents call. Implementations MUST
// keep internal component-state mirrors consistent with the value-
// state under MergeOnline. The orchestrator path is
// MergeOnline...; Finalize(); Components() — so per-aggregator
// freeze() helpers stamped from Finalize automatically cover the
// merged emission. Operators with ComponentsMergeability=Partial may
// stage merge at terminal flush rather than per-chunk; consult
// types.ComponentsMergeability.
type MergeableAggregator interface {
	OnlineAggregator
	MergeOnline(other OnlineAggregator) error
}

// RichAggregator is the optional sibling of Aggregator for aggregators
// whose natural output is not a scalar float64 — set-typed aggregators
// emit []string (resolved labels) or map[string]int (per-label counts).
//
// Callers invoke Aggregate or Finalize first (legacy float64 contract;
// implementations return a sensible scalar fallback such as popcount or
// distinct-mask-count), then check whether the aggregator also satisfies
// RichAggregator. When it does, Rich returns the typed value and the
// orchestrator routes it into Response.Data rows or Crosstab cells
// instead of the float64.
//
// Implementations MUST be safe to call Rich exactly once after
// Aggregate/Finalize. Returning (nil, nil) signals "use the float64
// path" — useful when an aggregator implements the interface for type
// reasons but produced no rich value for a given input.
type RichAggregator interface {
	Aggregator
	Rich() (any, error)
}

// MetaAggregator is the optional sibling of Aggregator for aggregators
// that emit a per-operator "components" map alongside their scalar (or
// rich) value. The map carries the named constituent-parts metadata
// declared in the aggregator's descriptor.ComponentSchema — Welford
// triples (mean, variance, sum_squares, m2), frequency tables,
// percentile sort sizes, distinct-mask popcounts, and so on.
//
// Components is called exactly once after Aggregate or Finalize. The
// orchestrator routes the result into Response.Components.Aggregations
// (or the Crosstab cell-components matrix when the aggregator runs
// inside a crosstab cell). Implementations MUST be safe to call
// Components once per result; calling it multiple times or before the
// terminating Aggregate / Finalize is a programming error and may
// return stale state.
//
// Universal floor contract: the orchestrator ALWAYS populates the
// universal floor keys ({"n", "n_null"}) on Response.Components from
// per-record bookkeeping. Components() returns ONLY the operator-
// specific keys (mean, variance, mode, range_min, dict_size, …) — do
// NOT re-emit n / n_null from the operator. Returning (nil, nil) is
// the canonical signal for "no operator-specific keys; caller still
// fills the universal floor unconditionally" (e.g. AGG_COUNT, where
// the universal floor IS the entire component payload).
//
// Streaming semantics follow the descriptor.ComponentsMergeability
// declared at registration time:
//
//   - Mergeable: components fold across chunks via the same
//     MergeOnline path as the scalar value (Welford, sums, counts).
//   - Partial: map / set merges that work but cost allocation
//     (frequency tables, distinct masks); orchestrator may stage the
//     merge at terminal flush.
//   - None: components emitted only on terminal buffered flush
//     (median / percentile — need a sorted view of the full input);
//     streaming chunks omit them and predict declares the slot as
//     buffered-components-only.
//
// Subsequent stories add the per-aggregator implementations under
// compile-time assertions of the form:
//
//	var _ processing.MetaAggregator = (*welfordAgg)(nil)
//
// which keep the wiring grep-discoverable and catch interface drift
// at build time. No runtime wiring is added by the interface itself —
// the descriptor.ComponentSchema declared in capabilities_*.go is the
// matching half consumed by predict / manifest.
type MetaAggregator interface {
	Aggregator
	// Components returns the per-operator components map keyed by the
	// snake_case names declared in the aggregator's
	// descriptor.ComponentSchema. Returning (nil, nil) means "no
	// operator-specific keys; caller fills the universal floor
	// unconditionally". A non-nil error aborts the request with the
	// same error-routing contract as Aggregate / Finalize.
	Components() (map[string]any, error)
}

// MetaGrouper is the optional sibling of Grouper / StreamingGrouper /
// StreamableGrouper / MultiKeyStreamingGrouper for groupers that emit
// a per-operator "components" map alongside their partitioning output.
// The map carries the named constituent-parts metadata declared in the
// grouper's descriptor.ComponentSchema — bucket arrays, edge values,
// dictionary mappings, range_min / range_max, granularity, etc.
//
// Components is called exactly once after the grouper's terminal pass:
//
//   - For buffered groupers (Grouper.Group), after Group returns.
//   - For streaming groupers (StreamingGrouper / StreamableGrouper),
//     after the per-record KeyForRow / KeyFor sequence terminates and
//     the iterator is exhausted.
//   - For multi-key streaming groupers (MultiKeyStreamingGrouper, e.g.
//     GROUP_SET_PER_ELEMENT), after the per-record KeysForRow sequence
//     terminates. Per-bucket counts in the emitted map reflect
//     emission count (one increment per emitted key), NOT row count —
//     a single row may contribute to multiple buckets, and the
//     orchestrator's TotalN reflects the row count.
//
// The orchestrator routes the result into
// Response.Components.Groupers (one entry per Request.Groups slot, in
// declared order). Implementations MUST be safe to call Components
// once per result; calling it multiple times or before the terminating
// pass is a programming error and may return stale state.
//
// Universal floor contract: the orchestrator ALWAYS populates the
// universal floor keys (TotalN, NNull on types.GrouperComponents)
// from the post-filter record walker. Components() returns ONLY the
// operator-specific keys (buckets, edges, dict_size, granularity, …)
// — do NOT re-emit total_n / n_null from the operator. Returning
// (nil, nil) is the canonical signal for "no operator-specific keys;
// caller still fills the universal floor unconditionally".
//
// Streaming merge semantics follow the descriptor.ComponentsMergeability
// declared at registration time, mirroring the MetaAggregator contract:
// mergeable groupers (bucket counts) fold across chunks; partial
// groupers (dict-bearing) stage merges at terminal flush; non-mergeable
// groupers (rare for groupers; QUANTILE-class needs the full input)
// emit components only on terminal buffered flush.
//
// E2-S4..S6 will add the per-grouper implementations under compile-
// time assertions of the form:
//
//	var _ processing.MetaGrouper = (*categoryGrouper)(nil)
//	var _ processing.MetaGrouper = (*rangeGrouper)(nil)
//	var _ processing.MetaGrouper = (*roundedGrouper)(nil)
//	var _ processing.MetaGrouper = (*quantileGrouper)(nil)
//	var _ processing.MetaGrouper = (*dateGrouper)(nil)
//	...
//
// which keep the wiring grep-discoverable and catch interface drift at
// build time. No runtime wiring is added by the interface itself — the
// descriptor.ComponentSchema declared in capabilities_groupers.go is
// the matching half consumed by predict / manifest.
type MetaGrouper interface {
	// Components returns the per-grouper components map keyed by the
	// snake_case names declared in the grouper's
	// descriptor.ComponentSchema. Returning (nil, nil) means "no
	// operator-specific keys; caller fills the universal floor
	// unconditionally". A non-nil error aborts the request with the
	// same error-routing contract as Group / KeyFor / KeyForRow /
	// KeysForRow.
	Components() (map[string]any, error)
}

// MetaFilterer is the optional sibling of FiltererBuilder for filterers
// that emit a per-filterer "components" map alongside their per-record
// predicate. The map carries the named constituent-parts metadata
// declared in the filterer's descriptor.ComponentSchema.
//
// In v1 (E2-S8) the components surface is uniform across every
// registered filterer — the orchestrator emits the universal floor
// {n_in, n_out, n_null_input} from the filter pass's record-walker
// counters. No built-in filterer implements Components(): the
// interface exists for extension-author parity with MetaAggregator /
// MetaGrouper and to leave room for future per-filter specifics
// (e.g. n_below / n_above for FILTER_RANGE; per-value n for
// FILTER_INCLUDE / FILTER_EXCLUDE). The orchestrator's universal-
// floor pass is always authoritative for the three floor keys; an
// embedder-supplied Components() returns ONLY the operator-specific
// extras.
//
// Components is called exactly once after the filter pass terminates
// (the iterator is exhausted). The orchestrator routes the result
// into Response.Components.Filterers (one entry per
// Request.Filterers slot, in declared order). Implementations MUST
// be safe to call Components once per result; calling it multiple
// times or before the pass completes is a programming error and may
// return stale state.
//
// Streaming merge semantics follow descriptor.ComponentsMergeability
// declared at registration time. Filterer components in v1 are
// Mergeable — counters fold trivially across chunks via integer
// addition, so the streaming path emits per-chunk deltas without
// staging. Future per-filter specifics may downgrade individual
// entries to Partial / None; the orchestrator follows the same
// per-operator dispatch the aggregator side uses.
//
// Subsequent stories (no built-in implementations in v1) may add
// per-filterer sentinels of the form
//
//	var _ processing.MetaFilterer = (*rangeFilterer)(nil)
//
// to keep the wiring grep-discoverable and catch interface drift at
// build time, mirroring the MetaAggregator / MetaGrouper pattern. No
// runtime wiring is added by the interface itself — the
// descriptor.ComponentSchema declared in capabilities_filterers.go
// is the matching half consumed by predict / manifest.
type MetaFilterer interface {
	// Components returns the per-filterer components map keyed by the
	// snake_case names declared in the filterer's
	// descriptor.ComponentSchema. Returning (nil, nil) means "no
	// operator-specific keys; caller fills the universal floor
	// unconditionally" — the canonical v1 contract for every built-in
	// filterer. A non-nil error aborts the request with the same
	// error-routing contract as Build / FilterFunc.
	Components() (map[string]any, error)
}
