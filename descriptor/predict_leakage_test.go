package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func makeLeakageSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{
		Fields: []encoding.Field{
			{
				Name:        "region",
				Type:        encoding.FieldTypeCategoricalU8,
				Dictionary:  makeDictionary(t, "north", "south"),
				Description: "Geographic region categorical field",
			},
			{
				Name:        "price",
				Type:        encoding.FieldTypeF64,
				Description: "Transaction price in USD numeric",
			},
		},
	}
}

func TestPredict_TargetEncode_WithoutSplit_EmitsLeakageWarning(t *testing.T) {
	schema := makeLeakageSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Features: []*types.Feature{
			{
				Type:   types.FEAT_TARGET_ENCODE,
				Field:  "region",
				Params: json.RawMessage(`{"target":"price"}`),
			},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if !hasCode(env.Warnings, errors.PULSE_FEAT_TARGET_LEAKAGE_RISK) {
		t.Errorf("expected PULSE_FEAT_TARGET_LEAKAGE_RISK warning, got: %v", env.Warnings)
	}
}

func TestPredict_TargetEncode_WithoutSplit_StrictUpgradesToError(t *testing.T) {
	schema := makeLeakageSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Features: []*types.Feature{
			{
				Type:   types.FEAT_TARGET_ENCODE,
				Field:  "region",
				Params: json.RawMessage(`{"target":"price"}`),
			},
		},
	}
	env := PredictFromBytes(data, req, &PredictOptions{Strict: true})
	if !hasCode(env.Errors, errors.PULSE_FEAT_TARGET_LEAKAGE_RISK) {
		t.Errorf("expected PULSE_FEAT_TARGET_LEAKAGE_RISK error in strict mode, got: %v", env.Errors)
	}
}

func TestPredict_TargetEncode_AfterSplit_NoWarning(t *testing.T) {
	schema := makeLeakageSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Features: []*types.Feature{
			{
				Type:   types.FEAT_TRAIN_TEST_SPLIT,
				Params: json.RawMessage(`{"ratios":[0.7,0.15,0.15],"seed":1}`),
			},
			{
				Type:   types.FEAT_TARGET_ENCODE,
				Field:  "region",
				Params: json.RawMessage(`{"target":"price"}`),
			},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if hasCode(env.Warnings, errors.PULSE_FEAT_TARGET_LEAKAGE_RISK) {
		t.Errorf("did not expect PULSE_FEAT_TARGET_LEAKAGE_RISK when split precedes target encode, warnings: %v", env.Warnings)
	}
}

func TestPredict_TargetEncode_BeforeSplit_StillWarns(t *testing.T) {
	schema := makeLeakageSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Features: []*types.Feature{
			{
				Type:   types.FEAT_TARGET_ENCODE,
				Field:  "region",
				Params: json.RawMessage(`{"target":"price"}`),
			},
			{
				Type:   types.FEAT_TRAIN_TEST_SPLIT,
				Params: json.RawMessage(`{"ratios":[0.7,0.3]}`),
			},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if !hasCode(env.Warnings, errors.PULSE_FEAT_TARGET_LEAKAGE_RISK) {
		t.Errorf("expected leakage warning when target encode runs before split, warnings: %v", env.Warnings)
	}
}

func hasCode(entries []*EnvelopeEntry, code errors.Code) bool {
	for _, e := range entries {
		if e.Code == string(code) || strings.Contains(e.Code, string(code)) {
			return true
		}
	}
	return false
}
