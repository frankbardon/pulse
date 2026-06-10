package service

import (
	"context"

	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// processCrosstab is the dispatch arm of Process for requests carrying a
// Crosstab section. The orchestrator opens the cohort, applies smart
// defaults to the cell aggregation (so omitting the cell Type still
// works when the field's schema type fixes the choice), drains every
// filter-passing record into memory, and hands off to
// processing.Processor.RunCrosstab for the recursive-partition + per-cell
// aggregation + margins + normalization pipeline.
//
// Crosstabs are inherently buffered (matrix shape, any margin, any
// normalization → buffered) except for the degenerate "long + no
// margins + normalize=none" case, which descriptor/predict.go flags as
// streamable. That degenerate case still routes through here because
// the existing process path does not yet support multi-grouper
// composite keys for nested axes; the RunCrosstab orchestrator is the
// only place those keys are materialised today.
func (s *Service) processCrosstab(ctx context.Context, req *types.Request) (*types.Response, error) {
	path := resolveCohortPath(req.Cohort)
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	s.applyDefaults(req, cohort.Schema())

	// Validate / inject label bindings exactly as Process does so a
	// labelled crosstab matches a labelled plain Process request.
	s.applyAutoLabels(&req.Labels, cohort.Schema(), collectOutputLabels(req), nil)
	if err := s.validateProcessLabels(req, cohort.Schema()); err != nil {
		return nil, err
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
	s.applyCrosstabProjection(iter, req, cohort.Schema())

	// Decode dispatch: when the cohort is single-file (not a shard
	// archive — those parallelise via ShardWorkers), the iterator's
	// mmap path engaged (resolveRealPath succeeds), the cohort exceeds
	// parallelDecodeRecordThreshold, and the caller has not forced
	// serial (DecodeWorkers != 1), segment the mmap'd record region
	// across N workers. Each worker decodes its segment with its own
	// bytes.Reader + RecordReader over the shared read-only mmap
	// bytes. Otherwise fall through to the serial
	// materializeRecords(iter) path that has driven crosstab since E2.
	//
	// E3-S2 wired the decode-only segment fan-out: workers append to
	// per-worker slabs and the orchestrator stitches them into a
	// single []*processing.Record consumed by RunCrosstab unchanged.
	// E3-S3 layers per-worker partial aggregator state on top via
	// reduceParallelBuffered, but only mergeable requests
	// (processing.CanMergeRequest) qualify. A crosstab request always
	// fails that gate today — validateCrosstabSpec rejects
	// req.Aggregations and CanMergeRequest requires non-empty
	// Aggregations — so the dispatch keeps the slice path for every
	// crosstab call. The mergeable arm is wired here so E3-S4 has a
	// single, documented dispatch point to broaden the eligibility
	// from.
	if processing.CanMergeRequest(req, cohort.Schema()) {
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

	proc := processing.NewProcessorWithExtensions(cohort.Schema(), s.extensions)
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

// crosstabDecodeReduceMergeable is the mergeable-arm dispatcher: it
// shares the same bail predicates as crosstabDecodeRecords (shard
// archive → bail, MemMapFs → bail, below threshold → bail) but on
// success returns a finalised *types.Response from
// reduceParallelBuffered instead of a []*processing.Record. The third
// return value (ok) is true when the reducer ran end-to-end; false
// signals the caller should fall back to the slice path (the caller,
// processCrosstab, then calls into materializeRecords + RunCrosstab as
// it did pre-E3-S3).
//
// Today this arm is unreachable from a crosstab request because
// validateCrosstabSpec rejects req.Aggregations and CanMergeRequest
// requires non-empty Aggregations — the gate in processCrosstab
// therefore always trips false for crosstab and the slice path is
// taken. The dispatch is wired here so E3-S4's broader Process-level
// gate has a single integration point to share, and so unit tests can
// exercise reduceParallelBuffered without re-routing the call chain.
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

	resp, err := s.reduceParallelBuffered(ctx, req, cohort.Schema(), pctx, workers)
	if err != nil {
		return nil, false, err
	}
	return resp, true, nil
}

// materializeRecords drains an iterator into a slice. Crosstab forces
// buffered execution, so this materialization is unavoidable. Pulled out
// of processCrosstab so future variants (streamable long-shape passthrough)
// can share the helper.
func materializeRecords(iter scanIterator) ([]*processing.Record, error) {
	var records []*processing.Record
	for iter.Next() {
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
		return s.crosstabDecodeRecordsSerial(iter)
	}

	si, ok := iter.(*streamingIterator)
	if !ok {
		// Defensive: today every single-file cohort returns
		// *streamingIterator from newScanIter. Future iterator variants
		// would have to opt in to parallel decode via a new interface;
		// for now anything unrecognised falls back to serial.
		return s.crosstabDecodeRecordsSerial(iter)
	}

	// Pre-flight bail: DecodeWorkers==1 means "force serial" and short-
	// circuits before we even probe the fs. Mirrors the symmetric
	// shouldFanOut shape in shard_reduce.go.
	if s.decodeWorkers == 1 {
		return s.crosstabDecodeRecordsSerial(iter)
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
		return s.crosstabDecodeRecordsSerial(iter)
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
		return s.crosstabDecodeRecordsSerial(iter)
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
		return s.crosstabDecodeRecordsSerial(iter)
	}

	return materializeRecordsParallel(ctx, pctx, workers)
}

// crosstabDecodeRecordsSerial is the existing materializeRecords path
// wrapped so the dispatcher can call it from every bail arm with the
// same signature.
func (s *Service) crosstabDecodeRecordsSerial(iter scanIterator) ([]*processing.Record, error) {
	records, err := materializeRecords(iter)
	if err != nil {
		return nil, err
	}
	if iter.Err() != nil {
		return nil, iter.Err()
	}
	return records, nil
}
