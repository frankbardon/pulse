package service

import (
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
// preservation matters for two reasons: order-free aggregators (count,
// min, max, null_count, frequency, distinct_count, mode, and the binned
// reproducible sums — AGG_SUM, AGG_AVERAGE, AGG_RATIO, the weighted
// sums, the sum_weights floor) produce byte-equal results vs the serial
// path when partials are folded in any order, but the Welford parallel
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
// Caller guarantees shouldFanOut returned true. The archive is opened
// once for random access (central directory only, no payload read),
// then each worker carves a buffered SectionReader for its assigned
// shards over the shared concurrency-safe ReaderAt and folds rows through fresh per-worker operator
// instances. Partials merge incrementally in shard insertion order as
// each prefix of shards completes, and are released once merged
// (scanAndMergeShards), so Welford drift is deterministic across runs
// and worker counts and the live partials stay within the dispatch
// window.
func (s *Service) processShardArchiveParallel(ctx context.Context, req *types.Request, cohort *Cohort, path string, workers int) (*types.Response, error) {
	// The projected decode, built once from the request as the caller
	// handed it — the serial arm's applyProjection reads the same — and
	// shared read-only by every worker: one plan, one record binding.
	dec := s.shardDecodeFor(ctx, req, cohort.Schema())
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

	// Open the archive once. Every worker reads its shards through its
	// own SectionReader over one concurrency-safe ReaderAt (a pread on
	// an OS file, a mutex on the in-memory one), so no shard is read
	// before a worker scans it and none stays resident after. The
	// cohort's fs counts the bytes read when the request carries
	// ExecInfo (countReads).
	fsys := cohort.fs
	if fsys == nil {
		fsys = s.fs.Fs()
	}
	arch, err := openArchiveFile(fsys, path)
	if err != nil {
		return nil, err
	}
	defer arch.Close()
	ei := execInfoFrom(ctx)
	if ei != nil {
		ei.setPlan(observe.ArmShardParallel, workers, len(shards), dec.projected)
	}
	// Parsing the central directory is the archive's open.
	ei.Lap(observe.PhaseOpen)

	// A fitted attribute bound to a prebuilt fit closes at bind: build
	// the row-local attributes once here for their fit warnings and
	// capture offer, so N shard builds never repeat them. The same
	// build is where the shards' per-run attribute tallies fold
	// (finalizeMergedPartial).
	compute := s.computePlanFor(ctx, req)
	bound, err := boundAttributes(req, schema, s.extensions, model.SetFrom(ctx), compute)
	if err != nil {
		return nil, err
	}

	// A total() formula (admitted by mergegate.ParallelTotals) runs two
	// phases: a parallel pre-pass publishes the cohort totals, then the
	// ordinary per-shard reduce binds them into every shard's formula.
	if processing.HasFormulaTotals(req.Attributes) {
		totals, err := s.shardTotalsPrepass(ctx, req, schema, arch, shards, workers)
		if err != nil {
			return nil, err
		}
		withTotals := *dec
		withTotals.totals = totals
		dec = &withTotals
	}

	merged, err := s.scanAndMergeShards(ctx, req, schema, dec, arch, shards, workers)
	if err != nil {
		return nil, err
	}
	ei.Lap(observe.PhaseScan)

	resp, err := finalizeMergedPartial(req, schema, merged, len(shards), compute, s.extensions, model.SetFrom(ctx), bound)
	if err != nil {
		return nil, err
	}
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

// shardMergeHook, when set (tests only — never in production), is
// called on the merging goroutine with each shard's index and partial
// just before the partial folds into the merged state.
var shardMergeHook func(idx int, p *shardPartial)

// shardResult is one scanned shard handed back to the merging
// goroutine: its archive index and partial, or the scan's error.
type shardResult struct {
	idx int
	p   *shardPartial
	err error
}

// scanAndMergeShards scans every shard on workers goroutines and folds
// the partials into one through a partialMerger, INCREMENTALLY and in
// shard insertion order: partial i merges as soon as partials 0..i are
// done, and is then dropped, so it can be collected while later shards
// still scan (U51 E2-S4, #358). The merge order is the one the
// one-shot merge used — the answer is byte-identical — and row order
// never came from it (the shared grouped tail orders the rows).
//
// The live partials are bounded by the dispatch window
// (limits.ShardMergeWindow(workers)): shard j is handed to a worker
// only once j < next + window, where next is the first shard not yet
// merged, so at most window partials (scanning, or done and waiting
// for an earlier shard) exist beside the merged state — whatever the
// shard count. Predict charges the same window (descriptor
// EstimateMemory's per-shard partition term).
//
// The first error (a shard's scan, a recovered worker panic, a merged
// MaxGroups trip) cancels the siblings and is returned once every
// dispatched shard has reported.
func (s *Service) scanAndMergeShards(ctx context.Context, req *types.Request, schema *encoding.Schema, dec *shardDecode, arch *archiveFile, shards []ShardEntry, workers int) (*shardPartial, error) {
	starts := shardStarts(shards)
	window := limits.ShardMergeWindow(workers)
	// Neither channel ever blocks its sender: at most window shards
	// are dispatched and not yet reported.
	jobs := make(chan int, window)
	results := make(chan shardResult, window)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		// joinOnReturn, never a deferred wg.Done: a worker re-panicking
		// under DisablePanicRecovery must not release wg.Wait before the
		// crash.
		go joinOnReturn(&wg, func() {
			s.shardWorker(cctx, req, schema, dec, arch, jobs, starts, shards, results)
		})
	}

	merger := partialMerger{req: req}
	pending := make(map[int]*shardPartial, window)
	var (
		firstErr                    error
		next, dispatched, reporting int
	)
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	for {
		for firstErr == nil && dispatched < len(shards) && dispatched < next+window {
			jobs <- dispatched
			dispatched++
			reporting++
		}
		if reporting == 0 {
			break
		}
		r := <-results
		reporting--
		if r.err != nil {
			fail(r.err)
		}
		if firstErr != nil {
			continue
		}
		pending[r.idx] = r.p
		for p, ok := pending[next]; ok; p, ok = pending[next] {
			delete(pending, next)
			if shardMergeHook != nil {
				shardMergeHook(next, p)
			}
			next++
			if err := merger.add(p); err != nil {
				fail(err)
				break
			}
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return merger.result(), nil
}

// shardWorker is one parallel-shard worker: it scans the shards it
// takes from jobs until jobs closes, reporting exactly one result per
// shard (scanShard) so the merging goroutine can account for every
// dispatched shard.
func (s *Service) shardWorker(ctx context.Context, req *types.Request, schema *encoding.Schema, dec *shardDecode, arch *archiveFile, jobs <-chan int, starts []int, shards []ShardEntry, results chan<- shardResult) {
	for idx := range jobs {
		p, err := s.scanShard(ctx, req, schema, dec, arch, idx, starts[idx], shards[idx].Filename)
		results <- shardResult{idx: idx, p: p, err: err}
	}
}

// scanShard scans one shard into its partial. Operator code runs per
// row on the worker's goroutine, so it recovers a panic into
// PROCESSING_PANIC (recoverInto), reported like any shard error — the
// merging goroutine records the first and cancels the siblings. A
// cancelled run reports ctx's error unchanged.
func (s *Service) scanShard(ctx context.Context, req *types.Request, schema *encoding.Schema, dec *shardDecode, arch *archiveFile, idx, start int, name string) (p *shardPartial, err error) {
	defer recoverInto(panicOpShardParallel, s.disablePanicRecovery, &err)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.processOneShard(ctx, req, schema, dec, arch, idx, start, name)
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
//     observed in this shard. Row order is never the partition's: the
//     shared grouped tail (processing.FinalizeGroupedStream) orders the
//     merged buckets, so a partition keeps no key-order list (U51
//     E2-S4 dropped it — 16-32 B a group).
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

	// groupFloorTmpl shapes a grouped partial's per-bucket floors
	// (processing.GroupBucket.Floors, minted with each bucket in route
	// and merged key by key like the buckets — the floors behind
	// Components.Aggregations' groups[]): the cohort-wide aggFloor when
	// the plan computes Groups, nil (no bucket floors) otherwise.
	groupFloorTmpl []processing.SlotFloor

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

	// attrs is this partition's row-local attribute set, one entry per
	// req.Attributes slot (nil without attributes): each slot's
	// instance — whose per-run tally (processing.MergeableAttributeTally:
	// ATTR_SCALE_SCORE, a reference-form ATTR_PROFILE_SIMILARITY, a
	// Mergeable extension) scored only this partition's rows — and its
	// Components.Attributes floor. The merge folds them slot by slot
	// (mergePartitionAttributes) and finalizeMergedPartial folds the
	// result into the run's one bound build, which emits the entries and
	// warnings once, cohort-wide (U51 E3-S2, #456).
	attrs []rowLocalAttrSpec

	aggs   []processing.OnlineAggregator
	groups map[string]*processing.GroupBucket
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
	// specs and schema mint a new bucket's aggregators, through mint
	// (built once by initGrouped, so a bucket mint allocates nothing
	// beyond the bucket itself).
	specs  []aggSpec
	schema *encoding.Schema
	mint   func(i int) (processing.OnlineAggregator, error)
}

// shardDecode is the per-shard parallel reducer's decode shape, built
// once per run (shardDecodeFor) and read by every worker: the
// projection's keep filter and DecodePlan (both nil: full decode), the
// retained-field count stamped as the plan's projected_fields, and the
// binding each row's fresh Record is minted from — projected to keep
// when the plan drives the decode, exactly as the parallel-decode
// reducer binds (parallel_decode.go) and the serial shardIter binds.
type shardDecode struct {
	keep      encx.FieldFilter
	plan      *encx.DecodePlan
	projected int
	binding   *processing.RecordBinding
	// totals is the run's published total() values (the two-phase
	// pre-pass, shardTotalsPrepass), bound into every shard's formula
	// instances before its first row; nil without a total() formula.
	totals *processing.PublishedTotals
}

// shardDecodeFor builds the shard reducer's decode shape for req over
// the canonical schema (decodeProjection — the serial scan's plan).
func (s *Service) shardDecodeFor(ctx context.Context, req *types.Request, schema *encoding.Schema) *shardDecode {
	keep, plan, projected := s.decodeProjection(ctx, req, schema)
	return &shardDecode{keep: keep, plan: plan, projected: projected, binding: recordBindingFor(schema, plan, keep)}
}

// shardRows reads one shard's rows in the run's decode shape.
type shardRows struct {
	rr     *encx.RecordReader
	dec    *shardDecode
	schema *encoding.Schema
	// exact carries the u64 words the projection-without-plan map decode
	// would lose to the float64 echo (nil otherwise).
	exact *encx.ExactU64Words
}

// openRows positions a RecordReader over the canonical schema at the
// first record of the archive's shard name. A deflated entry is refused
// (storedShard), never decoded as raw compressed bytes — the serial
// shardIter's rule — and the shard's schema block is read layout-only,
// as shardIter.openShard reads it: the canonical schema decodes the
// rows, so the shard's dictionaries are read past, never allocated.
func (d *shardDecode) openRows(arch *archiveFile, name string, schema *encoding.Schema) (*shardRows, error) {
	r, err := arch.shardReader(name)
	if err != nil {
		return nil, err
	}
	pulseVersion, err := encoding.ReadHeader(r)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard %q header", name))
	}
	if _, err := encx.ReadSchemaLayout(r, pulseVersion); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard %q schema", name))
	}
	rows := &shardRows{rr: encx.NewRecordReader(r, schema), dec: d, schema: schema}
	// The projection-without-plan map decode loses a u64 word above 2^53
	// to the float64 echo: capture it and hand it to the record, as
	// shardIter.openShard does (nil, and free, when the schema has no
	// u64 field; the reuse decoders write the word themselves).
	if d.plan == nil && d.keep != nil {
		rows.exact = encx.NewExactU64Words(schema)
		rows.rr.CaptureExactU64(rows.exact)
	}
	return rows, nil
}

// next decodes the shard's next row into a FRESH positional Record,
// written by schema position — the parallel-decode reducer's decode
// (parallel_decode.go). The old map decode built three maps and a
// converted Record per row (~58 allocations a row at p = 50, E8-S1);
// the values a consumer reads are the same, so the partial's bits are
// too (TestShardWorkers_* / TestMatrixCovariance_WorkerInvariant).
// Under a projection the plan seeks past the unretained fields and the
// record holds only the retained ones (U51 E2-S2); the projection-
// without-plan case (defensive) keeps the map decode, converted on
// construction. io.EOF ends the shard.
func (sr *shardRows) next() (*processing.Record, error) {
	d := sr.dec
	switch {
	case d.plan != nil:
		rec := d.binding.NewRecord()
		return rec, sr.rr.ReadRecordReusedWithPlan(rec, d.keep, d.plan)
	case d.keep != nil:
		values := make(map[string]float64, d.projected)
		nulls := make(map[string]bool)
		wide := make(map[string]any)
		if err := sr.rr.ReadRecordWithWideProjected(values, nulls, wide, d.keep); err != nil {
			return nil, err
		}
		rec := processing.NewRecordWithWide(sr.schema, values, nulls, wide)
		sr.exact.ApplyTo(rec)
		return rec, nil
	default:
		rec := d.binding.NewRecord()
		return rec, sr.rr.ReadRecordReused(rec)
	}
}

// processOneShard streams one shard's records through fresh per-shard
// OnlineAggregator instances (and per-group buckets when req.Groups is
// non-empty). Returns a partial state ready for merging. shardIdx is
// the shard's archive index and shardStart its first global record
// ordinal (shardStarts): with the in-shard record index they are each
// row's merge-block position, stamped exactly as the serial shardIter
// stamps it.
func (s *Service) processOneShard(ctx context.Context, req *types.Request, schema *encoding.Schema, dec *shardDecode, arch *archiveFile, shardIdx, shardStart int, shardName string) (*shardPartial, error) {
	rows, err := dec.openRows(arch, shardName, schema)
	if err != nil {
		return nil, err
	}
	rr := rows.rr

	// Pre-build per-shard operators. Each shard worker gets its own
	// instance set so there's no shared mutable state across workers.
	vecs := processing.ExprVectors(req, schema)
	filterFns, err := processing.BuildFiltersWithVectors(req.Filterers, schema, s.extensions, model.SetFrom(ctx), vecs)
	if err != nil {
		return nil, err
	}
	compute := s.computePlanFor(ctx, req)
	rowLocalAttrs, err := buildRowLocalAttrSpecs(req.Attributes, schema, s.extensions, partitionModels(ctx), vecs, dec.totals, compute)
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

	out, err := newPartitionPartial(req, schema, s.extensions, specs, grouperFactories, compute, model.SetFrom(ctx), s.Limits())
	if err != nil {
		return nil, err
	}
	out.attrs = rowLocalAttrs
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

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		rec, err := rows.next()
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
			ra.floor.Observe(val, ra.nullable)
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
	sp.groups = make(map[string]*processing.GroupBucket)
	if withFloors && processing.GroupFloorsOn() {
		sp.groupFloorTmpl = sp.aggFloor
	}
	sp.groupSpecs, sp.groupers = groups, groupers
	sp.specs, sp.schema = specs, schema
	sp.mint = func(i int) (processing.OnlineAggregator, error) {
		spec := sp.specs[i]
		inst, err := spec.factory(spec.agg, sp.schema)
		if err != nil {
			return nil, err
		}
		online, ok := inst.(processing.OnlineAggregator)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("aggregator %s does not implement OnlineAggregator", spec.agg.Type))
		}
		return online, nil
	}
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
		var err error
		if bucket, err = processing.NewGroupBucket(len(sp.specs), sp.mint, sp.groupFloorTmpl); err != nil {
			return err
		}
		sp.groups[key] = bucket
	}
	for i, oa := range bucket.Aggs {
		if err := oa.UpdateRow(rec, sp.specs[i].agg.Field); err != nil {
			return err
		}
	}
	processing.ObserveSlotFloors(bucket.Floors, rec)
	return sp.groupedMats.UpdateRow(key, rec)
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
	// floor tallies the slot's Components.Attributes floor {n, n_null};
	// nil when the slot emits no entry (processing.AttributeFloorFor).
	floor *processing.AttributeFloor
}

// buildRowLocalAttrSpecs builds one worker's row-local attributes, bound
// (processing.BindAttributeWithVectors) to the registry, the request's
// bound models and its resolved named vectors (processing.ExprVectors),
// labelled by the processor's own default-label rule. A total-bearing
// formula (admitted by mergegate.ParallelTotals) is row-local once its
// totals are published: totals.Bind installs the run's two-phase
// pre-pass values on it (nil totals binds none — a build that never
// evaluates a row, boundAttributes). compute is the run's plan: a slot
// emitting a Components.Attributes entry gets a floor under it.
func buildRowLocalAttrSpecs(attrs []*types.Attribute, schema *encoding.Schema, exts *processing.ExtensionRegistry, models *model.Set, vecs []vectors.Resolved, totals *processing.PublishedTotals, compute processing.ComputePlan) ([]rowLocalAttrSpec, error) {
	if len(attrs) == 0 {
		return nil, nil
	}
	out := make([]rowLocalAttrSpec, 0, len(attrs))
	for i, attr := range attrs {
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
		if err := totals.Bind(i, computer); err != nil {
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
		out = append(out, rowLocalAttrSpec{attr: attr, computer: rl, label: label, nullable: processing.EmitsNull(computer),
			floor: processing.AttributeFloorFor(computer, compute)})
	}
	return out, nil
}

// boundAttributes is a reducer's ONE orchestrator-level build of
// req's row-local attributes against the run's bound models: a fitted
// attribute bound to a prebuilt fit (vectors.FitBound — admitted by
// mergegate) closes at bind, raising its fit warnings and, on a
// Compose vector-fit request, its capture offer. The serial arms raise
// both once; the reducers build per partition, so the partitions bind
// with the capture stripped (partitionModels) and the run's warnings
// come from this build. It scores no row: finalizeMergedPartial folds
// the merged partitions' per-run tallies and floors into it
// (absorbPartitionAttributes), then emits its Components.Attributes
// entries and its warnings once, cohort-wide, in declared order exactly
// where the serial arms put them (prependRowLocalWarnings). A zero-row
// run (no partition published) thus reports the zero tallies the serial
// arms do. Nil without attributes.
func boundAttributes(req *types.Request, schema *encoding.Schema, exts *processing.ExtensionRegistry, models *model.Set, compute processing.ComputePlan) ([]rowLocalAttrSpec, error) {
	if len(req.Attributes) == 0 {
		return nil, nil
	}
	return buildRowLocalAttrSpecs(req.Attributes, schema, exts, models, processing.ExprVectors(req, schema), nil, compute)
}

// mergePartitionAttributes folds src's attribute slots into dst's, slot
// by slot: the floors add and each instance's per-run tally folds
// (processing.MergeAttributeTally). Integer tallies, so the fold is
// exact in any order. An empty src (no attributes, or a partition that
// built none) is a no-op.
func mergePartitionAttributes(dst, src []rowLocalAttrSpec) error {
	if len(src) == 0 {
		return nil
	}
	if len(dst) != len(src) {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"partition attribute count mismatch during merge",
			map[string]any{"merged": len(dst), "partial": len(src)})
	}
	for i := range dst {
		dst[i].floor.Merge(src[i].floor)
		if err := processing.MergeAttributeTally(dst[i].computer, src[i].computer); err != nil {
			return err
		}
	}
	return nil
}

// emitBoundAttributes writes the run's attribute figures from bound
// (the merged partitions already folded in): one Components.Attributes
// entry per slot holding a floor, in declared order, and the slots'
// Response warnings — fit warnings, the folded tallies' warnings —
// prepended in declared order. Each slot emits once.
func emitBoundAttributes(resp *types.Response, bound []rowLocalAttrSpec) error {
	var (
		entries []types.AttributeComponents
		warns   []*types.ResponseWarning
	)
	for _, sp := range bound {
		entry, ok, err := processing.AttributeComponentsEntry(sp.attr, sp.label, sp.computer, sp.floor)
		if err != nil {
			return err
		}
		if ok {
			entries = append(entries, entry)
		}
		warns = append(warns, processing.AttributeWarnings(sp.computer)...)
	}
	processing.AttachAttributeComponents(resp, entries)
	resp.Warnings = prependWarnings(resp.Warnings, warns)
	return nil
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

// mergeShardPartials folds the partition states into a single
// aggregated state, in slice order (partialMerger.add per entry, nil
// entries skipped). The parallel-decode reducer hands it its per-worker
// partials (segment order); the per-shard reducer merges incrementally
// through the same partialMerger instead, in shard insertion order as
// each prefix completes (processShardArchiveParallel).
func mergeShardPartials(req *types.Request, schema *encoding.Schema, partials []*shardPartial) (*shardPartial, error) {
	_ = schema
	m := partialMerger{req: req}
	for _, p := range partials {
		if err := m.add(p); err != nil {
			return nil, err
		}
	}
	return m.result(), nil
}

// partialMerger folds partition states into one, one partition at a
// time, in the order add is called — the caller's index order (shard
// insertion order, the central-directory enumeration, never worker
// completion order). The first non-nil partition becomes the merged
// state; every later one folds into it and is no longer referenced, so
// a caller that drops its own reference releases it (U51 E2-S4, #358:
// the per-shard reducer used to hold every shard's partial until one
// final merge).
//
// A processing.BlockMerger slot (ungrouped or per bucket) absorbs the
// other partition's per-block partials instead of MergeOnline: blocks
// are keyed by absolute (shard, block) position and combined only at
// Finalize through the one fixed tree, so for those slots the partition
// order here cannot reach the bits. Every other slot folds through
// MergeableAggregator.MergeOnline in add order — which is why the order
// is fixed: Welford's Chan merge is order-sensitive in the last ULP,
// while counts, sentinels and the binned sums are not.
//
// For ungrouped requests each aggregator slot's running state merges.
// For grouped requests per-key buckets union: a key the merged state
// lacks moves in whole (bucket, floors), a key both hold merges slot by
// slot; row order comes from the shared grouped tail, never from here.
type partialMerger struct {
	req    *types.Request
	merged *shardPartial
}

// result is the merged state: an empty partial when no partition was
// added (finalizeMergedPartial seeds it).
func (m *partialMerger) result() *shardPartial {
	if m.merged == nil {
		return &shardPartial{}
	}
	return m.merged
}

// add folds p into the merged state (p == nil is a no-op).
func (m *partialMerger) add(p *shardPartial) error {
	if p == nil {
		return nil
	}
	if m.merged == nil {
		m.merged = p
		return nil
	}
	merged, req := m.merged, m.req
	merged.totalRows += p.totalRows
	merged.filteredRows += p.filteredRows
	merged.nullRecords += p.nullRecords

	// Universal-floor and filter counters are plain tallies: the
	// fold is a slot-wise sum, associative and commutative, so no
	// ordering guarantee is needed here (unlike the Welford merge
	// below, which is why add is called in index order regardless).
	processing.MergeSlotFloors(merged.aggFloor, p.aggFloor)
	merged.weights.Merge(p.weights)
	processing.MergeFilterPassCounters(merged.filterCounters, p.filterCounters)
	if err := mergePartitionAttributes(merged.attrs, p.attrs); err != nil {
		return err
	}
	if err := merged.mats.Merge(p.mats); err != nil {
		return err
	}
	if err := merged.groupedMats.Merge(p.groupedMats); err != nil {
		return err
	}

	if merged.aggs != nil {
		if len(p.aggs) != len(merged.aggs) {
			return errors.NewCodedError(errors.PROCESSING_INTERNAL,
				"shard partial aggregator count mismatch during merge")
		}
		for slot, oa := range merged.aggs {
			if bm, ok := oa.(processing.BlockMerger); ok {
				if err := processing.MergeBlockMerger(bm, p.aggs[slot]); err != nil {
					return err
				}
				continue
			}
			m, ok := oa.(processing.MergeableAggregator)
			if !ok {
				return errors.NewCodedError(errors.PROCESSING_INTERNAL,
					fmt.Sprintf("aggregator %s does not implement MergeableAggregator", req.Aggregations[slot].Type))
			}
			if err := m.MergeOnline(p.aggs[slot]); err != nil {
				return err
			}
		}
	}

	if merged.groups != nil {
		// Per grouper slot: its live components state and its
		// assignment count, folded in partition order.
		if len(p.groupers) != len(merged.groupers) || len(p.assignments) != len(merged.assignments) {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"shard partial grouper count mismatch during merge",
				map[string]any{"merged": len(merged.groupers), "partial": len(p.groupers)})
		}
		for gi, g := range merged.groupers {
			m, ok := g.(processing.MergeableGrouper)
			if !ok {
				return errors.NewCodedError(errors.PROCESSING_INTERNAL,
					fmt.Sprintf("grouper %s does not implement MergeableGrouper", req.Groups[gi].Type))
			}
			if err := m.MergeGrouperState(p.groupers[gi]); err != nil {
				return err
			}
			merged.assignments[gi] += p.assignments[gi]
		}
		// The composite keys' tuples: a union (a key's tuple is a
		// function of the key, so which partition minted it is
		// immaterial).
		merged.keySet.Merge(p.keySet)
		// Key by key, in map order: each key's buckets still fold
		// in partition order (that order is the caller's), and no
		// figure depends on which key folds first — the grouped
		// tail orders the rows, and the MaxGroups trip below fires
		// at the same merged count whichever key crosses it.
		for key, bucket := range p.groups {
			existing, exists := merged.groups[key]
			if !exists {
				// MaxGroups on the MERGED count: every partition
				// can sit under the limit while their union does
				// not.
				if err := limits.Check(merged.limits, limits.MaxGroups, int64(len(merged.groups))+1); err != nil {
					return err
				}
				merged.groups[key] = bucket
				continue
			}
			processing.MergeSlotFloors(existing.Floors, bucket.Floors)
			for slot, oa := range existing.Aggs {
				if bm, ok := oa.(processing.BlockMerger); ok {
					if err := processing.MergeBlockMerger(bm, bucket.Aggs[slot]); err != nil {
						return err
					}
					continue
				}
				m, ok := oa.(processing.MergeableAggregator)
				if !ok {
					return errors.NewCodedError(errors.PROCESSING_INTERNAL,
						fmt.Sprintf("aggregator %s does not implement MergeableAggregator", req.Aggregations[slot].Type))
				}
				if err := m.MergeOnline(bucket.Aggs[slot]); err != nil {
					return err
				}
			}
		}
	}
	return nil
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
func finalizeMergedPartial(req *types.Request, schema *encoding.Schema, merged *shardPartial, shardCount int, compute processing.ComputePlan, exts *processing.ExtensionRegistry, models *model.Set, bound []rowLocalAttrSpec) (*types.Response, error) {
	// The partitions' attribute tallies fold into the run's one bound
	// build (boundAttributes), which emits them — entries and warnings —
	// once, cohort-wide, after the rows (emitBoundAttributes).
	if err := mergePartitionAttributes(bound, merged.attrs); err != nil {
		return nil, err
	}
	resp, err := finalizeMergedRows(req, schema, merged, shardCount, compute, exts, models)
	if err != nil {
		return nil, err
	}
	if err := emitBoundAttributes(resp, bound); err != nil {
		return nil, err
	}
	return resp, nil
}

// finalizeMergedRows is finalizeMergedPartial before the attribute
// figures: the rows and every other Components sub-part.
func finalizeMergedRows(req *types.Request, schema *encoding.Schema, merged *shardPartial, shardCount int, compute processing.ComputePlan, exts *processing.ExtensionRegistry, models *model.Set) (*types.Response, error) {
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
		// each bucket's GroupBucket.Floors), as do the per-bucket matrices,
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
