package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// numericAggsOnCategorical lists the aggregation types whose figure is
// arithmetic on the field's stored number — over a categorical, its
// dictionary index — which the accepted-types table refuses
// PULSE_FIELD_TYPE_UNSUPPORTED (E2-S4).
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
	types.AGG_DISTINCT_SUM,
	types.AGG_CI_LOWER,
	types.AGG_CI_UPPER,
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

// TestService_Process_CategoricalAggregation_Refused verifies that
// every numeric aggregation in numericAggsOnCategorical is refused
// PULSE_FIELD_TYPE_UNSUPPORTED when pointed at a categorical field,
// strict or not — the refusal is the runtime constructor's, not a
// promoted warning.
func TestService_Process_CategoricalAggregation_Refused(t *testing.T) {
	path, schema := categoricalCohortConfig(t)
	records := [][]uint64{{0}, {1}, {0}, {2}}
	cfg := setupTestFS(t, path, schema, records)

	for _, strict := range []bool{false, true} {
		for _, aggType := range numericAggsOnCategorical {
			t.Run(string(aggType), func(t *testing.T) {
				svc := New(cfg)
				svc.SetStrict(strict)
				req := &types.Request{
					Cohort: &types.Cohort{Filename: path},
					Aggregations: []*types.Aggregation{
						{Type: aggType, Field: "color", Label: "x"},
					},
				}
				_, err := svc.Process(context.Background(), req)
				if !errors.HasCode(err, errors.PULSE_FIELD_TYPE_UNSUPPORTED) {
					t.Errorf("strict=%v: expected PULSE_FIELD_TYPE_UNSUPPORTED, got: %v", strict, err)
				}
			})
		}
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
