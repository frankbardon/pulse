package service

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
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

func TestProcess_ParallelDecode_ByteEqualToSerial(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping parallel-decode Process equivalence in -short mode")
	}

	path, cfg := writeOsFsCohort(t, parallelDecodeRecordThreshold+2048)
	mkReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: path},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "weight", Label: "n"},
				{Type: types.AGG_SUM, Field: "weight", Label: "total"},
				{Type: types.AGG_MIN, Field: "weight", Label: "lo"},
				{Type: types.AGG_MAX, Field: "weight", Label: "hi"},
			},
		}
	}

	ctx := context.Background()

	// Serial baseline: DecodeWorkers=1 forces the gate to bail so the
	// existing scanIter path runs.
	svcSerial := New(cfg)
	svcSerial.SetDecodeWorkers(1)
	serialResp, err := svcSerial.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}
	if len(serialResp.Data) == 0 {
		t.Fatal("serial: expected aggregator row")
	}
	wantRow := serialResp.Data[0]

	// Parallel path: DecodeWorkers=4 ⇒ gate accepts.
	svcParallel := New(cfg)
	svcParallel.SetDecodeWorkers(4)
	parallelResp, err := svcParallel.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("parallel Process: %v", err)
	}
	if len(parallelResp.Data) == 0 {
		t.Fatal("parallel: expected aggregator row")
	}
	gotRow := parallelResp.Data[0]

	if !reflect.DeepEqual(gotRow["n"], wantRow["n"]) {
		t.Errorf("AGG_COUNT differs: got=%v want=%v", gotRow["n"], wantRow["n"])
	}
	if !reflect.DeepEqual(gotRow["lo"], wantRow["lo"]) {
		t.Errorf("AGG_MIN differs: got=%v want=%v", gotRow["lo"], wantRow["lo"])
	}
	if !reflect.DeepEqual(gotRow["hi"], wantRow["hi"]) {
		t.Errorf("AGG_MAX differs: got=%v want=%v", gotRow["hi"], wantRow["hi"])
	}
	// AGG_SUM on f64 — worker-partition order differs from the serial
	// left-fold; rounding can shift within 1 ULP (same policy as
	// parallel_reduce_test).
	if !floatWithinULP(toFloat(gotRow["total"]), toFloat(wantRow["total"]), 1) {
		t.Errorf("AGG_SUM beyond 1 ULP: got=%v want=%v", gotRow["total"], wantRow["total"])
	}

	if serialResp.Metadata == nil || parallelResp.Metadata == nil {
		t.Fatalf("expected metadata on both sides")
	}
	if serialResp.Metadata.TotalRows != parallelResp.Metadata.TotalRows {
		t.Errorf("TotalRows differs: serial=%d parallel=%d",
			serialResp.Metadata.TotalRows, parallelResp.Metadata.TotalRows)
	}
	if serialResp.Metadata.FilteredRows != parallelResp.Metadata.FilteredRows {
		t.Errorf("FilteredRows differs: serial=%d parallel=%d",
			serialResp.Metadata.FilteredRows, parallelResp.Metadata.FilteredRows)
	}
	if serialResp.Metadata.CohortFile != parallelResp.Metadata.CohortFile {
		t.Errorf("CohortFile differs: serial=%q parallel=%q",
			serialResp.Metadata.CohortFile, parallelResp.Metadata.CohortFile)
	}
}

// TestProcess_ParallelDecode_BelowThresholdBails verifies the threshold
// gate at the Process level: a cohort sized below the threshold takes
// the serial scanIter path regardless of DecodeWorkers. We assert
// shape correctness against a forced-serial baseline so any drift
// signals a dispatch leak.
func TestProcess_ParallelDecode_BelowThresholdBails(t *testing.T) {
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

// TestProcess_ParallelDecode_MemMapFsBails verifies the MemMapFs gate
// at the Process level: even with DecodeWorkers=4 and a cohort above
// the threshold, the resolveRealPath probe misses on MemMapFs and
// Process drops to the serial scanIter path. Correctness == serial.
func TestProcess_ParallelDecode_MemMapFsBails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping MemMapFs bail in -short mode")
	}

	path, cfg := writeMemMapCohort(t, parallelDecodeRecordThreshold+512)
	ctx := context.Background()

	mkReq := func() *types.Request { return mergeableRequest(path) }

	svcSerial := New(cfg)
	svcSerial.SetDecodeWorkers(1)
	serialResp, err := svcSerial.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}

	svcEligible := New(cfg)
	svcEligible.SetDecodeWorkers(4)
	memResp, err := svcEligible.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("MemMapFs Process: %v", err)
	}
	if !reflect.DeepEqual(memResp.Data, serialResp.Data) {
		t.Errorf("MemMapFs bail path drifted from serial:\n got=%v\nwant=%v",
			memResp.Data, serialResp.Data)
	}
}

// TestProcess_ParallelDecode_NonMergeableBails verifies the
// CanMergeRequest gate at the Process level. AGG_MEDIAN forces the
// buffered path (percentile aggregator); the parallel reducer cannot
// merge median state, so the gate must reject and Process must take
// the serial scanIter path.
func TestProcess_ParallelDecode_NonMergeableBails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping non-mergeable bail in -short mode")
	}

	path, cfg := writeOsFsCohort(t, parallelDecodeRecordThreshold+512)
	ctx := context.Background()

	mkReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: path},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_MEDIAN, Field: "weight", Label: "med"},
			},
		}
	}

	svcSerial := New(cfg)
	svcSerial.SetDecodeWorkers(1)
	serialResp, err := svcSerial.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}

	svcEligible := New(cfg)
	svcEligible.SetDecodeWorkers(4)
	nonMergeResp, err := svcEligible.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("non-mergeable Process: %v", err)
	}
	// AGG_MEDIAN must produce identical output regardless of
	// DecodeWorkers because the gate rejected and both runs took the
	// same serial scanIter path.
	if !reflect.DeepEqual(nonMergeResp.Data, serialResp.Data) {
		t.Errorf("non-mergeable bail path drifted from serial:\n got=%v\nwant=%v",
			nonMergeResp.Data, serialResp.Data)
	}
}

// TestProcess_ParallelDecode_WorkersOneBails verifies the explicit
// DecodeWorkers=1 forces the serial path at the Process level, same
// behaviour as ShardWorkers=1.
func TestProcess_ParallelDecode_WorkersOneBails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping workers=1 bail in -short mode")
	}

	path, cfg := writeOsFsCohort(t, parallelDecodeRecordThreshold+512)
	ctx := context.Background()

	mkReq := func() *types.Request { return mergeableRequest(path) }

	// Both runs use DecodeWorkers=1 so the gate rejects on both sides.
	// Output must be identical between two independent serial runs;
	// this is a smoke test that the bail path is deterministic and
	// repeatable.
	svcA := New(cfg)
	svcA.SetDecodeWorkers(1)
	respA, err := svcA.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("Process A: %v", err)
	}

	svcB := New(cfg)
	svcB.SetDecodeWorkers(1)
	respB, err := svcB.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("Process B: %v", err)
	}

	if !reflect.DeepEqual(respA.Data, respB.Data) {
		t.Errorf("forced-serial Process drifted between runs:\nA=%v\nB=%v",
			respA.Data, respB.Data)
	}
}
