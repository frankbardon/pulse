package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
)

// ExactlyOneOf at the request check (U53 decision #12): an extension
// operator declaring an ExactlyOneOf group has a request carrying none,
// or more than one, of its members refused PULSE_REQUEST_PARAM_INVALID
// reason exactly_one_of — in every one of the nine categories, at
// predict and at runtime alike — after the unknown-key, wrong-type and
// missing-required faults.

// oneOfParams is strictParams plus the group "rate" of span / halflife.
var oneOfParams = append(append([]pulse.ParamMeta(nil), strictParams...),
	pulse.ParamMeta{Name: "span", JSONType: "number", ExactlyOneOf: "rate", Description: "One of the rate."},
	pulse.ParamMeta{Name: "halflife", JSONType: "number", ExactlyOneOf: "rate", Description: "The other."},
)

// TestExtensions_ParamsExactlyOneOfEveryCategory: per category, no
// member and both members are refused exactly_one_of with identical
// code, message and details at predict and runtime; a wrong type or a
// missing Required param wins over the group fault, an unknown key
// over all of them; one member (either) runs.
func TestExtensions_ParamsExactlyOneOfEveryCategory(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, withParams(strictExtensions(), oneOfParams))
	ctx := context.Background()
	refusals := []struct {
		name, params, reason, present string
	}{
		{"no member", `{"scale":2}`, "exactly_one_of", "[]"},
		{"no params object", ``, "missing_required", ""},
		{"both members", `{"scale":2,"span":1,"halflife":3}`, "exactly_one_of", `["span","halflife"]`},
		{"missing required wins", `{"span":1,"halflife":3}`, "missing_required", ""},
		{"wrong type wins", `{"scale":2,"span":"1"}`, "wrong_type", ""},
		{"null member is a wrong type", `{"scale":2,"span":null,"halflife":3}`, "wrong_type", ""},
	}
	for _, tc := range strictCases() {
		t.Run(tc.category, func(t *testing.T) {
			for _, r := range refusals {
				t.Run(r.name, func(t *testing.T) {
					var raw json.RawMessage
					if r.params != "" {
						raw = json.RawMessage(r.params)
					}
					_, err := p.Process(ctx, tc.request(cohort, raw))
					var ce *perr.CodedError
					if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_REQUEST_PARAM_INVALID {
						t.Fatalf("Process err = %v, want PULSE_REQUEST_PARAM_INVALID", err)
					}
					if ce.Details["reason"] != r.reason {
						t.Fatalf("runtime reason = %v, want %s (%s)", ce.Details["reason"], r.reason, ce.Message)
					}
					if r.reason == "exactly_one_of" {
						if ce.Details["path"] != tc.slot+".params" || ce.Details["group"] != "rate" ||
							fmt.Sprint(ce.Details["members"]) != "[span halflife]" || jsonOf(ce.Details["present"]) != r.present {
							t.Fatalf("runtime details = %v", ce.Details)
						}
					}
					res, perr2 := p.Predict(ctx, tc.request(cohort, raw))
					if perr2 != nil {
						t.Fatalf("Predict: %v", perr2)
					}
					if res.Valid || len(res.Errors) == 0 {
						t.Fatalf("predict valid, runtime refused %s", ce.Message)
					}
					got := res.Errors[0]
					if got.Code != string(ce.Code) || got.Message != ce.Message || jsonOf(got.Details) != jsonOf(ce.Details) {
						t.Fatalf("predict %s %q %v, runtime %s %q %v", got.Code, got.Message, got.Details, ce.Code, ce.Message, ce.Details)
					}
				})
			}
			t.Run("unknown key wins", func(t *testing.T) {
				_, err := p.Process(ctx, tc.request(cohort, json.RawMessage(`{"scale":2,"spam":1}`)))
				var ce *perr.CodedError
				if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
					t.Fatalf("Process err = %v, want PULSE_REQUEST_UNKNOWN_FIELD", err)
				}
			})
			t.Run("one member runs", func(t *testing.T) {
				for _, params := range []string{`{"scale":2,"span":1}`, `{"scale":2,"halflife":3,"tag":"x"}`} {
					req := tc.request(cohort, json.RawMessage(params))
					if res, err := p.Predict(ctx, req); err != nil || !res.Valid {
						t.Fatalf("Predict(%s) = %+v / %v", params, res, err)
					}
					if _, err := p.Process(ctx, tc.request(cohort, json.RawMessage(params))); err != nil {
						t.Fatalf("Process(%s): %v", params, err)
					}
				}
			})
		})
	}
}

// TestExtensions_ParamsExactlyOneOfAbsentParams: with no Required
// param, a slot carrying no params object at all is refused
// exactly_one_of (present empty), at predict and runtime alike.
func TestExtensions_ParamsExactlyOneOfAbsentParams(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, onlyCategory(withParams(strictExtensions(), oneOfParams[1:]), "aggregator"))
	ctx := context.Background()
	req := strictCases()[0].request(cohort, nil)
	_, err := p.Process(ctx, req)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_REQUEST_PARAM_INVALID || ce.Details["reason"] != "exactly_one_of" || jsonOf(ce.Details["present"]) != "[]" {
		t.Fatalf("Process err = %v (%v), want exactly_one_of", err, ce)
	}
	res, err := p.Predict(ctx, strictCases()[0].request(cohort, nil))
	if err != nil || res.Valid || len(res.Errors) == 0 || res.Errors[0].Message != ce.Message {
		t.Fatalf("Predict = %+v / %v, want %q", res, err, ce.Message)
	}
}

// jsonOf is v's JSON encoding (a stable comparison of details maps).
func jsonOf(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(raw)
}
