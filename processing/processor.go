package processing

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing/feature"
	"github.com/frankbardon/pulse/processing/window"
	"github.com/frankbardon/pulse/types"
)

// ProcessPath identifies which execution path Process took. The
// streaming path runs aggregations in a single pass over the iterator
// without materializing the full record set. The buffered path
// collects every record into a slice first. This is exposed primarily
// for tests and benchmarks; production callers do not need it.
type ProcessPath int

const (
	// PathUnknown is the zero value; set before the first Process call.
	PathUnknown ProcessPath = iota
	// PathBuffered is the legacy materialize-then-aggregate path. It is
	// always correct and is the fallback whenever streaming would be
	// unsafe (groups, attributes, non-online aggregators, expression
	// filters that need the full set, etc.).
	PathBuffered
	// PathStreaming runs aggregations in a single pass over the iterator,
	// folding each record into the running state of every aggregator.
	// Selected only when every aggregation is an OnlineAggregator and
	// the request has no groups, no attributes, and only row-level
	// filters.
	PathStreaming
)

func (p ProcessPath) String() string {
	switch p {
	case PathBuffered:
		return "buffered"
	case PathStreaming:
		return "streaming"
	default:
		return "unknown"
	}
}

// Processor is the single dynamic processing engine for Pulse.
// It handles filtering, attribute computation, grouping, and aggregation
// over record iterators backed by .pulse encoded data.
type Processor struct {
	schema   *encoding.Schema
	lastPath ProcessPath
}

// NewProcessor creates a new Processor for the given schema.
func NewProcessor(schema *encoding.Schema) *Processor {
	return &Processor{schema: schema}
}

// LastPath returns the ProcessPath taken by the most recent Process call
// on this processor instance. Returns PathUnknown before any call. Used
// by tests to verify that the orchestrator selected the streaming path
// for online-only requests; not part of the stable API contract.
func (p *Processor) LastPath() ProcessPath {
	return p.lastPath
}

// Process executes a single request against the record iterator.
//
// Selects between two execution strategies:
//
//  1. Streaming: when every aggregation supports OnlineAggregator and
//     the request has no groups, no attributes, the iterator is consumed
//     in one pass. Filters are applied per row before each aggregator
//     folds the row into its running state. Memory is O(distinct values)
//     for FREQUENCY/MODE/DISTINCT_COUNT and O(1) for everything else.
//
//  2. Buffered: the legacy path. Every record is collected into a slice
//     first, then filters, attributes, grouping, and aggregations run
//     over the materialized set. Memory is O(rows). Always correct.
//
// Output is identical between paths to float64 precision on
// well-conditioned inputs (variance/stddev/skewness/kurtosis use
// Welford-Pébaÿ recurrences in the streaming path).
func (p *Processor) Process(ctx context.Context, req *types.Request, iter RecordIterator) (*types.Response, error) {
	if p.canStream(req) {
		resp, err := p.processStreaming(ctx, req, iter)
		if err != nil {
			return nil, err
		}
		p.lastPath = PathStreaming
		return resp, nil
	}

	// Buffered path: collect every record then dispatch.
	var allRecords []*Record
	for iter.Next() {
		allRecords = append(allRecords, iter.Record())
	}
	resp, err := p.processRecords(ctx, req, allRecords)
	if err != nil {
		return nil, err
	}
	p.lastPath = PathBuffered
	return resp, nil
}

// canStream reports whether the request can be safely executed via the
// streaming path. Streaming requires:
//   - no grouping (groups need the full record set partitioned by key)
//   - no attributes (ZSCORE/PERCENTILE/RANK/NORMALIZED need a first
//     pass to compute population stats; FORMULA is row-local but is
//     bundled in for simplicity — every attribute today is buffered)
//   - no features (every FEAT_* operator runs over the full record set —
//     global-pass operators need stats before per-row emit, and per-row
//     operators inject derived columns that downstream stages reference)
//   - every aggregation type supports OnlineAggregator
//   - filters are row-level only (every registered filter today is)
//
// Returns false on any unknown component type so the buffered path can
// surface the canonical error message.
func (p *Processor) canStream(req *types.Request) bool {
	if len(req.Groups) > 0 || len(req.Attributes) > 0 || len(req.Windows) > 0 || len(req.Features) > 0 {
		return false
	}
	if len(req.Aggregations) == 0 {
		// No aggregations: buffered path produces the same empty data
		// payload and exposes the same error surface (e.g., when a
		// downstream component validates against the materialized set).
		return false
	}
	for _, agg := range req.Aggregations {
		factory, ok := aggregatorRegistry[agg.Type]
		if !ok {
			return false
		}
		instance, err := factory(agg, p.schema)
		if err != nil {
			return false
		}
		if _, ok := instance.(OnlineAggregator); !ok {
			return false
		}
	}
	return true
}

// processStreaming runs the streaming execution path. The iterator is
// consumed exactly once. Filters are applied row-by-row before each
// aggregator's UpdateRow is called.
func (p *Processor) processStreaming(ctx context.Context, req *types.Request, iter RecordIterator) (*types.Response, error) {
	// Build filter functions once.
	filterFns, err := p.buildFilterFuncs(req.Filterers)
	if err != nil {
		return nil, err
	}

	// Build aggregator instances and their online interfaces. Each
	// factory call produces a fresh, zero-state instance; safe to use
	// directly as a streaming accumulator.
	type onlineEntry struct {
		agg    *types.Aggregation
		online OnlineAggregator
	}
	entries := make([]onlineEntry, len(req.Aggregations))
	for i, agg := range req.Aggregations {
		factory := aggregatorRegistry[agg.Type] // canStream verified existence
		instance, err := factory(agg, p.schema)
		if err != nil {
			return nil, err
		}
		online, ok := instance.(OnlineAggregator)
		if !ok {
			// canStream verified online support; defensive.
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("aggregator %s does not implement OnlineAggregator", agg.Type))
		}
		entries[i] = onlineEntry{agg: agg, online: online}
	}

	var totalRows, filteredRows int64
	for iter.Next() {
		totalRows++
		r := iter.Record()
		pass := true
		for _, fn := range filterFns {
			ok, err := fn(r)
			if err != nil {
				return nil, err
			}
			if !ok {
				pass = false
				break
			}
		}
		if !pass {
			continue
		}
		filteredRows++
		for _, e := range entries {
			if err := e.online.UpdateRow(r, e.agg.Field); err != nil {
				return nil, err
			}
		}
	}

	row := make(map[string]any, len(entries))
	for _, e := range entries {
		val, err := e.online.Finalize()
		if err != nil {
			return nil, err
		}
		label := e.agg.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", e.agg.Type, e.agg.Field)
		}
		row[label] = val
	}

	_ = ctx
	return &types.Response{
		Data: []map[string]any{row},
		Metadata: &types.ResponseMetadata{
			TotalRows:    totalRows,
			FilteredRows: filteredRows,
		},
	}, nil
}

// buildFilterFuncs constructs FilterFuncs from filter specifications.
// Shared between streaming and buffered paths to keep error semantics
// identical.
func (p *Processor) buildFilterFuncs(filterers []*types.Filterer) ([]FilterFunc, error) {
	if len(filterers) == 0 {
		return nil, nil
	}
	out := make([]FilterFunc, 0, len(filterers))
	for _, f := range filterers {
		factory, ok := filtererRegistry[f.Type]
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown filter type: %s", f.Type))
		}
		fn, err := factory().Build(f, p.schema)
		if err != nil {
			return nil, err
		}
		out = append(out, fn)
	}
	return out, nil
}

// ProcessComposed executes multiple requests against a shared record set.
func (p *Processor) ProcessComposed(ctx context.Context, composed *types.ComposedRequest, records []*Record) ([]*types.Response, error) {
	responses := make([]*types.Response, len(composed.Requests))
	for i, req := range composed.Requests {
		iter := NewSliceIterator(records)
		resp, err := p.Process(ctx, req, iter)
		if err != nil {
			return nil, fmt.Errorf("request %d: %w", i, err)
		}
		responses[i] = resp
	}
	return responses, nil
}

func (p *Processor) processRecords(ctx context.Context, req *types.Request, records []*Record) (*types.Response, error) {
	totalRows := int64(len(records))

	// Step 0: Apply pre-filter features. They mutate records in place to
	// add derived columns so filters and downstream stages can reference
	// them. Features run over the full unfiltered set so global-pass
	// operators (FREQUENCY_ENCODE, TARGET_ENCODE) see every row's
	// contribution to the stats.
	if err := p.applyFeatures(req.Features, records); err != nil {
		return nil, err
	}

	// Step 1: Apply filters
	filtered, err := p.applyFilters(req.Filterers, records)
	if err != nil {
		return nil, err
	}

	// Step 2: Compute attributes (adds derived fields to records)
	if err := p.applyAttributes(req.Attributes, filtered); err != nil {
		return nil, err
	}

	// Step 3: Group if needed, then aggregate
	var data []map[string]any

	if len(req.Groups) > 0 {
		data, err = p.processGrouped(req, filtered)
		if err != nil {
			return nil, err
		}
	} else if len(req.Aggregations) > 0 {
		row, err := p.aggregate(req.Aggregations, filtered)
		if err != nil {
			return nil, err
		}
		if row != nil {
			data = []map[string]any{row}
		}
	} else if len(req.Windows) > 0 {
		// No group, no aggregation, but we have windows. Materialize one row
		// per filtered record so windows can compute over the full set.
		data = recordsToRows(filtered)
	}

	if len(req.Windows) > 0 {
		if err := window.Apply(ctx, data, req.Windows); err != nil {
			return nil, err
		}
	}

	if len(req.Sort) > 0 {
		window.Sort(data, req.Sort)
	}

	return &types.Response{
		Data: data,
		Metadata: &types.ResponseMetadata{
			TotalRows:    totalRows,
			FilteredRows: int64(len(filtered)),
		},
	}, nil
}

// recordsToRows materializes Records into the post-aggregate row shape
// used by the window pipeline stage. Each record contributes one row
// containing every non-null value (categorical fields resolve to strings).
//
// AllValues returns a cached map owned by the Record; window operators
// mutate rows in place to write their output column, so we clone here to
// avoid leaking window outputs back into the Record's cache.
func recordsToRows(records []*Record) []map[string]any {
	rows := make([]map[string]any, len(records))
	for i, r := range records {
		src := r.AllValues()
		clone := make(map[string]any, len(src)+2)
		for k, v := range src {
			clone[k] = v
		}
		rows[i] = clone
	}
	return rows
}

func (p *Processor) applyFilters(filterers []*types.Filterer, records []*Record) ([]*Record, error) {
	if len(filterers) == 0 {
		return records, nil
	}

	filterFns, err := p.buildFilterFuncs(filterers)
	if err != nil {
		return nil, err
	}

	// Apply all filters (AND logic)
	var result []*Record
	for _, r := range records {
		pass := true
		for _, fn := range filterFns {
			ok, err := fn(r)
			if err != nil {
				return nil, err
			}
			if !ok {
				pass = false
				break
			}
		}
		if pass {
			result = append(result, r)
		}
	}
	return result, nil
}

// applyFeatures runs pre-filter feature operators over the unfiltered
// record set. The feature subpackage owns operator dispatch; this method
// adapts processing.Record into the feature.Record interface and forwards
// to feature.Apply. Empty feature lists are a fast no-op.
func (p *Processor) applyFeatures(features []*types.Feature, records []*Record) error {
	if len(features) == 0 || len(records) == 0 {
		return nil
	}
	view := make([]feature.Record, len(records))
	for i, r := range records {
		view[i] = r
	}
	return feature.Apply(view, features, p.schema)
}

func (p *Processor) applyAttributes(attrs []*types.Attribute, records []*Record) error {
	for _, attr := range attrs {
		factory, ok := attributeRegistry[attr.Type]
		if !ok {
			return errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown attribute type: %s", attr.Type))
		}
		computer, err := factory(attr, p.schema)
		if err != nil {
			return err
		}
		values, err := computer.Compute(records, attr.Field)
		if err != nil {
			return err
		}

		// Inject computed values back into records under the label name
		label := attr.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", attr.Type, attr.Field)
		}
		for i, r := range records {
			if i < len(values) {
				r.values[label] = values[i]
				// Direct mutation of values map invalidates any cached
				// AllValues() result on this Record.
				r.invalidateAllValuesCache()
			}
		}
	}
	return nil
}

func (p *Processor) processGrouped(req *types.Request, records []*Record) ([]map[string]any, error) {
	// Use the first group for now (single-level grouping)
	grp := req.Groups[0]
	factory, ok := grouperRegistry[grp.Type]
	if !ok {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("unknown group type: %s", grp.Type))
	}
	grouper, err := factory(grp, p.schema)
	if err != nil {
		return nil, err
	}

	groups, err := grouper.Group(records, grp.Field)
	if err != nil {
		return nil, err
	}

	data := make([]map[string]any, 0, len(groups))
	for key, groupRecords := range groups {
		row, err := p.aggregate(req.Aggregations, groupRecords)
		if err != nil {
			return nil, err
		}
		if row == nil {
			// +1 reserved for the group key written below.
			row = make(map[string]any, 1)
		}
		row[grp.Field] = key
		data = append(data, row)
	}
	return data, nil
}

func (p *Processor) aggregate(aggs []*types.Aggregation, records []*Record) (map[string]any, error) {
	if len(aggs) == 0 {
		return nil, nil
	}

	// Cache collected non-null float64 slices per field for the duration of
	// this call so multiple aggregations on the same field don't each scan
	// the record set. The cache owns pooled buffers; release returns them.
	cache := newCollectCache()
	defer cache.release()

	// Pre-size: one entry per aggregation. Grouped path adds +1 for the group
	// key after this returns; map will grow once but only in that branch.
	row := make(map[string]any, len(aggs))
	for _, agg := range aggs {
		factory, ok := aggregatorRegistry[agg.Type]
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown aggregation type: %s", agg.Type))
		}
		aggregator, err := factory(agg, p.schema)
		if err != nil {
			return nil, err
		}
		var val float64
		if va, ok := aggregator.(valueAggregator); ok {
			vals := cache.get(records, agg.Field)
			val, err = va.aggregateValues(vals)
		} else {
			val, err = aggregator.Aggregate(records, agg.Field)
		}
		if err != nil {
			return nil, err
		}
		label := agg.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", agg.Type, agg.Field)
		}
		row[label] = val
	}
	return row, nil
}
