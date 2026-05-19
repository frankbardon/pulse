package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// numericAggsOnCategorical lists the aggregation types that fire the
// PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL gate when paired with a
// categorical_* field. Mirrors descriptor.numericAggregations so a
// future expansion of that set lands a coverage gap here.
var numericAggsOnCategorical = []types.AggregationType{
	types.AGG_SUM,
	types.AGG_AVERAGE,
	types.AGG_MIN,
	types.AGG_MAX,
	types.AGG_STDDEV,
	types.AGG_RANGE,
	types.AGG_ZSCORE,
	types.AGG_MEDIAN,
	types.AGG_VARIANCE,
	types.AGG_SKEWNESS,
	types.AGG_KURTOSIS,
	types.AGG_PERCENTILE,
}

func categoricalCohortConfig(t *testing.T) (string, *encoding.Schema) {
	t.Helper()
	dict := encoding.NewDictionary()
	dict.Add("red")
	dict.Add("green")
	dict.Add("blue")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "color", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
		},
	}
	return "test.pulse", schema
}

// TestService_Process_CategoricalAggregation_StrictErrors verifies that
// every numeric aggregation in numericAggsOnCategorical fails fast under
// Service strict mode when pointed at a categorical field.
func TestService_Process_CategoricalAggregation_StrictErrors(t *testing.T) {
	path, schema := categoricalCohortConfig(t)
	records := [][]uint64{{0}, {1}, {0}, {2}}
	cfg := setupTestFS(t, path, schema, records)

	for _, aggType := range numericAggsOnCategorical {
		t.Run(string(aggType), func(t *testing.T) {
			svc := New(cfg)
			svc.SetStrict(true)
			req := &types.Request{
				Cohort: &types.Cohort{Filename: path},
				Aggregations: []*types.Aggregation{
					{Type: aggType, Field: "color", Label: "x"},
				},
			}
			_, err := svc.Process(context.Background(), req)
			if err == nil {
				t.Fatalf("strict Process(%s on categorical) should fail", aggType)
			}
			if !errors.HasCode(err, errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL) {
				t.Errorf("expected PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL, got: %v", err)
			}
		})
	}
}

// TestService_Process_CategoricalAggregation_NonStrictRuns confirms the
// default (non-strict) Process path does not block — the warning is
// advisory and surfaces through the descriptor envelope at the CLI
// boundary, not through Process's return value.
func TestService_Process_CategoricalAggregation_NonStrictRuns(t *testing.T) {
	path, schema := categoricalCohortConfig(t)
	records := [][]uint64{{0}, {1}, {0}, {2}}
	cfg := setupTestFS(t, path, schema, records)
	svc := New(cfg) // strict defaults to false

	req := &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "color", Label: "x"},
		},
	}
	_, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("non-strict Process should not error on categorical-numeric agg: %v", err)
	}
}

// TestService_Process_StrictPreservesValidRequests ensures strict mode
// does not regress the happy path: a numeric aggregation on a numeric
// field still runs to completion.
func TestService_Process_StrictPreservesValidRequests(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)
	svc.SetStrict(true)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}
	if _, err := svc.Process(context.Background(), req); err != nil {
		t.Fatalf("strict mode should not affect numeric-on-numeric: %v", err)
	}
}
