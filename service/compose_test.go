package service

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestCompose_MultipleRequests(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "data.pulse", schema, testRecords())
	svc := New(cfg)

	composed := &types.ComposedRequest{
		Requests: []*types.Request{
			{
				Cohort: &types.Cohort{Filename: "data.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "score", Label: "total"},
				},
			},
			{
				Cohort: &types.Cohort{Filename: "data.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_AVERAGE, Field: "score", Label: "avg"},
				},
			},
			{
				Cohort: &types.Cohort{Filename: "data.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_MIN, Field: "score", Label: "min"},
					{Type: types.AGG_MAX, Field: "score", Label: "max"},
				},
			},
		},
	}

	composedResp, err := svc.Compose(context.Background(), composed)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	responses := composedResp.Responses
	if len(responses) != 3 {
		t.Fatalf("response count = %d, want 3", len(responses))
	}

	// Check each response
	total, ok := responses[0].Data[0]["total"].(float64)
	if !ok || !floatClose(total, 150.0, 0.001) {
		t.Errorf("total = %v, want 150.0", responses[0].Data[0]["total"])
	}

	avg, ok := responses[1].Data[0]["avg"].(float64)
	if !ok || !floatClose(avg, 30.0, 0.001) {
		t.Errorf("avg = %v, want 30.0", responses[1].Data[0]["avg"])
	}

	minVal, ok := responses[2].Data[0]["min"].(float64)
	if !ok || !floatClose(minVal, 10.0, 0.001) {
		t.Errorf("min = %v, want 10.0", responses[2].Data[0]["min"])
	}

	maxVal, ok := responses[2].Data[0]["max"].(float64)
	if !ok || !floatClose(maxVal, 50.0, 0.001) {
		t.Errorf("max = %v, want 50.0", responses[2].Data[0]["max"])
	}
}

func TestCompose_SharedCohort(t *testing.T) {
	schema := testSchema()
	records := [][]uint64{
		{1, math.Float64bits(100.0)},
		{2, math.Float64bits(200.0)},
		{3, math.Float64bits(300.0)},
	}
	cfg := setupTestFS(t, "shared.pulse", schema, records)
	svc := New(cfg)

	// Two requests against the same cohort file
	composed := &types.ComposedRequest{
		Requests: []*types.Request{
			{
				Cohort: &types.Cohort{Filename: "shared.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "score", Label: "n"},
				},
			},
			{
				Cohort: &types.Cohort{Filename: "shared.pulse"},
				Filterers: []*types.Filterer{
					{Type: types.FILTER_RANGE, Field: "score", Values: []string{"150", "350"}},
				},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "score", Label: "filtered_n"},
				},
			},
		},
	}

	composedResp, err := svc.Compose(context.Background(), composed)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	responses := composedResp.Responses
	if len(responses) != 2 {
		t.Fatalf("response count = %d, want 2", len(responses))
	}

	n, ok := responses[0].Data[0]["n"].(float64)
	if !ok || !floatClose(n, 3.0, 0.001) {
		t.Errorf("n = %v, want 3.0", responses[0].Data[0]["n"])
	}

	filteredN, ok := responses[1].Data[0]["filtered_n"].(float64)
	if !ok || !floatClose(filteredN, 2.0, 0.001) {
		t.Errorf("filtered_n = %v, want 2.0", responses[1].Data[0]["filtered_n"])
	}

	// Both should report the same cohort file
	if responses[0].Metadata.CohortFile != "shared.pulse" {
		t.Errorf("response[0] CohortFile = %q, want %q", responses[0].Metadata.CohortFile, "shared.pulse")
	}
	if responses[1].Metadata.CohortFile != "shared.pulse" {
		t.Errorf("response[1] CohortFile = %q, want %q", responses[1].Metadata.CohortFile, "shared.pulse")
	}
}
