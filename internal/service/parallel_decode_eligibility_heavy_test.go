//go:build heavy

package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestProcess_ParallelDecode_ByteEqualToSerial(t *testing.T) {
	t.Parallel()

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
	// AGG_SUM on f64 — the binned reproducible sum is bit-identical
	// to the serial run under any worker partition.
	if !reflect.DeepEqual(gotRow["total"], wantRow["total"]) {
		t.Errorf("AGG_SUM differs: got=%v want=%v", gotRow["total"], wantRow["total"])
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

// TestProcess_ParallelDecode_MemMapFsBails verifies the MemMapFs gate
// at the Process level: even with DecodeWorkers=4 and a cohort above
// the threshold, the resolveRealPath probe misses on MemMapFs and
// Process drops to the serial scanIter path. Correctness == serial.
func TestProcess_ParallelDecode_MemMapFsBails(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
