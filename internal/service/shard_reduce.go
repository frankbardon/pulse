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
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/vectors"
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
	// A vector aggregator's factory reads its resolved members (the
	// rewrite Processor.prepare applies on every other arm).
	req = processing.ResolveAggregationInputs(req, schema, s.extensions)
	// So does a vector-input attribute (a fitted attribute bound to a
	// prebuilt fit is row-local, so it reaches this arm): its literal
	// members, default label and written-out labels.
	req = processing.ResolveAttributeInputs(req, schema, s.extensions)

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
	ei := execInfoFrom(ctx)
	if ei != nil {
		ei.setPlan(observe.ArmShardParallel, workers, len(shards), 0)
		ei.addBytes(int64(len(data)))
	}
	// Reading the archive into memory is its open.
	ei.Lap(observe.PhaseOpen)

	// A fitted attribute bound to a prebuilt fit closes at bind: build
	// the row-local attributes once here for their fit warnings and
	// capture offer, so N shard builds never repeat them.
	attrWarns, err := boundAttributeWarnings(req, schema, s.extensions, model.SetFrom(ctx))
	if err != nil {
		return nil, err
	}

	partials := make([]*shardPartial, len(shards))
	starts := shardStarts(shards)
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
				p, err := s.processOneShard(cctx, req, schema, arch, idx, starts[idx], shards[idx].Filename)
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
	ei.Lap(observe.PhaseScan)

	// Merge partials in shard insertion order. The order matters for
	// Welford-mean ULP determinism; for purely associative+commutative
	// aggregators the answer is identical regardless of merge order.
	merged, err := mergeShardPartials(req, schema, partials)
	if err != nil {
		return nil, err
	}
	resp, err := finalizeMergedPartial(req, schema, merged, len(shards), s.computePlanFor(ctx, req), s.extensions, model.SetFrom(ctx))
	if err != nil {
		return nil, err
	}
	resp.Warnings = prependWarnings(resp.Warnings, attrWarns)
	if err := merged.weights.Apply(resp, s.strict); err != nil {
		return nil, err
	}
	ei.Lap(observe.PhaseReduce)
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
	// groupSpecs and groupers are this partition's grouper slots
	// (Request.Groups, declared order) and its own instance per slot.
	// Each instance's live components state is folded across
	// partitions through processing.MergeableGrouper, slot by slot, so
	// every merged instance's Components() describes the whole cohort
	// exactly as the serial instance does.
	groupSpecs []*types.Group
	groupers   []processing.Grouper
	// limits is the run's effective resource limits (Service.Limits),
	// handed in by newShardPartial because the parallel reducers bypass
	// newProcessor. MaxGroups bounds groups (and groupedMats) here and
	// the merged key count in mergeShardPartials.
	limits limits.Limits
	// keyers is each partition grouper's resolved key dispatch.
	keyers []*processing.GroupKeyer
	// assignments[i] counts grouper i's (record, key) routings — one
	// per key the grouper gave a record, even when another position's
	// null key drops the record from every composite bucket — exactly
	// as the serial streaming-grouped path does. It is
	// Components.Groupers' TotalN for a grouper whose components carry
	// no "buckets" list (a bucket-less extension grouper), so the merge
	// sums it slot by slot and hands it to GroupedTail; a merged tail
	// without it reported total_n 0 and n_null = every filtered record.
	assignments []int64
	// keySet is the observed composite keys of a run with two or more
	// groupers, with their per-grouper tuples (nil for one grouper,
	// whose buckets are keyed by the plain key). Keys are minted only
	// through processing.CompositeKeySet.Expand — the serial arms'
	// keying, "\x00" refusal included — and merged by union.
	keySet *processing.CompositeKeySet
	// keySets is the per-record per-position key-set scratch of a
	// composite partition; cur and emit are the record being routed and
	// the Expand callback bound to it once (no per-record closure).
	keySets [][]string
	cur     *processing.Record
	emit    func(key string) error
	// specs and schema mint a new bucket's aggregators.
	specs  []aggSpec
	schema *encoding.Schema
	// keyOrder preserves the order distinct group keys were first seen
	// in this shard. The merger's stable sort by key ensures the
	// final response row order is deterministic across worker
	// scheduling perturbations.
	keyOrder []string
}

// processOneShard streams one shard's records through fresh per-shard
// OnlineAggregator instances (and per-group buckets when req.Groups is
// non-empty). Returns a partial state ready for merging. shardIdx is
// the shard's archive index and shardStart its first global record
// ordinal (shardStarts): with the in-shard record index they are each
// row's merge-block position, stamped exactly as the serial shardIter
// stamps it.
func (s *Service) processOneShard(ctx context.Context, req *types.Request, schema *encoding.Schema, arch *encx.Archive, shardIdx, shardStart int, shardName string) (*shardPartial, error) {
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
	// Layout-only, as shardIter.openShard: the canonical schema decodes
	// the rows, so the shard's dictionaries are read past, never
	// allocated.
	if _, err := encx.ReadSchemaLayout(r, pulseVersion); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard %q schema", shardName))
	}
	rr := encx.NewRecordReader(r, schema)

	// Pre-build per-shard operators. Each shard worker gets its own
	// instance set so there's no shared mutable state across workers.
	vecs := processing.ExprVectors(req, schema)
	filterFns, err := processing.BuildFiltersWithVectors(req.Filterers, schema, s.extensions, model.SetFrom(ctx), vecs)
	if err != nil {
		return nil, err
	}
	rowLocalAttrs, err := buildRowLocalAttrSpecs(req.Attributes, schema, s.extensions, partitionModels(ctx), vecs)
	if err != nil {
		return nil, err
	}

	specs, err := lookupAggSpecs(req, s.extensions)
	if err != nil {
		return nil, err
	}
	grouperFactories, err := lookupGrouperFactories(req, s.extensions)
	if err != nil {
		return nil, err
	}
	grouped := len(grouperFactories) > 0

	// Resolve the primary aggregation field for the per-shard null
	// counter (see primaryNullFieldFor).
	primaryNullField := primaryNullFieldFor(req)

	out, err := newPartitionPartial(req, schema, s.extensions, specs, grouperFactories, s.computePlanFor(ctx, req), model.SetFrom(ctx), s.Limits())
	if err != nil {
		return nil, err
	}
	aggsUngrouped := out.aggs

	// A grouped archive: the reader and the record share the CANONICAL
	// schema, whose dictionaries every shard's indices address, so each
	// record is handed its row's parent-group entries and a filter over
	// one group's members takes the per-entry precompute — the same hook
	// filter-to-file uses (readGroupIndices).
	var groupIdx []uint32
	if schema.HasGroups() {
		groupIdx = make([]uint32, len(schema.Groups))
	}

	// A FRESH positional Record per row, decoded by schema position —
	// the parallel-decode reducer's decode (parallel_decode.go). The
	// old map decode built three maps and a converted Record per row
	// (~58 allocations a row at p = 50, E8-S1); the values a consumer
	// reads are the same, so the partial's bits are too
	// (TestShardWorkers_* / TestMatrixCovariance_WorkerInvariant).
	binding := recordBindingFor(schema, nil, nil)
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		rec := binding.NewRecord()
		err := rr.ReadRecordReused(rec)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if groupIdx != nil && readGroupIndices(rr, groupIdx) {
			rec.SetGroupIndices(schema, groupIdx)
		}
		rec.SetMergePosition(shardIdx, shardStart, int(out.totalRows))
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
			processing.SetAttributeOutput(rec, ra.label, val, ra.nullable)
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

		if !grouped {
			for i, oa := range aggsUngrouped {
				if err := oa.UpdateRow(rec, specs[i].agg.Field); err != nil {
					return nil, err
				}
			}
			continue
		}
		if err := out.foldGroupedRow(rec); err != nil {
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

// newPartitionPartial builds one partition's complete, still-empty
// operator state: newShardPartial's tallies, the Request.Matrices state
// and either the ungrouped aggregator instances (aggs) or the grouped
// buckets with this partition's own grouper instances. Both parallel
// reducers build every partition through it, and finalizeMergedPartial
// seeds an empty merge (no partition published) through it, so a
// zero-record run finalises exactly the state a zero-record partition
// would have published. compute is the run's plan; models are the
// run's bound models.
func newPartitionPartial(req *types.Request, schema *encoding.Schema, exts *processing.ExtensionRegistry, specs []aggSpec, grouperFactories []processing.GrouperFactory, compute processing.ComputePlan, models *model.Set, l limits.Limits) (*shardPartial, error) {
	grouped := len(grouperFactories) > 0
	var groupers []processing.Grouper
	if grouped {
		var err error
		if groupers, err = newPartitionGroupers(req.Groups, grouperFactories, schema, exts); err != nil {
			return nil, err
		}
	}
	out := newShardPartial(req, specs, l)
	if err := out.buildMatrices(req, schema, exts, grouped, compute, models); err != nil {
		return nil, err
	}
	if grouped {
		if err := out.initGrouped(req.Groups, groupers, specs, schema, compute.Groups && len(req.Aggregations) > 0); err != nil {
			return nil, err
		}
		return out, nil
	}
	out.aggs = make([]processing.OnlineAggregator, len(specs))
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
		out.aggs[i] = online
	}
	return out, nil
}

// lookupAggSpecs resolves one factory per Request.Aggregations slot.
func lookupAggSpecs(req *types.Request, exts *processing.ExtensionRegistry) ([]aggSpec, error) {
	specs := make([]aggSpec, len(req.Aggregations))
	for i, agg := range req.Aggregations {
		factory, ok := exts.LookupAggregator(agg.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown aggregation type: %s", agg.Type))
		}
		specs[i] = aggSpec{agg: agg, factory: factory}
	}
	return specs, nil
}

// buildMatrices builds the partition's Request.Matrices state: one slot
// set for an ungrouped request (mats), per-bucket state for a grouped
// one (groupedMats). Shared by both parallel reducers. compute is the
// run's plan: a plan that accumulates no matrix (the `matrices` slot
// and components.matrices both excluded) builds none, so no partition
// folds a record into one. models are the run's bound models (a
// params.fit slot binds its vector fit there).
func (sp *shardPartial) buildMatrices(req *types.Request, schema *encoding.Schema, exts *processing.ExtensionRegistry, grouped bool, compute processing.ComputePlan, models *model.Set) error {
	var err error
	if grouped {
		sp.groupedMats, err = processing.BuildGroupedMatrices(req, schema, exts, compute, sp.limits)
		return err
	}
	sp.mats, err = processing.BuildMatrixSlots(req, schema, exts, compute, models)
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

// lookupGrouperFactories resolves one factory per Request.Groups slot,
// once per run (both parallel reducers call it before fanning out).
func lookupGrouperFactories(req *types.Request, exts *processing.ExtensionRegistry) ([]processing.GrouperFactory, error) {
	out := make([]processing.GrouperFactory, len(req.Groups))
	for i, grp := range req.Groups {
		f, ok := exts.LookupGrouper(grp.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown group type: %s", grp.Type))
		}
		out[i] = f
	}
	return out, nil
}

// newPartitionGroupers builds one fresh grouper instance per slot for
// one partition (shard or decode worker), extension hooks applied —
// every grouper of the request, not only Groups[0]'s.
func newPartitionGroupers(groups []*types.Group, factories []processing.GrouperFactory, schema *encoding.Schema, exts *processing.ExtensionRegistry) ([]processing.Grouper, error) {
	out := make([]processing.Grouper, len(groups))
	for i, grp := range groups {
		g, err := factories[i](grp, schema)
		if err != nil {
			return nil, err
		}
		processing.ApplyGrouperExtensions(g, exts)
		out[i] = g
	}
	return out, nil
}

// initGrouped makes sp a grouped partition over groupers (one per
// groups slot): empty buckets, per-bucket floors when withFloors, each
// grouper's key dispatch, per-grouper assignment counts and — for two
// or more groupers — the composite key set. Shared by both parallel
// reducers.
func (sp *shardPartial) initGrouped(groups []*types.Group, groupers []processing.Grouper, specs []aggSpec, schema *encoding.Schema, withFloors bool) error {
	sp.groups = make(map[string][]processing.OnlineAggregator)
	if withFloors {
		sp.groupFloors = processing.NewGroupFloors()
	}
	sp.groupSpecs, sp.groupers = groups, groupers
	sp.specs, sp.schema = specs, schema
	sp.keyers = make([]*processing.GroupKeyer, len(groupers))
	for i, g := range groupers {
		k, err := processing.NewGroupKeyer(g)
		if err != nil {
			return err
		}
		sp.keyers[i] = k
	}
	sp.assignments = make([]int64, len(groupers))
	if len(groupers) > 1 {
		sp.keySet = processing.NewCompositeKeySet(len(groupers))
		sp.keySets = make([][]string, len(groupers))
		sp.emit = func(key string) error { return sp.route(key, sp.cur) }
	}
	return nil
}

// foldGroupedRow fans one filter-passing record into every bucket this
// partition's groupers key it to — one for a single-key grouper, one per
// selected label for GROUP_SET_PER_ELEMENT — creating a bucket of fresh
// aggregators on a key's first sighting. Two or more groupers key the
// record by its composite keys exactly as processStreamingGrouped does:
// every grouper is driven once for the record (ONCE-PER-RECORD — its
// live state is its own Components.Groupers entry), even after an
// earlier position's null key, and CompositeKeySet.Expand emits the
// per-record cartesian product, nothing when any position is empty.
// Shared by both parallel reducers; the key dispatch is
// processing.GroupKeyer, the same one the serial streaming-grouped path
// uses.
func (sp *shardPartial) foldGroupedRow(rec *processing.Record) error {
	if sp.keySet == nil {
		keys, ok, err := sp.keyers[0].Keys(rec, sp.groupSpecs[0].Field)
		if err != nil || !ok {
			return err
		}
		sp.assignments[0] += int64(len(keys))
		for _, key := range keys {
			if err := sp.route(key, rec); err != nil {
				return err
			}
		}
		return nil
	}
	for i, k := range sp.keyers {
		ks, ok, err := k.Keys(rec, sp.groupSpecs[i].Field)
		if err != nil {
			return err
		}
		if !ok {
			sp.keySets[i] = nil
			continue
		}
		sp.keySets[i] = ks
		sp.assignments[i] += int64(len(ks))
	}
	sp.cur = rec
	return sp.keySet.Expand(sp.keySets, sp.emit)
}

// route folds rec into key's bucket, minting the bucket (its
// aggregators, floors and matrix slots) on the key's first sighting.
func (sp *shardPartial) route(key string, rec *processing.Record) error {
	bucket, exists := sp.groups[key]
	if !exists {
		// MaxGroups on this partition's (composite) key count: a
		// partition never holds more keys than the whole run, so a
		// local breach is a global one. Checked once per new key,
		// never per row.
		if err := limits.Check(sp.limits, limits.MaxGroups, int64(len(sp.groups))+1); err != nil {
			return err
		}
		bucket = make([]processing.OnlineAggregator, len(sp.specs))
		for i, spec := range sp.specs {
			inst, err := spec.factory(spec.agg, sp.schema)
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
			sp.groupFloors[key] = processing.NewSlotFloors(sp.slotAggs(sp.specs))
		}
	}
	for i, oa := range bucket {
		if err := oa.UpdateRow(rec, sp.specs[i].agg.Field); err != nil {
			return err
		}
	}
	processing.ObserveSlotFloors(sp.groupFloors[key], rec)
	return sp.groupedMats.UpdateRow(key, rec)
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
	// The serial arms' rule (processing.primaryNullFieldName): the first
	// aggregation that names a Field — a vector aggregator names none —
	// else the first grouper that names one.
	for _, agg := range req.Aggregations {
		if agg != nil && agg.Field != "" {
			return agg.Field
		}
	}
	for _, grp := range req.Groups {
		if grp != nil && grp.Field != "" {
			return grp.Field
		}
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
	// nullable: the computer writes NaN as a null (processing.EmitsNull).
	nullable bool
}

// buildRowLocalAttrSpecs builds one worker's row-local attributes, bound
// (processing.BindAttributeWithVectors) to the registry, the request's
// bound models and its resolved named vectors (processing.ExprVectors),
// labelled by the processor's own default-label rule.
func buildRowLocalAttrSpecs(attrs []*types.Attribute, schema *encoding.Schema, exts *processing.ExtensionRegistry, models *model.Set, vecs []vectors.Resolved) ([]rowLocalAttrSpec, error) {
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
		if err := processing.BindAttributeWithVectors(computer, exts, models, vecs); err != nil {
			return nil, err
		}
		rl, ok := computer.(processing.RowLocalAttribute)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("attribute %s is not row-local; not eligible for per-shard parallel reduce", attr.Type))
		}
		label := attr.Label
		if label == "" {
			label = processing.DefaultAttributeLabel(attr)
		}
		out = append(out, rowLocalAttrSpec{attr: attr, computer: rl, label: label, nullable: processing.EmitsNull(computer)})
	}
	return out, nil
}

// boundAttributeWarnings is a reducer's ONE orchestrator-level build of
// req's row-local attributes against the run's bound models: a fitted
// attribute bound to a prebuilt fit (vectors.FitBound — admitted by
// mergegate) closes at bind, raising its fit warnings and, on a
// Compose vector-fit request, its capture offer. The serial arms raise
// both once; the reducers build per partition, so the partitions bind
// with the capture stripped (partitionModels) and the run's warnings
// come from this build, prepended in declared order exactly where the
// serial arms put them (prependRowLocalWarnings). Nil without
// attributes.
func boundAttributeWarnings(req *types.Request, schema *encoding.Schema, exts *processing.ExtensionRegistry, models *model.Set) ([]*types.ResponseWarning, error) {
	if len(req.Attributes) == 0 {
		return nil, nil
	}
	specs, err := buildRowLocalAttrSpecs(req.Attributes, schema, exts, models, processing.ExprVectors(req, schema))
	if err != nil {
		return nil, err
	}
	var out []*types.ResponseWarning
	for _, sp := range specs {
		out = append(out, processing.AttributeWarnings(sp.computer)...)
	}
	return out, nil
}

// partitionModels is the run's bound models for one reducer partition's
// attribute build: the capture stripped, since boundAttributeWarnings'
// single build already offered (matrix slots keep the run's set — they
// offer once, from the merged state).
func partitionModels(ctx context.Context) *model.Set {
	set := model.SetFrom(ctx)
	if set.Capture() == nil {
		return set
	}
	return set.WithCapture(nil)
}

// prependWarnings returns warns with first in front (warns itself when
// first is empty).
func prependWarnings(warns, first []*types.ResponseWarning) []*types.ResponseWarning {
	if len(first) == 0 {
		return warns
	}
	return append(append([]*types.ResponseWarning(nil), first...), warns...)
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
			// Per grouper slot: its live components state and its
			// assignment count, folded in partition order.
			if len(p.groupers) != len(merged.groupers) || len(p.assignments) != len(merged.assignments) {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
					"shard partial grouper count mismatch during merge",
					map[string]any{"merged": len(merged.groupers), "partial": len(p.groupers)})
			}
			for gi, g := range merged.groupers {
				m, ok := g.(processing.MergeableGrouper)
				if !ok {
					return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
						fmt.Sprintf("grouper %s does not implement MergeableGrouper", req.Groups[gi].Type))
				}
				if err := m.MergeGrouperState(p.groupers[gi]); err != nil {
					return nil, err
				}
				merged.assignments[gi] += p.assignments[gi]
			}
			// The composite keys' tuples: a union (a key's tuple is a
			// function of the key, so which partition minted it is
			// immaterial).
			merged.keySet.Merge(p.keySet)
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
func finalizeMergedPartial(req *types.Request, schema *encoding.Schema, merged *shardPartial, shardCount int, compute processing.ComputePlan, exts *processing.ExtensionRegistry, models *model.Set) (*types.Response, error) {
	// An EMPTY merge — no partition published a partial (every decode
	// segment held zero records) — carries no aggregator, grouper or
	// matrix state. Seed it, in place so the caller's weights.Apply reads
	// the seeded tally, with exactly the state a zero-record partition
	// builds: the ungrouped arm then writes the zero-n row and one
	// zero-n Components.Aggregations entry per slot, and the grouped arm
	// runs the shared grouped tail over zero buckets (its groupers
	// block included) — the serial path's zero-record answer. Emitting
	// only the counters here dropped both.
	if merged.aggs == nil && merged.groups == nil {
		specs, err := lookupAggSpecs(req, exts)
		if err != nil {
			return nil, err
		}
		grouperFactories, err := lookupGrouperFactories(req, exts)
		if err != nil {
			return nil, err
		}
		seed, err := newPartitionPartial(req, schema, exts, specs, grouperFactories, compute, models, merged.limits)
		if err != nil {
			return nil, err
		}
		*merged = *seed
	}
	// The ungrouped arm runs neither Process overlay hook: the shared
	// wrong-slot / wrong-shape / SCALAR host refusal the serial hook
	// raises (the grouped arm's tail raises it itself).
	if merged.groups == nil {
		if err := processing.RefuseWrongHostOverlays(req, exts, compute); err != nil {
			return nil, err
		}
	}
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
				label = types.AggregationLabelOf(req.Aggregations[i])
			}
			// Lift through the Rich-or-scalar dispatch the serial
			// Processor uses. Finalize() alone returns a
			// RichAggregator's scalar FALLBACK, so writing it straight
			// into the row made AGG_SET_UNION emit a popcount and
			// AGG_SET_FREQUENCY a max-bin count under either parallel
			// knob while the serial path emitted labels and a
			// label→count map — the same request answering in two
			// different shapes depending on worker count.
			// (WriteAggregationResult also names an expanding vector
			// aggregation's per-element columns.)
			if err = processing.WriteAggregationResult(row, label, oa, val); err != nil {
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
		// Every grouper slot rides the tail: two or more key the buckets
		// by composite key, whose merged tuples (KeySet) write every key
		// column and order the rows exactly as the serial arms do.
		tail := processing.GroupedTail{
			Groups:         merged.groupSpecs,
			Groupers:       merged.groupers,
			KeySet:         merged.keySet,
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
	return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
		"finalizeMergedPartial: merged partial carries neither aggregator nor grouped state")
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
