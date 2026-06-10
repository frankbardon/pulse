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
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"golang.org/x/sync/errgroup"
)

// parallel_decode.go implements segment-aware parallel decoding of the
// record region of a single-file .pulse cohort. The buffered Process
// path (today only the crosstab orchestrator) materialises every
// filter-passing record into memory; on wide cohorts the decode loop
// dominates wall-clock. This file splits the mmap'd record region into
// N contiguous segments at record-stride boundaries and fans out one
// goroutine per segment.
//
// Story scope (E3-S2): decode only. Each worker fires a per-record
// callback supplied by the orchestrator. No partial-state aggregation,
// no merge. E3-S3 will replace the orchestrator's append-to-slice
// callback with per-worker partial accumulators.
//
// Non-applicable cases:
//
//   - Shard archives: the per-shard worker pool (shard_reduce.go,
//     ShardWorkers) already parallelises across shards. Stacking
//     intra-shard segment decode on top would double-spawn and
//     contend on the same CPUs. Shard cohorts fall back to the serial
//     scan-iter pre-existing path.
//   - mmap did not engage: when the iterator's RealPath probe missed
//     (MemMapFs, custom Fs without RealPather, mmap-unsupported
//     platform, file empty), the byte region we'd segment doesn't
//     exist in a goroutine-safe shared form. Bail to serial.
//   - Cohort below parallelDecodeRecordThreshold: worker spawn +
//     channel/group overhead dominates the segment win. Bail.
//   - DecodeWorkers == 1: caller forced strictly serial.
//
// Segment boundaries are computed at record-stride multiples, so
// bit-packed neighbours can never straddle a boundary — record stride
// is byte-aligned per the byte-layout invariants (Schema.RecordByteSize
// returns 1 byte per bit-packed field). A defensive assertion at the
// dispatch site catches a malformed schema before fan-out.

// DecodeCallback is invoked once per decoded record by a parallel
// decode worker. Implementations MUST be safe to call from a single
// goroutine — each worker is given its own DecodeCallback via the
// DecodeCallbackFactory, so the same callback instance is never shared
// across workers.
//
// Returning a non-nil error aborts the worker's decode loop; the
// errgroup propagates the error and cancels every other worker.
type DecodeCallback func(rec *processing.Record) error

// DecodeCallbackFactory constructs a per-worker DecodeCallback. Called
// once per worker before that worker begins decoding. The factory MUST
// be safe to call from the goroutine that owns workerIdx; the returned
// callback is consumed only by that same goroutine.
//
// workerIdx is the worker's 0-based index (0..workers-1) and is stable
// for the lifetime of the parallel run — partial-state implementations
// in E3-S3 can key per-worker accumulators on it. recordCount is the
// number of records the worker will receive (not the cohort total),
// useful when the callback wants to pre-size a slice or accumulator.
type DecodeCallbackFactory func(workerIdx, recordCount int) DecodeCallback

// canParallelDecode is the eligibility predicate for the buffered
// Process path's segment-aware parallel decode. It is the single
// integration site E3-S4 uses to gate the broader (non-crosstab) Process
// dispatch — every condition the parallel reducer needs is checked here
// in one place so the dispatch site can stay a single if-statement and
// so future stories that broaden the gate (e.g. extending mergeability
// to crosstab cells) have one function to amend.
//
// Eligibility is the AND of:
//
//   - workers != 1 — the caller has not forced strictly serial
//     execution (DecodeWorkers == 1 is the documented "force serial"
//     knob, mirroring ShardWorkers == 1)
//   - recordCount >= parallelDecodeRecordThreshold — the cohort is large
//     enough to amortise per-worker spawn + state-merge overhead
//   - the cohort is a single-file .pulse (not a shard archive) —
//     archives parallelise via the per-shard reducer in shard_reduce.go;
//     stacking intra-shard segment decode on top of that would
//     double-spawn workers on the same CPUs
//   - resolveRealPath succeeds on the cohort fs+path pair — mmap
//     requires a real on-disk file; MemMapFs, the anchor overlay, and
//     custom afero.Fs implementations without RealPather all bail here
//   - processing.CanMergeRequest(req, schema) is true — the request's
//     online state must be mergeable across worker partitions (the same
//     gate the per-shard reducer in shard_reduce.go consults)
//
// On success returns (true, ""). On bail returns (false, reason) where
// reason is a short, user-debuggable string suitable for a debug log /
// pprof label / future telemetry tag. The reason strings stay terse and
// stable so downstream tooling can pattern-match.
//
// canParallelDecode is a pure predicate — it never mmap's the file or
// constructs the parallelDecodeContext; that work happens at the
// dispatch site after the gate accepts. The cheap resolveRealPath probe
// is the closest to side-effect-ful work here, but it is bounded by one
// type-assertion + (for *afero.BasePathFs) one path-join, both
// constant-time.
func (s *Service) canParallelDecode(
	req *types.Request,
	schema *encoding.Schema,
	cohort *Cohort,
	workers int,
	recordCount int,
) (bool, string) {
	if workers == 1 {
		return false, "decode_workers=1 (forced serial)"
	}
	if recordCount < parallelDecodeRecordThreshold {
		return false, fmt.Sprintf("below threshold (%d < %d)",
			recordCount, parallelDecodeRecordThreshold)
	}
	if cohort == nil {
		return false, "nil cohort"
	}
	if len(cohort.Shards()) > 0 {
		return false, "shard archive (use ShardWorkers)"
	}
	if cohort.fs == nil {
		return false, "nil cohort fs"
	}
	if _, ok := resolveRealPath(cohort.fs, cohort.path); !ok {
		return false, "mmap unavailable (no RealPath)"
	}
	if !processing.CanMergeRequest(req, schema) {
		return false, "non-mergeable request"
	}
	return true, ""
}

// shouldFanOutDecode reports whether the buffered Process path should
// fan out per-segment parallel decode. The decision is gated by:
//
//   - the caller has not forced serial (decodeWorkers != 1)
//   - the cohort is large enough to amortise spawn overhead
//     (recordCount >= parallelDecodeRecordThreshold)
//   - after capping at recordCount the resolved worker count is >= 2
//
// Returns (resolvedWorkers, true) when fan-out applies, or (0, false)
// when the caller should fall through to the serial materializeRecords
// path. Mirrors shouldFanOut in shard_reduce.go (and is intentionally
// shaped the same way so the two parallel surfaces stay legible).
func shouldFanOutDecode(workers, recordCount int) (int, bool) {
	if workers == 1 {
		return 0, false
	}
	if recordCount < parallelDecodeRecordThreshold {
		return 0, false
	}
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > recordCount {
		workers = recordCount
	}
	if workers < 2 {
		return 0, false
	}
	return workers, true
}

// parallelDecodeContext bundles the inputs the per-worker decoder needs.
// Constructed once by the orchestrator and shared read-only across
// workers; nothing inside is mutated during the parallel run.
//
// mmapBytes is the read-only mmap'd region of the cohort file. It is
// safe to share across goroutines: each worker constructs its own
// bytes.Reader over its assigned sub-slice, and the kernel's MAP_SHARED
// + PROT_READ semantics make concurrent reads well-defined. The
// orchestrator owns the cleanup; workers never touch it.
//
// recordRegionStart is the byte offset within mmapBytes where the first
// record begins (= HeaderSize + on-wire schema block size). The
// orchestrator computes this once by replaying ReadHeader + ReadSchema
// against an in-memory reader; the workers receive the resolved offset
// so they don't pay the parse cost N times.
//
// stride is the on-wire byte size of one record (Schema.RecordByteSize,
// including the trailing null bitmap when present). Segment boundaries
// are always integer multiples of stride.
//
// plan is the precomputed DecodePlan for the projected field set, or
// nil when the orchestrator wants the full-decode walk. Shared across
// workers (read-only — DecodePlan carries no mutable state). When nil
// each worker falls through ReadRecordWithWide / ReadRecordWithWideProjected.
//
// keep is the field-keep filter matching plan. When plan == nil and
// keep == nil, the workers read every field.
//
// projectMapHint is the per-record map allocation hint (== len(keepSet)
// when projection is active, == len(schema.Fields) otherwise). Mirrors
// the streamingIterator's per-record allocation strategy.
type parallelDecodeContext struct {
	schema            *encoding.Schema
	mmapBytes         []byte
	recordRegionStart int
	stride            int
	totalRecords      int
	plan              *encoding.DecodePlan
	keep              encoding.FieldFilter
	projectMapHint    int
}

// parallelDecodeMmap segments the record region into worker-count
// contiguous ranges at stride-aligned boundaries and fans out one
// goroutine per segment. Each worker calls factory(workerIdx,
// segmentRecordCount) once to obtain its DecodeCallback, then walks
// its segment record-by-record firing the callback per row.
//
// The caller MUST guarantee:
//
//   - pctx.stride > 0 and stride-aligned over the record region
//   - workers >= 2 (the predicate shouldFanOutDecode already verified)
//   - pctx.mmapBytes outlives the call (orchestrator owns cleanup)
//
// Context cancellation aborts every in-flight worker promptly: the
// per-record loop polls ctx.Err() at segment boundaries. The first
// error any worker returns propagates through the errgroup; remaining
// workers are cancelled and their callbacks no longer invoked.
func parallelDecodeMmap(
	ctx context.Context,
	pctx *parallelDecodeContext,
	workers int,
	factory DecodeCallbackFactory,
) error {
	if pctx == nil {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"parallel decode: nil context")
	}
	if pctx.stride <= 0 {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"parallel decode: non-positive record stride")
	}
	if pctx.totalRecords <= 0 {
		return nil
	}

	// Defensive: every segment boundary lands at recordRegionStart +
	// k*stride. Bit-packed neighbours can never straddle a boundary
	// because stride includes 1 byte per bit-packed field by contract
	// (Schema.RecordByteSize). If a future schema kind broke that
	// invariant the per-worker bytes.Reader would EOF mid-record and
	// the decoder would return io.EOF; the assertion below converts
	// that latent failure into a typed error at dispatch.
	expected := pctx.totalRecords * pctx.stride
	available := len(pctx.mmapBytes) - pctx.recordRegionStart
	if available < expected {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"parallel decode: record region smaller than total_records * stride",
			map[string]any{
				"available_bytes": available,
				"expected_bytes":  expected,
				"stride":          pctx.stride,
				"total_records":   pctx.totalRecords,
			},
		)
	}

	// Compute per-worker segment boundaries in records. Each worker
	// receives floor(N/W) records; the last worker absorbs any
	// remainder so the partition covers exactly [0, totalRecords).
	type segment struct {
		startRec int
		endRec   int // exclusive
	}
	segs := make([]segment, workers)
	base := pctx.totalRecords / workers
	for w := 0; w < workers; w++ {
		segs[w].startRec = w * base
		segs[w].endRec = segs[w].startRec + base
	}
	segs[workers-1].endRec = pctx.totalRecords

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)

	for w := 0; w < workers; w++ {
		seg := segs[w]
		idx := w
		g.Go(func() error {
			segmentCount := seg.endRec - seg.startRec
			if segmentCount <= 0 {
				return nil
			}
			// Per-worker bytes.Reader scoped to this segment's slice of
			// the shared mmap region. Each worker constructs its own
			// reader so the underlying read offset is goroutine-local;
			// no shared cursor.
			startByte := pctx.recordRegionStart + seg.startRec*pctx.stride
			endByte := pctx.recordRegionStart + seg.endRec*pctx.stride
			segBytes := pctx.mmapBytes[startByte:endByte]
			r := bytes.NewReader(segBytes)
			rr := encoding.NewRecordReader(r, pctx.schema)
			cb := factory(idx, segmentCount)

			for recIdx := 0; recIdx < segmentCount; recIdx++ {
				// Cancellation poll. Cheap (atomic load) and amortised
				// across the per-record cost. Per-record polling is
				// the same cadence the serial path implicitly hits via
				// the caller's loop boundary.
				if err := gctx.Err(); err != nil {
					return err
				}

				mapHint := pctx.projectMapHint
				if mapHint <= 0 {
					mapHint = len(pctx.schema.Fields)
				}
				values := make(map[string]float64, mapHint)
				nulls := make(map[string]bool)
				wide := make(map[string]any)

				var derr error
				switch {
				case pctx.plan != nil:
					derr = rr.ReadRecordWithWidePlan(values, nulls, wide, pctx.keep, pctx.plan)
				case pctx.keep != nil:
					derr = rr.ReadRecordWithWideProjected(values, nulls, wide, pctx.keep)
				default:
					derr = rr.ReadRecordWithWide(values, nulls, wide)
				}
				if derr != nil {
					if derr == io.EOF {
						// Segment counted exactly stride*segmentCount
						// bytes; an early EOF here means stride was
						// miscomputed upstream. Surface as a typed
						// error rather than silently truncating.
						return errors.NewCodedErrorWithDetails(
							errors.PROCESSING_INTERNAL,
							"parallel decode: unexpected EOF mid-segment",
							map[string]any{
								"worker_idx":     idx,
								"segment_start":  seg.startRec,
								"segment_end":    seg.endRec,
								"records_seen":   recIdx,
								"records_target": segmentCount,
							},
						)
					}
					return derr
				}

				rec := processing.NewRecordWithWide(pctx.schema, values, nulls, wide)
				if err := cb(rec); err != nil {
					return err
				}
			}
			return nil
		})
	}

	return g.Wait()
}

// materializeRecordsParallel decodes pctx in parallel and returns the
// concatenated record slice in cohort order. Each worker collects its
// segment's records into a worker-local slice; the orchestrator stitches
// those slices in worker-index order (which equals cohort byte order
// because segments are assigned contiguous record ranges).
//
// Order preservation matters for the crosstab path: the matrix builder
// is order-insensitive (groupers partition by key), but the long-shape
// rendering and any downstream attribute injection that relies on row
// order would silently shift outputs if workers were stitched in
// completion order. We pay the per-worker slice allocation today so
// the stitching is deterministic.
//
// E3-S3 will replace this helper (and its append-into-slice callback)
// with a per-worker partial-state accumulator that merges directly into
// the orchestrator's processing.Processor. The factory signature
// already supports that: the closure constructs the accumulator, the
// returned callback updates it, and a post-Wait merge fold collapses
// partials in worker-index order.
func materializeRecordsParallel(
	ctx context.Context,
	pctx *parallelDecodeContext,
	workers int,
) ([]*processing.Record, error) {
	if pctx == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"materializeRecordsParallel: nil context")
	}

	// Per-worker collection slabs, indexed by worker id. Sized once
	// per worker from the worker's segment record count so the
	// per-record append stays amortised O(1) without a resize storm.
	// Each worker owns its own slot for the duration of decode; no
	// cross-worker writes happen, so no synchronisation is required
	// during the hot loop. The errgroup.Wait barrier below establishes
	// the happens-before edge the orchestrator relies on before
	// stitching.
	partials := make([][]*processing.Record, workers)
	// publishMu is grabbed only once per worker (at the worker's
	// final callback) to publish the slice header back to the
	// orchestrator. The post-Wait stitch then reads under no lock —
	// Wait() supplies the happens-before edge. The lock is here only
	// to satisfy the Go memory model around the slice header write,
	// not to coordinate decode work.
	var publishMu sync.Mutex
	publish := func(workerIdx int, buf []*processing.Record) {
		publishMu.Lock()
		partials[workerIdx] = buf
		publishMu.Unlock()
	}

	factory := func(workerIdx, recordCount int) DecodeCallback {
		buf := make([]*processing.Record, 0, recordCount)
		emitted := 0
		return func(rec *processing.Record) error {
			buf = append(buf, rec)
			emitted++
			// Publish the slice header on the final record so the
			// post-Wait stitch sees a non-nil slot. Earlier records
			// stay buffer-local to keep the hot loop lock-free.
			if emitted == recordCount {
				publish(workerIdx, buf)
			}
			return nil
		}
	}

	if err := parallelDecodeMmap(ctx, pctx, workers, factory); err != nil {
		return nil, err
	}

	total := 0
	for _, p := range partials {
		total += len(p)
	}
	out := make([]*processing.Record, 0, total)
	for _, p := range partials {
		out = append(out, p...)
	}
	return out, nil
}

// buildParallelDecodeContext constructs a parallelDecodeContext from
// the orchestrator's pre-computed inputs. Returns (pctx, true) when
// every precondition for the parallel path holds, or (nil, false) and
// the caller falls through to the serial materializeRecords path.
//
// Preconditions for parallel decode:
//
//   - the file resolves to a real on-disk path (resolveRealPath returns
//     a non-empty string) so mmap is even attempted
//   - mmapFileBytes succeeds (the data slice and a cleanup are returned)
//   - the resolved record region is large enough for one full record
//     (covers the empty-cohort and corrupted-file cases)
//
// On success the returned cleanup MUST be called by the caller once
// the parallel decode finishes — the orchestrator is the only path
// with the lifetime to manage it. On failure no resources are leaked.
//
// projectMapHint mirrors streamingIterator.projectSize: when keep is
// nil the orchestrator passes len(schema.Fields); when keep is active
// it passes the retained-set size.
func buildParallelDecodeContext(
	s *Service,
	path string,
	schema *encoding.Schema,
	plan *encoding.DecodePlan,
	keep encoding.FieldFilter,
	projectMapHint int,
) (pctx *parallelDecodeContext, cleanup func() error, ok bool, err error) {
	if schema == nil {
		return nil, nil, false, nil
	}
	realPath, hasReal := resolveRealPath(s.fs.Fs(), path)
	if !hasReal {
		return nil, nil, false, nil
	}
	data, cleanupFn, mErr := mmapFileBytes(realPath)
	if mErr != nil {
		// Falls back to serial path silently — the orchestrator's
		// streamingIterator will hit the same fallback when it
		// initFromFile()s, so there's nothing to recover here.
		return nil, nil, false, nil
	}

	// Replay header + schema to land the cursor at the first record
	// byte. We discard the schema return; the cohort already carries
	// its parsed schema (s.Open replays the same prefix bytes).
	r := bytes.NewReader(data)
	if hErr := encoding.ReadHeader(r); hErr != nil {
		_ = cleanupFn()
		return nil, nil, false, errors.WrapCodedError(hErr, errors.ENCODING_INVALID,
			fmt.Sprintf("parallel decode: re-reading cohort header: %s", path))
	}
	if _, sErr := encoding.ReadSchema(r); sErr != nil {
		_ = cleanupFn()
		return nil, nil, false, errors.WrapCodedError(sErr, errors.ENCODING_INVALID,
			fmt.Sprintf("parallel decode: re-reading cohort schema: %s", path))
	}
	recordRegionStart := len(data) - r.Len()
	stride := schema.RecordByteSize()
	if stride <= 0 {
		_ = cleanupFn()
		return nil, nil, false, nil
	}
	available := len(data) - recordRegionStart
	if available < stride {
		// Empty cohort or smaller than one record. The serial path
		// handles this trivially; cleanup the mapping and fall back.
		_ = cleanupFn()
		return nil, nil, false, nil
	}
	totalRecords := available / stride
	pctx = &parallelDecodeContext{
		schema:            schema,
		mmapBytes:         data,
		recordRegionStart: recordRegionStart,
		stride:            stride,
		totalRecords:      totalRecords,
		plan:              plan,
		keep:              keep,
		projectMapHint:    projectMapHint,
	}
	return pctx, cleanupFn, true, nil
}
