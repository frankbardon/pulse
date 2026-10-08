package pulse_test

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// codeInParityCases is every ATTR_CODE_IN constructor refusal, one row
// per rule (and per integer width for the range rule), over the
// acceptance cohort: g is categorical {a, b}; t_<type> carries every
// field type.
var codeInParityCases = []struct {
	name, field, params string
	target              string
	predictors          []string
	// derived: an earlier ATTR_DATE_PART writes column "d" — the
	// field-reference rule admits it, the constructor does not.
	derived bool
}{
	{name: "params_missing", field: "g"},
	{name: "codes_missing", field: "g", params: `{}`},
	{name: "codes_empty", field: "g", params: `{"codes":[]}`},
	{name: "codes_null", field: "g", params: `{"codes":null}`},
	{name: "codes_not_list", field: "g", params: `{"codes":"a"}`},
	{name: "params_not_object", field: "g", params: `["a"]`},
	{name: "code_fraction", field: "t_u8", params: `{"codes":[1.5]}`},
	{name: "code_exponent", field: "t_u8", params: `{"codes":[1e2]}`},
	{name: "code_bool", field: "g", params: `{"codes":[true]}`},
	{name: "code_null", field: "g", params: `{"codes":[null]}`},
	{name: "code_object", field: "g", params: `{"codes":[{"v":1}]}`},
	{name: "code_array", field: "g", params: `{"codes":[["a"]]}`},
	{name: "target", field: "g", params: `{"codes":["a"]}`, target: "x"},
	{name: "predictors", field: "g", params: `{"codes":["a"]}`, predictors: []string{"x"}},
	{name: "type_f32", field: "t_f32", params: `{"codes":[1]}`},
	{name: "type_f64", field: "t_f64", params: `{"codes":[1]}`},
	{name: "type_date", field: "t_date", params: `{"codes":[1]}`},
	{name: "type_datetime", field: "t_datetime", params: `{"codes":[1]}`},
	{name: "type_packed_bool", field: "t_packed_bool", params: `{"codes":[1]}`},
	{name: "type_decimal128", field: "t_decimal128", params: `{"codes":[1]}`},
	{name: "type_set_u8", field: "t_set_u8", params: `{"codes":["a"]}`},
	{name: "type_set_u256", field: "t_set_u256", params: `{"codes":["a"]}`},
	{name: "u4_over", field: "t_u4", params: `{"codes":[16]}`},
	{name: "u8_over", field: "t_u8", params: `{"codes":[256]}`},
	{name: "u16_over", field: "t_u16", params: `{"codes":["65536"]}`},
	{name: "u32_over", field: "t_u32", params: `{"codes":[4294967296]}`},
	{name: "u64_over", field: "t_u64", params: `{"codes":["18446744073709551616"]}`},
	{name: "int_negative", field: "t_u8", params: `{"codes":[-1]}`},
	{name: "int_label", field: "t_u8", params: `{"codes":["a"]}`},
	{name: "int_decimal_string", field: "t_u8", params: `{"codes":["6.5"]}`},
	{name: "int_signed_string", field: "t_u16", params: `{"codes":["+3"]}`},
	{name: "derived_column", field: "d", params: `{"codes":[1]}`, derived: true},
}

// TestCodeIn_PredictRefusalsMatchRuntime: every request the ATTR_CODE_IN
// constructor refuses, Process and ProcessStream refuse with
// PROCESSING_CONFIG, and predict reports the same code, message and
// details — the descriptor re-derives the rules (it may not import
// internal/processing), and this pins the two copies together.
func TestCodeIn_PredictRefusalsMatchRuntime(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	for _, c := range codeInParityCases {
		t.Run(c.name, func(t *testing.T) {
			mk := func() *types.Request {
				a := &types.Attribute{Type: types.ATTR_CODE_IN, Field: c.field, Label: "o", Target: c.target, Predictors: c.predictors}
				if c.params != "" {
					a.Params = []byte(c.params)
				}
				attrs := []*types.Attribute{a}
				if c.derived {
					attrs = append([]*types.Attribute{{Type: types.ATTR_DATE_PART, Field: "t_date", Label: "d",
						Params: []byte(`{"part":"month"}`)}}, attrs...)
				}
				return &types.Request{
					Cohort:       &types.Cohort{Filename: cohort},
					Attributes:   attrs,
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "o"}},
				}
			}
			_, rerr := p.Process(ctx, mk())
			ce := codeInCoded(t, "process", rerr)
			_, serr := p.ProcessStream(ctx, mk())
			if se := codeInCoded(t, "stream", serr); se.Message != ce.Message {
				t.Errorf("stream message %q, process %q", se.Message, ce.Message)
			}
			env := predictEnvelope(t, p, fs, cohort, mk())
			var hit *descriptor.EnvelopeEntry
			for _, e := range env.Errors {
				if e.Code == string(ce.Code) && e.Message == ce.Message {
					hit = e
				}
			}
			if hit == nil {
				t.Fatalf("predict errors %+v lack the runtime refusal %s %q", env.Errors, ce.Code, ce.Message)
			}
			if !reflect.DeepEqual(hit.Details, ce.Details) && !(len(hit.Details) == 0 && len(ce.Details) == 0) {
				t.Errorf("details: predict %#v, runtime %#v", hit.Details, ce.Details)
			}
			for _, w := range env.Warnings {
				if w.Code == string(errors.PULSE_ATTR_CODE_NOT_IN_DICTIONARY) {
					t.Errorf("a refused slot also warned: %+v", w)
				}
			}
		})
	}
}

// TestCodeIn_PredictAcceptsWhatRuntimeRuns: the accepted shapes run on
// both sides with no predict error, and the absent-code warning fires
// only on a categorical field — the runtime runs that slot regardless
// (the absent code matches no row).
func TestCodeIn_PredictAcceptsWhatRuntimeRuns(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	for _, c := range []struct {
		name, field, params string
		missing             []string // nil: no warning
	}{
		{"categorical_present", "g", `{"codes":["a"]}`, nil},
		{"categorical_absent", "g", `{"codes":["zz","a","b2","zz"]}`, []string{"b2", "zz"}},
		{"categorical_numeric_code", "g", `{"codes":[0]}`, []string{"0"}},
		{"u4_max", "t_u4", `{"codes":[15, "0"]}`, nil},
		{"u8_max", "t_u8", `{"codes":[255]}`, nil},
		{"u16_max", "t_u16", `{"codes":[65535]}`, nil},
		{"u32_max", "t_u32", `{"codes":[4294967295]}`, nil},
		{"u64_max", "t_u64", `{"codes":["18446744073709551615"]}`, nil},
		{"int_never_present", "t_u8", `{"codes":[200]}`, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Cohort:       &types.Cohort{Filename: cohort},
				Attributes:   []*types.Attribute{{Type: types.ATTR_CODE_IN, Field: c.field, Label: "o", Params: []byte(c.params)}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "o"}},
			}
			if _, err := p.Process(ctx, req); err != nil {
				t.Fatalf("runtime refused: %v", err)
			}
			env := predictEnvelope(t, p, fs, cohort, req)
			if len(env.Errors) != 0 {
				t.Fatalf("predict refused what the runtime runs: %+v", env.Errors)
			}
			var got []*descriptor.EnvelopeEntry
			for _, w := range env.Warnings {
				if w.Code == string(errors.PULSE_ATTR_CODE_NOT_IN_DICTIONARY) {
					got = append(got, w)
				}
			}
			if c.missing == nil {
				if len(got) != 0 {
					t.Fatalf("unexpected warning %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("warnings %+v, want exactly one", got)
			}
			want := map[string]any{"attribute": "ATTR_CODE_IN", "field": c.field, "missing_codes": c.missing}
			if !reflect.DeepEqual(got[0].Details, want) {
				t.Errorf("details %#v, want %#v", got[0].Details, want)
			}
		})
	}
}

func codeInCoded(t *testing.T, arm string, err error) *errors.CodedError {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: runtime accepted the request", arm)
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("%s: uncoded error %v", arm, err)
	}
	if ce.Code != errors.PROCESSING_CONFIG {
		t.Fatalf("%s: code %s (%v), want PROCESSING_CONFIG", arm, ce.Code, err)
	}
	return ce
}
