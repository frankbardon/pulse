package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestStreamingIterator_PlanCacheReusedForSameRetainedSet verifies
// that SetProjection builds the DecodePlan once and caches it by the
// sorted retained-set key, so re-calling SetProjection with a filter
// that resolves to the same set reuses the same plan reference.
func TestStreamingIterator_PlanCacheReusedForSameRetainedSet(t *testing.T) {
	schema := wideSchema()
	cfg := setupTestFS(t, "wide.pulse", schema, wideRecords())

	iter := newStreamingIterator(cfg.Fs(), "wide.pulse", schema)
	defer iter.Close()

	keepA := func(name string) bool { return name == "id" || name == "y" }
	iter.SetProjection(keepA, 2)
	if iter.plan == nil {
		t.Fatalf("plan should be built when projection installs")
	}
	planFirst := iter.plan
	keyFirst := iter.planKey

	// Re-install with a different filter closure but the same retained
	// set (same fields, different order shouldn't matter — sort makes
	// the key stable).
	keepB := func(name string) bool { return name == "y" || name == "id" }
	iter.SetProjection(keepB, 2)
	if iter.planKey != keyFirst {
		t.Errorf("plan key changed for same retained set: %q vs %q", iter.planKey, keyFirst)
	}
	if iter.plan != planFirst {
		t.Errorf("plan reference rebuilt for same retained set")
	}

	// Install with a different retained set ⇒ new plan.
	keepC := func(name string) bool { return name == "v" }
	iter.SetProjection(keepC, 1)
	if iter.planKey == keyFirst {
		t.Errorf("plan key should change for different retained set")
	}
	if iter.plan == planFirst {
		t.Errorf("plan reference should change for different retained set")
	}
}

// TestStreamingIterator_NilProjectionClearsPlan verifies that
// SetProjection(nil, 0) clears any cached plan and reverts to the
// full-decode path used today.
func TestStreamingIterator_NilProjectionClearsPlan(t *testing.T) {
	schema := wideSchema()
	cfg := setupTestFS(t, "wide.pulse", schema, wideRecords())

	iter := newStreamingIterator(cfg.Fs(), "wide.pulse", schema)
	defer iter.Close()

	iter.SetProjection(func(name string) bool { return name == "y" }, 1)
	if iter.plan == nil {
		t.Fatalf("plan should be built when projection installs")
	}

	iter.SetProjection(nil, 0)
	if iter.plan != nil {
		t.Errorf("plan should be cleared when projection is nilled")
	}
	if iter.planKey != "" {
		t.Errorf("plan key should be empty when projection is nilled")
	}
	if iter.project != nil {
		t.Errorf("project filter should be cleared")
	}
}

func TestStreamingIterator_PlanDrivenDecodeMatchesFullDecode(t *testing.T) {
	schema := wideSchema()
	cfg := setupTestFS(t, "wide.pulse", schema, wideRecords())

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "wide.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "y", Label: "sum_y"},
		},
	}

	svcOff := New(cfg)
	respOff, err := svcOff.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process (projection off): %v", err)
	}

	svcOn := New(cfg)
	svcOn.SetProjectBufferedFields(true)
	respOn, err := svcOn.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process (projection on, plan-driven): %v", err)
	}

	if !reflect.DeepEqual(respOff.Data, respOn.Data) {
		t.Fatalf("plan-driven decode != full decode:\n full: %v\n plan: %v",
			respOff.Data, respOn.Data)
	}
}

// TestStreamingIterator_PlanReadsTrueOnEmptyRetained verifies that an
// edge-case retained set produces a single-SkipBytes plan that drains
// the whole stride without reading any field. Defensive — the service
// layer never installs an empty retained set (size==0 short-circuits
// in installProjection), but the iterator API allows it.
func TestStreamingIterator_PlanReadsTrueOnEmptyRetained(t *testing.T) {
	schema := wideSchema()
	plan, err := schema.BuildDecodePlan(nil)
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	if len(plan.Segments) != 1 {
		t.Fatalf("empty retained set ⇒ 1 segment, got %d", len(plan.Segments))
	}
	sb, ok := plan.Segments[0].(encoding.SkipBytes)
	if !ok {
		t.Fatalf("empty retained set ⇒ SkipBytes, got %T", plan.Segments[0])
	}
	if sb.N != schema.RecordByteSize() {
		t.Errorf("empty-retained skip = %d, want %d", sb.N, schema.RecordByteSize())
	}
}
