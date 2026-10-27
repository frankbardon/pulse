package service

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// mergeableRequest is the canonical mergeable request used across the
// eligibility tests. AGG_COUNT + AGG_SUM are associative+commutative
// integer/float reductions; both implement OnlineAggregator and
// MergeableAggregator (the shard-reduce path already covers them).
func mergeableRequest(path string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "weight", Label: "n"},
			{Type: types.AGG_SUM, Field: "weight", Label: "total"},
		},
	}
}

// writeOsFsCohort lays a hermetic synthetic cohort onto disk via OsFs
// inside t.TempDir() so the resolveRealPath probe in canParallelDecode
// accepts. Returns the (cohort path, fs.Config) pair plus the schema +
// rowCount for caller inspection.
func writeOsFsCohort(t *testing.T, rowCount int) (string, *fs.Config) {
	t.Helper()
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/eligibility.pulse"

	schema, payload := buildParallelEquivCohort(t, rowCount)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	if err := afero.WriteFile(osFs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	return path, cfg
}

// writeMemMapCohort lays a synthetic cohort onto MemMapFs so the
// resolveRealPath probe in canParallelDecode bails. Returns the
// (cohort path, fs.Config) pair.
func writeMemMapCohort(t *testing.T, rowCount int) (string, *fs.Config) {
	t.Helper()
	cfg := fs.NewMemMap()
	path := "mem.pulse"

	schema, payload := buildParallelEquivCohort(t, rowCount)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	if err := afero.WriteFile(cfg.Fs(), path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile mem: %v", err)
	}
	return path, cfg
}

// TestCanParallelDecode_Eligible pins the happy path: a mergeable
// request against a single-file cohort above the threshold, on an
// OsFs (so resolveRealPath succeeds), with workers=4 ⇒ canParallelDecode
// returns true and the reason string is empty.
func TestCanParallelDecode_Eligible(t *testing.T) {
	t.Parallel()

	path, cfg := writeOsFsCohort(t, parallelDecodeRecordThreshold+1024)
	svc := New(cfg)

	cohort, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	req := mergeableRequest(path)

	ok, reason := svc.canParallelDecode(req, cohort.Schema(), cohort, 4, parallelDecodeRecordThreshold+1024)
	if !ok {
		t.Fatalf("canParallelDecode: expected eligible; got bail reason=%q", reason)
	}
	if reason != "" {
		t.Errorf("canParallelDecode: eligible path must return empty reason; got %q", reason)
	}
}

// TestCanParallelDecode_WorkersOneBails verifies the explicit
// strictly-serial knob: workers==1 ⇒ bail regardless of every other
// condition. Mirrors the ShardWorkers==1 semantics in shouldFanOut.
func TestCanParallelDecode_WorkersOneBails(t *testing.T) {
	t.Parallel()

	path, cfg := writeOsFsCohort(t, parallelDecodeRecordThreshold+1024)
	svc := New(cfg)

	cohort, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	req := mergeableRequest(path)

	ok, reason := svc.canParallelDecode(req, cohort.Schema(), cohort, 1, parallelDecodeRecordThreshold+1024)
	if ok {
		t.Fatal("canParallelDecode: expected bail for workers=1")
	}
	if !strings.Contains(reason, "decode_workers=1") {
		t.Errorf("canParallelDecode: reason should mention decode_workers=1; got %q", reason)
	}
}

// TestCanParallelDecode_BelowThreshold verifies a cohort with fewer
// records than parallelDecodeRecordThreshold bails on the threshold
// check. Reason string should include both the actual count and the
// floor so downstream debug consumers can spot the cause at a glance.
func TestCanParallelDecode_BelowThreshold(t *testing.T) {
	t.Parallel()

	path, cfg := writeOsFsCohort(t, 1024)
	svc := New(cfg)

	cohort, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	req := mergeableRequest(path)

	ok, reason := svc.canParallelDecode(req, cohort.Schema(), cohort, 4, 1024)
	if ok {
		t.Fatal("canParallelDecode: expected bail below threshold")
	}
	if !strings.Contains(reason, "below threshold") {
		t.Errorf("canParallelDecode: reason should mention 'below threshold'; got %q", reason)
	}
}

// TestCanParallelDecode_MemMapFsBails verifies the mmap probe gate:
// a MemMapFs-backed cohort cannot mmap (no RealPath), so the gate
// rejects even when every other condition holds.
func TestCanParallelDecode_MemMapFsBails(t *testing.T) {
	t.Parallel()

	path, cfg := writeMemMapCohort(t, parallelDecodeRecordThreshold+1024)
	svc := New(cfg)

	cohort, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	req := mergeableRequest(path)

	ok, reason := svc.canParallelDecode(req, cohort.Schema(), cohort, 4, parallelDecodeRecordThreshold+1024)
	if ok {
		t.Fatal("canParallelDecode: expected bail for MemMapFs cohort")
	}
	if !strings.Contains(reason, "mmap") {
		t.Errorf("canParallelDecode: reason should mention mmap; got %q", reason)
	}
}

// TestCanParallelDecode_NonMergeableBails verifies the CanMergeRequest
// gate. A request whose aggregator is non-mergeable (AGG_MEDIAN —
// percentile aggregator, requires buffered sort) must bail even on an
// otherwise-eligible cohort.
func TestCanParallelDecode_NonMergeableBails(t *testing.T) {
	t.Parallel()

	path, cfg := writeOsFsCohort(t, parallelDecodeRecordThreshold+1024)
	svc := New(cfg)

	cohort, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// AGG_MEDIAN is non-mergeable per the shard-reduce gate (percentile
	// aggregator). CanMergeRequest must reject.
	req := &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_MEDIAN, Field: "weight", Label: "med"},
		},
	}

	ok, reason := svc.canParallelDecode(req, cohort.Schema(), cohort, 4, parallelDecodeRecordThreshold+1024)
	if ok {
		t.Fatal("canParallelDecode: expected bail for non-mergeable request")
	}
	if !strings.Contains(reason, "non-mergeable") {
		t.Errorf("canParallelDecode: reason should mention non-mergeable; got %q", reason)
	}
}

// TestCanParallelDecode_ShardArchiveBails verifies the shard-archive
// gate. Shard archives parallelise via the per-shard reducer in
// shard_reduce.go (ShardWorkers); the per-cohort parallel decode would
// double-spawn on the same CPUs and must bail.
func TestCanParallelDecode_ShardArchiveBails(t *testing.T) {
	t.Parallel()

	cfg, _, _ := fourShardScoreCohort(t)
	svc := New(cfg)

	cohort, err := svc.Open(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatalf("Open shard archive: %v", err)
	}
	if len(cohort.Shards()) == 0 {
		t.Fatal("expected shard archive cohort to have non-empty Shards()")
	}
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}

	// Force a synthetic large record count so the threshold gate does
	// not pre-empt the shard-archive check.
	ok, reason := svc.canParallelDecode(req, cohort.Schema(), cohort, 4, parallelDecodeRecordThreshold*2)
	if ok {
		t.Fatal("canParallelDecode: expected bail for shard archive cohort")
	}
	if !strings.Contains(reason, "shard archive") {
		t.Errorf("canParallelDecode: reason should mention 'shard archive'; got %q", reason)
	}
}

// TestCanParallelDecode_NilCohortBails defends against a nil-cohort
// call. The dispatch site never passes nil today, but the predicate
// must stay safe.
func TestCanParallelDecode_NilCohortBails(t *testing.T) {
	t.Parallel()

	cfg := fs.NewMemMap()
	svc := New(cfg)
	req := mergeableRequest("dummy")

	ok, reason := svc.canParallelDecode(req, nil, nil, 4, parallelDecodeRecordThreshold+1)
	if ok {
		t.Fatal("canParallelDecode: expected bail on nil cohort")
	}
	if !strings.Contains(reason, "nil cohort") {
		t.Errorf("canParallelDecode: reason should mention nil cohort; got %q", reason)
	}
}

// TestCanParallelDecode_AutoWorkersTreatedAsParallel verifies the
// workers=0 (auto-scale) sentinel passes the workers-gate; the post-
// gate shouldFanOutDecode then resolves the zero into runtime.NumCPU().
// The gate itself must not reject zero — only an explicit 1 forces
// serial.
func TestCanParallelDecode_AutoWorkersTreatedAsParallel(t *testing.T) {
	t.Parallel()

	path, cfg := writeOsFsCohort(t, parallelDecodeRecordThreshold+1024)
	svc := New(cfg)

	cohort, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	req := mergeableRequest(path)

	ok, reason := svc.canParallelDecode(req, cohort.Schema(), cohort, 0, parallelDecodeRecordThreshold+1024)
	if !ok {
		t.Fatalf("canParallelDecode: expected workers=0 to pass; got reason=%q", reason)
	}
}

// TestProcess_ParallelDecode_BelowThresholdBails verifies the threshold
// gate at the Process level: a cohort sized below the threshold takes
// the serial scanIter path regardless of DecodeWorkers. We assert
// shape correctness against a forced-serial baseline so any drift
// signals a dispatch leak.
func TestProcess_ParallelDecode_BelowThresholdBails(t *testing.T) {
	t.Parallel()

	path, cfg := writeOsFsCohort(t, 1024)
	ctx := context.Background()

	mkReq := func() *types.Request { return mergeableRequest(path) }

	svcSerial := New(cfg)
	svcSerial.SetDecodeWorkers(1)
	serialResp, err := svcSerial.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}
	if len(serialResp.Data) == 0 {
		t.Fatal("serial: expected aggregator row")
	}

	svcEligible := New(cfg)
	svcEligible.SetDecodeWorkers(4) // gate would accept but threshold rejects
	belowResp, err := svcEligible.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("below-threshold Process: %v", err)
	}
	if !reflect.DeepEqual(belowResp.Data, serialResp.Data) {
		t.Errorf("below-threshold path drifted from serial:\n got=%v\nwant=%v",
			belowResp.Data, serialResp.Data)
	}
}
