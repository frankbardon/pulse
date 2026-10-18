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

// TestPredict_TargetEncode_FitOnLeakageRule pins the E3-S3 rule, decided
// from the request alone: fit_on silences PULSE_FEAT_TARGET_LEAKAGE_RISK
// (and its strict error); folds without fit_on keeps a reworded entry
// carrying details.folds; neither keeps the full one. One entry per
// unprotected encoder, in request order.
func TestPredict_TargetEncode_FitOnLeakageRule(t *testing.T) {
	schema := makeLeakageSchema(t)
	data := buildTestPulseFile(t, schema)
	split := &types.Feature{Type: types.FEAT_TRAIN_TEST_SPLIT, Label: "split", Params: json.RawMessage(`{"ratios":[0.7,0.3]}`)}
	te := func(params string) *types.Feature {
		return &types.Feature{Type: types.FEAT_TARGET_ENCODE, Field: "region", Params: json.RawMessage(params)}
	}
	leaks := func(es []*descriptor.EnvelopeEntry) []*descriptor.EnvelopeEntry {
		var out []*descriptor.EnvelopeEntry
		for _, e := range es {
			if e.Code == string(errors.PULSE_FEAT_TARGET_LEAKAGE_RISK) {
				out = append(out, e)
			}
		}
		return out
	}

	fitOn := &types.Request{Features: []*types.Feature{split, te(`{"target":"price","fit_on":"split == 0"}`)}}
	for _, strict := range []bool{false, true} {
		env := predictFromBytes(data, fitOn, &PredictOptions{Strict: strict})
		if n := len(leaks(env.Warnings)) + len(leaks(env.Errors)); n != 0 {
			t.Errorf("strict=%v: fit_on still raised %d leakage entries", strict, n)
		}
		if len(env.Errors) != 0 && !strict {
			t.Errorf("fit_on request refused: %v", env.Errors)
		}
	}
	if got := TargetLeakageWarnings(fitOn, nil); len(got) != 0 {
		t.Errorf("TargetLeakageWarnings = %v, want none under fit_on", got)
	}

	mixed := &types.Request{Features: []*types.Feature{
		split,
		te(`{"target":"price","folds":5,"rng_seed":2}`),
		te(`{"target":"price","fit_on":"split == 0","folds":5}`),
		te(`{"target":"price"}`),
	}}
	env := predictFromBytes(data, mixed, nil)
	got := leaks(env.Warnings)
	if len(got) != 2 {
		t.Fatalf("leakage warnings %v, want the folds-only and the bare encoders", got)
	}
	if got[0].Details["feature_index"] != 1 || got[0].Details["folds"] != 5 ||
		!strings.Contains(got[0].Message, "out-of-fold") || !strings.Contains(got[0].Message, "fit_on") {
		t.Errorf("folds-only entry = %q %v", got[0].Message, got[0].Details)
	}
	if got[1].Details["feature_index"] != 3 || got[1].Details["folds"] != nil || !strings.Contains(got[1].Message, "without params.fit_on") {
		t.Errorf("bare entry = %q %v", got[1].Message, got[1].Details)
	}
	rule := TargetLeakageWarnings(mixed, nil)
	if len(rule) != 2 || rule[0].Message != got[0].Message || rule[1].Message != got[1].Message {
		t.Errorf("TargetLeakageWarnings %v disagrees with predict %v", rule, got)
	}
}

// TestFieldRefRefusals_TargetEncodeFitOn: fit_on compiles against the
// schema plus EARLIER features' columns — a forward or self reference is
// the PULSE_FORMULA_COMPILE_ERROR unknown-name refusal, an earlier
// feature's column compiles.
func TestFieldRefRefusals_TargetEncodeFitOn(t *testing.T) {
	schema := makeLeakageSchema(t)
	split := &types.Feature{Type: types.FEAT_TRAIN_TEST_SPLIT, Label: "split", Params: json.RawMessage(`{"ratios":[0.7,0.3]}`)}
	te := &types.Feature{Type: types.FEAT_TARGET_ENCODE, Field: "region", Label: "te", Params: json.RawMessage(`{"target":"price","fit_on":"split == 0"}`)}
	self := &types.Feature{Type: types.FEAT_TARGET_ENCODE, Field: "region", Label: "te", Params: json.RawMessage(`{"target":"price","fit_on":"te > 0"}`)}
	cases := []struct {
		name  string
		feats []*types.Feature
		bad   bool
	}{
		{"earlier split", []*types.Feature{split, te}, false},
		{"stored field", []*types.Feature{{Type: types.FEAT_TARGET_ENCODE, Field: "region", Params: json.RawMessage(`{"target":"price","fit_on":"price < 10"}`)}}, false},
		{"forward reference", []*types.Feature{te, split}, true},
		{"self reference", []*types.Feature{self}, true},
		{"not boolean-typed name", []*types.Feature{{Type: types.FEAT_TARGET_ENCODE, Field: "region", Params: json.RawMessage(`{"target":"price","fit_on":"nope == 1"}`)}}, true},
	}
	for _, c := range cases {
		got := FieldRefRefusals(&types.Request{Features: c.feats}, schema, nil)
		if !c.bad {
			if len(got) != 0 {
				t.Errorf("%s: refused %v", c.name, got)
			}
			continue
		}
		if len(got) == 0 || got[0].Code != errors.PULSE_FORMULA_COMPILE_ERROR {
			t.Errorf("%s: %v, want %s", c.name, got, errors.PULSE_FORMULA_COMPILE_ERROR)
		}
	}
}
