package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
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
	env := predictFromBytes(data, req, nil)
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
	env := predictFromBytes(data, req, &PredictOptions{Strict: true})
	if !hasCode(env.Errors, errors.PULSE_FEAT_TARGET_LEAKAGE_RISK) {
		t.Errorf("expected PULSE_FEAT_TARGET_LEAKAGE_RISK error in strict mode, got: %v", env.Errors)
	}
}

// A preceding split does not protect: the encoder reads no split column
// and features run before filters, so every encoded value still averages
// the validation/test records' outcomes and the record's own. The warning
// must therefore fire with or without a split ahead of the encoder.
func TestPredict_TargetEncode_AfterSplit_StillWarns(t *testing.T) {
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
	env := predictFromBytes(data, req, nil)
	if !hasCode(env.Warnings, errors.PULSE_FEAT_TARGET_LEAKAGE_RISK) {
		t.Errorf("expected PULSE_FEAT_TARGET_LEAKAGE_RISK even when a split precedes target encode, warnings: %v", env.Warnings)
	}
	strict := predictFromBytes(data, req, &PredictOptions{Strict: true})
	if !hasCode(strict.Errors, errors.PULSE_FEAT_TARGET_LEAKAGE_RISK) {
		t.Errorf("expected PULSE_FEAT_TARGET_LEAKAGE_RISK error in strict mode even after a split, errors: %v", strict.Errors)
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
	env := predictFromBytes(data, req, nil)
	if !hasCode(env.Warnings, errors.PULSE_FEAT_TARGET_LEAKAGE_RISK) {
		t.Errorf("expected leakage warning when target encode runs before split, warnings: %v", env.Warnings)
	}
}

func hasCode(entries []*descriptor.EnvelopeEntry, code errors.Code) bool {
	for _, e := range entries {
		if e.Code == string(code) || strings.Contains(e.Code, string(code)) {
			return true
		}
	}
	return false
}
