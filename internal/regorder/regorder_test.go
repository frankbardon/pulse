package regorder

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func schemaOf(fields ...encoding.Field) *encoding.Schema {
	return &encoding.Schema{Fields: fields}
}

// TestCheck: the spec rule — absent passes, then field_missing,
// gaps_invalid, partition_is_field, and the schema field types (a name
// the schema lacks, and a nil schema, are not judged).
func TestCheck(t *testing.T) {
	t.Parallel()

	schema := schemaOf(
		encoding.Field{Name: "week", Type: encoding.FieldTypeU16},
		encoding.Field{Name: "day", Type: encoding.FieldTypeDate},
		encoding.Field{Name: "ts", Type: encoding.FieldTypeDateTime},
		encoding.Field{Name: "x", Type: encoding.FieldTypeF64},
		encoding.Field{Name: "mkt", Type: encoding.FieldTypeCategoricalU8},
	)
	spec := func(o *types.RegressionOrder) *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}, Order: o}
	}
	for _, tc := range []struct {
		name   string
		order  *types.RegressionOrder
		schema *encoding.Schema
		reason string
	}{
		{"absent", nil, schema, ""},
		{"integer", &types.RegressionOrder{Field: "week"}, schema, ""},
		{"date", &types.RegressionOrder{Field: "day", Gaps: types.OrderGapsRefuse}, schema, ""},
		{"datetime_partitioned", &types.RegressionOrder{Field: "ts", Partition: "mkt"}, schema, ""},
		{"derived_not_judged", &types.RegressionOrder{Field: "t_index", Partition: "seg"}, schema, ""},
		{"nil_schema_not_judged", &types.RegressionOrder{Field: "x"}, nil, ""},
		{"field_missing", &types.RegressionOrder{Partition: "mkt"}, schema, ReasonFieldMissing},
		{"gaps_invalid", &types.RegressionOrder{Field: "week", Gaps: "fill"}, schema, ReasonGapsInvalid},
		{"partition_is_field", &types.RegressionOrder{Field: "week", Partition: "week"}, schema, ReasonPartitionIsField},
		{"float_field", &types.RegressionOrder{Field: "x"}, schema, ReasonFieldType},
		{"categorical_field", &types.RegressionOrder{Field: "mkt"}, schema, ReasonFieldType},
		{"float_partition", &types.RegressionOrder{Field: "week", Partition: "x"}, schema, ReasonPartitionType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Check(spec(tc.order), tc.schema)
			if tc.reason == "" {
				if got != nil {
					t.Fatalf("Check = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("Check = nil, want reason %s", tc.reason)
			}
			if got.Code != errors.PROCESSING_CONFIG || got.Details["slot"] != Slot || got.Details["reason"] != tc.reason || got.Details["name"] != "m" {
				t.Errorf("Check = %s %v, want PROCESSING_CONFIG slot order reason %s name m", got.Code, got.Details, tc.reason)
			}
		})
	}
}

// TestRequiredRefusal: PROCESSING_CONFIG {slot: order, reason:
// order_required, requires: order}.
func TestRequiredRefusal(t *testing.T) {
	t.Parallel()
	got := RequiredRefusal(&types.RegressionSpec{Type: "REG_ACME_X"})
	if got.Code != errors.PROCESSING_CONFIG || got.Details["reason"] != ReasonOrderRequired || got.Details["requires"] != Slot || got.Details["regression"] != "REG_ACME_X" {
		t.Errorf("RequiredRefusal = %s %v", got.Code, got.Details)
	}
	if _, named := got.Details["name"]; named {
		t.Errorf("an unnamed spec's refusal carries a name: %v", got.Details)
	}
}

// TestCheckSeries: the gap policy over sorted series — contiguous at any
// cadence passes (the cadence is the smallest positive step over every
// series), a repeat is duplicate, a jump is gap; the first offence in
// series order is reported, duplicates before gaps.
func TestCheckSeries(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.REG_OLS, Order: &types.RegressionOrder{Field: "week", Partition: "mkt"}}
	for _, tc := range []struct {
		name   string
		series []Series
		reason string
		want   map[string]any
	}{
		{"empty", nil, "", nil},
		{"single_rows", []Series{{Partition: "a", Values: []int64{5}}, {Partition: "b", Values: []int64{9}}}, "", nil},
		{"unit_steps_ragged", []Series{{Partition: "a", Values: []int64{1, 2, 3}}, {Partition: "b", Values: []int64{2, 3}}}, "", nil},
		{"weekly_dates", []Series{{Values: []int64{7, 14, 21, 28}}}, "", nil},
		{"duplicate", []Series{{Partition: "a", Values: []int64{1, 2, 3}}, {Partition: "b", Values: []int64{1, 1}}}, ReasonDuplicate,
			map[string]any{"partition": "b", "value": int64(1)}},
		{"gap", []Series{{Partition: "a", Values: []int64{1, 2, 4}}}, ReasonGap,
			map[string]any{"partition": "a", "from": int64(2), "to": int64(4), "step": int64(1)}},
		{"cadence_from_another_series", []Series{{Partition: "a", Values: []int64{2, 4}}, {Partition: "b", Values: []int64{1, 2}}}, ReasonGap,
			map[string]any{"partition": "a", "from": int64(2), "to": int64(4), "step": int64(1)}},
		{"duplicate_before_gap", []Series{{Partition: "a", Values: []int64{1, 3}}, {Partition: "b", Values: []int64{5, 5}}}, ReasonDuplicate,
			map[string]any{"partition": "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CheckSeries(spec, tc.series)
			if tc.reason == "" {
				if got != nil {
					t.Fatalf("CheckSeries = %v, want nil", got)
				}
				return
			}
			if got == nil || got.Code != errors.PROCESSING_REGRESSION_ORDER || got.Details["reason"] != tc.reason || got.Details["field"] != "week" {
				t.Fatalf("CheckSeries = %v, want PROCESSING_REGRESSION_ORDER reason %s", got, tc.reason)
			}
			for k, v := range tc.want {
				if got.Details[k] != v {
					t.Errorf("details[%s] = %v (%T), want %v", k, got.Details[k], got.Details[k], v)
				}
			}
		})
	}
	// An unpartitioned order names no series.
	got := CheckSeries(&types.RegressionSpec{Type: types.REG_OLS, Order: &types.RegressionOrder{Field: "week"}}, []Series{{Values: []int64{1, 2, 4}}})
	if got == nil {
		t.Fatal("unpartitioned gap passed")
	}
	if _, ok := got.Details["partition"]; ok {
		t.Errorf("unpartitioned refusal names a partition: %v", got.Details)
	}
}
