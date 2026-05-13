package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func TestPredict_FeatureLogValid(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "income", Type: encoding.FieldTypeF64, Description: "Annual income in USD"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Features: []*types.Feature{
			{Type: types.FEAT_LOG, Field: "income"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "LOG_income", Label: "avg_log"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) > 0 {
		t.Errorf("unexpected errors: %v", env.Errors)
	}
	result := env.Data.(*PredictResult)
	if !result.Valid {
		t.Error("expected Valid=true; aggregator should reach feature output column")
	}
}

func TestPredict_FeatureUnknownField(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Numeric measurement value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Features: []*types.Feature{
			{Type: types.FEAT_LOG, Field: "missing"},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) == 0 {
		t.Error("expected error for missing input field")
	}
}

func TestPredict_FeatureUnknownType(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Numeric measurement value"},
		},
	}
	data := buildTestPulseFile(t, schema)
	req := &types.Request{
		Features: []*types.Feature{
			{Type: "FEAT_NOT_REAL", Field: "x"},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) == 0 {
		t.Error("expected error for unknown feature type")
	}
}

func TestPredict_FeatureOneHotRequiresCategorical(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Numeric score field"},
		},
	}
	data := buildTestPulseFile(t, schema)
	req := &types.Request{
		Features: []*types.Feature{
			{Type: types.FEAT_ONE_HOT, Field: "score"},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) == 0 {
		t.Error("expected error for ONE_HOT on non-categorical field")
	}
}

func TestPredict_FeatureOneHotProjectsCategoryColumns(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{
				Name:        "region",
				Type:        encoding.FieldTypeCategoricalU8,
				Dictionary:  makeDictionary(t, "north", "south"),
				Description: "Geographic region categorical field",
			},
		},
	}
	data := buildTestPulseFile(t, schema)

	// Reference one of the projected columns in a downstream filter.
	req := &types.Request{
		Features: []*types.Feature{
			{Type: types.FEAT_ONE_HOT, Field: "region"},
		},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_INCLUDE, Field: "region_north", Values: []string{"1"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "region", Label: "n"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) > 0 {
		t.Errorf("unexpected errors: %v", env.Errors)
	}
}

func TestPredict_FeaturePolyValid(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Numeric predictor"},
			{Name: "y", Type: encoding.FieldTypeF64, Description: "Numeric target"},
		},
	}
	data := buildTestPulseFile(t, schema)

	// Reference the synthesized x_2 / x_3 columns in an aggregator so
	// predict's downstream column-resolution catches them via the
	// virtual post-feature schema.
	req := &types.Request{
		Features: []*types.Feature{
			{
				Type:   types.FEAT_POLY,
				Field:  "x",
				Label:  "x",
				Params: json.RawMessage(`{"degree":3}`),
			},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "x_2", Label: "avg_x2"},
			{Type: types.AGG_AVERAGE, Field: "x_3", Label: "avg_x3"},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) > 0 {
		t.Errorf("unexpected errors: %v", env.Errors)
	}
}

func TestPredict_FeaturePolyDegreeOutOfRange(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Numeric predictor"},
		},
	}
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name   string
		params string
	}{
		{"degree-one", `{"degree":1}`},
		{"degree-zero", `{"degree":0}`},
		{"degree-too-high", `{"degree":99}`},
		{"missing-degree", `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Features: []*types.Feature{
					{Type: types.FEAT_POLY, Field: "x", Params: json.RawMessage(c.params)},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if len(env.Errors) == 0 {
				t.Errorf("expected error for %s; got none", c.name)
			}
		})
	}
}

func TestPredict_FeaturePolyRejectsCategoricalField(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{
				Name:        "region",
				Type:        encoding.FieldTypeCategoricalU8,
				Dictionary:  makeDictionary(t, "n", "s"),
				Description: "Geographic region",
			},
		},
	}
	data := buildTestPulseFile(t, schema)
	req := &types.Request{
		Features: []*types.Feature{
			{Type: types.FEAT_POLY, Field: "region", Params: json.RawMessage(`{"degree":2}`)},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) == 0 {
		t.Error("expected error: FEAT_POLY rejects non-numeric field")
	}
}

func TestPredict_FeatureBucketizeMissingParams(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Numeric measurement value"},
		},
	}
	data := buildTestPulseFile(t, schema)
	req := &types.Request{
		Features: []*types.Feature{
			{Type: types.FEAT_BUCKETIZE, Field: "x"},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) == 0 {
		t.Error("expected error for BUCKETIZE without params")
	}
}

func TestPredict_FeatureSplitBadRatios(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Numeric measurement value"},
		},
	}
	data := buildTestPulseFile(t, schema)
	req := &types.Request{
		Features: []*types.Feature{
			{
				Type:   types.FEAT_TRAIN_TEST_SPLIT,
				Params: json.RawMessage(`{"ratios":[0.5,0.4]}`),
			},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) == 0 {
		t.Error("expected error for ratios that do not sum to 1.0")
	}
}

func TestPredict_FeatureTargetEncodeRejectsCategoricalTarget(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{
				Name:        "region",
				Type:        encoding.FieldTypeCategoricalU8,
				Dictionary:  makeDictionary(t, "n", "s"),
				Description: "Geographic region categorical field",
			},
			{
				Name:        "label",
				Type:        encoding.FieldTypeCategoricalU8,
				Dictionary:  makeDictionary(t, "yes", "no"),
				Description: "Categorical outcome label field",
			},
		},
	}
	data := buildTestPulseFile(t, schema)
	req := &types.Request{
		Features: []*types.Feature{
			{
				Type:   types.FEAT_TARGET_ENCODE,
				Field:  "region",
				Params: json.RawMessage(`{"target":"label"}`),
			},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) == 0 {
		t.Error("expected error for categorical target")
	}
}
