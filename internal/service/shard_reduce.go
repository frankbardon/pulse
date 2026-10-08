package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"sync"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// shard_reduce.go implements per-shard parallel execution for the
// subset of requests that processing.CanMergeRequestWithExtensions accepts: every
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
//   - the request is mergeable per processing.CanMergeRequestWithExtensions
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
	if !processing.CanMergeRequestWithExtensions(req, cohort.Schema(), s.extensions) {
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
	// Every worker builds its aggregators off the stamped spec, so each
	// slot carries its resolved weight (processing.StampWeightsWith).
	req = processing.StampWeightsWith(req, s.defaultWeight, s.extensions)
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
	arch, err := encx.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	if ei := execInfoFrom(ctx); ei != nil {
		ei.setPlan(observe.ArmShardParallel, workers, len(shards), 0)
		ei.addBytes(int64(len(data)))
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
				p, err := s.processOneShard(cctx, req, schema, arch, idx, shards[idx].Filename)
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
	resp, err := finalizeMergedPartial(req, schema, merged, len(shards), s.computePlanFor(ctx, req), s.extensions)
	if err != nil {
		return nil, err
	}
	if err := merged.weights.Apply(resp, s.strict); err != nil {
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

	// aggFloor carries the UNIVERSAL FLOOR of
	// Response.Components.Aggregations — one {n, n_null} pair plus the
	// weighted floor tally (sum_weights, n_eff, n_weight_invalid; inert
	// on an unweighted slot) per req.Aggregations slot, indexed by slot
	// position. On a grouped partial it is the COHORT-WIDE slot floor.
	// All plain per-record tallies, so the merge is a slot-wise sum and
	// is associative and commutative: any partition of the record stream
	// across workers or shards yields the serial totals.
	//
	// It exists because nullRecords above is a SINGLE primary-field
	// counter. It answers Run.NullRecords and nothing else, so before
	// it the parallel arms emitted no per-slot floor at all while the
	// serial path emitted one entry per aggregator — the same request
	// returning a different Components shape depending on a concurrency
	// knob.
	//
	// Presence is asked through processing.FieldPresent, never through
	// NumericValue: a set-typed column has no numeric value but does
	// have presence, and asking the wrong question would move every
	// respondent in a set column into n_null.
	aggFloor []processing.SlotFloor

	// groupFloors is a grouped partial's per-bucket aggFloor, minted
	// with each bucket in foldGroupedRow and merged key by key like the
	// buckets — the floors behind Components.Aggregations' groups[].
	// Nil when components are disabled or the request is ungrouped.
	groupFloors map[string][]processing.SlotFloor

	// weights is the per-weight-field invalid-row tally behind the
	// PULSE_WEIGHT_INVALID_ROWS warning (nil when nothing is weighted),
	// merged like aggFloor.
	weights *processing.WeightRowTally

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
	// mats is the partition's Request.Matrices state (nil without
	// matrices) — the matrix-slot analogue of aggs. It folds every
	// filter-passing row and merges by absorbing the other partition's
	// per-block co-moments, so the finalized matrices carry the serial
	// bits under any worker count.
	mats *processing.MatrixSlots
	// groupedMats is a grouped partition's per-bucket Request.Matrices
	// state (nil without matrices or groups; mats is then nil). A
	// bucket's slots are minted with its aggregator bucket in
	// foldGroupedRow and merge key by key, so each bucket's matrices
	// carry the serial bits under any worker count.
	groupedMats *processing.GroupedMatrices
	// grouper is this partition's own grouper instance. Its live
	// components state is folded across partitions through
	// processing.MergeableGrouper, so the merged instance's Components()
	// describes the whole cohort exactly as the serial instance does.
	grouper processing.Grouper
	// limits is the run's effective resource limits (Service.Limits),
	// handed in by newShardPartial because the parallel reducers bypass
	// newProcessor. MaxGroups bounds groups (and groupedMats) here and
	// the merged key count in mergeShardPartials.
	limits limits.Limits
	// keyer is the partition grouper's resolved key dispatch.
	keyer *processing.GroupKeyer
	// assignments counts (record, bucket) routings — one per key a
	// record is keyed into — exactly as the serial streaming-grouped
	// path does. It is Components.Groupers' TotalN for a grouper whose
	// components carry no "buckets" list (a bucket-less extension
	// grouper), so the merge sums it and hands it to GroupedTail; a
	// merged tail without it reported total_n 0 and n_null = every
	// filtered record.
	assignments int64
	// keyOrder preserves the order distinct group keys were first seen
	// in this shard. The merger's stable sort by key ensures the
	// final response row order is deterministic across worker
	// scheduling perturbations.
	keyOrder []string
}

// processOneShard streams one shard's records through fresh per-shard
// OnlineAggregator instances (and per-group buckets when req.Groups is
// non-empty). Returns a partial state ready for merging. shardIdx is
// the shard's archive index: with the in-shard record index it is each
// row's merge-block position, stamped exactly as the serial shardIter
// stamps it.
func (s *Service) processOneShard(ctx context.Context, req *types.Request, schema *encoding.Schema, arch *encx.Archive, shardIdx int, shardName string) (*shardPartial, error) {
	sect, err := arch.OpenAt(shardName)
	if err != nil {
		return nil, err
	}
	r := &sect
	pulseVersion, err := encoding.ReadHeader(r)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard %q header", shardName))
	}
	if _, err := encoding.ReadSchema(r, pulseVersion); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard %q schema", shardName))
	}
	rr := encx.NewRecordReader(r, schema)

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
	var grouper processing.Grouper
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
		grouper = grp
	}

	// Resolve the primary aggregation field for the per-shard null
	// counter (see primaryNullFieldFor).
	primaryNullField := primaryNullFieldFor(req)

	out := newShardPartial(req, specs, s.Limits())
	if err := out.buildMatrices(req, schema, s.extensions, grouper != nil, s.computePlanFor(ctx, req)); err != nil {
		return nil, err
	}
	var aggsUngrouped []processing.OnlineAggregator
	if grouper == nil {
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
		if s.computePlanFor(ctx, req).Groups && len(req.Aggregations) > 0 {
			out.groupFloors = processing.NewGroupFloors()
		}
		out.grouper = grouper
		if out.keyer, err = processing.NewGroupKeyer(grouper); err != nil {
			return nil, err
		}
	}

	// A grouped archive: the reader and the record share the CANONICAL
	// schema, whose dictionaries every shard's indices address, so each
	// record is handed its row's parent-group entries and a filter over
	// one group's members takes the per-entry precompute — the same hook
	// filter-to-file uses (readGroupIndices).
	var groupIdx []uint32
	if schema.HasGroups() {
		groupIdx = make([]uint32, len(schema.Groups))
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
		if groupIdx != nil && readGroupIndices(rr, groupIdx) {
			rec.SetGroupIndices(schema, groupIdx)
		}
		rec.SetMergePosition(shardIdx, int(out.totalRows))
		out.totalRows++

		pass, ferr := processing.ApplyFilterPass(rec, req.Filterers, filterFns, out.filterCounters)
		if ferr != nil {
			return nil, ferr
		}
		if !pass {
			continue
		}
		out.filteredRows++

		for _, ra := range rowLocalAttrs {
			val, err := ra.computer.Row(rec, ra.attr.Field)
			if err != nil {
				return nil, err
			}
			rec.Set(ra.label, val)
		}

		// Run.NullRecords' primary-field tally, AFTER row-local
		// attributes land: the primary field may be an attribute
		// label, absent from the decoded record until Set above.
		if primaryNullField != "" && rec.IsNull(primaryNullField) {
			out.nullRecords++
		}

		// Universal floor, tallied AFTER row-local attributes land so a
		// slot aggregating an attribute label sees the same presence
		// the serial orchestrator sees. Ordering mirrors
		// processing.processStreaming exactly.
		out.observeFloor(rec, specs)
		if err := out.mats.UpdateRow(rec); err != nil {
			return nil, err
		}

		if grouper == nil {
			for i, oa := range aggsUngrouped {
				if err := oa.UpdateRow(rec, specs[i].agg.Field); err != nil {
					return nil, err
				}
			}
			continue
		}
		if err := out.foldGroupedRow(rec, grouperSpec.Field, specs, schema); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// newShardPartial returns an empty partial for req's slots: the
// universal floor, the weighted floor and invalid-row tallies, and the
// filter counters. Shared by both parallel reducers, which bypass
// newProcessor — so the run's effective limits l are handed in here
// explicitly and bound this partition's bucket mints (foldGroupedRow,
// the grouped matrices) and, on the seed partial, the merged count.
func newShardPartial(req *types.Request, specs []aggSpec, l limits.Limits) *shardPartial {
	aggs := make([]*types.Aggregation, len(specs))
	for i, sp := range specs {
		aggs[i] = sp.agg
	}
	return &shardPartial{
		aggFloor:       processing.NewSlotFloors(aggs),
		weights:        processing.NewWeightRowTally(req),
		filterCounters: processing.NewFilterPassCounters(req.Filterers),
		limits:         l,
	}
}

// buildMatrices builds the partition's Request.Matrices state: one slot
// set for an ungrouped request (mats), per-bucket state for a grouped
// one (groupedMats). Shared by both parallel reducers. compute is the
// run's plan: a plan that accumulates no matrix (the `matrices` slot
// and components.matrices both excluded) builds none, so no partition
// folds a record into one.
func (sp *shardPartial) buildMatrices(req *types.Request, schema *encoding.Schema, exts *processing.ExtensionRegistry, grouped bool, compute processing.ComputePlan) error {
	var err error
	if grouped {
		sp.groupedMats, err = processing.BuildGroupedMatrices(req, schema, exts, compute, sp.limits)
		return err
	}
	sp.mats, err = processing.BuildMatrixSlots(req, schema, exts, compute)
	return err
}

// observeFloor tallies one filter-passing record into the universal
// floor (presence via processing.FieldPresent, never NumericValue: a
// set column has presence but no numeric value), the weighted floor
// and the invalid-weight tally — AFTER row-local attributes land, in
// processing.processStreaming's order. Shared by both parallel
// reducers.
func (sp *shardPartial) observeFloor(rec *processing.Record, specs []aggSpec) {
	sp.weights.Observe(rec)
	processing.ObserveSlotFloors(sp.aggFloor, rec)
}

// foldGroupedRow fans one filter-passing record into every bucket this
// partition's grouper keys it to — one for a single-key grouper, one per
// selected label for GROUP_SET_PER_ELEMENT — creating a bucket of fresh
// aggregators on a key's first sighting. Shared by both parallel
// reducers; the key dispatch is processing.GroupKeyer, the same one
// the serial streaming-grouped path uses.
func (sp *shardPartial) foldGroupedRow(rec *processing.Record, field string, specs []aggSpec, schema *encoding.Schema) error {
	keys, ok, err := sp.keyer.Keys(rec, field)
	if err != nil || !ok {
		return err
	}
	sp.assignments += int64(len(keys))
	for _, key := range keys {
		bucket, exists := sp.groups[key]
		if !exists {
			// MaxGroups on this partition's count: a partition never
			// holds more keys than the whole run, so a local breach is
			// a global one. Checked once per new key, never per row.
			if err := limits.Check(sp.limits, limits.MaxGroups, int64(len(sp.groups))+1); err != nil {
				return err
			}
			bucket = make([]processing.OnlineAggregator, len(specs))
			for i, spec := range specs {
				inst, err := spec.factory(spec.agg, schema)
				if err != nil {
					return err
				}
				online, ok := inst.(processing.OnlineAggregator)
				if !ok {
					return errors.NewCodedError(errors.PROCESSING_INTERNAL,
						fmt.Sprintf("aggregator %s does not implement OnlineAggregator", spec.agg.Type))
				}
				bucket[i] = online
			}
			sp.groups[key] = bucket
			sp.keyOrder = append(sp.keyOrder, key)
			if sp.groupFloors != nil {
				sp.groupFloors[key] = processing.NewSlotFloors(sp.slotAggs(specs))
			}
		}
		for i, oa := range bucket {
			if err := oa.UpdateRow(rec, specs[i].agg.Field); err != nil {
				return err
			}
		}
		processing.ObserveSlotFloors(sp.groupFloors[key], rec)
		if err := sp.groupedMats.UpdateRow(key, rec); err != nil {
			return err
		}
	}
	return nil
}

// slotAggs returns the slot declarations behind specs, in order.
func (sp *shardPartial) slotAggs(specs []aggSpec) []*types.Aggregation {
	aggs := make([]*types.Aggregation, len(specs))
	for i := range specs {
		aggs[i] = specs[i].agg
	}
	return aggs
}

// primaryNullFieldFor resolves the field whose per-record null tally
// feeds Response.Components.Run.NullRecords: the first aggregator's
// Field, else the first grouper's Field, else empty (no tally).
// Convention matches internal/processing/run_components.go's
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
// A processing.BlockMerger slot (ungrouped or per bucket) absorbs the
// other partition's per-block partials instead of MergeOnline: blocks
// are keyed by absolute (shard, block) position and combined only at
// Finalize through the one fixed tree, so for those slots the partition
// order here cannot reach the bits. Every other slot folds through
// MergeableAggregator.MergeOnline exactly as before.
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
		merged.assignments += p.assignments

		// Universal-floor and filter counters are plain tallies: the
		// fold is a slot-wise sum, associative and commutative, so no
		// ordering guarantee is needed here (unlike the Welford merge
		// below, which is why this function walks partials in shard
		// insertion order regardless).
		processing.MergeSlotFloors(merged.aggFloor, p.aggFloor)
		merged.weights.Merge(p.weights)
		processing.MergeFilterPassCounters(merged.filterCounters, p.filterCounters)
		if err := merged.mats.Merge(p.mats); err != nil {
			return nil, err
		}
		if err := merged.groupedMats.Merge(p.groupedMats); err != nil {
			return nil, err
		}

		if merged.aggs != nil {
			if len(p.aggs) != len(merged.aggs) {
				return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
					"shard partial aggregator count mismatch during merge")
			}
			for slot, oa := range merged.aggs {
				if bm, ok := oa.(processing.BlockMerger); ok {
					if err := processing.MergeBlockMerger(bm, p.aggs[slot]); err != nil {
						return nil, err
					}
					continue
				}
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
			if merged.grouper != nil && p.grouper != nil {
				m, ok := merged.grouper.(processing.MergeableGrouper)
				if !ok {
					return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
						fmt.Sprintf("grouper %s does not implement MergeableGrouper", req.Groups[0].Type))
				}
				if err := m.MergeGrouperState(p.grouper); err != nil {
					return nil, err
				}
			}
			for _, key := range p.keyOrder {
				bucket := p.groups[key]
				existing, exists := merged.groups[key]
				if !exists {
					// MaxGroups on the MERGED count: every partition
					// can sit under the limit while their union does
					// not.
					if err := limits.Check(merged.limits, limits.MaxGroups, int64(len(merged.groups))+1); err != nil {
						return nil, err
					}
					merged.groups[key] = bucket
					merged.keyOrder = append(merged.keyOrder, key)
					if merged.groupFloors != nil {
						merged.groupFloors[key] = p.groupFloors[key]
					}
					continue
				}
				if merged.groupFloors != nil {
					processing.MergeSlotFloors(merged.groupFloors[key], p.groupFloors[key])
				}
				for slot, oa := range existing {
					if bm, ok := oa.(processing.BlockMerger); ok {
						if err := processing.MergeBlockMerger(bm, bucket[slot]); err != nil {
							return nil, err
						}
						continue
					}
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
// compute is the run's processing.ComputePlan (Service.computePlanFor):
// each Components sub-part it drops is never built — with every
// sub-part off (the DisableComponents opt-out) Response.Components stays
// nil, so the merged-shard wire form is byte-identical to the
// pre-Components baseline.
func finalizeMergedPartial(req *types.Request, schema *encoding.Schema, merged *shardPartial, shardCount int, compute processing.ComputePlan, exts *processing.ExtensionRegistry) (*types.Response, error) {
	_ = schema
	// The ungrouped arms' matrices (nil on a grouped partial, whose
	// per-bucket matrices render in the grouped tail).
	matrices, matrixComps, err := merged.mats.Finalize()
	if err != nil {
		return nil, err
	}
	resp := &types.Response{
		Metadata: &types.ResponseMetadata{
			TotalRows:    merged.totalRows,
			FilteredRows: merged.filteredRows,
		},
		Matrices: matrices,
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
		if err := attachMergedAggregationComponents(resp, req, merged, compute.Aggs); err != nil {
			return nil, err
		}
		attachMergedFiltererComponents(resp, req, merged, compute.Filterers)
		attachMergedRunComponents(resp, merged, shardCount, compute.Run)
		processing.AttachMatrixComponents(resp, matrixComps)
		return resp, nil
	}

	if merged.groups != nil {
		// The grouped arm hands the merged buckets and the merged
		// grouper to the ONE grouped emission tail the serial
		// streaming-grouped path uses: row order (include order, else
		// sorted keys), Request.Sort, Components.Groupers off the merged
		// grouper's live state, filterers, run and the SERIES overlay
		// fold. A private copy of that tail here used to drop Sort,
		// Overlays, include ordering and the groupers block — the same
		// request answering differently under a worker count. The
		// per-group aggregation Components ride the same tail off the
		// merged buckets and the merged floors (cohort-wide aggFloor,
		// per-bucket groupFloors), as do the per-bucket matrices,
		// rendered over the same ordered keys as the rows.
		tail := processing.GroupedTail{
			Group:          req.Groups[0],
			Grouper:        merged.grouper,
			Buckets:        merged.groups,
			TotalRows:      merged.totalRows,
			FilteredRows:   merged.filteredRows,
			NullRecords:    merged.nullRecords,
			Assignments:    merged.assignments,
			FilterCounters: merged.filterCounters,
			ShardCount:     shardCount,
			Matrices:       merged.groupedMats,
			Compute:        compute,
			Extensions:     exts,
		}
		if compute.Aggs {
			tail.SlotTotals = merged.aggFloor
		}
		if compute.Groups {
			tail.BucketFloors = merged.groupFloors
		}
		return processing.FinalizeGroupedStream(req, tail)
	}
	// Reached only by an EMPTY partial (no worker published one): it
	// carries no aggregator, grouper or matrix state, so matrices is nil
	// here and only the counters render.
	attachMergedFiltererComponents(resp, req, merged, compute.Filterers)
	attachMergedRunComponents(resp, merged, shardCount, compute.Run)
	processing.AttachMatrixComponents(resp, matrixComps)
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
// processing.CanMergeRequestWithExtensions has already refused any
// request whose operator state cannot fold, so no mergeability-class
// gate is needed here — every operator that clears that gate folds its
// components with its state. For an extension aggregator that is
// guaranteed at pulse.New: a Mergeable registration whose
// ComponentSchema classifies its keys "none" is refused with
// PULSE_EXTENSION_MERGEABLE_MISMATCH.
//
// No-op unless the plan computes the aggregation components (build),
// so the build cost is skipped rather than incurred and discarded.
func attachMergedAggregationComponents(resp *types.Response, req *types.Request, merged *shardPartial, build bool) error {
	if resp == nil || merged == nil || !build || merged.aggs == nil {
		return nil
	}
	for i, oa := range merged.aggs {
		var slot *types.Aggregation
		if i < len(req.Aggregations) {
			slot = req.Aggregations[i]
		}
		var fl processing.SlotFloor
		if i < len(merged.aggFloor) {
			fl = merged.aggFloor[i]
		}
		entry, err := processing.BuildAggregationComponents(oa, slot, fl.N(), fl.NNull())
		if err != nil {
			return err
		}
		fl.Stamp(&entry)
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
func attachMergedFiltererComponents(resp *types.Response, req *types.Request, merged *shardPartial, build bool) {
	if resp == nil || merged == nil || !build {
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
// No-op unless the plan computes the run components (build) — with the
// DisableComponents opt-out every sub-part is off, so resp.Components
// stays nil for byte-identical wire output against the pre-Components
// baseline.
func attachMergedRunComponents(resp *types.Response, merged *shardPartial, shardCount int, build bool) {
	if resp == nil || merged == nil || !build {
		return
	}
	processing.AttachRunComponents(resp, processing.RunCountersInput{
		TotalRecords:    merged.totalRows,
		FilteredRecords: merged.filteredRows,
		NullRecords:     merged.nullRecords,
		ShardCount:      shardCount,
	})
}
