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
		var resp *types.Response
		var err error
		switch {
		case len(req.Groups) > 0:
			resp, err = p.processStreamingGrouped(ctx, req, iter)
		case hasTwoPassAttribute(req):
			resp, err = p.processStreamingTwoPass(ctx, req, iter)
		default:
			resp, err = p.processStreaming(ctx, req, iter)
		}
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

// CanStreamRequest is the exported parity hook used by tests in
// descriptor/ (which cannot import processing) to confirm that a
// PredictResult.Streamable value matches the runtime gate. Application
// code should rely on PredictResult.Streamable, not this helper.
func CanStreamRequest(req *types.Request, schema *encoding.Schema) bool {
	p := &Processor{schema: schema}
	return p.canStream(req)
}

// requiresTwoPass reports whether the attribute type implements
// TwoPassAttribute (and therefore needs a PrePass over filter-passing
// records before per-row emission). Mirrors the attribute factories in
// processing/attribute.go: ZSCORE/TSCORE/NORMALIZED need population
// stats; FORMULA/DATE_PART are pure row-local.
func requiresTwoPass(t types.AttributeType) bool {
	switch t {
	case types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_NORMALIZED:
		return true
	}
	return false
}

// hasTwoPassAttribute reports whether req has any attribute whose type
// requires the two-pass streaming path.
func hasTwoPassAttribute(req *types.Request) bool {
	for _, attr := range req.Attributes {
		if requiresTwoPass(attr.Type) {
			return true
		}
	}
	return false
}

// canStream reports whether the request can be safely executed via the
// streaming path. Streaming requires:
//   - groups: empty, OR every grouper.Type.Streamable()=true (CATEGORY,
//     RANGE, ROUNDED, H3_CELL — partitioned via grouped streaming path).
//     QUANTILE/DATE require a finalize-time view of the full set.
//   - attributes: empty, OR every attribute.Type.Streamable()=true
//     (FORMULA, DATE_PART implement RowLocalAttribute and execute
//     inline). ZSCORE/TSCORE/NORMALIZED/PERCENTILE need population stats.
//   - no windows (window operators run over the post-aggregate row set)
//   - features either empty or every operator implements
//     feature.StreamingComputer (PrePass + Finalize + EmitRow)
//   - every aggregation type supports OnlineAggregator
//   - filters are row-level only (every registered filter today is)
//
// Returns false on any unknown component type so the buffered path can
// surface the canonical error message.
func (p *Processor) canStream(req *types.Request) bool {
	if len(req.Windows) > 0 {
		return false
	}
	for _, grp := range req.Groups {
		if !grp.Type.Streamable() {
			return false
		}
	}
	hasTwoPassAttr := false
	for _, attr := range req.Attributes {
		if !attr.Type.Streamable() {
			return false
		}
		if requiresTwoPass(attr.Type) {
			hasTwoPassAttr = true
		}
	}
	// Two-pass attribute orchestration does not yet compose with the
	// streaming feature pipeline (filter/EmitRow ordering) or grouped
	// streaming (per-group attribute stats). Force buffered for those
	// combinations until the next iteration extends coverage.
	if hasTwoPassAttr && (len(req.Features) > 0 || len(req.Groups) > 0) {
		return false
	}
	if len(req.Features) > 0 && !feature.IsStreamable(req.Features, p.schema) {
		return false
	}
	if len(req.Aggregations) == 0 {
		// No aggregations: buffered path produces the same empty data
		// payload and exposes the same error surface (e.g., when a
		// downstream component validates against the materialized set).
		return false
	}
	for _, agg := range req.Aggregations {
		// Geo aggregations dispatch through the buffered AggregateGeoField
		// path; the streaming numeric fold does not apply.
		if IsGeoAggregation(agg.Type) {
			return false
		}
		// Decimal-typed fields are aggregated via AggregateDecimalField;
		// the streaming numeric fold loses precision.
		if p.schema != nil {
			if f := p.schema.Field(agg.Field); f != nil && f.Type.IsDecimal() {
				return false
			}
		}
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

// processStreaming runs the streaming execution path. With no features
// the iterator is consumed exactly once: filters apply row-by-row, then
// each aggregator's UpdateRow is called. With features, the iterator is
// consumed twice: pass 1 drives feature.StreamingComputer.PrePass, then
// Finalize captures any global state, iter.Reset() rewinds, and pass 2
// emits derived columns into each record before filters and online
// aggregators see it.
func (p *Processor) processStreaming(ctx context.Context, req *types.Request, iter RecordIterator) (*types.Response, error) {
	// Streaming consumes each record inline; opt the iterator into
	// per-row Record reuse so the map allocations in the source
	// (typically service.streamingIterator) collapse to one set for the
	// lifetime of the call.
	EnableReuse(iter)
	// Build streaming feature handles, if any. canStream verified that
	// every operator supports streaming, so factory failures here are
	// PROCESSING_CONFIG bubbling up the canonical error.
	var streamingFeatures []feature.StreamingHandle
	if len(req.Features) > 0 {
		handles, err := feature.BuildStreaming(req.Features, p.schema)
		if err != nil {
			return nil, err
		}
		streamingFeatures = handles

		// Pass 1: feed every record through each computer's PrePass.
		for iter.Next() {
			rec := iter.Record()
			for _, h := range streamingFeatures {
				if err := h.Computer.PrePass(rec, h.Feature.Field); err != nil {
					return nil, err
				}
			}
		}
		for _, h := range streamingFeatures {
			if err := h.Computer.Finalize(); err != nil {
				return nil, err
			}
		}
		iter.Reset()
	}

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

	// Build row-local attribute computers. canStream verified every
	// attribute is row-local; factory failures here are PROCESSING_CONFIG.
	rowLocalAttrs, err := p.buildRowLocalAttributes(req.Attributes)
	if err != nil {
		return nil, err
	}

	var totalRows, filteredRows int64
	for iter.Next() {
		totalRows++
		r := iter.Record()

		// Inject derived feature columns onto the record so filters and
		// online aggregators see them. EmitRow is called once per record
		// in iteration order; operators that need positional state
		// (e.g., FEAT_TRAIN_TEST_SPLIT) rely on that ordering.
		for _, h := range streamingFeatures {
			outputs, err := h.Computer.EmitRow(r, h.Feature.Field)
			if err != nil {
				return nil, err
			}
			for label, out := range outputs {
				if len(out.Nulls) > 0 && out.Nulls[0] {
					r.SetNull(label)
				} else {
					r.Set(label, out.Values[0])
				}
			}
		}

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

		// Apply row-local attributes inline; values land on the record
		// under their label so aggregations referencing the label resolve
		// like they do in the buffered path.
		for _, ra := range rowLocalAttrs {
			val, err := ra.computer.Row(r, ra.attr.Field)
			if err != nil {
				return nil, err
			}
			r.Set(ra.label, val)
		}

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

// processStreamingGrouped runs the streaming execution path for
// requests with one streamable grouper. Each filter-passing record's
// group key indexes a per-key bucket of fresh OnlineAggregator
// instances; finalize emits one output row per distinct key with the
// group field set to the key string. Matches the buffered processGrouped
// contract: only the first group is consulted (multi-grouper composite
// keys are not supported in either path today).
//
// Memory bound: O(distinct_groups × per_aggregator_state). High-cardinality
// groups still hold every key's aggregator in memory; the win is avoiding
// the full record buffer that the buffered path requires.
func (p *Processor) processStreamingGrouped(ctx context.Context, req *types.Request, iter RecordIterator) (*types.Response, error) {
	EnableReuse(iter)
	grp := req.Groups[0]
	grouperFactory, ok := grouperRegistry[grp.Type]
	if !ok {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("unknown group type: %s", grp.Type))
	}
	grouperInstance, err := grouperFactory(grp, p.schema)
	if err != nil {
		return nil, err
	}
	streamGrp, ok := grouperInstance.(StreamingGrouper)
	if !ok {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("grouper %s does not implement StreamingGrouper", grp.Type))
	}

	// Pre-compute aggregator labels so per-key bucket construction is cheap.
	type aggSpec struct {
		agg     *types.Aggregation
		label   string
		factory AggregatorFactory
	}
	specs := make([]aggSpec, len(req.Aggregations))
	for i, agg := range req.Aggregations {
		factory := aggregatorRegistry[agg.Type] // canStream verified
		label := agg.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", agg.Type, agg.Field)
		}
		specs[i] = aggSpec{agg: agg, label: label, factory: factory}
	}

	// Build feature handles + filter funcs + row-local attrs once.
	var streamingFeatures []feature.StreamingHandle
	if len(req.Features) > 0 {
		handles, err := feature.BuildStreaming(req.Features, p.schema)
		if err != nil {
			return nil, err
		}
		streamingFeatures = handles
		for iter.Next() {
			rec := iter.Record()
			for _, h := range streamingFeatures {
				if err := h.Computer.PrePass(rec, h.Feature.Field); err != nil {
					return nil, err
				}
			}
		}
		for _, h := range streamingFeatures {
			if err := h.Computer.Finalize(); err != nil {
				return nil, err
			}
		}
		iter.Reset()
	}

	filterFns, err := p.buildFilterFuncs(req.Filterers)
	if err != nil {
		return nil, err
	}
	rowLocalAttrs, err := p.buildRowLocalAttributes(req.Attributes)
	if err != nil {
		return nil, err
	}

	// Per-group bucket. Keys are insertion-ordered to match buffered
	// path's map-iteration nondeterminism: callers that need stable
	// ordering must apply Sort.
	type bucket struct {
		online []OnlineAggregator
	}
	buckets := make(map[string]*bucket)

	var totalRows, filteredRows int64
	for iter.Next() {
		totalRows++
		r := iter.Record()

		for _, h := range streamingFeatures {
			outputs, err := h.Computer.EmitRow(r, h.Feature.Field)
			if err != nil {
				return nil, err
			}
			for label, out := range outputs {
				if len(out.Nulls) > 0 && out.Nulls[0] {
					r.SetNull(label)
				} else {
					r.Set(label, out.Values[0])
				}
			}
		}

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

		for _, ra := range rowLocalAttrs {
			val, err := ra.computer.Row(r, ra.attr.Field)
			if err != nil {
				return nil, err
			}
			r.Set(ra.label, val)
		}

		key, ok, err := streamGrp.KeyForRow(r, grp.Field)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue // null group key — buffered path skips these too
		}

		b, exists := buckets[key]
		if !exists {
			online := make([]OnlineAggregator, len(specs))
			for i, s := range specs {
				inst, err := s.factory(s.agg, p.schema)
				if err != nil {
					return nil, err
				}
				oa, ok := inst.(OnlineAggregator)
				if !ok {
					return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
						fmt.Sprintf("aggregator %s does not implement OnlineAggregator", s.agg.Type))
				}
				online[i] = oa
			}
			b = &bucket{online: online}
			buckets[key] = b
		}
		for i, oa := range b.online {
			if err := oa.UpdateRow(r, specs[i].agg.Field); err != nil {
				return nil, err
			}
		}
	}

	data := make([]map[string]any, 0, len(buckets))
	for key, b := range buckets {
		// +1 reserved for the group key written below.
		row := make(map[string]any, len(specs)+1)
		for i, oa := range b.online {
			val, err := oa.Finalize()
			if err != nil {
				return nil, err
			}
			row[specs[i].label] = val
		}
		row[grp.Field] = key
		data = append(data, row)
	}

	// Apply sort to grouped output, mirroring processRecords behavior so
	// callers receive deterministic ordering on a small post-aggregate set.
	if len(req.Sort) > 0 {
		window.Sort(data, req.Sort)
	}

	_ = ctx
	return &types.Response{
		Data: data,
		Metadata: &types.ResponseMetadata{
			TotalRows:    totalRows,
			FilteredRows: filteredRows,
		},
	}, nil
}

// twoPassAttrEntry pairs an attribute spec with its constructed
// two-pass computer and resolved output label. processStreamingTwoPass
// drives one entry per filter-passing record (PrePass) then one per
// record again in pass 2 (Row).
type twoPassAttrEntry struct {
	attr     *types.Attribute
	computer TwoPassAttribute
	label    string
}

// buildTwoPassAttributes splits the attribute list into two-pass and
// row-local buckets. Both buckets share the same construction +
// validation path; only the runtime drive differs.
func (p *Processor) buildTwoPassAttributes(attrs []*types.Attribute) (twoPass []twoPassAttrEntry, rowLocal []rowLocalAttrEntry, err error) {
	for _, attr := range attrs {
		factory, ok := attributeRegistry[attr.Type]
		if !ok {
			return nil, nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown attribute type: %s", attr.Type))
		}
		computer, factoryErr := factory(attr, p.schema)
		if factoryErr != nil {
			return nil, nil, factoryErr
		}
		label := attr.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", attr.Type, attr.Field)
		}
		if tp, ok := computer.(TwoPassAttribute); ok {
			twoPass = append(twoPass, twoPassAttrEntry{attr: attr, computer: tp, label: label})
			continue
		}
		if rl, ok := computer.(RowLocalAttribute); ok {
			rowLocal = append(rowLocal, rowLocalAttrEntry{attr: attr, computer: rl, label: label})
			continue
		}
		return nil, nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("attribute %s implements neither TwoPassAttribute nor RowLocalAttribute", attr.Type))
	}
	return twoPass, rowLocal, nil
}

// processStreamingTwoPass runs the two-pass streaming path: pass 1
// folds every filter-passing record into TwoPassAttribute.PrePass,
// Finalize locks population stats, iter.Reset() rewinds, and pass 2
// emits attribute values + folds online aggregations row-by-row.
//
// canStream verified: no features, no groups, no windows, every
// aggregation online, every attribute either two-pass or row-local.
// These restrictions keep the orchestration matrix manageable for v1;
// extending to feature/grouped combinations is tracked separately.
//
// Memory bound: O(per_attribute_state). Pass 1 + pass 2 = 2× iter
// scan; the underlying file is typically OS-page-cached after pass 1.
func (p *Processor) processStreamingTwoPass(ctx context.Context, req *types.Request, iter RecordIterator) (*types.Response, error) {
	EnableReuse(iter)
	filterFns, err := p.buildFilterFuncs(req.Filterers)
	if err != nil {
		return nil, err
	}
	twoPassAttrs, rowLocalAttrs, err := p.buildTwoPassAttributes(req.Attributes)
	if err != nil {
		return nil, err
	}

	// Build aggregator instances once; they are reset implicitly by
	// being constructed fresh per Process call. Each one will fold
	// pass 2's filter-passing records into its running state.
	type onlineEntry struct {
		agg    *types.Aggregation
		online OnlineAggregator
	}
	entries := make([]onlineEntry, len(req.Aggregations))
	for i, agg := range req.Aggregations {
		factory := aggregatorRegistry[agg.Type]
		instance, err := factory(agg, p.schema)
		if err != nil {
			return nil, err
		}
		online, ok := instance.(OnlineAggregator)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("aggregator %s does not implement OnlineAggregator", agg.Type))
		}
		entries[i] = onlineEntry{agg: agg, online: online}
	}

	// Pass 1: fold every filter-passing record into each two-pass
	// attribute's PrePass. Filters apply here so attribute population
	// stats match the buffered Compute path (which receives the already-
	// filtered slice).
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
		for _, ta := range twoPassAttrs {
			if err := ta.computer.PrePass(r, ta.attr.Field); err != nil {
				return nil, err
			}
		}
	}
	for _, ta := range twoPassAttrs {
		if err := ta.computer.Finalize(); err != nil {
			return nil, err
		}
	}
	iter.Reset()

	// Pass 2: re-scan, apply filters again, emit attribute values onto
	// each filter-passing record (two-pass first so any row-local
	// attribute referencing a two-pass label sees it), fold each agg.
	for iter.Next() {
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
		for _, ta := range twoPassAttrs {
			val, err := ta.computer.Row(r, ta.attr.Field)
			if err != nil {
				return nil, err
			}
			r.Set(ta.label, val)
		}
		for _, ra := range rowLocalAttrs {
			val, err := ra.computer.Row(r, ra.attr.Field)
			if err != nil {
				return nil, err
			}
			r.Set(ra.label, val)
		}
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

// rowLocalAttrEntry pairs an attribute spec with its constructed
// row-local computer and resolved output label. The streaming path
// drives one entry per filter-passing record.
type rowLocalAttrEntry struct {
	attr     *types.Attribute
	computer RowLocalAttribute
	label    string
}

// buildRowLocalAttributes constructs RowLocalAttribute instances for the
// streaming path. Returns PROCESSING_INTERNAL if any attribute fails the
// type assertion (canStream should have rejected the request earlier).
func (p *Processor) buildRowLocalAttributes(attrs []*types.Attribute) ([]rowLocalAttrEntry, error) {
	if len(attrs) == 0 {
		return nil, nil
	}
	out := make([]rowLocalAttrEntry, 0, len(attrs))
	for _, attr := range attrs {
		factory, ok := attributeRegistry[attr.Type]
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown attribute type: %s", attr.Type))
		}
		computer, err := factory(attr, p.schema)
		if err != nil {
			return nil, err
		}
		rowLocal, ok := computer.(RowLocalAttribute)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("attribute %s does not implement RowLocalAttribute", attr.Type))
		}
		label := attr.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", attr.Type, attr.Field)
		}
		out = append(out, rowLocalAttrEntry{attr: attr, computer: rowLocal, label: label})
	}
	return out, nil
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
		label := agg.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", agg.Type, agg.Field)
		}
		// Geo aggregations dispatch to typed AggregateGeoField.
		if IsGeoAggregation(agg.Type) {
			out, err := AggregateGeoField(agg.Type, records, agg.Field)
			if err != nil {
				return nil, err
			}
			row[label] = out
			continue
		}
		// Decimal-typed fields dispatch to AggregateDecimalField.
		if p.schema != nil {
			if f := p.schema.Field(agg.Field); f != nil && f.Type.IsDecimal() {
				if !IsDecimalAggregationSupported(agg.Type) {
					return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
						"aggregation has no decimal128 implementation",
						map[string]any{"aggregation": string(agg.Type), "field": agg.Field})
				}
				out, err := AggregateDecimalField(agg.Type, records, agg.Field, f.Scale)
				if err != nil {
					return nil, err
				}
				row[label] = out
				continue
			}
		}
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
		row[label] = val
	}
	return row, nil
}
