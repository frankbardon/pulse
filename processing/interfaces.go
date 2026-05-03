package processing

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

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
// full record set. CATEGORY / RANGE / ROUNDED / H3_CELL implement this
// interface; QUANTILE and DATE require finalize-time work over the
// full input.
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

// GrouperFactory creates a Grouper from a type specification.
type GrouperFactory func(grp *types.Group, schema *encoding.Schema) (Grouper, error)
