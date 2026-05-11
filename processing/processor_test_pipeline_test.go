package processing

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestProcessor_Tier1Streaming_TTest verifies that a one-sample t-test
// runs alongside an online aggregator in the streaming path and that
// the response carries both `data` and `tests` slots.
func TestProcessor_Tier1Streaming_TTest(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "score", []float64{10, 12, 14, 16, 18})
	iter := NewSliceIterator(records)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
		},
		Tests: []*types.Test{
			{
				Type:   types.TEST_T,
				Field:  "score",
				Alpha:  0.05,
				Label:  "score_vs_zero",
				Params: json.RawMessage(`{"mu": 0.0}`),
			},
		},
	}
	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if proc.LastPath() != PathStreaming {
		t.Errorf("LastPath = %s, want streaming", proc.LastPath())
	}
	if len(resp.Data) != 1 {
		t.Fatalf("Data length = %d, want 1", len(resp.Data))
	}
	if got := resp.Data[0]["avg_score"].(float64); math.Abs(got-14) > 1e-9 {
		t.Errorf("avg_score = %g, want 14", got)
	}
	if len(resp.Tests) != 1 {
		t.Fatalf("Tests length = %d, want 1", len(resp.Tests))
	}
	tr := resp.Tests[0]
	if tr.Label != "score_vs_zero" {
		t.Errorf("Tests[0].Label = %s, want score_vs_zero", tr.Label)
	}
	if tr.Type != types.TEST_T {
		t.Errorf("Tests[0].Type = %s, want TEST_T", tr.Type)
	}
	if !tr.RejectNull {
		t.Errorf("Tests[0].RejectNull = false; expected true (mean=14, μ=0 with n=5)")
	}
	if tr.PValue > 1e-3 {
		t.Errorf("Tests[0].PValue = %g, want very small", tr.PValue)
	}
}

// TestProcessor_Tier1Buffered_TTest verifies that the same test still
// runs when something else in the request forces the buffered path
// (here: AGG_MEDIAN which is not streamable).
func TestProcessor_Tier1Buffered_TTest(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "score", []float64{10, 12, 14, 16, 18})
	iter := NewSliceIterator(records)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_MEDIAN, Field: "score", Label: "med"},
		},
		Tests: []*types.Test{
			{
				Type:   types.TEST_T,
				Field:  "score",
				Alpha:  0.05,
				Params: json.RawMessage(`{"mu": 0.0}`),
			},
		},
	}
	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if proc.LastPath() != PathBuffered {
		t.Errorf("LastPath = %s, want buffered", proc.LastPath())
	}
	if len(resp.Tests) != 1 {
		t.Fatalf("Tests length = %d, want 1", len(resp.Tests))
	}
	if !resp.Tests[0].RejectNull {
		t.Error("buffered tier-1 t-test failed to reject null on clear effect")
	}
}

// TestProcessor_UnknownRowTestType surfaces PULSE_TEST_UNKNOWN_TYPE when
// a test references a type without a row-test factory. TEST_TREND is
// tier-2 only — no row-test factory is registered for it.
func TestProcessor_UnknownRowTestType(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "score", []float64{1, 2, 3})
	iter := NewSliceIterator(records)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score"},
		},
		Tests: []*types.Test{
			{Type: types.TEST_TREND, Field: "score"},
		},
	}
	_, err := proc.Process(context.Background(), req, iter)
	if err == nil {
		t.Fatal("expected PULSE_TEST_UNKNOWN_TYPE, got nil")
	}
	if !containsCode(err, "PULSE_TEST_UNKNOWN_TYPE") {
		t.Errorf("error = %v, want PULSE_TEST_UNKNOWN_TYPE", err)
	}
}

// TestProcessor_PostTestUnknownType verifies tier-2 surfaces
// PULSE_TEST_UNKNOWN_TYPE for a type with no post-test factory.
// TEST_T is registered as a tier-1 row test only.
func TestProcessor_PostTestUnknownType(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)
	records := makeRecords(schema, "score", []float64{1, 2, 3})
	iter := NewSliceIterator(records)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score"},
		},
		PostTests: []*types.Test{
			{Type: types.TEST_T, Field: "score"},
		},
	}
	_, err := proc.Process(context.Background(), req, iter)
	if err == nil {
		t.Fatal("expected PULSE_TEST_UNKNOWN_TYPE, got nil")
	}
	if !containsCode(err, "PULSE_TEST_UNKNOWN_TYPE") {
		t.Errorf("error = %v, want PULSE_TEST_UNKNOWN_TYPE", err)
	}
}
