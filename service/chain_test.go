package service

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestProcessChain_TwoStageByteEqualToManual validates that a
// 2-stage chain (filter + aggregate → re-filter + re-aggregate)
// produces the same numeric result as running stage 1 to a manual
// intermediate map and re-running stage 2 against it.
func TestProcessChain_TwoStageByteEqualToManual(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)
	ctx := context.Background()

	// Stage 1: filter score > 20, sum score per category.
	// Stage 2: chain on the aggregate, filter sum < 90.
	stage1 := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "score", Values: []string{"20", "100"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum_score"},
		},
	}
	stage2 := &types.Request{
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "sum_score", Values: []string{"0", "200"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "sum_score", Label: "n"},
		},
	}

	chain := &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Stages: []*types.ChainStage{
			{Name: "filter_sum", Request: stage1},
			{Name: "count_results", Request: stage2},
		},
	}

	resp, err := svc.ProcessChain(ctx, chain)
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	if resp.Final == nil || len(resp.Final.Data) == 0 {
		t.Fatalf("final empty: %#v", resp)
	}
	if got := len(resp.Stages); got != 2 {
		t.Fatalf("stages length = %d, want 2", got)
	}

	// FILTER_RANGE [20, 100] keeps 20+30+40+50 = 140.
	if got := resp.Stages[0].Data[0]["sum_score"].(float64); !floatClose(got, 140.0, 0.001) {
		t.Errorf("stage1 sum_score = %v, want 140.0", got)
	}
	// Stage 2 counts that 1 row. Filter 0<sum<200 keeps it.
	if got := resp.Final.Data[0]["n"].(float64); !floatClose(got, 1.0, 0.001) {
		t.Errorf("stage2 count = %v, want 1.0", got)
	}
}

// TestProcessChain_NonMergeableMiddleStageReturnsError validates that
// the chain executor refuses a non-mergeable stage and surfaces the
// offending stage index.
func TestProcessChain_NonMergeableMiddleStageReturnsError(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)
	ctx := context.Background()

	mergeableStage := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum_score"},
		},
	}
	// AGG_MEDIAN is not mergeable; chain gate must reject.
	badStage := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_MEDIAN, Field: "sum_score", Label: "med"},
		},
	}

	chain := &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Stages: []*types.ChainStage{
			{Name: "ok", Request: mergeableStage},
			{Name: "median_blocked", Request: badStage},
		},
	}

	_, err := svc.ProcessChain(ctx, chain)
	if err == nil {
		t.Fatalf("expected PULSE_CHAIN_NOT_MERGEABLE")
	}
	if !errors.HasCode(err, errors.PULSE_CHAIN_NOT_MERGEABLE) {
		t.Fatalf("expected PULSE_CHAIN_NOT_MERGEABLE, got: %v", err)
	}
}

// TestProcessChain_FrequencyAggregatorRejected validates that
// AGG_FREQUENCY — mergeable but emits a map — fails the chain gate.
func TestProcessChain_FrequencyAggregatorRejected(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)
	ctx := context.Background()

	chain := &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Stages: []*types.ChainStage{
			{
				Name: "freq",
				Request: &types.Request{
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_FREQUENCY, Field: "id", Label: "freq_id"},
					},
				},
			},
			{
				Name: "noop",
				Request: &types.Request{
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_COUNT, Field: "freq_id", Label: "n"},
					},
				},
			},
		},
	}

	_, err := svc.ProcessChain(ctx, chain)
	if err == nil {
		t.Fatalf("expected PULSE_CHAIN_NOT_MERGEABLE for AGG_FREQUENCY chain stage")
	}
	if !errors.HasCode(err, errors.PULSE_CHAIN_NOT_MERGEABLE) {
		t.Fatalf("expected PULSE_CHAIN_NOT_MERGEABLE, got %v", err)
	}
}

// TestProcessChain_EmptyStagesError validates the empty-chain guard.
func TestProcessChain_EmptyStagesError(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)
	ctx := context.Background()

	_, err := svc.ProcessChain(ctx, &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Stages: nil,
	})
	if err == nil || !errors.HasCode(err, errors.PULSE_CHAIN_EMPTY) {
		t.Fatalf("expected PULSE_CHAIN_EMPTY, got: %v", err)
	}
}

// TestProcessChain_FiveStageScalarPipeline validates a longer chain
// where each stage filters or re-aggregates the prior stage's
// rows. Builds confidence that the synth-schema + slice-iterator
// machinery handles repeated re-materialization.
func TestProcessChain_FiveStageScalarPipeline(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	records := [][]uint64{
		{1, math.Float64bits(10.0)},
		{1, math.Float64bits(20.0)},
		{2, math.Float64bits(30.0)},
		{2, math.Float64bits(40.0)},
		{3, math.Float64bits(50.0)},
	}
	cfg := setupTestFS(t, "test.pulse", schema, records)
	svc := New(cfg)
	ctx := context.Background()

	stages := []*types.ChainStage{
		{
			Name: "group_sum",
			Request: &types.Request{
				Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "score", Label: "s"},
				},
			},
		},
		{
			Name: "filter_low",
			Request: &types.Request{
				Filterers: []*types.Filterer{
					{Type: types.FILTER_RANGE, Field: "s", Values: []string{"25", "1000"}},
				},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "s", Label: "ss"},
				},
			},
		},
		{
			Name: "double_count",
			Request: &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "ss", Label: "n"},
				},
			},
		},
		{
			Name: "min_max_n",
			Request: &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_MIN, Field: "n", Label: "mn"},
					{Type: types.AGG_MAX, Field: "n", Label: "mx"},
				},
			},
		},
		{
			Name: "final_sum",
			Request: &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "mn", Label: "total"},
				},
			},
		},
	}

	chain := &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Stages: stages,
	}

	resp, err := svc.ProcessChain(ctx, chain)
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	if len(resp.Stages) != 5 {
		t.Fatalf("expected 5 stages, got %d", len(resp.Stages))
	}
	if resp.Final == nil || len(resp.Final.Data) == 0 {
		t.Fatalf("final empty")
	}
	// stage1: region 1 -> 30, region 2 -> 70, region 3 -> 50
	// stage2 filter s>25: keeps 70, 50 -> sum ss=120
	// stage3 count rows of stage2: 1
	// stage4 min(1)=1, max(1)=1
	// stage5 sum(1) = 1
	if got := resp.Final.Data[0]["total"].(float64); !floatClose(got, 1.0, 0.001) {
		t.Errorf("final total = %v, want 1.0", got)
	}
}
