package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"sort"
	"sync"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// shard_reduce.go implements per-shard parallel execution for the
// subset of requests that processing.CanMergeRequest accepts: every
// aggregator and grouper is mergeable, no windows/features/tests/
// regressions, attributes are at most row-local, and the cohort backs
// onto a shard archive with N >= 2 shards.
//
// The orchestrator opens the archive once, fans out per-shard
// processing across a bounded worker pool, then merges partial states
// in shard insertion order (zip central-directory order). Order
// preservation matters for two reasons: associative+commutative
// aggregators (count, sum, min, max, null_count, frequency,
// distinct_count, mode) produce byte-equal results vs the serial path
// when partials are folded in any order, but the Welford-mean parallel
// merge introduces ULP drift that is sensitive to merge order;
// shard insertion order is the documented, reproducible choice.
//
// Non-mergeable requests fall through to the serial shardIter path
// (process.go's newScanIter dispatch); workers never spawn. This
// preserves byte-for-byte semantics for percentile aggregators,
// window operators, tier-1/tier-2 tests, two-pass attributes combined
// with groupers, and every other gate processing.CanStreamRequest
// already routes through buffered execution.

// shouldFanOut reports whether the current Process call qualifies for
// per-shard parallelization. The decision is gated by:
//
//   - the cohort is archive-backed (cohort.Shards() non-empty)
//   - more than one shard (no point parallelising N=1)
//   - the request is mergeable per processing.CanMergeRequest
//   - the configured worker cap is not 1 (1 forces serial)
//
// Returns the resolved worker count (>= 2) and true when fan-out
// applies, or (0, false) when the caller should fall through to the
// serial path.
func (s *Service) shouldFanOut(req *types.Request, cohort *Cohort) (int, bool) {
	if s.shardWorkers == 1 {
		return 0, false
	}
	shards := cohort.Shards()
	if len(shards) < 2 {
		return 0, false
	}
	if !processing.CanMergeRequest(req, cohort.Schema()) {
		return 0, false
	}
	workers := s.shardWorkers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > len(shards) {
		workers = len(shards)
	}
	if workers < 2 {
		return 0, false
	}
	return workers, true
}

// processShardArchiveParallel runs the per-shard parallel reducer.
// Caller guarantees shouldFanOut returned true. The archive is read
// once via afero, then each worker carves a SectionReader for its
// assigned shards and folds rows through fresh per-worker operator
// instances. Partials accumulate in a slice indexed by shard insertion
// order; a final pass merges them in that order so Welford drift is
// deterministic across runs.
func (s *Service) processShardArchiveParallel(ctx context.Context, req *types.Request, cohort *Cohort, path string, workers int) (*types.Response, error) {
	shards := cohort.Shards()
	schema := cohort.Schema()

	// Read the archive once. Each worker re-wraps the byte buffer in a
	// bytes.Reader (which is an io.ReaderAt under the hood) so worker
	// reads never contend on a shared file handle.
	data, err := afero.ReadFile(s.fs.Fs(), path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening shard archive for parallel reduce: %s", path))
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	partials := make([]*shardPartial, len(shards))
	jobs := make(chan int, len(shards))
	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if cctx.Err() != nil {
					return
				}
				p, err := s.processOneShard(cctx, req, schema, arch, shards[idx].Filename)
				if err != nil {
					errOnce.Do(func() { firstErr = err; cancel() })
					return
				}
				partials[idx] = p
			}
		}()
	}

	for i := range shards {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	// Merge partials in shard insertion order. The order matters for
	// Welford-mean ULP determinism; for purely associative+commutative
	// aggregators the answer is identical regardless of merge order.
	merged, err := mergeShardPartials(req, schema, partials)
	if err != nil {
		return nil, err
	}
	resp, err := finalizeMergedPartial(req, schema, merged, len(shards), s.effectiveDisableComponents(req))
	if err != nil {
		return nil, err
	}
	if resp.Metadata != nil {
		resp.Metadata.CohortFile = path
	}
	if err := s.buildAndApplyLabels(req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// shardPartial holds a single shard's per-aggregator (and per-group
// per-aggregator) state after streaming that shard's filter-passing
// records through fresh OnlineAggregator instances. The orchestrator
// merges these in shard insertion order.
//
// Two shapes:
//
//   - ungrouped: aggs is a slice indexed by req.Aggregations position;
//     groups is nil.
//   - grouped: aggs is nil; groups holds one bucket per distinct key
//     observed in this shard, plus keyOrder so the merger can rebuild
//     insertion order when constructing the final response row set.
type shardPartial struct {
	totalRows    int64
	filteredRows int64

	// nullRecords counts post-filter records where the primary
	// aggregation field (first agg's Field, else first grouper's Field)
	// was null. Used by finalizeMergedPartial to populate
	// Response.Components.Run.NullRecords. Merger sums across shards.
	nullRecords int64

	// aggN / aggNNull carry the UNIVERSAL FLOOR of
	// Response.Components.Aggregations — one {n, n_null} pair per
	// req.Aggregations slot, indexed by slot position. Both are plain
	// per-record tallies, so the merge is a slot-wise sum and is
	// associative and commutative: any partition of the record stream
	// across workers or shards yields the serial totals.
	//
	// They exist because nullRecords above is a SINGLE primary-field
	// counter. It answers Run.NullRecords and nothing else, so before
	// these fields the parallel arms emitted no per-slot floor at all
	// while the serial path emitted one entry per aggregator — the
	// same request returning a different Components shape depending on
	// a concurrency knob.
	//
	// Presence is asked through processing.FieldPresent, never through
	// NumericValue: a set-typed column has no numeric value but does
	// have presence, and asking the wrong question would move every
	// respondent in a set column into n_null.
	aggN     []int64
	aggNNull []int64

	// filterCounters carries the per-slot {n_in, n_out, n_null_input}
	// triple behind Response.Components.Filterers, one entry per
	// req.Filterers slot. Produced and folded by the processing
	// package's own walk (ApplyFilterPass / MergeFilterPassCounters)
	// so the counter semantics — the n_in invariant, the null-input
	// tally that is independent of pass/fail, the AND short-circuit —
	// have exactly one implementation.
	filterCounters []processing.FilterPassCounters

	aggs   []processing.OnlineAggregator
	groups map[string][]processing.OnlineAggregator
	// keyOrder preserves the order distinct group keys were first seen
	// in this shard. The merger's stable sort by key ensures the
	// final response row order is deterministic across worker
	// scheduling perturbations.
	keyOrder []string
}

// processOneShard streams one shard's records through fresh per-shard
// OnlineAggregator instances (and per-group buckets when req.Groups is
// non-empty). Returns a partial state ready for merging.
func (s *Service) processOneShard(ctx context.Context, req *types.Request, schema *encoding.Schema, arch *encoding.Archive, shardName string) (*shardPartial, error) {
	sect, err := arch.OpenAt(shardName)
	if err != nil {
		return nil, err
	}
	r := &sect
	if err := encoding.ReadHeader(r); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard %q header", shardName))
	}
	if _, err := encoding.ReadSchema(r); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard %q schema", shardName))
	}
	rr := encoding.NewRecordReader(r, schema)

	// Pre-build per-shard operators. Each shard worker gets its own
	// instance set so there's no shared mutable state across workers.
	filterFns, err := processing.BuildFilters(req.Filterers, schema, s.extensions)
	if err != nil {
		return nil, err
	}
	rowLocalAttrs, err := buildRowLocalAttrSpecs(req.Attributes, schema, s.extensions)
	if err != nil {
		return nil, err
	}

	specs := make([]aggSpec, len(req.Aggregations))
	for i, agg := range req.Aggregations {
		factory, ok := s.extensions.LookupAggregator(agg.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown aggregation type: %s", agg.Type))
		}
		specs[i] = aggSpec{agg: agg, factory: factory}
	}

	var grouperSpec *types.Group
	var streamGrp processing.StreamingGrouper
	if len(req.Groups) > 0 {
		grouperSpec = req.Groups[0]
		grouperFactory, ok := s.extensions.LookupGrouper(grouperSpec.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown group type: %s", grouperSpec.Type))
		}
		grp, err := grouperFactory(grouperSpec, schema)
		if err != nil {
			return nil, err
		}
		processing.ApplyGrouperExtensions(grp, s.extensions)
		sg, ok := grp.(processing.StreamingGrouper)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("grouper %s does not implement StreamingGrouper", grouperSpec.Type))
		}
		streamGrp = sg
	}

	// Resolve the primary aggregation field for the per-shard null
	// counter (see primaryNullFieldFor).
	primaryNullField := primaryNullFieldFor(req)

	out := &shardPartial{
		aggN:           make([]int64, len(specs)),
		aggNNull:       make([]int64, len(specs)),
		filterCounters: processing.NewFilterPassCounters(req.Filterers),
	}
	var aggsUngrouped []processing.OnlineAggregator
	if streamGrp == nil {
		aggsUngrouped = make([]processing.OnlineAggregator, len(specs))
		for i, sp := range specs {
			inst, err := sp.factory(sp.agg, schema)
			if err != nil {
				return nil, err
			}
			online, ok := inst.(processing.OnlineAggregator)
			if !ok {
				return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
					fmt.Sprintf("aggregator %s does not implement OnlineAggregator", sp.agg.Type))
			}
			aggsUngrouped[i] = online
		}
		out.aggs = aggsUngrouped
	} else {
		out.groups = make(map[string][]processing.OnlineAggregator)
	}

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		values := make(map[string]float64, len(schema.Fields))
		nulls := make(map[string]bool)
		wide := make(map[string]any)
		err := rr.ReadRecordWithWide(values, nulls, wide)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		rec := processing.NewRecordWithWide(schema, values, nulls, wide)
		out.totalRows++

		pass, ferr := processing.ApplyFilterPass(rec, req.Filterers, filterFns, out.filterCounters)
		if ferr != nil {
			return nil, ferr
		}
		if !pass {
			continue
		}
		out.filteredRows++

		if primaryNullField != "" && rec.IsNull(primaryNullField) {
			out.nullRecords++
		}

		for _, ra := range rowLocalAttrs {
			val, err := ra.computer.Row(rec, ra.attr.Field)
			if err != nil {
				return nil, err
			}
			rec.Set(ra.label, val)
		}

		// Universal floor, tallied AFTER row-local attributes land so a
		// slot aggregating an attribute label sees the same presence
		// the serial orchestrator sees. Ordering mirrors
		// processing.processStreaming exactly.
		for i := range specs {
			if processing.FieldPresent(rec, specs[i].agg.Field) {
				out.aggN[i]++
			} else {
				out.aggNNull[i]++
			}
		}

		if streamGrp == nil {
			for i, oa := range aggsUngrouped {
				if err := oa.UpdateRow(rec, specs[i].agg.Field); err != nil {
					return nil, err
				}
			}
			continue
		}

		key, ok, err := streamGrp.KeyForRow(rec, grouperSpec.Field)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		bucket, exists := out.groups[key]
		if !exists {
			bucket = make([]processing.OnlineAggregator, len(specs))
			for i, sp := range specs {
				inst, err := sp.factory(sp.agg, schema)
				if err != nil {
					return nil, err
				}
				online, ok := inst.(processing.OnlineAggregator)
				if !ok {
					return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
						fmt.Sprintf("aggregator %s does not implement OnlineAggregator", sp.agg.Type))
				}
				bucket[i] = online
			}
			out.groups[key] = bucket
			out.keyOrder = append(out.keyOrder, key)
		}
		for i, oa := range bucket {
			if err := oa.UpdateRow(rec, specs[i].agg.Field); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// primaryNullFieldFor resolves the field whose per-record null tally
// feeds Response.Components.Run.NullRecords: the first aggregator's
// Field, else the first grouper's Field, else empty (no tally).
// Convention matches processing/run_components.go's
// primaryNullFieldName. Shared by BOTH parallel reducers — the
// per-shard one (shard_reduce.go) and the per-segment one
// (parallel_reduce.go) — because two copies of a convention is how
// the DecodeWorkers arm ended up reporting NullRecords: 0 while the
// other two paths reported the real count.
func primaryNullFieldFor(req *types.Request) string {
	if req == nil {
		return ""
	}
	if len(req.Aggregations) > 0 && req.Aggregations[0] != nil && req.Aggregations[0].Field != "" {
		return req.Aggregations[0].Field
	}
	if len(req.Groups) > 0 && req.Groups[0] != nil {
		return req.Groups[0].Field
	}
	return ""
}

// aggSpec carries the resolved factory for one aggregator slot.
// Shared between the per-shard processor and the final-state
// reconstruction step.
type aggSpec struct {
	agg     *types.Aggregation
	factory processing.AggregatorFactory
}

// rowLocalAttrSpec is a parallel structure to processor.rowLocalAttrEntry
// used by the per-shard worker. We can't reuse the processor type
// directly (unexported) so we mirror the shape.
type rowLocalAttrSpec struct {
	attr     *types.Attribute
	computer processing.RowLocalAttribute
	label    string
}

func buildRowLocalAttrSpecs(attrs []*types.Attribute, schema *encoding.Schema, exts *processing.ExtensionRegistry) ([]rowLocalAttrSpec, error) {
	if len(attrs) == 0 {
		return nil, nil
	}
	out := make([]rowLocalAttrSpec, 0, len(attrs))
	for _, attr := range attrs {
		factory, ok := exts.LookupAttribute(attr.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown attribute type: %s", attr.Type))
		}
		computer, err := factory(attr, schema)
		if err != nil {
			return nil, err
		}
		if aware, ok := computer.(processing.ExtensionAware); ok {
			aware.SetExtensions(exts)
		}
		rl, ok := computer.(processing.RowLocalAttribute)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("attribute %s is not row-local; not eligible for per-shard parallel reduce", attr.Type))
		}
		label := attr.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", attr.Type, attr.Field)
		}
		out = append(out, rowLocalAttrSpec{attr: attr, computer: rl, label: label})
	}
	return out, nil
}

// mergeShardPartials folds the per-shard partial states into a single
// aggregated state. Walks partials in shard insertion order — the
// caller (processShardArchiveParallel) pre-sorts the slice so order is
// the central-directory enumeration, NOT the worker completion order.
//
// For ungrouped requests we merge each aggregator slot's running state
// across all shards. For grouped requests we union per-key buckets:
// merging by key preserves the per-key associativity, then a stable
// sort produces deterministic row order.
func mergeShardPartials(req *types.Request, schema *encoding.Schema, partials []*shardPartial) (*shardPartial, error) {
	if len(partials) == 0 {
		return &shardPartial{}, nil
	}
	// Find the first non-nil partial to seed; with len(partials) >= 1
	// and shouldFanOut requiring N >= 2, every entry must be non-nil
	// post-worker-success. Defensive guard for the empty/skipped case.
	var head int
	for head < len(partials) && partials[head] == nil {
		head++
	}
	if head >= len(partials) {
		return &shardPartial{}, nil
	}
	merged := partials[head]
	for i := head + 1; i < len(partials); i++ {
		p := partials[i]
		if p == nil {
			continue
		}
		merged.totalRows += p.totalRows
		merged.filteredRows += p.filteredRows
		merged.nullRecords += p.nullRecords

		// Universal-floor and filter counters are plain tallies: the
		// fold is a slot-wise sum, associative and commutative, so no
		// ordering guarantee is needed here (unlike the Welford merge
		// below, which is why this function walks partials in shard
		// insertion order regardless).
		for slot := range merged.aggN {
			if slot < len(p.aggN) {
				merged.aggN[slot] += p.aggN[slot]
			}
			if slot < len(p.aggNNull) {
				merged.aggNNull[slot] += p.aggNNull[slot]
			}
		}
		processing.MergeFilterPassCounters(merged.filterCounters, p.filterCounters)

		if merged.aggs != nil {
			if len(p.aggs) != len(merged.aggs) {
				return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
					"shard partial aggregator count mismatch during merge")
			}
			for slot, oa := range merged.aggs {
				m, ok := oa.(processing.MergeableAggregator)
				if !ok {
					return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
						fmt.Sprintf("aggregator %s does not implement MergeableAggregator", req.Aggregations[slot].Type))
				}
				if err := m.MergeOnline(p.aggs[slot]); err != nil {
					return nil, err
				}
			}
		}

		if merged.groups != nil {
			for _, key := range p.keyOrder {
				bucket := p.groups[key]
				existing, exists := merged.groups[key]
				if !exists {
					merged.groups[key] = bucket
					merged.keyOrder = append(merged.keyOrder, key)
					continue
				}
				for slot, oa := range existing {
					m, ok := oa.(processing.MergeableAggregator)
					if !ok {
						return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
							fmt.Sprintf("aggregator %s does not implement MergeableAggregator", req.Aggregations[slot].Type))
					}
					if err := m.MergeOnline(bucket[slot]); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	_ = schema
	return merged, nil
}

// finalizeMergedPartial calls Finalize on each merged aggregator and
// emits the response row(s). Mirrors the tail of processStreaming /
// processStreamingGrouped in the processing package.
//
// shardCount carries the number of shards that contributed to the
// merged state — populated by the shard-archive parallel reducer
// (processShardArchiveParallel) and left at 0 by the single-file
// parallel buffered reducer (reduceParallelBuffered). The value flows
// into Response.Components.Run.ShardCount and stays at 0 for
// single-file cohorts so the omitempty wire shape is byte-identical
// against the single-cohort baseline.
//
// disableComponents mirrors the engine-level
// pulse.Options.DisableComponents (with per-request override) — when
// true, attachMergedRunComponents is a no-op and Response.Components
// stays nil so the merged-shard wire form is byte-identical to the
// pre-Components baseline.
func finalizeMergedPartial(req *types.Request, schema *encoding.Schema, merged *shardPartial, shardCount int, disableComponents bool) (*types.Response, error) {
	_ = schema
	resp := &types.Response{
		Metadata: &types.ResponseMetadata{
			TotalRows:    merged.totalRows,
			FilteredRows: merged.filteredRows,
		},
	}

	if merged.aggs != nil {
		row := make(map[string]any, len(merged.aggs))
		for i, oa := range merged.aggs {
			val, err := oa.Finalize()
			if err != nil {
				return nil, err
			}
			label := req.Aggregations[i].Label
			if label == "" {
				label = fmt.Sprintf("%s_%s", req.Aggregations[i].Type, req.Aggregations[i].Field)
			}
			// Lift through the Rich-or-scalar dispatch the serial
			// Processor uses. Finalize() alone returns a
			// RichAggregator's scalar FALLBACK, so writing it straight
			// into the row made AGG_SET_UNION emit a popcount and
			// AGG_SET_FREQUENCY a max-bin count under either parallel
			// knob while the serial path emitted labels and a
			// label→count map — the same request answering in two
			// different shapes depending on worker count.
			row[label], err = processing.DispatchAggregatorResult(oa, val)
			if err != nil {
				return nil, err
			}
		}
		if len(merged.aggs) > 0 {
			resp.Data = []map[string]any{row}
		}
		if err := attachMergedAggregationComponents(resp, req, merged, disableComponents); err != nil {
			return nil, err
		}
		attachMergedFiltererComponents(resp, req, merged, disableComponents)
		attachMergedRunComponents(resp, merged, shardCount, disableComponents)
		return resp, nil
	}

	if merged.groups != nil {
		grp := req.Groups[0]
		// Stable sort by key so output row order is deterministic across
		// runs regardless of map-iteration order or worker scheduling.
		keys := make([]string, len(merged.keyOrder))
		copy(keys, merged.keyOrder)
		sort.SliceStable(keys, func(i, j int) bool { return keys[i] < keys[j] })
		data := make([]map[string]any, 0, len(keys))
		for _, key := range keys {
			bucket := merged.groups[key]
			row := make(map[string]any, len(bucket)+1)
			for i, oa := range bucket {
				val, err := oa.Finalize()
				if err != nil {
					return nil, err
				}
				label := req.Aggregations[i].Label
				if label == "" {
					label = fmt.Sprintf("%s_%s", req.Aggregations[i].Type, req.Aggregations[i].Field)
				}
				// Same Rich-or-scalar lift as the ungrouped arm above.
				row[label], err = processing.DispatchAggregatorResult(oa, val)
				if err != nil {
					return nil, err
				}
			}
			row[grp.Field] = key
			data = append(data, row)
		}
		resp.Data = data
	}
	// The GROUPED arm deliberately emits no Components.Aggregations.
	// That is parity, not an omission: processGrouped and
	// processStreamingGrouped both leave the slice nil too (per-group
	// components emission is a separate, unlanded surface), so emitting
	// a cohort-wide floor here would make the parallel arm the only
	// path in the engine that answers the question — and answer it at
	// the wrong granularity, since a grouped request's floor is
	// per-group. Components.Groupers is likewise not emitted: the
	// per-shard grouper instances are never merged (the fold happens at
	// the bucket-key level), so no merged grouper exists to ask. An
	// absent component beats a fabricated one.
	attachMergedFiltererComponents(resp, req, merged, disableComponents)
	attachMergedRunComponents(resp, merged, shardCount, disableComponents)
	return resp, nil
}

// attachMergedAggregationComponents emits one
// Response.Components.Aggregations entry per req.Aggregations slot
// from the merged partial — the universal floor {n, n_null} off the
// merged per-slot tallies, and the operator-specific map off the
// MERGED aggregator instance.
//
// Reading the operator map off the merged instance is what makes this
// correct without a components-level merge: the parallel arms fold
// OPERATOR STATE via MergeableAggregator.MergeOnline, so after
// mergeShardPartials each slot holds the complete cohort state and its
// Components() is byte-for-byte what a serial instance would report.
// processing.CanMergeRequest has already refused any request whose
// operator state cannot fold, so no mergeability-class gate is needed
// here — every operator that clears that gate folds its components
// with its state.
//
// No-op when disableComponents is true, so the build cost is skipped
// rather than incurred and discarded.
func attachMergedAggregationComponents(resp *types.Response, req *types.Request, merged *shardPartial, disableComponents bool) error {
	if resp == nil || merged == nil || disableComponents || merged.aggs == nil {
		return nil
	}
	for i, oa := range merged.aggs {
		var slot *types.Aggregation
		if i < len(req.Aggregations) {
			slot = req.Aggregations[i]
		}
		var n, nNull int
		if i < len(merged.aggN) {
			n = int(merged.aggN[i])
		}
		if i < len(merged.aggNNull) {
			nNull = int(merged.aggNNull[i])
		}
		entry, err := processing.BuildAggregationComponents(oa, slot, n, nNull)
		if err != nil {
			return err
		}
		processing.AttachAggregationComponents(resp, entry)
	}
	return nil
}

// attachMergedFiltererComponents emits the per-slot
// {n_in, n_out, n_null_input} triple from the merged filter counters.
// Rendering goes through the processing package's own builder so the
// parallel arms and the serial paths cannot disagree on the shape.
// An empty filter chain leaves the slice nil and the wire form
// byte-identical.
func attachMergedFiltererComponents(resp *types.Response, req *types.Request, merged *shardPartial, disableComponents bool) {
	if resp == nil || merged == nil || disableComponents {
		return
	}
	processing.AttachFiltererComponents(resp,
		processing.BuildFiltererComponents(req.Filterers, merged.filterCounters))
}

// attachMergedRunComponents writes Response.Components.Run from a
// merged shardPartial. Mirrors processing.attachRunComponents but is
// service-local so the per-shard reducer (and the single-file parallel
// buffered reducer that reuses the same merge state) can populate the
// typed cohort counters without reaching into the processing package's
// unexported helper.
//
// No-op when disableComponents is true — the caller's gate (the
// effective engine + per-request decision) suppresses Response.Components
// entirely, so this helper leaves resp.Components at nil for
// byte-identical wire output against the pre-Components baseline.
func attachMergedRunComponents(resp *types.Response, merged *shardPartial, shardCount int, disableComponents bool) {
	if resp == nil || merged == nil || disableComponents {
		return
	}
	if resp.Components == nil {
		resp.Components = &types.ResponseComponents{}
	}
	resp.Components.Run = &types.RunComponents{
		TotalRecords:    merged.totalRows,
		FilteredRecords: merged.filteredRows,
		NullRecords:     merged.nullRecords,
		ShardCount:      shardCount,
	}
}
