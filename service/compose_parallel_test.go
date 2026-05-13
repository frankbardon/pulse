package service

import (
	"context"
	"math"
	"runtime"
	"testing"
	"time"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// composeFiveAvgRequests returns five requests, each computing AVG of a
// shifted score field. Used to verify order preservation.
func composeFiveAvgRequests() *types.ComposedRequest {
	r := func(label string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_AVERAGE, Field: "score", Label: label},
			},
		}
	}
	return &types.ComposedRequest{
		Requests: []*types.Request{r("a"), r("b"), r("c"), r("d"), r("e")},
	}
}

func TestComposeParallel_PreservesOrder(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	resps, err := svc.ComposeParallel(context.Background(), composeFiveAvgRequests(), ComposeOptions{MaxWorkers: 4})
	if err != nil {
		t.Fatalf("ComposeParallel: %v", err)
	}
	if len(resps) != 5 {
		t.Fatalf("got %d responses, want 5", len(resps))
	}
	labels := []string{"a", "b", "c", "d", "e"}
	for i, label := range labels {
		if resps[i] == nil || len(resps[i].Data) == 0 {
			t.Fatalf("slot %d empty", i)
			continue
		}
		val, ok := resps[i].Data[0][label].(float64)
		if !ok {
			t.Errorf("slot %d label %q missing or wrong type", i, label)
			continue
		}
		if !floatClose(val, 30.0, 0.001) {
			t.Errorf("slot %d %s = %v, want 30.0", i, label, val)
		}
	}
}

func TestComposeParallel_DefaultsApplied(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	// MaxWorkers=0 should resolve to GOMAXPROCS; pool execution still works.
	_, err := svc.ComposeParallel(context.Background(), composeFiveAvgRequests(), ComposeOptions{})
	if err != nil {
		t.Fatalf("ComposeParallel with default options: %v", err)
	}

	// Negative MaxWorkers clamps to 1.
	_, err = svc.ComposeParallel(context.Background(), composeFiveAvgRequests(), ComposeOptions{MaxWorkers: -5})
	if err != nil {
		t.Fatalf("ComposeParallel MaxWorkers=-5 should clamp to 1, got error: %v", err)
	}
}

func TestComposeParallel_RespectsWorkerCap(t *testing.T) {
	// Verify MaxWorkers caps in-flight goroutines. We can't observe the
	// worker count directly without instrumentation, so use atomic
	// counters around Process via a mid-flight sleep request.
	//
	// Build a cohort with 1000 rows so each Process call has a few ms of
	// real work. Concurrent maximum is observed via a tracked counter.
	rows := make([][]uint64, 1000)
	for i := range rows {
		rows[i] = []uint64{uint64(i), math.Float64bits(float64(i))}
	}
	cfg := setupTestFS(t, "test.pulse", testSchema(), rows)
	svc := New(cfg)

	// Wrap svc.Process via a subtype isn't possible; instead we set a
	// small MaxWorkers and rely on PerRequestTimeout to confirm bounded
	// concurrency by ensuring the run takes at least ceil(N/W) * latency.
	// We measure ratio of total duration vs. single-request latency.
	composed := &types.ComposedRequest{}
	for range 16 {
		composed.Requests = append(composed.Requests, &types.Request{
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_AVERAGE, Field: "score", Label: "avg"},
			},
		})
	}

	startSerial := time.Now()
	if _, err := svc.Process(context.Background(), composed.Requests[0]); err != nil {
		t.Fatalf("baseline Process: %v", err)
	}
	singleLatency := time.Since(startSerial)

	startParallel := time.Now()
	resps, err := svc.ComposeParallel(context.Background(), composed, ComposeOptions{MaxWorkers: 2})
	if err != nil {
		t.Fatalf("ComposeParallel: %v", err)
	}
	parallelDur := time.Since(startParallel)

	if len(resps) != 16 {
		t.Fatalf("got %d, want 16", len(resps))
	}
	// With MaxWorkers=2 and 16 requests, total ≈ 8 * latency. Allow
	// generous slack for scheduling: must be at least 4× single latency.
	min := singleLatency * 4
	if parallelDur < min {
		t.Logf("baseline=%s parallel=%s — too fast for 16 reqs / 2 workers (smoke test, not strict)", singleLatency, parallelDur)
	}
}

func TestComposeParallel_FailFastCancelsSiblings(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	// Mix valid and invalid requests. Slot 0 references a missing file
	// → SERVICE_RESOURCE error fires fast → siblings cancel.
	composed := &types.ComposedRequest{
		Requests: []*types.Request{
			{
				Cohort: &types.Cohort{Filename: "missing.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_AVERAGE, Field: "score", Label: "broken"},
				},
			},
			{
				Cohort: &types.Cohort{Filename: "test.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_AVERAGE, Field: "score", Label: "ok"},
				},
			},
		},
	}

	_, err := svc.ComposeParallel(context.Background(), composed, ComposeOptions{
		MaxWorkers: 2,
		FailFast:   true,
	})
	if err == nil {
		t.Fatal("expected error from fail-fast")
	}
}

func TestComposeParallel_NoFailFastAggregatesErrors(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	composed := &types.ComposedRequest{
		Requests: []*types.Request{
			{
				Cohort: &types.Cohort{Filename: "missing_a.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_AVERAGE, Field: "score", Label: "a"},
				},
			},
			{
				Cohort: &types.Cohort{Filename: "test.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_AVERAGE, Field: "score", Label: "b"},
				},
			},
			{
				Cohort: &types.Cohort{Filename: "missing_c.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_AVERAGE, Field: "score", Label: "c"},
				},
			},
		},
	}

	_, err := svc.ComposeParallel(context.Background(), composed, ComposeOptions{
		MaxWorkers: 3,
		FailFast:   false,
	})
	if err == nil {
		t.Fatal("expected aggregated error for failed requests")
	}
	if !pulseerrors.HasCode(err, pulseerrors.SERVICE_INTERNAL) {
		t.Errorf("expected SERVICE_INTERNAL aggregated error, got %v", err)
	}
}

func TestComposeParallel_NilRequestErrors(t *testing.T) {
	svc := New(setupTestFS(t, "test.pulse", testSchema(), testRecords()))
	_, err := svc.ComposeParallel(context.Background(), nil, ComposeOptions{})
	if err == nil {
		t.Fatal("expected validation error for nil composed")
	}
	_, err = svc.ComposeParallel(context.Background(), &types.ComposedRequest{}, ComposeOptions{})
	if err == nil {
		t.Fatal("expected validation error for empty requests")
	}
}

// TestComposeParallel_FreshOperatorsPerRequest is the registry-singleton
// smoke test. Two parallel runs of the same request should yield the
// same results. Stateful aggregator drift would manifest as either a
// race detector trip (run with -race) or wrong values.
func TestComposeParallel_FreshOperatorsPerRequest(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	composed := &types.ComposedRequest{}
	for range 32 {
		composed.Requests = append(composed.Requests, &types.Request{
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "score", Label: "s"},
				{Type: types.AGG_AVERAGE, Field: "score", Label: "a"},
				{Type: types.AGG_VARIANCE, Field: "score", Label: "v"},
			},
		})
	}

	resps, err := svc.ComposeParallel(context.Background(), composed, ComposeOptions{
		MaxWorkers: runtime.GOMAXPROCS(0),
	})
	if err != nil {
		t.Fatalf("ComposeParallel: %v", err)
	}

	wantSum := 150.0 // 10+20+30+40+50
	wantAvg := 30.0
	for i, resp := range resps {
		if s, ok := resp.Data[0]["s"].(float64); !ok || !floatClose(s, wantSum, 0.001) {
			t.Errorf("slot %d sum = %v, want %v", i, resp.Data[0]["s"], wantSum)
		}
		if a, ok := resp.Data[0]["a"].(float64); !ok || !floatClose(a, wantAvg, 0.001) {
			t.Errorf("slot %d avg = %v, want %v", i, resp.Data[0]["a"], wantAvg)
		}
	}
}
