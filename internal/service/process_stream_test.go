package service

import (
	"context"
	"errors"
	"math"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestProcessStream_DrainsRowsInOrder(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
		},
	}

	iter, err := svc.ProcessStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	defer iter.Close()

	rows := drain(t, iter)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	val, ok := rows[0]["avg_score"].(float64)
	if !ok || !floatClose(val, 30.0, 0.001) {
		t.Errorf("avg_score = %v, want 30.0", rows[0]["avg_score"])
	}
}

func TestProcessStream_GroupedRowsAvailable(t *testing.T) {
	// Group by score (numeric, not categorical here, but GROUP_RANGE works
	// on numeric fields). Five rows × 10..50 → groups by 25.
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, [][]uint64{
		{1, math.Float64bits(10)},
		{2, math.Float64bits(15)},
		{3, math.Float64bits(30)},
		{4, math.Float64bits(45)},
		{5, math.Float64bits(50)},
	})
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "score", Interval: 25}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}

	iter, err := svc.ProcessStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	defer iter.Close()

	rows := drain(t, iter)
	if len(rows) < 2 {
		t.Fatalf("expected at least 2 group rows, got %d", len(rows))
	}
}

func TestProcessStream_MetadataAvailableAfterDrain(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}

	iter, err := svc.ProcessStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	defer iter.Close()

	_ = drain(t, iter)
	meta := iter.Metadata()
	if meta == nil {
		t.Fatal("metadata is nil after drain")
	}
	if meta.TotalRows != 5 {
		t.Errorf("TotalRows = %d, want 5", meta.TotalRows)
	}
	if meta.CohortFile != "test.pulse" {
		t.Errorf("CohortFile = %q, want test.pulse", meta.CohortFile)
	}
}

func TestProcessStream_NilCohortReturnsValidationError(t *testing.T) {
	svc := New(setupTestFS(t, "test.pulse", testSchema(), testRecords()))
	_, err := svc.ProcessStream(context.Background(), &types.Request{})
	if err == nil {
		t.Fatal("expected validation error for nil cohort")
	}
	if !pulseerrors.HasCode(err, pulseerrors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION, got %v", err)
	}
}

func TestProcessStream_CtxCancelledDuringDrain(t *testing.T) {
	svc := New(setupTestFS(t, "test.pulse", testSchema(), testRecords()))
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
	}
	iter, err := svc.ProcessStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	defer iter.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = iter.Next(ctx)
	if err == nil {
		t.Fatal("expected context error from Next on cancelled ctx")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestProcessStream_CloseIsIdempotent(t *testing.T) {
	svc := New(setupTestFS(t, "test.pulse", testSchema(), testRecords()))
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
	}
	iter, err := svc.ProcessStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	if err := iter.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := iter.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	_, _, err = iter.Next(context.Background())
	if err == nil {
		t.Error("expected error from Next after Close")
	}
}

func drain(t *testing.T, iter RowIter) []Row {
	t.Helper()
	var out []Row
	for {
		row, ok, err := iter.Next(context.Background())
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !ok {
			return out
		}
		out = append(out, row)
	}
}
