package processing

import (
	"context"
	"fmt"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing/feature"
	"github.com/frankbardon/pulse/processing/regression"
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
	exts     *ExtensionRegistry
}

// NewProcessor creates a new Processor for the given schema. The
// resulting Processor uses only Pulse-shipped operator factories;
// embedder extensions land via NewProcessorWithExtensions.
func NewProcessor(schema *encoding.Schema) *Processor {
	return &Processor{schema: schema}
}

// NewProcessorWithExtensions creates a Processor whose operator
// lookups consult exts before falling through to the built-in
// registries. Passing nil is equivalent to NewProcessor.
func NewProcessorWithExtensions(schema *encoding.Schema, exts *ExtensionRegistry) *Processor {
	return &Processor{schema: schema, exts: exts}
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

// CanMergeRequest reports whether a request's online state is
// mergeable across input partitions — the gate that the per-shard
// parallel reducer in service/shard_reduce.go consults before fanning
// out work across a worker pool. Returns true iff every aggregator,
// grouper, and filterer is mergeable AND the request contains no
// windows, no features, no regressions, no tests, no two-pass
// attributes, and no decimal-typed aggregation targets. A nil/empty
// aggregation list returns false (nothing to merge).
//
// Mergeable is a strict subset of Streamable. Custom extension
// operators are conservatively treated as non-mergeable (the merge
// surface is not yet exposed on the extension registration struct).
func CanMergeRequest(req *types.Request, schema *encoding.Schema) bool {
	if req == nil {
		return false
	}
	if len(req.Aggregations) == 0 {
		return false
	}
	if len(req.Windows) > 0 || len(req.Features) > 0 ||
		len(req.Regressions) > 0 || len(req.Tests) > 0 ||
		len(req.PostTests) > 0 {
		return false
	}
	for _, attr := range req.Attributes {
		// Row-local attributes (FORMULA, DATE_PART) are mergeable in
		// principle — they add a derived column per row before each
		// aggregator folds it. Two-pass attributes (ZSCORE, TSCORE,
		// NORMALIZED, REG_*) need pass-1 population stats that the
		// per-shard reducer would have to derive from merged Welford
		// state; v1 routes those through the serial path.
		if attr == nil {
			return false
		}
		switch attr.Type {
		case types.ATTR_FORMULA, types.ATTR_DATE_PART:
			// row-local: mergeable.
		default:
			return false
		}
	}
	for _, grp := range req.Groups {
		if grp == nil || !grp.Type.Mergeable() {
			return false
		}
	}
	for _, f := range req.Filterers {
		if f == nil || !f.Type.Streamable() {
			return false
		}
	}
	for _, agg := range req.Aggregations {
		if agg == nil || !agg.Type.Mergeable() {
			return false
		}
		// Decimal-typed fields aggregate via AggregateDecimalField;
		// the per-shard reducer doesn't yet handle the wide path.
		if schema != nil {
			if f := schema.Field(agg.Field); f != nil && f.Type.IsDecimal() {
				return false
			}
		}
		// Custom extension aggregators do not yet expose MergeOnline;
		// reject conservatively.
		if _, builtin := aggregatorRegistry[agg.Type]; !builtin {
			return false
		}
	}
	return true
}

// requiresTwoPass reports whether the attribute type implements
// TwoPassAttribute (and therefore needs a PrePass over filter-passing
// records before per-row emission). Mirrors the attribute factories in
// processing/attribute.go and attribute_reg.go: ZSCORE/TSCORE/NORMALIZED
// need population stats; ATTR_REG_FITTED/RESIDUAL/LEVERAGE need a
// finalize-time regression fit; FORMULA/DATE_PART are pure row-local.
func requiresTwoPass(t types.AttributeType) bool {
	switch t {
	case types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_NORMALIZED,
		types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE:
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
//     RANGE, ROUNDED — partitioned via grouped streaming path).
//     QUANTILE/DATE require a finalize-time view of the full set.
//   - attributes: empty, OR every attribute.Type.Streamable()=true
//     (FORMULA, DATE_PART implement RowLocalAttribute and execute
//     inline). ZSCORE/TSCORE/NORMALIZED/PERCENTILE need population stats.
//   - no windows (window operators run over the post-aggregate row set)
//   - features either empty or every operator implements
//     feature.StreamingComputer (PrePass + Finalize + EmitRow)
//   - every aggregation type supports OnlineAggregator
//   - filters are row-level only (every registered filter today is)
//   - tier-1 tests: empty, OR every test type streamable AND registered
//     AND no groupers / features / two-pass attributes (those
//     combinations are not yet wired through the streaming paths)
//
// Tier-2 tests (req.PostTests) never affect streamability — they run
// after `data` is materialized regardless of which path produced it.
//
// Returns false on any unknown component type so the buffered path can
// surface the canonical error message.
func (p *Processor) canStream(req *types.Request) bool {
	if len(req.Windows) > 0 {
		return false
	}
	// Mixed-mode overlay downgrade (E3-S6, per PRD §6 "Performance /
	// scale"): if any spec on req.Overlays is non-streamable (E3-S5
	// sibling kinds today), force the buffered path so the post-
	// finalize hook (applyOverlaysSeriesToResponse) sees a fully
	// materialised SeriesHostView. Unknown overlay kinds also force
	// buffered so ApplyOverlaysSeries can surface the canonical
	// PULSE_OVERLAY_KIND_UNKNOWN error from the buffered exit. The
	// gate stays central — every other canStream branch already runs
	// in O(req-slot-count); one extra slice walk here keeps the
	// streaming-eligibility decision single-pass.
	if !canStreamOverlays(req) {
		return false
	}
	// Regression slots stream only when every spec opts in via
	// RegressionSpec.Streamable() (today: unpenalized REG_OLS without
	// Resample/Selection modifiers). The streaming path does not yet
	// compose regressions with groupers, features, two-pass attributes,
	// or row tests — those combinations route through the buffered
	// path so the orchestrator's invariants stay tight.
	if len(req.Regressions) > 0 {
		for _, reg := range req.Regressions {
			if reg == nil {
				return false
			}
			if !reg.Streamable() {
				return false
			}
		}
		if len(req.Groups) > 0 || len(req.Features) > 0 || hasTwoPassAttribute(req) || len(req.Tests) > 0 {
			return false
		}
	}
	if len(req.Tests) > 0 {
		if !canRunRowTests(req.Tests, p.exts) {
			return false
		}
		for _, t := range req.Tests {
			if !t.Type.Streamable() {
				return false
			}
		}
		// Tier-1 tests do not yet compose with groups, features, or
		// two-pass attributes inside the streaming paths. Route those
		// combinations through the buffered path so the row tests
		// still execute correctly over the filtered record set.
		if len(req.Groups) > 0 || len(req.Features) > 0 || hasTwoPassAttribute(req) {
			return false
		}
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
	if len(req.Features) > 0 && !feature.IsStreamableWithExt(req.Features, p.schema, p.exts.FeatureFactories()) {
		return false
	}
	if len(req.Aggregations) == 0 {
		// No aggregations: buffered path produces the same empty data
		// payload and exposes the same error surface (e.g., when a
		// downstream component validates against the materialized set).
		return false
	}
	for _, agg := range req.Aggregations {
		// Decimal-typed fields are aggregated via AggregateDecimalField;
		// the streaming numeric fold loses precision.
		if p.schema != nil {
			if f := p.schema.Field(agg.Field); f != nil && f.Type.IsDecimal() {
				return false
			}
		}
		factory, ok := p.exts.LookupAggregator(agg.Type)
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
		handles, err := feature.BuildStreamingWithExt(req.Features, p.schema, p.exts.FeatureFactories())
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

	// Build filter functions once. The per-slot universal-floor
	// counter triple {n_in, n_out, n_null_input} lives in
	// filterCounters and rides alongside filterFns through the
	// per-record applyFilterPass walk (E2-S9). Empty filter chains
	// keep the counter slice nil so the no-filter fast path stays
	// allocation-free.
	filterFns, err := p.buildFilterFuncs(req.Filterers)
	if err != nil {
		return nil, err
	}
	filterCounters := newFilterPassCounters(req.Filterers)

	// Build aggregator instances and their online interfaces. Each
	// factory call produces a fresh, zero-state instance; safe to use
	// directly as a streaming accumulator. Per-slot n / nNull counters
	// feed the universal floor on Response.Components.Aggregations
	// (E1-S5) — n counts non-null contributors, nNull counts null
	// inputs; the orchestrator increments them per record alongside
	// each aggregator's UpdateRow.
	type onlineEntry struct {
		agg    *types.Aggregation
		online OnlineAggregator
		n      int
		nNull  int
	}
	entries := make([]onlineEntry, len(req.Aggregations))
	for i, agg := range req.Aggregations {
		factory, _ := p.exts.LookupAggregator(agg.Type) // canStream verified existence
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

	// Build tier-1 row tests; they fold each filter-passing record
	// alongside the online aggregators.
	rowTests, err := p.buildRowTests(req.Tests)
	if err != nil {
		return nil, err
	}

	// Build streaming regression engines. canStream verified every
	// regression spec is streamable (today: unpenalized REG_OLS with no
	// modifiers); BuildStreaming surfaces PROCESSING_INTERNAL if any
	// engine slipped through without a streaming implementation.
	regressionEngines, err := regression.BuildStreaming(req.Regressions, p.schema)
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

		pass, err := applyFilterPass(r, req.Filterers, filterFns, filterCounters)
		if err != nil {
			return nil, err
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

		for i := range entries {
			e := &entries[i]
			if _, ok := r.NumericValue(e.agg.Field); ok {
				e.n++
			} else {
				e.nNull++
			}
			if err := e.online.UpdateRow(r, e.agg.Field); err != nil {
				return nil, err
			}
		}
		for _, rt := range rowTests {
			if err := rt.test.UpdateRow(r); err != nil {
				return nil, err
			}
		}
		// Regression engines see the same filter-passing record once.
		// listwise null deletion happens inside the engine.
		for _, re := range regressionEngines {
			if err := re.UpdateRow(r); err != nil {
				return nil, err
			}
		}
	}

	row := make(map[string]any, len(entries))
	type finalizedEntry struct {
		online OnlineAggregator
		agg    *types.Aggregation
		n      int
		nNull  int
	}
	finalized := make([]finalizedEntry, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		val, err := e.online.Finalize()
		if err != nil {
			return nil, err
		}
		label := e.agg.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", e.agg.Type, e.agg.Field)
		}
		row[label], err = dispatchAggregatorResult(e.online, val)
		if err != nil {
			return nil, err
		}
		finalized = append(finalized, finalizedEntry{
			online: e.online,
			agg:    e.agg,
			n:      e.n,
			nNull:  e.nNull,
		})
	}

	var data []map[string]any
	if len(entries) > 0 {
		data = []map[string]any{row}
	}

	testResults, err := finalizeRowTests(rowTests)
	if err != nil {
		return nil, err
	}
	postResults, err := p.runPostTests(req.PostTests, data)
	if err != nil {
		return nil, err
	}

	// Finalize streaming regression fits. Each engine's Finalize() may
	// return PROCESSING_REGRESSION_INSUFFICIENT_DATA / _RANK_DEFICIENT;
	// either short-circuits the Process call, matching the buffered path.
	var regressionResults []*types.RegressionResult
	if len(regressionEngines) > 0 {
		regressionResults = make([]*types.RegressionResult, 0, len(regressionEngines))
		for _, re := range regressionEngines {
			res, err := re.Finalize()
			if err != nil {
				return nil, err
			}
			regressionResults = append(regressionResults, res)
		}
	}

	_ = ctx
	resp := &types.Response{
		Data: data,
		Metadata: &types.ResponseMetadata{
			TotalRows:    totalRows,
			FilteredRows: filteredRows,
		},
		Tests:       testResults,
		PostTests:   postResults,
		Regressions: regressionResults,
	}

	// E1-S5: emit AggregationComponents per slot. Universal floor (n,
	// nNull) is the orchestrator-tracked per-record bookkeeping; the
	// operator-specific map rides off the MetaAggregator sibling when
	// the aggregator implements it. Aggregators without MetaAggregator
	// fall through to a floor-only entry (Operator left nil), which
	// marshals as omitempty so the wire form stays byte-identical
	// against the pre-MetaAggregator baseline.
	for _, fe := range finalized {
		entry, err := buildAggregationComponents(fe.online, fe.agg, fe.n, fe.nNull)
		if err != nil {
			return nil, err
		}
		attachAggregationComponents(resp, entry)
	}

	// E2-S9: emit FiltererComponents per slot from the per-record
	// counter walk. Empty filter chains stay nil so the omitempty
	// wire shape is byte-identical against the pre-E2-S9 baseline.
	attachFiltererComponents(resp, buildFiltererComponents(req.Filterers, filterCounters))

	// E2-S11: emit RunComponents — typed cohort-level counters.
	// NullRecords pulls from the FIRST aggregator's per-record nNull
	// counter (the primary-field convention locked in
	// run_components.go); single-pass streaming has no shard concept,
	// so ShardCount stays 0 and PartialCohortReason stays empty.
	var nullRecords int64
	if len(finalized) > 0 {
		nullRecords = int64(finalized[0].nNull)
	}
	attachRunComponents(resp, RunCountersInput{
		TotalRecords:    totalRows,
		FilteredRecords: filteredRows,
		NullRecords:     nullRecords,
	})
	return resp, nil
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
	grouperFactory, ok := p.exts.LookupGrouper(grp.Type)
	if !ok {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("unknown group type: %s", grp.Type))
	}
	grouperInstance, err := grouperFactory(grp, p.schema)
	if err != nil {
		return nil, err
	}
	streamGrp, _ := grouperInstance.(StreamingGrouper)
	multiGrp, _ := grouperInstance.(MultiKeyStreamingGrouper)
	if streamGrp == nil && multiGrp == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("grouper %s does not implement StreamingGrouper or MultiKeyStreamingGrouper", grp.Type))
	}

	// Pre-compute aggregator labels so per-key bucket construction is cheap.
	type aggSpec struct {
		agg     *types.Aggregation
		label   string
		factory AggregatorFactory
	}
	specs := make([]aggSpec, len(req.Aggregations))
	for i, agg := range req.Aggregations {
		factory, _ := p.exts.LookupAggregator(agg.Type) // canStream verified
		label := agg.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", agg.Type, agg.Field)
		}
		specs[i] = aggSpec{agg: agg, label: label, factory: factory}
	}

	// Build feature handles + filter funcs + row-local attrs once.
	var streamingFeatures []feature.StreamingHandle
	if len(req.Features) > 0 {
		handles, err := feature.BuildStreamingWithExt(req.Features, p.schema, p.exts.FeatureFactories())
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
	// E2-S9: per-slot filter-pass counters ride alongside filterFns
	// through the streaming grouped path; same applyFilterPass walk
	// the ungrouped streaming path uses.
	filterCounters := newFilterPassCounters(req.Filterers)
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

	// E2-S11: track null count on the primary aggregation field for
	// RunComponents.NullRecords. Resolution mirrors the package-level
	// "primary field" convention: first aggregator's Field, else the
	// grouper's Field. Counter increments on every post-filter row
	// where the primary field is null (the streaming-grouped path has
	// no per-aggregator nNull counter — buckets allocate lazily on
	// non-null key matches — so the orchestrator tracks it inline).
	primaryField := ""
	if len(req.Aggregations) > 0 && req.Aggregations[0] != nil {
		primaryField = req.Aggregations[0].Field
	}
	if primaryField == "" {
		primaryField = grp.Field
	}
	var primaryNullRecords int64

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

		pass, err := applyFilterPass(r, req.Filterers, filterFns, filterCounters)
		if err != nil {
			return nil, err
		}
		if !pass {
			continue
		}
		filteredRows++

		if primaryField != "" && r.IsNull(primaryField) {
			primaryNullRecords++
		}

		for _, ra := range rowLocalAttrs {
			val, err := ra.computer.Row(r, ra.attr.Field)
			if err != nil {
				return nil, err
			}
			r.Set(ra.label, val)
		}

		var rowKeys []string
		if multiGrp != nil {
			keys, ok, err := multiGrp.KeysForRow(r, grp.Field)
			if err != nil {
				return nil, err
			}
			if !ok || len(keys) == 0 {
				continue
			}
			rowKeys = keys
		} else {
			key, ok, err := streamGrp.KeyForRow(r, grp.Field)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue // null group key — buffered path skips these too
			}
			rowKeys = []string{key}
		}

		for _, key := range rowKeys {
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
	}

	// Stable-emit by sorted bucket key so row order is deterministic
	// across runs regardless of Go's map-iteration randomness or
	// streaming-vs-buffered codepath choice.
	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	data := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		b := buckets[key]
		// +1 reserved for the group key written below.
		row := make(map[string]any, len(specs)+1)
		for i, oa := range b.online {
			val, err := oa.Finalize()
			if err != nil {
				return nil, err
			}
			row[specs[i].label], err = dispatchAggregatorResult(oa, val)
			if err != nil {
				return nil, err
			}
		}
		row[grp.Field] = key
		data = append(data, row)
	}

	// Apply explicit Sort to grouped output, mirroring processRecords
	// behavior. The default stable key-order above gives a deterministic
	// fallback when no Sort is specified.
	if len(req.Sort) > 0 {
		window.Sort(data, req.Sort)
	}

	postResults, err := p.runPostTests(req.PostTests, data)
	if err != nil {
		return nil, err
	}

	_ = ctx
	resp := &types.Response{
		Data: data,
		Metadata: &types.ResponseMetadata{
			TotalRows:    totalRows,
			FilteredRows: filteredRows,
		},
		PostTests: postResults,
	}

	// E2-S4: streaming grouped path emits one GrouperComponents entry
	// per Group slot (single-grouper today). Bucket counts ride off
	// the grouper's live state (populated by KeyForRow per filter-
	// passing row); TotalN sums the bucket counts; NNull falls out of
	// filteredRows minus TotalN — every post-filter record that did
	// NOT land in a bucket (null field value, include-filter rejection,
	// empty set mask, etc.). The grouper instance is the same object
	// KeyForRow drove against, so MetaGrouper.Components() reads off
	// the same map.
	streamEntry, gerr := buildStreamingGrouperComponents(grouperInstance, grp, int(filteredRows))
	if gerr != nil {
		return nil, gerr
	}
	attachGrouperComponents(resp, streamEntry)

	// E2-S9: emit FiltererComponents per slot. Empty filter chains
	// stay nil so the omitempty wire shape is byte-identical against
	// the pre-E2-S9 baseline.
	attachFiltererComponents(resp, buildFiltererComponents(req.Filterers, filterCounters))

	// E2-S11: emit RunComponents — typed cohort-level counters. The
	// streaming-grouped path has no shard concept (records flow off a
	// single iterator regardless of the underlying cohort topology), so
	// ShardCount stays 0 and PartialCohortReason stays empty.
	attachRunComponents(resp, RunCountersInput{
		TotalRecords:    totalRows,
		FilteredRecords: filteredRows,
		NullRecords:     primaryNullRecords,
	})

	// SERIES-host overlay hook (E3-S6). Streaming grouped exit calls
	// into the same post-finalize wiring as the buffered processRecords
	// path. Streamable overlay kinds (E3-S2/S3/S4 — INDEX_VS_TOTAL /
	// SHARE_OF_TOTAL / ZSCORE_VS_TOTAL) fold against the already-
	// materialised per-group SeriesPayload at this exit; mixed-mode
	// requests carrying a non-streamable kind never reach here because
	// canStream's canStreamOverlays gate forces them to the buffered
	// path (see processor.go canStream above).
	if err := applyOverlaysSeriesToResponse(req, resp); err != nil {
		return nil, err
	}
	return resp, nil
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
		factory, ok := p.exts.LookupAttribute(attr.Type)
		if !ok {
			return nil, nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown attribute type: %s", attr.Type))
		}
		computer, factoryErr := factory(attr, p.schema)
		if factoryErr != nil {
			return nil, nil, factoryErr
		}
		if aware, ok := computer.(ExtensionAware); ok {
			aware.SetExtensions(p.exts)
		}
		label := attr.Label
		if label == "" {
			label = defaultAttributeLabel(attr)
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
	// E2-S9: per-slot filter-pass counters track {n_in, n_out,
	// n_null_input}. The two-pass path walks every filter-passing
	// record twice (PrePass + Row); counters increment ONLY on pass 1
	// so the values match the single-pass observed record set —
	// per-slot n_in / n_out / n_null_input count distinct input rows,
	// not pass-1+pass-2 doubled visits.
	filterCounters := newFilterPassCounters(req.Filterers)
	twoPassAttrs, rowLocalAttrs, err := p.buildTwoPassAttributes(req.Attributes)
	if err != nil {
		return nil, err
	}

	// Build aggregator instances once; they are reset implicitly by
	// being constructed fresh per Process call. Each one will fold
	// pass 2's filter-passing records into its running state. Per-slot
	// n / nNull mirror the universal-floor counters used by the single-
	// pass streaming path (see processStreaming above).
	type onlineEntry struct {
		agg    *types.Aggregation
		online OnlineAggregator
		n      int
		nNull  int
	}
	entries := make([]onlineEntry, len(req.Aggregations))
	for i, agg := range req.Aggregations {
		factory, _ := p.exts.LookupAggregator(agg.Type)
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
	// filtered slice). Per-slot {n_in, n_out, n_null_input} counters
	// increment only here so totals match the single-pass observed
	// record set.
	var totalRows, filteredRows int64
	for iter.Next() {
		totalRows++
		r := iter.Record()
		pass, err := applyFilterPass(r, req.Filterers, filterFns, filterCounters)
		if err != nil {
			return nil, err
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
	// Counters were closed in pass 1; the per-record filter walk here
	// re-runs each FilterFunc to gate the per-record work but skips
	// the counter increment so values stay aligned with pass 1.
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
		for i := range entries {
			e := &entries[i]
			if _, ok := r.NumericValue(e.agg.Field); ok {
				e.n++
			} else {
				e.nNull++
			}
			if err := e.online.UpdateRow(r, e.agg.Field); err != nil {
				return nil, err
			}
		}
	}

	row := make(map[string]any, len(entries))
	for i := range entries {
		e := &entries[i]
		val, err := e.online.Finalize()
		if err != nil {
			return nil, err
		}
		label := e.agg.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", e.agg.Type, e.agg.Field)
		}
		row[label], err = dispatchAggregatorResult(e.online, val)
		if err != nil {
			return nil, err
		}
	}

	var data []map[string]any
	if len(entries) > 0 {
		data = []map[string]any{row}
	}

	postResults, err := p.runPostTests(req.PostTests, data)
	if err != nil {
		return nil, err
	}

	_ = ctx
	resp := &types.Response{
		Data: data,
		Metadata: &types.ResponseMetadata{
			TotalRows:    totalRows,
			FilteredRows: filteredRows,
		},
		PostTests: postResults,
	}

	// E1-S5: emit AggregationComponents per slot for the two-pass
	// streaming path. Mirrors the single-pass streaming exit (see
	// processStreaming above).
	for i := range entries {
		e := &entries[i]
		entry, err := buildAggregationComponents(e.online, e.agg, e.n, e.nNull)
		if err != nil {
			return nil, err
		}
		attachAggregationComponents(resp, entry)
	}

	// E2-S9: emit FiltererComponents per slot. Counters were
	// populated during pass 1 (pass 2 re-runs the filter funcs for
	// gating but skips the counter increment so totals match the
	// observed record set, not pass-1+pass-2 visits).
	attachFiltererComponents(resp, buildFiltererComponents(req.Filterers, filterCounters))

	// E2-S11: emit RunComponents — typed cohort-level counters.
	// NullRecords pulls from the FIRST aggregator's per-record nNull
	// counter (mirrors processStreaming above); two-pass streaming has
	// no shard concept so ShardCount stays 0 and PartialCohortReason
	// stays empty.
	var nullRecords int64
	if len(entries) > 0 {
		nullRecords = int64(entries[0].nNull)
	}
	attachRunComponents(resp, RunCountersInput{
		TotalRecords:    totalRows,
		FilteredRecords: filteredRows,
		NullRecords:     nullRecords,
	})
	return resp, nil
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
		factory, ok := p.exts.LookupAttribute(attr.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown attribute type: %s", attr.Type))
		}
		computer, err := factory(attr, p.schema)
		if err != nil {
			return nil, err
		}
		if aware, ok := computer.(ExtensionAware); ok {
			aware.SetExtensions(p.exts)
		}
		rowLocal, ok := computer.(RowLocalAttribute)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("attribute %s does not implement RowLocalAttribute", attr.Type))
		}
		label := attr.Label
		if label == "" {
			label = defaultAttributeLabel(attr)
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
		factory, ok := p.exts.LookupFilterer(f.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown filter type: %s", f.Type))
		}
		builder := factory()
		if aware, ok := builder.(ExtensionAware); ok {
			aware.SetExtensions(p.exts)
		}
		fn, err := builder.Build(f, p.schema)
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

	// Step 1: Apply filters. The counter slice carries the per-slot
	// {n_in, n_out, n_null_input} universal floor used by
	// Response.Components.Filterers (E2-S9). Empty filter chains
	// short-circuit to a nil counters slice so the omitempty wire
	// shape stays byte-identical against the pre-E2-S9 baseline.
	filtered, filterCounters, err := p.applyFiltersWithCounters(req.Filterers, records)
	if err != nil {
		return nil, err
	}

	// Step 2: Compute attributes (adds derived fields to records)
	if err := p.applyAttributes(req.Attributes, filtered); err != nil {
		return nil, err
	}

	// Step 3: Group if needed, then aggregate
	var data []map[string]any
	var aggComponents []types.AggregationComponents
	var grpComponents []types.GrouperComponents

	if len(req.Groups) > 0 {
		data, grpComponents, err = p.processGrouped(req, filtered)
		if err != nil {
			return nil, err
		}
	} else if len(req.Aggregations) > 0 {
		var row map[string]any
		row, aggComponents, err = p.aggregateWithComponents(req.Aggregations, filtered, true)
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
		if err := window.ApplyWithExt(ctx, data, req.Windows, p.exts.WindowFactories()); err != nil {
			return nil, err
		}
	}

	if len(req.Sort) > 0 {
		window.Sort(data, req.Sort)
	}

	// Tier-1 row tests fold over the filtered record set. Buffered path
	// hits the same per-record UpdateRow contract as streaming.
	rowTests, err := p.buildRowTests(req.Tests)
	if err != nil {
		return nil, err
	}
	for _, rt := range rowTests {
		for _, rec := range filtered {
			if err := rt.test.UpdateRow(rec); err != nil {
				return nil, err
			}
		}
	}
	testResults, err := finalizeRowTests(rowTests)
	if err != nil {
		return nil, err
	}
	postResults, err := p.runPostTests(req.PostTests, data)
	if err != nil {
		return nil, err
	}

	// Regression fits run after aggregation / windows so engines can
	// observe the filtered record set. Phase 1 routes the filtered
	// slice through FitBuffered: the unpenalized OLS engine consumes
	// every record via its Welford-style accumulator, while engines
	// for unimplemented operators (penalized OLS, GLM, Bayes, and the
	// Resample/Selection modifier wrappers) still surface
	// PROCESSING_REGRESSION_NOT_IMPLEMENTED via Fit().
	regressionResults, err := regression.FitBuffered(req.Regressions, p.schema, recordsAsRegressionRecords(filtered))
	if err != nil {
		return nil, err
	}

	resp := &types.Response{
		Data: data,
		Metadata: &types.ResponseMetadata{
			TotalRows:    totalRows,
			FilteredRows: int64(len(filtered)),
		},
		Tests:       testResults,
		PostTests:   postResults,
		Regressions: regressionResults,
	}

	// E1-S5: attach per-slot AggregationComponents emitted by
	// aggregateWithComponents. The grouped buffered path leaves
	// aggComponents nil (per-group components emission is reserved
	// for a later story); the ungrouped buffered exit and the two
	// streaming exits all flow through the same attach helper so the
	// shape of Response.Components.Aggregations stays uniform.
	for _, entry := range aggComponents {
		attachAggregationComponents(resp, entry)
	}

	// E2-S4: attach per-slot GrouperComponents emitted by
	// processGrouped. The ungrouped path leaves grpComponents nil so
	// nothing is appended; the grouped exit always appends exactly
	// one entry per Request.Groups slot (the single-grouper limit
	// matches processGrouped today).
	for _, entry := range grpComponents {
		attachGrouperComponents(resp, entry)
	}

	// E2-S9: attach per-slot FiltererComponents. The buffered
	// applyFiltersWithCounters returned one counter triple per
	// declared filterer slot; build + attach mirrors the streaming
	// exits in processStreaming / processStreamingGrouped /
	// processStreamingTwoPass. Empty filter chains leave
	// filterCounters nil so the omitempty wire shape stays byte-
	// identical against the pre-E2-S9 baseline.
	attachFiltererComponents(resp, buildFiltererComponents(req.Filterers, filterCounters))

	// E2-S11: emit RunComponents — typed cohort-level counters.
	// NullRecords resolution: prefer the FIRST aggregator's nNull from
	// aggComponents (already computed by aggregateWithComponents's
	// per-field floor cache); fall back to the FIRST grouper's NNull
	// when no aggregators were declared; fall back to a buffered scan
	// of the post-filter record slice when neither carries a primary
	// field. The buffered processRecords path has no shard concept
	// (the iterator already merged shard archives into a flat record
	// slice at this point) so ShardCount stays 0 and
	// PartialCohortReason stays empty.
	var nullRecords int64
	switch {
	case len(aggComponents) > 0:
		nullRecords = int64(aggComponents[0].NNull)
	case len(grpComponents) > 0:
		nullRecords = int64(grpComponents[0].NNull)
	default:
		nullRecords = countNullsBuffered(filtered, primaryNullFieldName(req))
	}
	attachRunComponents(resp, RunCountersInput{
		TotalRecords:    totalRows,
		FilteredRecords: int64(len(filtered)),
		NullRecords:     nullRecords,
	})

	// SERIES-host overlay hook (E3-S6). Wraps the finalized per-group
	// Response.Data as a SeriesHostView and dispatches each
	// req.Overlays spec through the SERIES handler registry
	// (overlay_series.go). The hook short-circuits unless the request
	// is grouped (req.Groups non-empty) AND carries a primary
	// aggregator AND req.Overlays is non-empty — matching the E3 scope
	// (grouped Process only). Crosstab requests are NOT routed through
	// processRecords (Service.Process dispatches them to
	// processCrosstab), so the SERIES hook never collides with the
	// MATRIX hook in processing/crosstab.go.
	if err := applyOverlaysSeriesToResponse(req, resp); err != nil {
		return nil, err
	}

	return resp, nil
}

// recordsAsRegressionRecords adapts a []*Record into the
// []regression.Record slice the regression engines consume. The
// regression subpackage defines its own Record interface so it stays
// free of the processing import (mirroring processing/feature); the
// adapter is a zero-copy widening since *Record already implements the
// required NumericValue method.
func recordsAsRegressionRecords(records []*Record) []regression.Record {
	out := make([]regression.Record, len(records))
	for i, r := range records {
		out[i] = r
	}
	return out
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
	filtered, _, err := p.applyFiltersWithCounters(filterers, records)
	return filtered, err
}

// applyFiltersWithCounters is the buffered-path companion to
// applyFilterPass: it runs the per-record filter walk over a
// materialised []*Record slice while tracking the per-slot
// {n_in, n_out, n_null_input} universal-floor counters used by
// Response.Components.Filterers (E2-S9). Returns the filter-passing
// record subset plus one counter triple per filterer slot in
// declared order. An empty filter chain returns the input slice
// unchanged and nil counters so the no-filter fast path remains
// allocation-free.
func (p *Processor) applyFiltersWithCounters(filterers []*types.Filterer, records []*Record) ([]*Record, []filterPassCounters, error) {
	if len(filterers) == 0 {
		return records, nil, nil
	}

	filterFns, err := p.buildFilterFuncs(filterers)
	if err != nil {
		return nil, nil, err
	}
	counters := newFilterPassCounters(filterers)

	// Apply all filters (AND logic) via the shared applyFilterPass
	// helper so the per-record counter semantics match the streaming
	// paths byte-for-byte.
	var result []*Record
	for _, r := range records {
		pass, err := applyFilterPass(r, filterers, filterFns, counters)
		if err != nil {
			return nil, nil, err
		}
		if pass {
			result = append(result, r)
		}
	}
	return result, counters, nil
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
	return feature.ApplyWithExt(view, features, p.schema, p.exts.FeatureFactories())
}

func (p *Processor) applyAttributes(attrs []*types.Attribute, records []*Record) error {
	for _, attr := range attrs {
		factory, ok := p.exts.LookupAttribute(attr.Type)
		if !ok {
			return errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown attribute type: %s", attr.Type))
		}
		computer, err := factory(attr, p.schema)
		if err != nil {
			return err
		}
		if aware, ok := computer.(ExtensionAware); ok {
			aware.SetExtensions(p.exts)
		}
		values, err := computer.Compute(records, attr.Field)
		if err != nil {
			return err
		}

		// Inject computed values back into records under the label name
		label := attr.Label
		if label == "" {
			label = defaultAttributeLabel(attr)
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

func (p *Processor) processGrouped(req *types.Request, records []*Record) ([]map[string]any, []types.GrouperComponents, error) {
	// Use the first group for now (single-level grouping)
	grp := req.Groups[0]
	factory, ok := p.exts.LookupGrouper(grp.Type)
	if !ok {
		return nil, nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("unknown group type: %s", grp.Type))
	}
	grouper, err := factory(grp, p.schema)
	if err != nil {
		return nil, nil, err
	}

	groups, err := grouper.Group(records, grp.Field)
	if err != nil {
		return nil, nil, err
	}

	// Stable-emit by sorted group key so row order is deterministic
	// across runs regardless of Go's map-iteration randomness.
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	data := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		groupRecords := groups[key]
		row, err := p.aggregate(req.Aggregations, groupRecords)
		if err != nil {
			return nil, nil, err
		}
		if row == nil {
			// +1 reserved for the group key written below.
			row = make(map[string]any, 1)
		}
		row[grp.Field] = key
		data = append(data, row)
	}

	// E2-S4: emit GrouperComponents for the single grouper slot.
	// Universal floor (TotalN = sum of bucket counts, NNull =
	// post-filter records minus TotalN — the records the grouper
	// skipped due to null inputs or include-filter rejection) is
	// derived directly from the bucket map; the operator-specific
	// keys ride off MetaGrouper.Components() when the grouper
	// implements it.
	entry, gerr := buildGrouperComponents(grouper, grp, groups, len(records))
	if gerr != nil {
		return nil, nil, gerr
	}
	return data, []types.GrouperComponents{entry}, nil
}

func (p *Processor) aggregate(aggs []*types.Aggregation, records []*Record) (map[string]any, error) {
	row, _, err := p.aggregateWithComponents(aggs, records, false)
	return row, err
}

// aggregateWithComponents runs the buffered aggregation pass and
// optionally captures per-slot AggregationComponents entries. The
// universal floor (n, nNull) is derived from the same record walk
// that drives the value path so the orchestrator never re-scans the
// record set just to populate the floor. Decimal-typed slots emit
// floor-only entries today (the decimal aggregation path lives
// outside the MetaAggregator dispatch).
//
// Pass collectComponents=true on the ungrouped buffered exit; the
// grouped buffered exit (processGrouped) leaves it false because
// per-group components emission is reserved for a later story.
func (p *Processor) aggregateWithComponents(aggs []*types.Aggregation, records []*Record, collectComponents bool) (map[string]any, []types.AggregationComponents, error) {
	if len(aggs) == 0 {
		return nil, nil, nil
	}

	// Cache collected non-null float64 slices per field for the duration of
	// this call so multiple aggregations on the same field don't each scan
	// the record set. The cache owns pooled buffers; release returns them.
	cache := newCollectCache()
	defer cache.release()

	// Per-field {n, nNull} cache so multiple aggregator slots that
	// share a field do not re-scan the record set. n counts non-null
	// inputs, nNull counts null inputs — together they tile the
	// post-filter record count.
	type floor struct {
		n     int
		nNull int
	}
	floorCache := make(map[string]floor)
	floorFor := func(field string) floor {
		if f, ok := floorCache[field]; ok {
			return f
		}
		var f floor
		for _, r := range records {
			if _, ok := r.NumericValue(field); ok {
				f.n++
			} else {
				f.nNull++
			}
		}
		floorCache[field] = f
		return f
	}

	// Pre-size: one entry per aggregation. Grouped path adds +1 for the group
	// key after this returns; map will grow once but only in that branch.
	row := make(map[string]any, len(aggs))
	var components []types.AggregationComponents
	if collectComponents {
		components = make([]types.AggregationComponents, 0, len(aggs))
	}
	for _, agg := range aggs {
		label := agg.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", agg.Type, agg.Field)
		}
		// Decimal-typed fields dispatch to AggregateDecimalField.
		if p.schema != nil {
			if f := p.schema.Field(agg.Field); f != nil && f.Type.IsDecimal() {
				if !IsDecimalAggregationSupported(agg.Type) {
					return nil, nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
						"aggregation has no decimal128 implementation",
						map[string]any{"aggregation": string(agg.Type), "field": agg.Field})
				}
				out, err := AggregateDecimalField(agg.Type, records, agg.Field, f.Scale)
				if err != nil {
					return nil, nil, err
				}
				row[label] = out
				if collectComponents {
					fl := floorFor(agg.Field)
					components = append(components, types.AggregationComponents{
						Label: agg.Label,
						N:     fl.n,
						NNull: fl.nNull,
					})
				}
				continue
			}
		}
		factory, ok := p.exts.LookupAggregator(agg.Type)
		if !ok {
			return nil, nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown aggregation type: %s", agg.Type))
		}
		aggregator, err := factory(agg, p.schema)
		if err != nil {
			return nil, nil, err
		}
		var val float64
		if va, ok := aggregator.(valueAggregator); ok {
			vals := cache.get(records, agg.Field)
			val, err = va.aggregateValues(vals)
		} else {
			val, err = aggregator.Aggregate(records, agg.Field)
		}
		if err != nil {
			return nil, nil, err
		}
		row[label], err = dispatchAggregatorResult(aggregator, val)
		if err != nil {
			return nil, nil, err
		}
		if collectComponents {
			fl := floorFor(agg.Field)
			entry, err := buildAggregationComponents(aggregator, agg, fl.n, fl.nNull)
			if err != nil {
				return nil, nil, err
			}
			components = append(components, entry)
		}
	}
	return row, components, nil
}

// dispatchAggregatorResult routes the aggregator's output into the
// response row. Aggregators that implement RichAggregator emit their
// structured payload (map[string]int for AGG_SET_FREQUENCY, []string
// for AGG_SET_UNION / AGG_SET_INTERSECTION); the float64 contract
// remains the scalar fallback for callers that only consume scalars.
// A RichAggregator returning nil (Rich payload not applicable for this
// invocation, e.g. empty input) falls back to the scalar value.
//
// Accepts `any` so the same dispatch wraps both Aggregator (buffered
// path) and OnlineAggregator (streaming path) instances — the
// underlying concrete type satisfies both interfaces when it implements
// RichAggregator.
func dispatchAggregatorResult(agg any, scalar float64) (any, error) {
	if rich, ok := agg.(RichAggregator); ok {
		v, err := rich.Rich()
		if err != nil {
			return nil, err
		}
		if v != nil {
			return v, nil
		}
	}
	return scalar, nil
}

// dispatchAggregatorCellResult is the MatrixCell.Value-bound sibling of
// dispatchAggregatorResult. It preserves the Rich-or-scalar lift for
// every aggregator EXCEPT AGG_WELFORD: WelfordTriple is now an internal
// statistical-moment carrier owned by Components.Crosstab.CellComponents
// (E3-S7 migration), and no longer rides MatrixCell.Value. For
// AGG_WELFORD specifically the cell builder writes the scalar mean
// (matching welfordAggregator.Aggregate / Finalize) so the cell payload
// stays a plain float64 — overlay handlers source `(mean, variance, n)`
// from CellComponents under the post-S7 contract.
//
// All other RichAggregator payloads (map[string]int from
// AGG_SET_FREQUENCY, []string from AGG_SET_UNION / AGG_SET_INTERSECTION,
// future families) continue to ride MatrixCell.Value untouched — this
// carve-out is type-name-specific to the WelfordTriple shape and stays
// orthogonal to other rich families.
//
// E3-S8: removes the WelfordTriple smuggling that previously flowed
// through MatrixCell.Value. The WelfordTriple type itself is retained
// (it's still emitted by RichAggregator for non-crosstab Response.Data
// rows via dispatchAggregatorResult); only its MatrixCell.Value payload
// role is gone.
func dispatchAggregatorCellResult(agg any, scalar float64) (any, error) {
	if rich, ok := agg.(RichAggregator); ok {
		v, err := rich.Rich()
		if err != nil {
			return nil, err
		}
		if v != nil {
			if _, isWelford := v.(WelfordTriple); isWelford {
				return scalar, nil
			}
			return v, nil
		}
	}
	return scalar, nil
}

// buildAggregationComponents builds a types.AggregationComponents from
// the post-finalize aggregator instance, the orchestrator-tracked
// universal floor (n, nNull), and the originating Aggregation slot.
// The returned entry always carries the floor; the operator-specific
// map is populated only when the aggregator implements MetaAggregator
// and returns a non-nil map. Extension aggregators that have not yet
// adopted the MetaAggregator sibling fall through cleanly to a
// floor-only entry (Operator == nil → marshals as omitempty).
//
// Accepts `any` so the same dispatch wraps both Aggregator (buffered
// path) and OnlineAggregator (streaming path) instances — the concrete
// type satisfies both interfaces when it implements MetaAggregator.
func buildAggregationComponents(agg any, slot *types.Aggregation, n, nNull int) (types.AggregationComponents, error) {
	entry := types.AggregationComponents{
		N:     n,
		NNull: nNull,
	}
	if slot != nil {
		entry.Label = slot.Label
	}
	if meta, ok := agg.(MetaAggregator); ok {
		op, err := meta.Components()
		if err != nil {
			return types.AggregationComponents{}, err
		}
		entry.Operator = op
	}
	return entry, nil
}

// attachAggregationComponents appends a freshly-built
// AggregationComponents entry onto resp.Components.Aggregations,
// allocating the parent ResponseComponents and the slice as needed.
// The helper centralises the "lazy allocate then append" pattern so
// every execution path (buffered, streaming, two-pass) populates the
// components shell identically — an additive omitempty payload that
// marshals to a no-op when the slice ends up empty.
func attachAggregationComponents(resp *types.Response, entry types.AggregationComponents) {
	if resp.Components == nil {
		resp.Components = &types.ResponseComponents{}
	}
	resp.Components.Aggregations = append(resp.Components.Aggregations, entry)
}

// buildGrouperComponents builds a types.GrouperComponents from the
// buffered processGrouped path: the post-Group grouper instance, the
// originating Group slot, the partition output (key → []*Record map),
// and the post-filter record count. The universal floor — TotalN
// (records partitioned, equal to the sum of bucket sizes) and NNull
// (records the grouper skipped: null inputs, include-filter rejections,
// empty set masks, etc.) — is derived directly from the partition map
// and totalFiltered without re-scanning records. The operator-specific
// keys ride off MetaGrouper.Components() when the grouper implements
// it; groupers without MetaGrouper fall through to a floor-only entry
// (Operator == nil → marshals as omitempty).
//
// Single-key groupers contribute one row to exactly one bucket so
// TotalN ≤ totalFiltered; the difference equals NNull. Multi-key
// streaming groupers (GROUP_SET_PER_ELEMENT) contribute one row to
// every selected label, so TotalN here would over-count; the buffered
// path does not currently invoke multi-key groupers (KeysForRow lives
// on the streaming path), so the simple subtraction stays correct for
// the single-key buffered grouped exit.
func buildGrouperComponents(grouper Grouper, slot *types.Group, groups map[string][]*Record, totalFiltered int) (types.GrouperComponents, error) {
	totalN := 0
	for _, bucket := range groups {
		totalN += len(bucket)
	}
	nNull := totalFiltered - totalN
	if nNull < 0 {
		nNull = 0
	}
	entry := types.GrouperComponents{
		Field:  slot.Field,
		TotalN: totalN,
		NNull:  nNull,
	}
	if meta, ok := grouper.(MetaGrouper); ok {
		op, err := meta.Components()
		if err != nil {
			return types.GrouperComponents{}, err
		}
		entry.Operator = op
	}
	return entry, nil
}

// buildStreamingGrouperComponents builds a types.GrouperComponents
// from the streaming grouped path: the grouper instance KeyForRow
// drove, the originating Group slot, and the post-filter row count
// observed by the streaming iterator. Mirrors buildGrouperComponents
// except TotalN is derived from MetaGrouper.Components()'s buckets
// payload (the orchestrator never built the buffered partition map
// on this path). When the grouper does not implement MetaGrouper,
// TotalN collapses to totalFiltered as a conservative floor — the
// orchestrator has no other signal for partitioned vs skipped rows.
func buildStreamingGrouperComponents(grouper any, slot *types.Group, totalFiltered int) (types.GrouperComponents, error) {
	entry := types.GrouperComponents{
		Field: slot.Field,
	}
	meta, ok := grouper.(MetaGrouper)
	if !ok {
		entry.TotalN = totalFiltered
		return entry, nil
	}
	op, err := meta.Components()
	if err != nil {
		return types.GrouperComponents{}, err
	}
	entry.Operator = op
	// Sum per-bucket counts to derive TotalN. The buckets payload is
	// a []map[string]any with int "count" entries by GROUP_CATEGORY /
	// GROUP_DATE convention. Other groupers that wire MetaGrouper in
	// later stories MUST follow the same {"count": int} convention;
	// the type assertion guards drift.
	totalN := 0
	if buckets, ok := op["buckets"].([]map[string]any); ok {
		for _, b := range buckets {
			if c, ok := b["count"].(int); ok {
				totalN += c
			}
		}
	}
	entry.TotalN = totalN
	nNull := totalFiltered - totalN
	if nNull < 0 {
		nNull = 0
	}
	entry.NNull = nNull
	return entry, nil
}

// attachGrouperComponents appends a freshly-built GrouperComponents
// entry onto resp.Components.Groupers, allocating the parent
// ResponseComponents and the slice as needed. Mirrors
// attachAggregationComponents — the lazy-allocate-then-append
// pattern keeps the components shell uniform across every grouped
// execution path (buffered processGrouped, streaming
// processStreamingGrouped) so an additive omitempty payload marshals
// to a no-op when the slice ends up empty.
func attachGrouperComponents(resp *types.Response, entry types.GrouperComponents) {
	if resp.Components == nil {
		resp.Components = &types.ResponseComponents{}
	}
	resp.Components.Groupers = append(resp.Components.Groupers, entry)
}
