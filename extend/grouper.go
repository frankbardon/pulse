package extend

import (
	stderrors "errors"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// ErrGrouperKeyNull is the null-key sentinel for grouper key methods. A
// StreamingGrouper.KeyForRow or MultiKeyStreamingGrouper.KeysForRow
// that returns an error matching it (errors.Is) is treated exactly like
// ok=false: the row lands in no bucket and the run continues. It exists
// because an empty-string key is a legitimate bucket name, so "no key"
// cannot be signalled in-band; returning ok=false is equivalent.
var ErrGrouperKeyNull = stderrors.New("extend: grouper key null")

// Grouper partitions the rows of one buffered call into named buckets.
// It is the base interface every extension grouper implements.
//
// Group returns, per bucket key, the INDICES into rows (0 <= i <
// rows.Len()) of the rows that land in that bucket. A row may appear in
// more than one bucket only when the registration declares FansOut. An
// out-of-range index is refused with a PROCESSING_INTERNAL coded error;
// it never panics.
type Grouper interface {
	Group(rows Rows, field string) (map[string][]int, error)
}

// StreamingGrouper is the optional streaming sibling of Grouper: it
// derives one bucket key from a single row. When a registration
// declares Streamable: true the factory MUST return a value
// implementing it (or MultiKeyStreamingGrouper for a fan-out grouper);
// the engine then keys rows one at a time instead of buffering them.
// ok=false (or an ErrGrouperKeyNull error) skips the row.
//
// Implementing it also makes the grouper eligible as a fused crosstab
// axis, whatever Streamable declares: the engine keys each record with
// field set to the grouper's own types.Group.Field.
type StreamingGrouper interface {
	KeyForRow(rec Record, field string) (key string, ok bool, err error)
}

// MultiKeyStreamingGrouper is the optional fan-out sibling: one row
// lands in every returned bucket. A registration whose factory returns
// it MUST declare FansOut: true, and one declaring FansOut: true MUST
// return it; pulse.New refuses either mismatch with
// PULSE_EXTENSION_FANOUT_MISMATCH. ok=false, an empty key slice, or an
// ErrGrouperKeyNull error skips the row.
type MultiKeyStreamingGrouper interface {
	KeysForRow(rec Record, field string) (keys []string, ok bool, err error)
}

// MergeableGrouper is the optional merge sibling for groupers whose
// components state folds across input partitions. When a registration
// declares both Streamable: true and Mergeable: true, the parallel
// reducers (pulse.Options.ShardWorkers over a shard archive,
// pulse.Options.DecodeWorkers over a large single-file cohort) build
// one grouper per partition, key that partition's rows through
// KeyForRow / KeysForRow, then fold the partials into the first with
// MergeState before reading the ComponentsFunc (or Components())
// output once, off the merged receiver. The same declaration admits
// the operator to ProcessChain stages. Without it the request runs
// serially and a chain refuses it.
//
// The bucket rows themselves are merged by the engine (per-key
// aggregator state), so MergeState folds ONLY what the grouper keeps
// for its components figures — per-bucket counts, an observed range.
// A Mergeable grouper that emits no components has nothing to fold
// and need not implement this interface; one that does emit must, or
// pulse.New refuses the registration with
// PULSE_EXTENSION_MERGEABLE_MISMATCH.
//
// MergeState receives another instance built by the SAME factory from
// the SAME spec — the embedder's own value, never an engine wrapper —
// so a type assertion to the concrete type succeeds. It absorbs
// other's state into the receiver; other is not reused afterwards.
// Partials arrive in a deterministic order (shard order, or segment
// order for a single file), but how the cohort is partitioned depends
// on the worker count, so the fold must be associative.
type MergeableGrouper interface {
	Grouper
	// MergeState folds other's components state into the receiver.
	MergeState(other Grouper) error
}

// GrouperFactory builds a fresh Grouper for one group slot of one
// request. During probe-validation at pulse.New it is called once with
// a spec carrying only the operator name and an empty schema, and must
// tolerate both.
type GrouperFactory func(spec *types.Group, schema *encoding.Schema) (Grouper, error)
