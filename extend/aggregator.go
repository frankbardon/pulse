package extend

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Aggregator computes one aggregate value over a set of rows. It is
// the base interface every extension aggregator implements; the
// buffered engine path calls Aggregate once with every row that passed
// the request's filters.
type Aggregator interface {
	// Aggregate computes the aggregation over rows for the named field.
	// Implementations decide how nulls contribute.
	Aggregate(rows Rows, field string) (float64, error)
}

// OnlineAggregator is the optional streaming sibling of Aggregator.
// When an aggregator implements it and its registration declares
// Streamable: true, the engine folds rows one at a time instead of
// buffering them: UpdateRow once per row that passed the filters, then
// Finalize once. Finalize MUST be safe to call with no prior UpdateRow
// (the empty-input case). A Streamable registration whose factory does
// not return an OnlineAggregator is refused at pulse.New with
// PULSE_EXTENSION_STREAMABLE_MISMATCH. The declaration holds for every
// field type, decimal128 included: UpdateRow sees decimal fields via
// Record.DecimalValue.
type OnlineAggregator interface {
	// UpdateRow folds one row into the running state.
	UpdateRow(rec Record, field string) error
	// Finalize returns the aggregated value.
	Finalize() (float64, error)
}

// MergeableAggregator is the optional sibling of OnlineAggregator for
// aggregators whose running state folds across input partitions. When
// an aggregator implements it and its registration declares both
// Streamable: true and Mergeable: true, the parallel reducers
// (pulse.Options.ShardWorkers over a shard archive,
// pulse.Options.DecodeWorkers over a large single-file cohort) fold
// each partition through UpdateRow on a fresh instance, then combine
// the partials with Merge before calling Finalize once on the
// receiver. The same declaration admits the operator to ProcessChain
// stages. Without it the request runs serially and a chain refuses it.
//
// Merge receives another instance built by the SAME factory from the
// SAME spec — the embedder's own value, never an engine wrapper — so a
// type assertion to the concrete type succeeds. It absorbs other's
// state into the receiver; other is not reused afterwards. Merge must
// be associative: partials are combined in a deterministic order, but
// how the cohort is partitioned depends on the worker count, so a
// floating-point fold may differ from the serial result in the last
// ULP. After Merge, the receiver's Finalize, Rich and ComponentsFunc
// output must describe the merged state. A registration declaring
// Mergeable whose value lacks this interface, or whose ComponentSchema
// classifies its keys as non-mergeable, is refused at pulse.New with
// PULSE_EXTENSION_MERGEABLE_MISMATCH.
type MergeableAggregator interface {
	OnlineAggregator
	// Merge folds other's running state into the receiver.
	Merge(other OnlineAggregator) error
}

// RichAggregator is the optional sibling for aggregators whose natural
// output is not a scalar (a label list, a per-label count map, …). The
// engine calls Aggregate or Finalize first, then Rich exactly once;
// a non-nil result replaces the float64 in the response. Returning
// (nil, nil) keeps the float64.
type RichAggregator interface {
	Rich() (any, error)
}

// AggregatorFactory builds a fresh Aggregator for one aggregation slot
// of one request. It receives the slot's spec and the cohort schema;
// during probe-validation at pulse.New it is called once with a spec
// carrying only the operator name and an empty schema, and must
// tolerate both.
type AggregatorFactory func(spec *types.Aggregation, schema *encoding.Schema) (Aggregator, error)
