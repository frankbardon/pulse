package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestProcessor_RegressionBayesModifierRejected exercises the end-to-end
// rejection of REG_BAYES_LINEAR + any modifier. Phase 5 retired the
// not-implemented stub for OLS / GLM + modifiers; Bayes now rejects
// at spec validation because the posterior already conveys uncertainty
// (credible intervals via the Student-t marginal) and stepwise feature
// selection in a Bayesian setting is a posterior-based question that
// the conjugate-NIG engine doesn't support.
func TestProcessor_RegressionBayesModifierRejected(t *testing.T) {
	schema := numericSchema()
	records := makeRecords(schema, "score", []float64{10, 20, 30})

	cases := []struct {
		name string
		spec *types.RegressionSpec
	}{
		{"REG_BAYES_LINEAR bootstrap", &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "score", Predictors: []string{"score"}, Resample: "bootstrap"}},
		{"REG_BAYES_LINEAR jackknife", &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "score", Predictors: []string{"score"}, Resample: "jackknife"}},
		{"REG_BAYES_LINEAR stepwise", &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "score", Predictors: []string{"score"}, Selection: "stepwise", Criterion: "aic"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			proc := NewProcessor(schema)
			iter := NewSliceIterator(records)
			req := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_AVERAGE, Field: "score"},
				},
				Regressions: []*types.RegressionSpec{c.spec},
			}
			_, err := proc.Process(context.Background(), req, iter)
			if err == nil {
				t.Fatalf("Process returned nil error; want PROCESSING_CONFIG")
			}
			if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
				t.Errorf("Process error = %v; want PROCESSING_CONFIG", err)
			}
		})
	}
}

func TestProcessor_SimpleRequest(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)

	records := makeRecords(schema, "score", []float64{10, 20, 30})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
		},
	}

	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("response is nil")
	}
	if len(resp.Data) == 0 {
		t.Fatal("response data is empty")
	}

	val, ok := resp.Data[0]["avg_score"]
	if !ok {
		t.Fatal("avg_score not found in response")
	}
	fval, ok := val.(float64)
	if !ok {
		t.Fatalf("avg_score is not float64: %T", val)
	}
	if !floatClose(fval, 20.0, 0.001) {
		t.Errorf("avg_score = %f, want 20.0", fval)
	}

	if resp.Metadata == nil {
		t.Fatal("metadata is nil")
	}
	if resp.Metadata.TotalRows != 3 {
		t.Errorf("TotalRows = %d, want 3", resp.Metadata.TotalRows)
	}
}

func TestProcessor_MultipleAggregations(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)

	records := makeRecords(schema, "score", []float64{10, 20, 30})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg"},
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}

	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatal("response data is empty")
	}

	row := resp.Data[0]
	if avg, ok := row["avg"].(float64); !ok || !floatClose(avg, 20.0, 0.001) {
		t.Errorf("avg = %v, want 20.0", row["avg"])
	}
	if total, ok := row["total"].(float64); !ok || !floatClose(total, 60.0, 0.001) {
		t.Errorf("total = %v, want 60.0", row["total"])
	}
	if n, ok := row["n"].(float64); !ok || !floatClose(n, 3.0, 0.001) {
		t.Errorf("n = %v, want 3.0", row["n"])
	}
}

func TestProcessor_WithFilter(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)

	records := makeRecords(schema, "score", []float64{10, 20, 30, 40, 50})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "score", Values: []string{"20", "40"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}

	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	row := resp.Data[0]
	if n, ok := row["n"].(float64); !ok || !floatClose(n, 3.0, 0.001) {
		t.Errorf("filtered count = %v, want 3.0", row["n"])
	}
	if resp.Metadata.FilteredRows != 3 {
		t.Errorf("FilteredRows = %d, want 3", resp.Metadata.FilteredRows)
	}
}

func TestProcessor_WithGroup(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeU8},
			{Name: "score", Type: encoding.FieldTypeF64},
		},
	}
	proc := NewProcessor(schema)

	records := []*Record{
		NewRecord(schema, map[string]float64{"region": 1, "score": 10}),
		NewRecord(schema, map[string]float64{"region": 2, "score": 20}),
		NewRecord(schema, map[string]float64{"region": 1, "score": 30}),
		NewRecord(schema, map[string]float64{"region": 2, "score": 40}),
	}
	iter := NewSliceIterator(records)

	req := &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
		},
	}

	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Data) != 2 {
		t.Fatalf("response data length = %d, want 2", len(resp.Data))
	}

	// Find the row for region "1"
	found := false
	for _, row := range resp.Data {
		if row["region"] == "1" {
			avg, ok := row["avg_score"].(float64)
			if !ok {
				t.Errorf("avg_score is not float64")
				continue
			}
			if !floatClose(avg, 20.0, 0.001) {
				t.Errorf("region 1 avg_score = %f, want 20.0", avg)
			}
			found = true
		}
	}
	if !found {
		t.Error("region 1 not found in response")
	}
}

func TestProcessor_WithAttribute(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)

	records := makeRecords(schema, "score", []float64{10, 20, 30})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_NORMALIZED, Field: "score", Label: "norm_score"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "norm_score", Label: "avg_norm"},
		},
	}

	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatal("response data is empty")
	}

	avg, ok := resp.Data[0]["avg_norm"].(float64)
	if !ok {
		t.Fatalf("avg_norm not found or not float64")
	}
	// Normalized [10,20,30] = [0, 0.5, 1.0], average = 0.5
	if !floatClose(avg, 0.5, 0.001) {
		t.Errorf("avg_norm = %f, want 0.5", avg)
	}
}

func TestProcessor_ComposedRequest(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)

	records := makeRecords(schema, "score", []float64{10, 20, 30})

	composed := &types.ComposedRequest{
		Requests: []*types.Request{
			{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "score", Label: "total"},
				},
			},
			{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "score", Label: "n"},
				},
			},
		},
	}

	responses, err := proc.ProcessComposed(context.Background(), composed, records)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(responses) != 2 {
		t.Fatalf("response count = %d, want 2", len(responses))
	}

	total, ok := responses[0].Data[0]["total"].(float64)
	if !ok || !floatClose(total, 60.0, 0.001) {
		t.Errorf("total = %v, want 60.0", responses[0].Data[0]["total"])
	}

	n, ok := responses[1].Data[0]["n"].(float64)
	if !ok || !floatClose(n, 3.0, 0.001) {
		t.Errorf("n = %v, want 3.0", responses[1].Data[0]["n"])
	}
}
