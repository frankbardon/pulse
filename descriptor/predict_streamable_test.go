package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// TestPredict_Streamable_OnlineAggregations confirms that a request with
// only online aggregations on numeric fields reports streamable=true with
// no reasons.
func TestPredict_Streamable_OnlineAggregations(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score"},
			{Type: types.AGG_AVERAGE, Field: "score"},
			{Type: types.AGG_COUNT, Field: "score"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if !result.Streamable {
		t.Errorf("Streamable = false, want true. Reasons: %v", result.StreamableReasons)
	}
	if len(result.StreamableReasons) != 0 {
		t.Errorf("expected no reasons, got %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_StreamableGroupAllowed: GROUP_CATEGORY with
// online aggregators reports streamable=true.
func TestPredict_Streamable_StreamableGroupAllowed(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
			{Name: "grade", Type: encoding.FieldTypeCategoricalU8, Description: "Letter grade for the student", Dictionary: makeDictionary(t, "A", "B", "C")},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score"}},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
	}

	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if !result.Streamable {
		t.Errorf("Streamable = false, want true. Reasons: %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_NonStreamableGroupBlocks: GROUP_QUANTILE forces
// buffered.
func TestPredict_Streamable_NonStreamableGroupBlocks(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score"}},
		Groups:       []*types.Group{{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4}},
	}
	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Error("Streamable = true, want false (QUANTILE not streamable)")
	}
	if !containsReason(result.StreamableReasons, "GROUP_QUANTILE") {
		t.Errorf("expected reason mentioning GROUP_QUANTILE, got %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_NonOnlineAggBlocksStreaming asserts MEDIAN forces
// buffered with a per-aggregation reason.
func TestPredict_Streamable_NonOnlineAggBlocksStreaming(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "score"}},
	}

	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Error("Streamable = true, want false")
	}
	if !containsReason(result.StreamableReasons, "AGG_MEDIAN") {
		t.Errorf("expected reason mentioning AGG_MEDIAN, got %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_PercentileAttributeBlocks asserts ATTR_PERCENTILE
// (the only remaining buffered-only attribute) forces buffered.
func TestPredict_Streamable_PercentileAttributeBlocks(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
		Attributes:   []*types.Attribute{{Type: types.ATTR_PERCENTILE, Field: "score"}},
	}

	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Error("Streamable = true, want false")
	}
	if !containsReason(result.StreamableReasons, "ATTR_PERCENTILE") {
		t.Errorf("expected reason mentioning ATTR_PERCENTILE, got %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_TwoPassAttributeAllowed: ZSCORE/TSCORE/NORMALIZED
// implement TwoPassAttribute and report streamable=true.
func TestPredict_Streamable_TwoPassAttributeAllowed(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	for _, attrType := range []types.AttributeType{types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_NORMALIZED} {
		req := &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
			Attributes:   []*types.Attribute{{Type: attrType, Field: "score"}},
		}
		env := PredictFromBytes(data, req, nil)
		result := env.Data.(*PredictResult)
		if !result.Streamable {
			t.Errorf("%s: Streamable = false, want true. Reasons: %v", attrType, result.StreamableReasons)
		}
	}
}

// TestPredict_Streamable_RowLocalAttributesAllowed: FORMULA + DATE_PART
// are row-local; predict should report Streamable=true even with them
// present (provided no other gates fire).
func TestPredict_Streamable_RowLocalAttributesAllowed(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Field: "score", Expression: "score * 2", Label: "doubled"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if !result.Streamable {
		t.Errorf("Streamable = false, want true. Reasons: %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_DecimalFieldBlocks asserts decimal128 fields
// force buffered even with online aggregations.
func TestPredict_Streamable_DecimalFieldBlocks(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2, Description: "Transaction amount in USD cents"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount"}},
	}

	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Error("Streamable = true, want false")
	}
	if !containsReason(result.StreamableReasons, "decimal") {
		t.Errorf("expected reason mentioning decimal, got %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_EmptyAggregationsBlocks asserts that requests
// with no aggregations are non-streamable (no work to drive UpdateRow).
func TestPredict_Streamable_EmptyAggregationsBlocks(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{}
	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Error("Streamable = true, want false (no aggregations)")
	}
}

// TestPredict_Streamable_MatchesRuntime asserts predict's Streamable flag
// agrees with processing.CanStreamRequest for a representative matrix.
// This is the cross-package parity gate: drift in either side breaks it.
func TestPredict_Streamable_MatchesRuntime(t *testing.T) {
	numericSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	decimalSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2, Description: "Transaction amount in USD cents"},
		},
	}
	geoSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "loc", Type: encoding.FieldTypePointF64, Description: "Latitude/longitude pair (degrees)"},
		},
	}
	cases := []struct {
		name   string
		req    *types.Request
		schema *encoding.Schema
	}{
		{"online aggregations on numeric", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}}}, numericSchema},
		{"median on numeric", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "score"}}}, numericSchema},
		{"sum on decimal", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount"}}}, decimalSchema},
		{"empty request", &types.Request{}, numericSchema},
		{"geo centroid", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_GEO_CENTROID, Field: "loc"}}}, geoSchema},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := buildTestPulseFile(t, c.schema)
			env := PredictFromBytes(data, c.req, nil)
			result := env.Data.(*PredictResult)
			runtime := processing.CanStreamRequest(c.req, c.schema)
			if result.Streamable != runtime {
				t.Errorf("predict.Streamable=%v but runtime CanStreamRequest=%v (reasons: %v)",
					result.Streamable, runtime, result.StreamableReasons)
			}
		})
	}
}

func containsReason(reasons []string, needle string) bool {
	for _, r := range reasons {
		if strings.Contains(r, needle) {
			return true
		}
	}
	return false
}
