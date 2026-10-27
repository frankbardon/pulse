package service

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/frankbardon/pulse/observe"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// attributeSlotsRequest is k ATTR_FORMULA month flags over the
// retention-shape fixture, labelled a00 … a(k-1).
func attributeSlotsRequest(path string, k int) *types.Request {
	req := &types.Request{Cohort: &types.Cohort{Filename: path},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "customer_id"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "net_amount", Label: "spend"}}}
	for i := range k {
		req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_FORMULA, Field: "order_date",
			Label: fmt.Sprintf("a%02d", i), Expression: "order_date >= 20089 ? 1 : 0"})
	}
	return req
}

// TestAttributeSlots_AllocsFlatInAttributeCount (U51 E5-S4 (a)): the
// record a run mints per row reserves a positional slot for each of its
// attribute labels (recordBindingFor's extras), so writing a row's
// attribute outputs allocates nothing however many attributes there are
// — on the per-shard parallel arm (shardDecodeFor), the parallel-decode
// arm (attributeLabels into the same binding) and the serial iterators
// (applyProjection → SetExtras). Before, every output landed in a
// per-record overflow map that grew with the attribute count.
func TestAttributeSlots_AllocsFlatInAttributeCount(t *testing.T) {
	// serial: testing.AllocsPerRun counts process-wide allocations, which concurrent tests inflate

	dir := t.TempDir()
	path := retentionShapeArchive(t, dir, 4, 500, 300)
	svc := New(matrixScaleOSConfig(t, dir))
	ctx := context.Background()
	cohort := mustOpen(t, svc, path)
	schema := cohort.Schema()
	labels := make([]string, 13)
	for i := range labels {
		labels[i] = fmt.Sprintf("a%02d", i)
	}
	write := func(rec *processing.Record, k int) {
		for i := range k {
			processing.SetAttributeOutput(rec, labels[i], float64(i%3), true)
		}
	}
	shardArm := func(k int) float64 {
		dec := svc.shardDecodeFor(ctx, attributeSlotsRequest(path, k), schema)
		return testing.AllocsPerRun(200, func() { write(dec.binding.NewRecord(), k) })
	}
	serialArm := func(k int) float64 {
		iter := svc.newScanIter(cohort, path)
		defer iter.Close()
		svc.applyProjection(ctx, iter, attributeSlotsRequest(path, k), schema)
		return testing.AllocsPerRun(200, func() {
			if !iter.Next() {
				iter.Reset()
				iter.Next()
			}
			write(iter.Record(), k)
		})
	}
	for name, arm := range map[string]func(int) float64{"shard": shardArm, "serial": serialArm} {
		one, many := arm(1), arm(13)
		t.Logf("%s arm: %.1f allocations a row at 1 attribute, %.1f at 13", name, one, many)
		if many != one {
			t.Errorf("%s arm: %.1f allocations a row at 13 attributes, %.1f at 1 — the outputs reach the overflow map", name, many, one)
		}
	}
	// The labels list is the request's, declared or defaulted.
	if got := attributeLabels(attributeSlotsRequest(path, 13)); fmt.Sprint(got) != fmt.Sprint(labels) {
		t.Fatalf("attributeLabels = %v, want %v", got, labels)
	}
}

// TestAttributeSlots_ProcessAllocsFlat runs whole Process calls with 1
// and 13 row-local ATTR_DATE_PART attributes (an allocation-free row
// computation) on every arm that mints a record per row — per-shard
// parallel, parallel decode, and the serial buffered scan over an
// archive and a single file — and pins that the allocations a row do
// not grow with the attribute count: every arm's binding reserves the
// labels (U51 E5-S4 (a)).
func TestAttributeSlots_ProcessAllocsFlat(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	dir := t.TempDir()
	archive := retentionShapeArchive(t, dir, 4, 30_000, 2_000)
	single := retentionShapeCohort(t, dir, 120_000, 2_000)
	mk := func(path string, k int, buffered bool) *types.Request {
		req := &types.Request{Cohort: &types.Cohort{Filename: path},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "customer_id"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "net_amount", Label: "spend"}}}
		if buffered {
			// Not streamable, not mergeable: the serial buffered scan.
			req.Aggregations[0].Type = types.AGG_MEDIAN
		}
		for i := range k {
			req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_DATE_PART, Field: "order_date",
				Label: fmt.Sprintf("p%02d", i), Params: []byte(`{"part": "month"}`)})
		}
		return req
	}
	arms := []struct {
		name     string
		path     string
		shards   int
		decode   int
		buffered bool
	}{
		{"shard-parallel", archive, 4, 1, false},
		{"parallel-decode", single, 1, 4, false},
		{"serial-archive", archive, 1, 1, true},
		{"serial-single", single, 1, 1, true},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			svc := New(matrixScaleOSConfig(t, dir))
			svc.SetShardWorkers(arm.shards)
			svc.SetDecodeWorkers(arm.decode)
			svc.SetDisableComponents(true)
			perRow := func(k int) float64 {
				req := mk(arm.path, k, arm.buffered)
				ei := NewExecInfo(time.Now())
				ctx := WithExecInfo(context.Background(), ei)
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)
				if _, err := svc.Process(ctx, req); err != nil {
					t.Fatal(err)
				}
				runtime.ReadMemStats(&after)
				if arm.name == "shard-parallel" && ei.arm != observe.ArmShardParallel ||
					arm.name == "parallel-decode" && ei.arm != observe.ArmParallelDecode {
					t.Fatalf("ran on arm %q", ei.arm)
				}
				return float64(after.Mallocs-before.Mallocs) / 120_000
			}
			one, many := perRow(1), perRow(13)
			t.Logf("%.2f allocations a row at 1 attribute, %.2f at 13", one, many)
			if many-one > 0.5 {
				t.Fatalf("%.2f allocations a row at 13 attributes vs %.2f at 1: the outputs reach the overflow map", many, one)
			}
		})
	}
}
