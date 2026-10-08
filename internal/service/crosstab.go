package service

import (
	"context"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// processCrosstab is the dispatch arm of Process for requests carrying a
// Crosstab section. The orchestrator opens the cohort, applies smart
// defaults to the cell aggregation (so omitting the cell Type still
// works when the field's schema type fixes the choice), then either
// hands off to the fused streaming accumulator (when
// processing.CanFuseCrosstab accepts the request) or drains every
// filter-passing record into memory and runs the buffered
// processing.Processor.RunCrosstab pipeline.
//
// Fused vs buffered: the fused path holds O(cells + margins) memory and
// decodes the cohort exactly once; the buffered path materialises every
// filter-passing record before partitioning. Both produce byte-equal
// MatrixPayload / long-shape output for any request the gate accepts,
// asserted by TestCrosstabFused_EquivalenceVsBuffered. The gate rejects
// requests that require buffer-only finalize (AGG_MEDIAN cell, tier-1
// tests, two-pass attributes, etc.); those still take the buffered
// path here.
//
// Crosstabs are inherently buffered (matrix shape, any margin, any
// normalization → buffered) except for the degenerate "long + no
// margins + normalize=none" case, which descriptor/predict.go flags as
// streamable. That degenerate case still routes through here because
// the existing process path does not yet support multi-grouper
// composite keys for nested axes; the RunCrosstab orchestrator is the
// only place those keys are materialised today.
func (s *Service) processCrosstab(ctx context.Context, req *types.Request) (*types.Response, error) {
	// A crosstab carrying its one JoinSpec runs over the joined row
	// stream. Process applied descx.JoinCountRefusal already, and
	// CanFuseCrosstab declines any join, so this arm is always buffered.
	if len(req.Joins) > 0 {
		return s.processCrosstabWithJoin(ctx, req)
	}

	path := resolveCohortPath(req.Cohort)
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	countReads(ctx, cohort)

	s.applyDefaults(req, cohort.Schema())
	zones, err := s.resolveZones(req, cohort.Schema())
	if err != nil {
		return nil, err
	}
	if err := s.checkFieldRefs(req, cohort.Schema()); err != nil {
		return nil, err
	}
	lin, err := s.limitInputs(ctx, cohort, path)
	if err != nil {
		return nil, err
	}
	if err := s.limitsPreflight(req, cohort.Schema(), lin); err != nil {
		return nil, err
	}

	// Validate / inject label bindings exactly as Process does so a
	// labelled crosstab matches a labelled plain Process request.
	s.applyAutoLabels(&req.Labels, cohort.Schema(), collectOutputLabels(req), nil)
	if err := s.validateProcessLabels(req, cohort.Schema()); err != nil {
		return nil, err
	}

	// Both crosstab arms (and the fusion gate's construct probe) build
	// their axes from the zoned request.
	req = s.zoned(req, zones)

	// Dispatch to the fused streaming path when the gate accepts.
	// The gate is the load-bearing exclusion check — features, tier-1
	// tests, two-pass attributes, non-streamable groupers, non-online
	// cell aggregators, expression-runtime filters/attributes, and
	// opaque extension operators all fall back to the buffered path.
	if !s.disableCrosstabFusion {
		if ok, _ := processing.CanFuseCrosstab(req, cohort.Schema(), s.extensions); ok {
			return s.processCrosstabFused(ctx, cohort, path, req)
		}
	}

	iter := s.newScanIter(cohort, path)
	defer iter.Close()

	// Crosstab always materializes the filter-passing record set. On
	// wide cohorts that materialization is the dominant memory cost.
	// Project the iterator to only the fields the request actually
	// references so each Record's value/null/wide maps allocate at
	// retained-field width instead of full schema width. Forced on
	// independent of opts.ProjectBufferedFields because the crosstab
	// path has no streamable alternative — the savings are load-bearing
	// on cohorts beyond ~50 fields.
	projected := s.applyCrosstabProjection(iter, req, cohort.Schema())
	// The buffered crosstab arm; a decode that fans out below restamps
	// it as parallel decode.
	execInfoFrom(ctx).setPlan(observe.ArmBuffered, 1, len(cohort.Shards()), projected)

	// Decode dispatch: when the cohort is single-file (not a shard
	// archive — those parallelise via ShardWorkers), the iterator's
	// mmap path engaged (resolveRealPath succeeds), the cohort exceeds
	// parallelDecodeRecordThreshold, and the caller has not forced
	// serial (DecodeWorkers != 1), segment the mmap'd record region
	// across N workers. Each worker decodes its segment with its own
	// bytes.Reader + RecordReader over the shared read-only mmap
	// bytes. Otherwise fall through to the serial
	// materializeRecords(iter) path that has driven crosstab.
	//
	// Workers append to per-worker slabs and the orchestrator stitches
	// them into a single []*processing.Record consumed by RunCrosstab
	// unchanged. The dispatch also layers per-worker partial aggregator
	// state on top via reduceParallelBuffered, but only mergeable
	// requests (processing.CanMergeRequestWithExtensions) qualify. A crosstab request
	// always fails that gate today — validateCrosstabSpec rejects
	// req.Aggregations and CanMergeRequest requires non-empty
	// Aggregations — so the dispatch keeps the slice path for every
	// crosstab call. The mergeable arm is wired here so the broader
	// Process-level eligibility gate has a single, documented dispatch
	// point to share.
	if processing.CanMergeRequestWithExtensions(req, cohort.Schema(), s.extensions) {
		mergedResp, mergedOK, err := s.crosstabDecodeReduceMergeable(ctx, req, cohort, path, iter)
		if err != nil {
			return nil, err
		}
		if mergedOK {
			if mergedResp.Metadata != nil {
				mergedResp.Metadata.CohortFile = path
			}
			if err := s.buildAndApplyLabels(req, mergedResp); err != nil {
				return nil, err
			}
			return mergedResp, nil
		}
		// mergedOK=false signals the mergeable path bailed pre-dispatch
		// (small cohort, MemMapFs, shard archive). Fall through to the
		// slice path.
	}

	records, err := s.crosstabDecodeRecords(ctx, cohort, path, iter)
	if err != nil {
		return nil, err
	}

	proc := s.newProcessor(ctx, cohort.Schema(), req)
	resp, err := proc.RunCrosstab(ctx, req, records)
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

// processCrosstabWithJoin is the crosstab arm for a Request carrying
// exactly one JoinSpec. It builds the same joined row stream
// processWithJoin consumes (openJoinStream), materialises it, and runs
// the buffered RunCrosstab pipeline over the JOINED schema — so an axis
// or the cell may name a right-side (JoinSpec.As-renamed) field,
// unmatched left rows never reach a cell, and a 1:N right match counts
// once per joined row in every cell, margin and Components figure.
//
// It is always buffered: the fused walk decodes the left cohort
// directly and has no join leg, which is why CanFuseCrosstab declines
// a joined request rather than letting it reach
// FusedCrosstabState.AssertCanFuse. The parallel segment decode and the
// crosstab projection are single-cohort optimisations and do not apply
// to the joined stream.
func (s *Service) processCrosstabWithJoin(ctx context.Context, req *types.Request) (*types.Response, error) {
	// Strip Joins so RunCrosstab sees a plain crosstab over the joined
	// records — the same clone processWithJoin hands its processor.
	// Defaults, zones, field references and the limits pre-flight run
	// on the joined schema before the build side decodes a record.
	clone := *req
	clone.Joins = nil
	var zones []descriptor.ResolvedZone
	join, joinedSchema, leftPath, leftIter, err := s.openJoinStream(ctx, req, func(joined *encoding.Schema, lin descx.LimitInputs) error {
		s.applyDefaults(&clone, joined)
		z, err := s.resolveZones(&clone, joined)
		if err != nil {
			return err
		}
		zones = z
		if err := s.checkFieldRefs(&clone, joined); err != nil {
			return err
		}
		return s.limitsPreflight(&clone, joined, lin)
	})
	if err != nil {
		return nil, err
	}
	defer leftIter.Close()
	execInfoFrom(ctx).setPlan(observe.ArmJoin, 1, 0, 0)

	s.applyAutoLabels(&clone.Labels, joinedSchema, collectOutputLabels(&clone), nil)
	if err := s.validateProcessLabels(&clone, joinedSchema); err != nil {
		return nil, err
	}

	// HashJoinIterator.Record builds a fresh record per call, so the
	// slice survives the left iterator's buffer reuse.
	records, err := materializeRecords(ctx, join)
	if err != nil {
		return nil, err
	}
	if err := leftIter.Err(); err != nil {
		return nil, err
	}

	proc := s.newProcessor(ctx, joinedSchema, req)
	resp, err := proc.RunCrosstab(ctx, s.zoned(&clone, zones), records)
	if err != nil {
		return nil, err
	}
	if resp.Metadata != nil {
		resp.Metadata.CohortFile = leftPath
	}
	if err := s.buildAndApplyLabels(&clone, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// crosstabDecodeReduceMergeable is the mergeable-arm dispatcher: it
// shares the same bail predicates as crosstabDecodeRecords (shard
// archive → bail, MemMapFs → bail, below threshold → bail) but on
// success returns a finalised *types.Response from
// reduceParallelBuffered instead of a []*processing.Record. The third
// return value (ok) is true when the reducer ran end-to-end; false
// signals the caller should fall back to the slice path (the caller,
// processCrosstab, then calls into materializeRecords + RunCrosstab).
//
// Today this arm is unreachable from a crosstab request because
// validateCrosstabSpec rejects req.Aggregations and CanMergeRequest
// requires non-empty Aggregations — the gate in processCrosstab
// therefore always trips false for crosstab and the slice path is
// taken. The dispatch is wired here so the broader Process-level
// eligibility gate has a single integration point to share, and so
// unit tests can exercise reduceParallelBuffered without re-routing
// the call chain.
func (s *Service) crosstabDecodeReduceMergeable(
	ctx context.Context,
	req *types.Request,
	cohort *Cohort,
	path string,
	iter scanIterator,
) (*types.Response, bool, error) {
	// Shard archives: bail. Same reasoning as crosstabDecodeRecords —
	// intra-shard segment decode on top of the per-shard reducer would
	// double-spawn workers.
	if len(cohort.Shards()) > 0 {
		return nil, false, nil
	}
	si, ok := iter.(*streamingIterator)
	if !ok {
		return nil, false, nil
	}
	if s.decodeWorkers == 1 {
		return nil, false, nil
	}

	projectMapHint := len(cohort.Schema().Fields)
	if si.projectSize > 0 {
		projectMapHint = si.projectSize
	}
	pctx, cleanup, available, err := buildParallelDecodeContext(
		s, path, cohort.Schema(), si.plan, si.project, projectMapHint,
	)
	if err != nil {
		return nil, false, err
	}
	if !available {
		return nil, false, nil
	}
	workers, ok := shouldFanOutDecode(s.decodeWorkers, pctx.totalRecords)
	if !ok {
		if cleanup != nil {
			_ = cleanup()
		}
		return nil, false, nil
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	stampParallelDecode(ctx, pctx, workers, si.projectSize)

	resp, err := s.reduceParallelBuffered(ctx, req, cohort.Schema(), pctx, workers)
	if err != nil {
		return nil, false, err
	}
	return resp, true, nil
}

// materializeRecords drains an iterator into a slice. Crosstab forces
// buffered execution, so this materialization is unavoidable. Pulled out
// of processCrosstab so future variants (streamable long-shape passthrough)
// can share the helper; the joined crosstab drains its HashJoinIterator
// through it too. It polls ctx every processing.CtxPollInterval rows and
// returns the caller's ctx error unchanged.
func materializeRecords(ctx context.Context, iter processing.RecordIterator) ([]*processing.Record, error) {
	var records []*processing.Record
	poll := processing.NewCtxPoller(ctx)
	for iter.Next() {
		if err := poll.Poll(); err != nil {
			return nil, err
		}
		records = append(records, iter.Record())
	}
	return records, nil
}

// crosstabDecodeRecords picks between the serial and segment-aware
// parallel decode paths. Returns the same []*processing.Record both
// arms emit so the surrounding orchestration (processor construction,
// RunCrosstab, label overlay) is unchanged.
//
// Bail to serial when any of these conditions fire:
//
//   - the cohort is a shard archive (Shards non-empty); shard-level
//     parallelism is handled by shouldFanOut + processShardArchiveParallel
//     elsewhere and the crosstab path does not yet reach that route
//   - shouldFanOutDecode rejects (DecodeWorkers==1 or recordCount below
//     the threshold)
//   - resolveRealPath misses (mmap can't engage on this fs — MemMapFs,
//     custom fs without RealPather, mmap-unsupported platform)
//   - the cohort is the OpenAnchor in-memory overlay (its overlay fs
//     doesn't satisfy RealPather, so resolveRealPath naturally misses)
//
// On bail we fall through to materializeRecords(iter) verbatim — the
// iterator already has applyCrosstabProjection installed, so the
// projection + plan caching applies in both arms.
func (s *Service) crosstabDecodeRecords(
	ctx context.Context,
	cohort *Cohort,
	path string,
	iter scanIterator,
) ([]*processing.Record, error) {
	// Shard archives delegate to the shard parallel reducer elsewhere
	// (today crosstab+shards routes through this serial path because
	// the shard reducer doesn't speak crosstab); intra-shard segment
	// decode on top would double-stack workers. Bail.
	if len(cohort.Shards()) > 0 {
		return s.crosstabDecodeRecordsSerial(ctx, iter)
	}

	si, ok := iter.(*streamingIterator)
	if !ok {
		// Defensive: today every single-file cohort returns
		// *streamingIterator from newScanIter. Future iterator variants
		// would have to opt in to parallel decode via a new interface;
		// for now anything unrecognised falls back to serial.
		return s.crosstabDecodeRecordsSerial(ctx, iter)
	}

	// Pre-flight bail: DecodeWorkers==1 means "force serial" and short-
	// circuits before we even probe the fs. Mirrors the symmetric
	// shouldFanOut shape in shard_reduce.go.
	if s.decodeWorkers == 1 {
		return s.crosstabDecodeRecordsSerial(ctx, iter)
	}

	// Build the parallel context: probes the fs for a real on-disk
	// path, mmaps the file, and resolves the record-region offset +
	// stride + total record count. Returns ok=false when the fs
	// doesn't satisfy RealPather (MemMapFs, in-memory anchor
	// overlay), when mmap fails, or when the file is too small for
	// even one record. The cost of this probe is bounded: one
	// resolveRealPath syscall + one mmap (constant-time on linux/
	// darwin) + a 9-byte header read and the schema replay against an
	// already-mapped slice. Far cheaper than the alternative
	// (cohort.RecordCount whole-file slurp) and only paid once per
	// crosstab call.
	projectMapHint := len(cohort.Schema().Fields)
	if si.projectSize > 0 {
		projectMapHint = si.projectSize
	}
	pctx, cleanup, available, err := buildParallelDecodeContext(
		s, path, cohort.Schema(), si.plan, si.project, projectMapHint,
	)
	if err != nil {
		return nil, err
	}
	if !available {
		return s.crosstabDecodeRecordsSerial(ctx, iter)
	}

	// Now that the resolved totalRecords is in hand, apply the
	// threshold + worker-count predicate. Below-threshold cohorts
	// release the mmap and fall back to the serial iterator (which
	// will re-mmap on its own — afero.ReadFile on the bail path is
	// fine because we only reach it for cohorts < 100K records,
	// where the whole-file alloc is a few MB).
	workers, ok := shouldFanOutDecode(s.decodeWorkers, pctx.totalRecords)
	if !ok {
		if cleanup != nil {
			_ = cleanup()
		}
		return s.crosstabDecodeRecordsSerial(ctx, iter)
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	// Defensive stride sanity: bit-packed neighbours can never straddle
	// a stride boundary because Schema.RecordByteSize allots one byte
	// per bit-packed field. If a future schema introduced a sub-byte-
	// aligned stride, the resulting fractional split would silently
	// truncate; surface that loudly instead.
	if pctx.stride <= 0 {
		return s.crosstabDecodeRecordsSerial(ctx, iter)
	}

	stampParallelDecode(ctx, pctx, workers, si.projectSize)
	return materializeRecordsParallel(ctx, pctx, workers)
}

// crosstabDecodeRecordsSerial is the existing materializeRecords path
// wrapped so the dispatcher can call it from every bail arm with the
// same signature.
func (s *Service) crosstabDecodeRecordsSerial(ctx context.Context, iter scanIterator) ([]*processing.Record, error) {
	records, err := materializeRecords(ctx, iter)
	if err != nil {
		return nil, err
	}
	if iter.Err() != nil {
		return nil, iter.Err()
	}
	return records, nil
}

// stampParallelDecode records a parallel segment decode that fanned out
// over workers, and the mapped region it reads (no-op off). projected
// is the iterator's retained-field count (0: full decode).
func stampParallelDecode(ctx context.Context, pctx *parallelDecodeContext, workers, projected int) {
	ei := execInfoFrom(ctx)
	if ei == nil {
		return
	}
	ei.setPlan(observe.ArmParallelDecode, workers, 0, projected)
	ei.addBytes(int64(len(pctx.mmapBytes)))
}
