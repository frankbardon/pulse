package processing

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mergegate"
	"github.com/frankbardon/pulse/internal/processing/feature"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/internal/processing/window"
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
	// PathBuffered is the materialize-then-aggregate path. It is
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
	schema            *encoding.Schema
	lastPath          ProcessPath
	exts              *ExtensionRegistry
	disableComponents bool

	// defaultWeight is pulse.Options.DefaultWeight (nil = none) and
	// strictWeights promotes PULSE_WEIGHT_INVALID_ROWS to an error;
	// both set through SetWeighting.
	defaultWeight *types.WeightSpec
	strictWeights bool
}

// SetWeighting installs the instance default weight (nil = none) that
// StampWeights folds into every run, and whether an invalid-weight row
// is an error (strict) rather than a PULSE_WEIGHT_INVALID_ROWS warning.
// The service sets both from pulse.Options; a processor built without
// the call weights only by request and slot weights, non-strictly.
func (p *Processor) SetWeighting(def *types.WeightSpec, strict bool) {
	p.defaultWeight = def
	p.strictWeights = strict
}

// stampWeights is StampWeights with this processor's default weight.
func (p *Processor) stampWeights(req *types.Request) *types.Request {
	return StampWeights(req, p.defaultWeight)
}

// SetDisableComponents toggles Response.Components emission for this
// processor instance. When true, every attach helper (aggregation,
// grouper, filterer, run, crosstab) early-returns before constructing
// any per-operator components map — the MetaAggregator.Components /
// MetaGrouper.Components calls are skipped entirely. Service-layer
// callers compute the effective decision (per-request override against
// the engine default) and flip this flag before invoking Process. See
// pulse.Options.DisableComponents and types.Request.DisableComponents
// for the full contract.
func (p *Processor) SetDisableComponents(disabled bool) {
	p.disableComponents = disabled
}

// DisableComponents reports the current setting. Exposed so the
// crosstab / fused-crosstab dispatch paths (which build their own
// emission helper) can consult it.
func (p *Processor) DisableComponents() bool {
	return p.disableComponents
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
//  2. Buffered: the fallback path. Every record is collected into a slice
//     first, then filters, attributes, grouping, and aggregations run
//     over the materialized set. Memory is O(rows). Always correct.
//
// Output is identical between paths to float64 precision on
// well-conditioned inputs (variance/stddev/skewness/kurtosis use
// Welford-Pébaÿ recurrences in the streaming path).
func (p *Processor) Process(ctx context.Context, req *types.Request, iter RecordIterator) (*types.Response, error) {
	req = p.stampWeights(req)
	if p.canStream(req) {
		var resp *types.Response
		var err error
		switch {
		case len(req.Groups) > 0:
			resp, err = p.processStreamingGrouped(ctx, req, iter)
		case hasTwoPassAttribute(req, p.exts):
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
	return CanStreamRequestWithExtensions(req, schema, nil)
}

// CanStreamRequestWithExtensions is CanStreamRequest against a
// processor whose operator lookups consult exts — the parity hook for
// requests naming embedder-registered operators, whose streamability
// is their DECLARED registration flag (ExtensionRegistry.IsStreamable).
// A nil exts is exactly CanStreamRequest.
func CanStreamRequestWithExtensions(req *types.Request, schema *encoding.Schema, exts *ExtensionRegistry) bool {
	p := &Processor{schema: schema, exts: exts}
	return p.canStream(req)
}

// CanMergeRequest reports whether a request's online state is
// mergeable across input partitions — the gate that the per-shard
// parallel reducer in internal/service/shard_reduce.go and the
// single-file parallel decode reducer consult before fanning out work
// across a worker pool. Returns true iff every aggregator, grouper,
// and filterer is mergeable AND the request contains no windows, no
// features, no regressions, no tests, no two-pass attributes, and no
// decimal-typed built-in aggregation targets. A nil/empty aggregation
// list returns false (nothing to merge).
//
// Mergeable is a strict subset of Streamable. This is the built-in-only
// gate: it is CanMergeRequestWithExtensions with a nil registry, so
// every embedder-registered operator name is refused.
func CanMergeRequest(req *types.Request, schema *encoding.Schema) bool {
	return CanMergeRequestWithExtensions(req, schema, nil)
}

// CanMergeRequestWithExtensions is CanMergeRequest against a registry
// that knows embedder-registered operators. Every caller that holds an
// ExtensionRegistry (the service's parallel reducers, ProcessChain)
// routes through here; a nil exts is exactly CanMergeRequest.
//
// Extension operators merge on these terms:
//   - aggregators: their DECLARED Mergeable flag
//     (ExtensionRegistry.IsMergeable), probe-validated at pulse.New to
//     imply Streamable and an extend.MergeableAggregator value. A
//     decimal128 target does not refuse an extension aggregator — the
//     built-in decimal fold is buffered-only, but an extension reads
//     decimals through Record.DecimalValue on every path.
//   - groupers: their DECLARED Mergeable flag (IsMergeable), single-key
//     and fan-out alike, probe-validated at pulse.New to imply
//     Streamable and — when the grouper emits components — an
//     extend.MergeableGrouper value. The adapter always exposes
//     MergeableGrouper on a declared grouper (a no-op fold when it
//     emits nothing), so the reducers' grouper fold never meets a
//     grouper it cannot merge.
//   - filterers: row-local, so mergeable when streamable (every
//     registered extension filterer is).
//   - attributes: a row_local extension attribute merges like
//     ATTR_FORMULA / ATTR_DATE_PART; two_pass and buffered ones do not.
//
// The decision itself is internal/mergegate.MergeRefusal — the one
// rule the chain validator in internal/descriptor also calls, through
// the ExtensionsSnapshot instead of this registry.
func CanMergeRequestWithExtensions(req *types.Request, schema *encoding.Schema, exts *ExtensionRegistry) bool {
	return mergegate.MergeRefusal(req, schema, exts.mergeFacts()) == ""
}

// requiresTwoPass reports whether the attribute type implements
// TwoPassAttribute (and therefore needs a PrePass over filter-passing
// records before per-row emission). Mirrors the attribute factories in
// internal/processing/attribute.go and attribute_reg.go: ZSCORE/TSCORE/NORMALIZED
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
// requires the two-pass streaming path — a built-in two-pass type or an
// extension attribute that declared the two-pass mode on exts.
func hasTwoPassAttribute(req *types.Request, exts *ExtensionRegistry) bool {
	for _, attr := range req.Attributes {
		if exts.attributeRequiresTwoPass(attr.Type) {
			return true
		}
	}
	return false
}

// canStream reports whether the request can be safely executed via the
// streaming path. Every per-operator streamability fact below is read
// through ExtensionRegistry.IsStreamable: a built-in answers from its
// per-type Streamable() method, an extension from its DECLARED
// registration flag (probe-validated at pulse.New). Streaming requires:
//   - groups: empty, OR every grouper streamable (CATEGORY, RANGE,
//     ROUNDED, … or an extension declared Streamable — partitioned via
//     the grouped streaming path). QUANTILE/DATE require a
//     finalize-time view of the full set.
//   - attributes: empty, OR every attribute streamable (FORMULA,
//     DATE_PART implement RowLocalAttribute and execute inline;
//     ZSCORE/TSCORE/NORMALIZED and extension two_pass attributes take
//     the two-pass drive). PERCENTILE and extension buffered-mode
//     attributes need the materialised set.
//   - no windows (window operators run over the post-aggregate row set)
//   - features either empty or every operator implements
//     feature.StreamingComputer (PrePass + Finalize + EmitRow)
//   - every aggregation declared/typed streamable AND its instance
//     implements OnlineAggregator
//   - filters are row-level only (every registered filter today is)
//   - tier-1 tests: empty, OR every test streamable AND registered
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
	// Mixed-mode overlay downgrade: if any spec on req.Overlays is
	// non-streamable, force the buffered path so the post-finalize
	// hook (applyOverlaysSeriesToResponse) sees a fully materialised
	// SeriesHostView. Unknown overlay kinds also force
	// buffered so ApplyOverlaysSeries can surface the canonical
	// PULSE_OVERLAY_KIND_UNKNOWN error from the buffered exit. The
	// gate stays central — every other canStream branch already runs
	// in O(req-slot-count); one extra slice walk here keeps the
	// streaming-eligibility decision single-pass.
	if !canStreamOverlays(req, p.exts) {
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
			// A hidden type is never streamable, like a type nothing
			// registered: both route buffered and fail at the same
			// "unknown regression type" site.
			if !reg.Streamable() || p.exts.isHidden(string(reg.Type)) {
				return false
			}
		}
		if len(req.Groups) > 0 || len(req.Features) > 0 || hasTwoPassAttribute(req, p.exts) || len(req.Tests) > 0 {
			return false
		}
	}
	if len(req.Tests) > 0 {
		if !canRunRowTests(req.Tests, p.exts) {
			return false
		}
		for _, t := range req.Tests {
			if !p.exts.IsStreamable("test", string(t.Type)) {
				return false
			}
		}
		// Tier-1 tests do not yet compose with groups, features, or
		// two-pass attributes inside the streaming paths. Route those
		// combinations through the buffered path so the row tests
		// still execute correctly over the filtered record set.
		if len(req.Groups) > 0 || len(req.Features) > 0 || hasTwoPassAttribute(req, p.exts) {
			return false
		}
	}
	for _, grp := range req.Groups {
		if !p.exts.IsStreamable("grouper", string(grp.Type)) {
			return false
		}
	}
	hasTwoPassAttr := false
	for _, attr := range req.Attributes {
		if !p.exts.IsStreamable("attribute", string(attr.Type)) {
			return false
		}
		if p.exts.attributeRequiresTwoPass(attr.Type) {
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
	if len(req.Features) > 0 && !feature.IsStreamableWithExt(req.Features, p.schema, p.exts.LookupFeature) {
		return false
	}
	if len(req.Aggregations) == 0 {
		// No aggregations: buffered path produces the same empty data
		// payload and exposes the same error surface (e.g., when a
		// downstream component validates against the materialized set).
		return false
	}
	for _, agg := range req.Aggregations {
		// Built-in decimal-typed fields are aggregated via
		// AggregateDecimalField; the streaming numeric fold loses
		// precision. An extension aggregator reads a decimal target
		// itself (extend.Record.DecimalValue) on both paths, so its
		// declared Streamable flag below decides, exactly as for any
		// other field type.
		if p.schema != nil && !p.exts.isExtensionAggregator(agg.Type) {
			if f := p.schema.Field(agg.Field); f != nil && f.Type.IsDecimal() {
				return false
			}
		}
		// The declared flag gates first: an extension registered
		// Streamable=false runs buffered even when its value also
		// implements OnlineAggregator, so predict (which reads the
		// declaration) and runtime cannot disagree. Built-ins fall
		// through to AggregationType.Streamable(), which
		// TestRegistryStreamabilityMatchesTypes holds equal to the
		// interface assertion below.
		if !p.exts.IsStreamable("aggregator", string(agg.Type)) {
			return false
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
		handles, err := feature.BuildStreamingWithExt(req.Features, p.schema, p.exts.LookupFeature)
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
	// per-record applyFilterPass walk. Empty filter chains
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
	// — n counts non-null contributors, nNull counts null
	// inputs; the orchestrator increments them per record alongside
	// each aggregator's UpdateRow.
	type onlineEntry struct {
		agg    *types.Aggregation
		online OnlineAggregator
		n      int
		nNull  int
		weight WeightFloor
	}
	weights := NewWeightRowTally(req)
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
		entries[i] = onlineEntry{agg: agg, online: online, weight: NewWeightFloor(agg)}
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
	regressionEngines, err := regression.BuildStreamingWith(req.Regressions, p.schema, p.exts.LookupRegression)
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

		weights.Observe(r)
		for i := range entries {
			e := &entries[i]
			if FieldPresent(r, e.agg.Field) {
				e.n++
			} else {
				e.nNull++
			}
			e.weight.Observe(r, e.agg.Field)
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
		weight WeightFloor
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
			weight: e.weight,
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

	// Components emission block — opt-out via
	// pulse.Options.DisableComponents / Request.DisableComponents. The
	// gate sits on the build+attach pair so the MetaAggregator.Components
	// construction work is skipped, not built then discarded.
	if !p.disableComponents {
		// Emit AggregationComponents per slot. Universal floor (n,
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
			fe.weight.Stamp(&entry)
			attachAggregationComponents(resp, entry)
		}

		// Emit FiltererComponents per slot from the per-record
		// counter walk. Empty filter chains stay nil so the omitempty
		// wire shape is byte-identical.
		attachFiltererComponents(resp, buildFiltererComponents(req.Filterers, filterCounters))

		// Emit RunComponents — typed cohort-level counters.
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
	}
	if err := weights.Apply(resp, p.strictWeights); err != nil {
		return nil, err
	}
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
	ApplyGrouperExtensions(grouperInstance, p.exts)
	keyer, err := NewGroupKeyer(grouperInstance)
	if err != nil {
		return nil, err
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
		handles, err := feature.BuildStreamingWithExt(req.Features, p.schema, p.exts.LookupFeature)
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
	// Per-slot filter-pass counters ride alongside filterFns through
	// the streaming grouped path; same applyFilterPass walk the
	// ungrouped streaming path uses.
	filterCounters := newFilterPassCounters(req.Filterers)
	rowLocalAttrs, err := p.buildRowLocalAttributes(req.Attributes)
	if err != nil {
		return nil, err
	}

	// Per-group aggregator buckets; FinalizeGroupedStream orders them.
	buckets := make(map[string][]OnlineAggregator)

	// Track null count on the primary aggregation field for
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
	weights := NewWeightRowTally(req)

	var totalRows, filteredRows, assignments int64
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

		for _, ra := range rowLocalAttrs {
			val, err := ra.computer.Row(r, ra.attr.Field)
			if err != nil {
				return nil, err
			}
			r.Set(ra.label, val)
		}

		// Tallied AFTER row-local attributes land: the primary field
		// may be an attribute label, which before Set above is either
		// absent (the first row) or the PREVIOUS row's value on a
		// reused record. Matches the buffered exit and both parallel
		// reducers.
		if primaryField != "" && r.IsNull(primaryField) {
			primaryNullRecords++
		}
		weights.Observe(r)

		rowKeys, ok, err := keyer.Keys(r, grp.Field)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue // null group key — buffered path skips these too
		}
		assignments += int64(len(rowKeys))

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
				b = online
				buckets[key] = b
			}
			for i, oa := range b {
				if err := oa.UpdateRow(r, specs[i].agg.Field); err != nil {
					return nil, err
				}
			}
		}
	}

	_ = ctx
	// Row order, the Rich-or-scalar lift, Sort, post-tests, Components
	// and the SERIES overlay fold live in the ONE grouped tail the
	// parallel reducers share, so no worker count can change the answer.
	// The streaming-grouped path has no shard concept (records flow off
	// a single iterator whatever the cohort topology); the service stamps
	// Run.ShardCount on an archive.
	resp, err := FinalizeGroupedStream(req, GroupedTail{
		Group:             grp,
		Grouper:           grouperInstance,
		Buckets:           buckets,
		TotalRows:         totalRows,
		FilteredRows:      filteredRows,
		NullRecords:       primaryNullRecords,
		Assignments:       assignments,
		FilterCounters:    filterCounters,
		DisableComponents: p.disableComponents,
		PostTests: func(rows []map[string]any) ([]*types.TestResult, error) {
			return p.runPostTests(req.PostTests, rows)
		},
		Extensions: p.exts,
	})
	if err != nil {
		return nil, err
	}
	if err := weights.Apply(resp, p.strictWeights); err != nil {
		return nil, err
	}
	return resp, nil
}

// twoPassStage is one attribute in DECLARED order on the two-pass
// streaming path: its constructed computer (Row for every stage; tp is
// non-nil when it also drives PrePass/Finalize) and its output label.
type twoPassStage struct {
	attr  *types.Attribute
	row   RowLocalAttribute
	tp    TwoPassAttribute
	label string
}

// buildTwoPassStages constructs every attribute in declared order and
// plans its prepass layers (see twoPassPlan). Construction and
// validation are shared with the buffered arm; only the drive differs.
func (p *Processor) buildTwoPassStages(attrs []*types.Attribute) ([]twoPassStage, twoPassPlan, error) {
	stages := make([]twoPassStage, 0, len(attrs))
	isTwoPass := make([]bool, 0, len(attrs))
	for _, attr := range attrs {
		factory, ok := p.exts.LookupAttribute(attr.Type)
		if !ok {
			return nil, twoPassPlan{}, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown attribute type: %s", attr.Type))
		}
		computer, factoryErr := factory(attr, p.schema)
		if factoryErr != nil {
			return nil, twoPassPlan{}, factoryErr
		}
		if err := bindAttribute(computer, p.exts); err != nil {
			return nil, twoPassPlan{}, err
		}
		label := attr.Label
		if label == "" {
			label = defaultAttributeLabel(attr)
		}
		st := twoPassStage{attr: attr, label: label}
		if tp, ok := computer.(TwoPassAttribute); ok {
			st.tp, st.row = tp, tp
		} else if rl, ok := computer.(RowLocalAttribute); ok {
			st.row = rl
		} else {
			return nil, twoPassPlan{}, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("attribute %s implements neither TwoPassAttribute nor RowLocalAttribute", attr.Type))
		}
		stages = append(stages, st)
		isTwoPass = append(isTwoPass, st.tp != nil)
	}
	return stages, planTwoPassStages(attrs, isTwoPass, p.exts), nil
}

// processStreamingTwoPass runs the two-pass streaming path in DECLARED
// attribute order (see twoPassPlan): prepass scans 0..layers-1 each
// evaluate the attributes their layer's PrePass depends on, then fold
// that layer's two-pass attributes and Finalize them; iter.Reset()
// rewinds between scans; the final scan emits every attribute in
// declared order and folds online aggregations row-by-row. Every scan
// applies the filters, so each attribute observes the same filtered,
// declared-order record state the buffered arm's Compute does.
//
// canStream verified: no features, no groups, no windows, every
// aggregation online, every attribute either two-pass or row-local.
//
// Memory bound: O(per_attribute_state). Scans = prepass layers + 1 —
// two for any request whose two-pass attributes read only source
// fields or row-locals of them; one more per dependent two-pass layer.
// The underlying file is typically OS-page-cached after scan 1.
func (p *Processor) processStreamingTwoPass(ctx context.Context, req *types.Request, iter RecordIterator) (*types.Response, error) {
	EnableReuse(iter)
	filterFns, err := p.buildFilterFuncs(req.Filterers)
	if err != nil {
		return nil, err
	}
	// Per-slot filter-pass counters track {n_in, n_out,
	// n_null_input}. The two-pass path walks every record once per
	// scan; counters increment ONLY on scan 0 so the values match the
	// single-pass observed record set — per-slot n_in / n_out /
	// n_null_input count distinct input rows, not repeated visits.
	filterCounters := newFilterPassCounters(req.Filterers)
	stages, plan, err := p.buildTwoPassStages(req.Attributes)
	if err != nil {
		return nil, err
	}
	if plan.layers == 0 {
		// Defensive: the router only sends requests with a two-pass
		// attribute here, but scan 0 is also where rows are counted.
		plan.layers, plan.needed = 1, [][]int{nil}
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
		weight WeightFloor
	}
	weights := NewWeightRowTally(req)
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
		entries[i] = onlineEntry{agg: agg, online: online, weight: NewWeightFloor(agg)}
	}

	// passesFilters re-runs the filter chain without touching the
	// counters (closed on scan 0).
	passesFilters := func(r *Record) (bool, error) {
		for _, fn := range filterFns {
			ok, err := fn(r)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}
	setRow := func(st *twoPassStage, r *Record) error {
		val, err := st.row.Row(r, st.attr.Field)
		if err != nil {
			return err
		}
		r.Set(st.label, val)
		return nil
	}

	// Prepass scans: one per layer. Filters apply so attribute
	// population stats match the buffered Compute path (which receives
	// the already-filtered slice). Row counts and per-slot filter
	// counters are taken on scan 0 only.
	var totalRows, filteredRows int64
	for layer := 0; layer < plan.layers; layer++ {
		if layer > 0 {
			iter.Reset()
		}
		needed := plan.needed[layer]
		for iter.Next() {
			r := iter.Record()
			var pass bool
			if layer == 0 {
				totalRows++
				pass, err = applyFilterPass(r, req.Filterers, filterFns, filterCounters)
			} else {
				pass, err = passesFilters(r)
			}
			if err != nil {
				return nil, err
			}
			if !pass {
				continue
			}
			if layer == 0 {
				filteredRows++
			}
			// Walk declared order: evaluate each stage this layer
			// depends on, and fold each layer-L PrePass at its own
			// position so it sees exactly the earlier stages' output.
			k := 0
			for i := range stages {
				if k < len(needed) && needed[k] == i {
					k++
					if err := setRow(&stages[i], r); err != nil {
						return nil, err
					}
					continue
				}
				if stages[i].tp != nil && plan.layer[i] == layer {
					if err := stages[i].tp.PrePass(r, stages[i].attr.Field); err != nil {
						return nil, err
					}
				}
			}
		}
		for i := range stages {
			if stages[i].tp != nil && plan.layer[i] == layer {
				if err := stages[i].tp.Finalize(); err != nil {
					return nil, err
				}
			}
		}
	}
	iter.Reset()

	// Emission scan: re-apply filters, emit every attribute in declared
	// order (each sees every earlier label, as on the buffered arm),
	// fold each aggregation.
	for iter.Next() {
		r := iter.Record()
		pass, err := passesFilters(r)
		if err != nil {
			return nil, err
		}
		if !pass {
			continue
		}
		for i := range stages {
			if err := setRow(&stages[i], r); err != nil {
				return nil, err
			}
		}
		weights.Observe(r)
		for i := range entries {
			e := &entries[i]
			if FieldPresent(r, e.agg.Field) {
				e.n++
			} else {
				e.nNull++
			}
			e.weight.Observe(r, e.agg.Field)
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

	// Components emission block — gated by the processor's
	// disableComponents flag (see attachAggregationComponents for the
	// full opt-out contract).
	if !p.disableComponents {
		// Emit AggregationComponents per slot for the two-pass
		// streaming path. Mirrors the single-pass streaming exit (see
		// processStreaming above).
		for i := range entries {
			e := &entries[i]
			entry, err := buildAggregationComponents(e.online, e.agg, e.n, e.nNull)
			if err != nil {
				return nil, err
			}
			e.weight.Stamp(&entry)
			attachAggregationComponents(resp, entry)
		}

		// Emit FiltererComponents per slot. Counters were
		// populated during pass 1 (pass 2 re-runs the filter funcs for
		// gating but skips the counter increment so totals match the
		// observed record set, not pass-1+pass-2 visits).
		attachFiltererComponents(resp, buildFiltererComponents(req.Filterers, filterCounters))

		// Emit RunComponents — typed cohort-level counters.
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
	}
	if err := weights.Apply(resp, p.strictWeights); err != nil {
		return nil, err
	}
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
		if err := bindAttribute(computer, p.exts); err != nil {
			return nil, err
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
		out = append(out, wrapFilterPrecompute(fn, f, p.schema, p.exts))
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
	// Response.Components.Filterers. Empty filter chains short-circuit
	// to a nil counters slice so the omitempty wire shape stays
	// byte-identical.
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

	recordRows := false
	if len(req.Groups) > 0 {
		data, grpComponents, err = p.processGrouped(req, filtered)
		if err != nil {
			return nil, err
		}
	} else if len(req.Aggregations) > 0 {
		var row map[string]any
		row, aggComponents, err = p.aggregateWithComponents(req.Aggregations, filtered, !p.disableComponents)
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
		recordRows = true
	}

	if len(req.Windows) > 0 {
		if err := window.ApplyWithExt(ctx, data, req.Windows, p.exts.LookupWindow); err != nil {
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
	if recordRows {
		finalizeRowCells(data)
	}

	// Regression fits run after aggregation / windows so engines can
	// observe the filtered record set. Phase 1 routes the filtered
	// slice through FitBuffered: the unpenalized OLS engine consumes
	// every record via its Welford-style accumulator, while engines
	// for unimplemented operators (penalized OLS, GLM, Bayes, and the
	// Resample/Selection modifier wrappers) still surface
	// PROCESSING_REGRESSION_NOT_IMPLEMENTED via Fit().
	regressionResults, err := regression.FitBufferedWith(req.Regressions, p.schema, recordsAsRegressionRecords(filtered), p.exts.LookupRegression)
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

	// Components emission block — gated by the processor's
	// disableComponents flag (see attachAggregationComponents for the
	// full opt-out contract). aggregateWithComponents above already
	// received !p.disableComponents as its collectComponents argument,
	// so aggComponents is nil when this gate trips and the build cost
	// upstream was skipped too.
	if !p.disableComponents {
		// Attach per-slot AggregationComponents emitted by
		// aggregateWithComponents. The grouped buffered path leaves
		// aggComponents nil (per-group components emission is reserved
		// for a later story); the ungrouped buffered exit and the two
		// streaming exits all flow through the same attach helper so the
		// shape of Response.Components.Aggregations stays uniform.
		for _, entry := range aggComponents {
			attachAggregationComponents(resp, entry)
		}

		// Attach per-slot GrouperComponents emitted by
		// processGrouped. The ungrouped path leaves grpComponents nil so
		// nothing is appended; the grouped exit always appends exactly
		// one entry per Request.Groups slot (the single-grouper limit
		// matches processGrouped today).
		for _, entry := range grpComponents {
			attachGrouperComponents(resp, entry)
		}

		// Attach per-slot FiltererComponents. The buffered
		// applyFiltersWithCounters returned one counter triple per
		// declared filterer slot; build + attach mirrors the streaming
		// exits in processStreaming / processStreamingGrouped /
		// processStreamingTwoPass. Empty filter chains leave
		// filterCounters nil so the omitempty wire shape stays
		// byte-identical.
		attachFiltererComponents(resp, buildFiltererComponents(req.Filterers, filterCounters))

		// Emit RunComponents — typed cohort-level counters.
		// NullRecords resolution: the FIRST aggregator's nNull from
		// aggComponents when the ungrouped exit computed it (the per-field
		// floor cache); otherwise a scan of the post-filter records for
		// nulls on the primary field (first aggregator's Field, else the
		// grouper's) — the rule every streaming exit applies. A grouped
		// run emits no aggComponents, and this used to fall back to the
		// grouper's NNull instead: a different quantity (it also counts
		// include rejections, and is about the GROUPER field), so a
		// grouped request reported a different null_records on the
		// buffered path than on the streaming one. The buffered path has
		// no shard concept (the service stamps ShardCount on an archive).
		var nullRecords int64
		if len(aggComponents) > 0 {
			nullRecords = int64(aggComponents[0].NNull)
		} else {
			nullRecords = countNullsBuffered(filtered, primaryNullFieldName(req))
		}
		attachRunComponents(resp, RunCountersInput{
			TotalRecords:    totalRows,
			FilteredRecords: int64(len(filtered)),
			NullRecords:     nullRecords,
		})
	}

	// SERIES-host overlay hook. Wraps the finalized per-group
	// Response.Data as a SeriesHostView and dispatches each
	// req.Overlays spec through the SERIES handler registry
	// (overlay_series.go). The hook short-circuits unless the request
	// is grouped (req.Groups non-empty) AND carries a primary
	// aggregator AND req.Overlays is non-empty — grouped Process only.
	// Crosstab requests are NOT routed through
	// processRecords (Service.Process dispatches them to
	// processCrosstab), so the SERIES hook never collides with the
	// MATRIX hook in internal/processing/crosstab.go.
	if err := applyOverlaysSeriesToResponse(req, resp, p.exts); err != nil {
		return nil, err
	}

	// One PULSE_WEIGHT_INVALID_ROWS warning per weight field over the
	// filter-passing rows (after attributes, as every streaming mode).
	weights := NewWeightRowTally(req)
	weights.ObserveAll(filtered)
	if err := weights.Apply(resp, p.strictWeights); err != nil {
		return nil, err
	}

	return resp, nil
}

// recordsAsRegressionRecords adapts a []*Record into the
// []regression.Record slice the regression engines consume. The
// regression subpackage defines its own Record interface so it stays
// free of the processing import (mirroring internal/processing/feature); the
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
// containing every non-null value (categorical fields resolve to strings,
// decimal128 to a scale-carrying decimalCell that finalizeRowCells
// renders as its decimal string — see row_cells.go).
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
			if _, isDec := v.(encoding.Decimal128); isDec {
				v = rowCell(r.fieldNamed(k), v)
			}
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
// Response.Components.Filterers. Returns the filter-passing
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
	return feature.ApplyWithExt(view, features, p.schema, p.exts.LookupFeature)
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
		if err := bindAttribute(computer, p.exts); err != nil {
			return err
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
				// Writes the value without clearing a null mark and
				// invalidates any cached AllValues() result.
				r.injectValue(label, values[i])
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
	ApplyGrouperExtensions(grouper, p.exts)

	groups, err := grouper.Group(records, grp.Field)
	if err != nil {
		return nil, nil, err
	}

	// Stable-emit by group key so row order is deterministic across runs
	// regardless of Go's map-iteration randomness. When the grouper carries
	// an active Group.Include list, keys emit in include order; otherwise
	// orderKeysByInclude funnels through sort.Strings (byte-identical to the
	// pre-Include alphabetical default). An explicit req.Sort below still
	// overrides this default ordering.
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	keys = orderKeysByInclude(includeFilterOf(grouper), keys)

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

	// Emit GrouperComponents for the single grouper slot.
	// Universal floor (TotalN = sum of bucket counts, NNull =
	// post-filter records minus TotalN — the records the grouper
	// skipped due to null inputs or include-filter rejection) is
	// derived directly from the bucket map; the operator-specific
	// keys ride off MetaGrouper.Components() when the grouper
	// implements it.
	//
	// Skip the build entirely when components emission is disabled —
	// MetaGrouper.Components is non-trivial work and the caller's gate
	// drops the slice on the floor anyway. processRecords passes the
	// returned slice through its own disableComponents gate.
	if p.disableComponents {
		return data, nil, nil
	}
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
			if FieldPresent(r, field) {
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
		// Decimal-typed fields dispatch to AggregateDecimalField. A
		// registered extension aggregator owns its decimal semantics
		// (extend.Record.DecimalValue) and falls through to its factory.
		if p.schema != nil && !p.exts.isExtensionAggregator(agg.Type) {
			if f := p.schema.Field(agg.Field); f != nil && f.Type.IsDecimal() {
				if !p.exts.decimalAggregationSupported(agg.Type) {
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
			wf := NewWeightFloor(agg)
			if wf.spec != nil {
				for _, r := range records {
					wf.Observe(r, agg.Field)
				}
				wf.Stamp(&entry)
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

// DispatchAggregatorResult is the exported form of
// dispatchAggregatorResult, for reducers that live outside this package
// and finalise merged aggregator state themselves — the per-shard
// (service.processShardArchiveParallel) and per-segment
// (service.reduceParallelBuffered) parallel arms, both of which build
// their response row from Finalize() directly rather than through the
// Processor.
//
// They MUST lift through this function. A RichAggregator's Finalize()
// returns only the scalar FALLBACK — popcount for AGG_SET_UNION, the
// max-bin count for AGG_SET_FREQUENCY — so a reducer that writes the
// float64 straight into the row emits a bare number where the serial
// path emits resolved labels or a label→count map. The arms then
// disagree on the SHAPE of Response.Data for the same request, decided
// by a concurrency knob.
func DispatchAggregatorResult(agg any, scalar float64) (any, error) {
	return dispatchAggregatorResult(agg, scalar)
}

// dispatchAggregatorCellResult is the MatrixCell.Value-bound sibling of
// dispatchAggregatorResult. It preserves the Rich-or-scalar lift for
// every aggregator EXCEPT AGG_WELFORD: WelfordTriple is an internal
// statistical-moment carrier owned by Components.Crosstab.CellComponents,
// not a MatrixCell.Value payload. For AGG_WELFORD specifically the cell
// builder writes the scalar mean (matching welfordAggregator.Aggregate /
// Finalize) so the cell payload stays a plain float64 — overlay handlers
// source `(mean, variance, n)` from CellComponents.
//
// All other RichAggregator payloads (map[string]int from
// AGG_SET_FREQUENCY, []string from AGG_SET_UNION / AGG_SET_INTERSECTION,
// future families) continue to ride MatrixCell.Value untouched — this
// carve-out is type-name-specific to the WelfordTriple shape and stays
// orthogonal to other rich families.
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
//
// Callers MUST guard the build+attach call sequence with a check on
// the processor's disableComponents flag — the guard sits at the
// per-execution-path emission block so the upstream
// MetaAggregator.Components / build work is skipped too, not built
// then discarded.
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
// drove, the originating Group slot, the post-filter row count
// observed by the streaming iterator, and the orchestrator's own count
// of (record, bucket) assignments. Mirrors buildGrouperComponents:
// TotalN is derived from MetaGrouper.Components()'s buckets payload
// when the grouper emits one (every built-in does), and otherwise from
// assignments — the streaming twin of summing the buffered partition
// map — so an extension grouper (no buckets payload, or no
// MetaGrouper at all) reports the same floor on either path.
func buildStreamingGrouperComponents(grouper any, slot *types.Group, totalFiltered int, assignments int64) (types.GrouperComponents, error) {
	entry := types.GrouperComponents{
		Field: slot.Field,
	}
	totalN := int(assignments)
	if meta, ok := grouper.(MetaGrouper); ok {
		op, err := meta.Components()
		if err != nil {
			return types.GrouperComponents{}, err
		}
		entry.Operator = op
		// Sum per-bucket counts to derive TotalN. The buckets payload is
		// a []map[string]any with int "count" entries by GROUP_CATEGORY /
		// GROUP_DATE convention; every built-in MetaGrouper follows it,
		// and the type assertion guards drift.
		if buckets, ok := op["buckets"].([]map[string]any); ok {
			totalN = 0
			for _, b := range buckets {
				if c, ok := b["count"].(int); ok {
					totalN += c
				}
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
//
// Callers MUST guard the build+attach call sequence with a check on
// the processor's disableComponents flag; see attachAggregationComponents
// for the full opt-out contract.
func attachGrouperComponents(resp *types.Response, entry types.GrouperComponents) {
	if resp.Components == nil {
		resp.Components = &types.ResponseComponents{}
	}
	resp.Components.Groupers = append(resp.Components.Groupers, entry)
}
