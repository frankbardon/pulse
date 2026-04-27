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

// AttributeComputer computes derived attribute values for each record.
type AttributeComputer interface {
	// Compute calculates derived values for all records, returning a value per record.
	// The returned slice has one entry per record. A nil second return means no null handling.
	Compute(records []*Record, field string) ([]float64, error)
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

// GrouperFactory creates a Grouper from a type specification.
type GrouperFactory func(grp *types.Group, schema *encoding.Schema) (Grouper, error)
