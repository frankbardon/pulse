package descriptor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func codeInPredictSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Description: "Brand the respondent rated", Dictionary: makeDictionary(t, "acme", "beta", "gamma")},
		{Name: "bare", Type: encoding.FieldTypeCategoricalU16, Description: "Categorical column with no dictionary entries"},
		{Name: "q1", Type: encoding.FieldTypeU8, Description: "Five-point agreement scale answer"},
		{Name: "score", Type: encoding.FieldTypeF64, Description: "Numeric exam score out of one hundred"},
	}}
}

func codeInAttr(field, params string) *types.Attribute {
	a := &types.Attribute{Type: types.ATTR_CODE_IN, Field: field, Label: "top"}
	if params != "" {
		a.Params = json.RawMessage(params)
	}
	return a
}

func codeInEntries(entries []*descriptor.EnvelopeEntry, code errors.Code) []*descriptor.EnvelopeEntry {
	var out []*descriptor.EnvelopeEntry
	for _, e := range entries {
		if e.Code == string(code) {
			out = append(out, e)
		}
	}
	return out
}

// TestPredict_CodeInMissingCodes: one PULSE_ATTR_CODE_NOT_IN_DICTIONARY
// per ATTR_CODE_IN slot whose categorical field lacks a code, missing
// codes sorted and deduped; a warning by default, an error under
// Strict; never for an integer field or a fully present code list.
func TestPredict_CodeInMissingCodes(t *testing.T) {
	data := buildTestPulseFile(t, codeInPredictSchema(t))
	req := &types.Request{Attributes: []*types.Attribute{
		codeInAttr("brand", `{"codes":["zeta","acme","delta","zeta"]}`),
		{Type: types.ATTR_CODE_IN, Field: "brand", Label: "all_present", Params: json.RawMessage(`{"codes":["acme","gamma"]}`)},
		{Type: types.ATTR_CODE_IN, Field: "bare", Label: "no_dict", Params: json.RawMessage(`{"codes":["x", 3]}`)},
		{Type: types.ATTR_CODE_IN, Field: "q1", Label: "int", Params: json.RawMessage(`{"codes":[4, "5"]}`)},
	}}

	env := predictFromBytes(data, req, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", env.Errors)
	}
	got := codeInEntries(env.Warnings, errors.PULSE_ATTR_CODE_NOT_IN_DICTIONARY)
	if len(got) != 2 {
		t.Fatalf("warnings = %+v, want exactly two (brand, bare)", got)
	}
	want := []map[string]any{
		{"attribute": "ATTR_CODE_IN", "field": "brand", "missing_codes": []string{"delta", "zeta"}},
		{"attribute": "ATTR_CODE_IN", "field": "bare", "missing_codes": []string{"3", "x"}},
	}
	for i, w := range want {
		if !reflect.DeepEqual(got[i].Details, w) {
			t.Errorf("warning %d details = %#v, want %#v", i, got[i].Details, w)
		}
	}
	if !strings.Contains(got[0].Message, `"delta", "zeta"`) {
		t.Errorf("message %q does not name the missing codes", got[0].Message)
	}

	strict := predictFromBytes(data, req, &PredictOptions{Strict: true})
	if n := len(codeInEntries(strict.Errors, errors.PULSE_ATTR_CODE_NOT_IN_DICTIONARY)); n != 2 {
		t.Errorf("strict: %d errors, want 2 (%+v)", n, strict.Errors)
	}
	if n := len(codeInEntries(strict.Warnings, errors.PULSE_ATTR_CODE_NOT_IN_DICTIONARY)); n != 0 {
		t.Errorf("strict: %d warnings left, want 0", n)
	}
}

// TestPredict_CodeInRefusals: predict raises the runtime constructor's
// refusals (PROCESSING_CONFIG) — the root-package parity test holds the
// messages equal to the runtime's.
func TestPredict_CodeInRefusals(t *testing.T) {
	data := buildTestPulseFile(t, codeInPredictSchema(t))
	cases := []struct {
		name string
		attr *types.Attribute
		msg  string // substring
	}{
		{"no_params", codeInAttr("brand", ""), `non-empty "codes" list`},
		{"empty_codes", codeInAttr("brand", `{"codes":[]}`), `non-empty "codes" list`},
		{"codes_not_list", codeInAttr("brand", `{"codes":"acme"}`), "parsing ATTR_CODE_IN params"},
		{"fraction", codeInAttr("q1", `{"codes":[4.5]}`), "JSON string or a JSON integer"},
		{"bool", codeInAttr("brand", `{"codes":[true]}`), "JSON string or a JSON integer"},
		{"float_field", codeInAttr("score", `{"codes":[1]}`), "accepts only categorical"},
		{"int_over_width", codeInAttr("q1", `{"codes":[256]}`), "0..255"},
		{"int_negative", codeInAttr("q1", `{"codes":[-1]}`), "can never match"},
		{"int_label", codeInAttr("q1", `{"codes":["6.5"]}`), "can never match"},
		{"target", &types.Attribute{Type: types.ATTR_CODE_IN, Field: "q1", Target: "q1", Params: json.RawMessage(`{"codes":[1]}`)}, "target or predictors"},
		{"predictors", &types.Attribute{Type: types.ATTR_CODE_IN, Field: "q1", Predictors: []string{"score"}, Params: json.RawMessage(`{"codes":[1]}`)}, "target or predictors"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := predictFromBytes(data, &types.Request{Attributes: []*types.Attribute{c.attr}}, nil)
			got := codeInEntries(env.Errors, errors.PROCESSING_CONFIG)
			if len(got) != 1 || !strings.Contains(got[0].Message, c.msg) {
				t.Fatalf("errors = %+v, want one PROCESSING_CONFIG containing %q", env.Errors, c.msg)
			}
			if n := len(codeInEntries(env.Warnings, errors.PULSE_ATTR_CODE_NOT_IN_DICTIONARY)); n != 0 {
				t.Errorf("a refused slot also warned (%d)", n)
			}
		})
	}
	// Every unsigned-integer width bounds its codes at its own max.
	for _, ok := range []string{`{"codes":[0, 255]}`, `{"codes":["7"]}`} {
		if env := predictFromBytes(data, &types.Request{Attributes: []*types.Attribute{codeInAttr("q1", ok)}}, nil); len(env.Errors) != 0 {
			t.Errorf("%s: refused an in-range integer code: %+v", ok, env.Errors)
		}
	}
}

// TestPredict_CodeInHidden: on an instance that hides ATTR_CODE_IN the
// slot routes like a never-registered name — no code-in refusal and no
// absent-code warning names the hidden operator.
func TestPredict_CodeInHidden(t *testing.T) {
	data := buildTestPulseFile(t, codeInPredictSchema(t))
	req := &types.Request{Attributes: []*types.Attribute{
		codeInAttr("brand", `{"codes":["zeta"]}`),
		codeInAttr("score", `{"codes":[1]}`),
	}}
	env := predictFromBytes(data, req, &PredictOptions{Instance: hideOnly(string(types.ATTR_CODE_IN))})
	if n := len(codeInEntries(env.Warnings, errors.PULSE_ATTR_CODE_NOT_IN_DICTIONARY)); n != 0 {
		t.Errorf("hidden ATTR_CODE_IN still warned: %+v", env.Warnings)
	}
	for _, e := range env.Errors {
		if strings.Contains(e.Message, "accepts only categorical") {
			t.Errorf("hidden ATTR_CODE_IN still refused by type: %+v", e)
		}
	}
}
