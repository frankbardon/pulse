package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestPercentileParam_PredictMatchesRuntime (U66 E9-S4): AGG_PERCENTILE's
// params.percentile is one rule with two callers (internal/percentilespec)
// — a percentile outside [0, 100] or a non-numeric one is refused by
// predict with exactly the runtime's code, message and details at every
// aggregation slot (top level, grouped, crosstab cell, crosstab
// margin_aggregations), on the plain and the ordered-categorical arms;
// a missing one (absent params, an object without the key, a null —
// D33, E9-S5) is refused the same way; an accepted percentile (a bound,
// an in-range value) runs on both. Before E9-S4 predict accepted every
// one of them; before E9-S5 a missing percentile read 50 or 0.
func TestPercentileParam_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	const axes = `"rows":[{"type":"GROUP_CATEGORY","field":"subj"}],"columns":[{"type":"GROUP_CATEGORY","field":"t_categorical_u8"}]`
	slots := map[string]func(agg string) string{
		"top-level": func(agg string) string { return `{"aggregations":[` + agg + `]}` },
		"grouped": func(agg string) string {
			return `{"groups":[{"type":"GROUP_CATEGORY","field":"subj"}],"aggregations":[` + agg + `]}`
		},
		"crosstab cell": func(agg string) string { return `{"crosstab":{` + axes + `,"cell":` + agg + `}}` },
		"margin_aggregations": func(agg string) string {
			return `{"crosstab":{` + axes + `,"cell":{"type":"AGG_COUNT","field":"x","label":"n"},"margin_aggregations":[` + agg + `]}}`
		},
	}
	cases := []struct {
		name, agg string
		refused   bool
	}{
		{"above 100", `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":{"percentile":150}}`, true},
		{"below 0", `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":{"percentile":-0.5}}`, true},
		{"string", `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":{"percentile":"90"}}`, true},
		{"bool", `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":{"percentile":true}}`, true},
		{"ordered above 100", `{"type":"AGG_PERCENTILE","field":"g","label":"a","params":{"ordered":true,"percentile":101}}`, true},
		{"ordered string", `{"type":"AGG_PERCENTILE","field":"g","label":"a","params":{"ordered":true,"percentile":"x"}}`, true},
		{"absent params", `{"type":"AGG_PERCENTILE","field":"x","label":"a"}`, true},
		{"null params", `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":null}`, true},
		{"no percentile key", `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":{}}`, true},
		{"null percentile", `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":{"percentile":null}}`, true},
		{"ordered no percentile", `{"type":"AGG_PERCENTILE","field":"g","label":"a","params":{"ordered":true}}`, true},
		{"ordered null percentile", `{"type":"AGG_PERCENTILE","field":"g","label":"a","params":{"ordered":true,"percentile":null}}`, true},
		{"lower bound", `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":{"percentile":0}}`, false},
		{"upper bound", `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":{"percentile":100}}`, false},
		{"ordered in range", `{"type":"AGG_PERCENTILE","field":"g","label":"a","params":{"ordered":true,"percentile":90}}`, false},
	}
	for _, tc := range cases {
		for slotName, wrap := range slots {
			where := tc.name + "/" + slotName
			body := wrap(tc.agg)
			mk := func() *types.Request {
				var req types.Request
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					t.Fatalf("%s: %v", where, err)
				}
				req.Cohort = &types.Cohort{Filename: cohort}
				return &req
			}
			_, rerr := p.Process(ctx, mk())
			env := predictEnvelope(t, p, fs, cohort, mk())
			if !tc.refused {
				if rerr != nil || len(env.Errors) != 0 {
					t.Errorf("%s: runtime %v, predict %+v; want both to run", where, rerr, env.Errors)
				}
				continue
			}
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != errors.PROCESSING_CONFIG {
				t.Errorf("%s: runtime %v, want %s", where, rerr, errors.PROCESSING_CONFIG)
				continue
			}
			if len(env.Errors) != 1 {
				t.Errorf("%s: predict errors %+v, want exactly the runtime's %s %q %v", where, env.Errors, ce.Code, ce.Message, ce.Details)
				continue
			}
			got := env.Errors[0]
			want, _ := json.Marshal(ce.Details)
			have, _ := json.Marshal(got.Details)
			if got.Code != string(ce.Code) || got.Message != ce.Message || string(want) != string(have) {
				t.Errorf("%s: predict %s %q %s, runtime %s %q %s", where, got.Code, got.Message, have, ce.Code, ce.Message, want)
			}
		}
	}
}

// TestAggregationSlot_FirstFaultMatchesRuntime (U66 E9-S5): a slot with
// two faults — a field-type, decimal or ordered-categorical refusal AND
// a bad params.percentile / params.ddof — is refused by the runtime with
// the field rule (its factory never reaches the param), so predict's
// FIRST error is that same refusal, code, message and details, at every
// aggregation slot. Before E9-S5 predict listed the param refusal first.
func TestAggregationSlot_FirstFaultMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	const axes = `"rows":[{"type":"GROUP_CATEGORY","field":"subj"}],"columns":[{"type":"GROUP_CATEGORY","field":"t_categorical_u8"}]`
	slots := map[string]func(agg string) string{
		"top-level": func(agg string) string { return `{"aggregations":[` + agg + `]}` },
		"grouped": func(agg string) string {
			return `{"groups":[{"type":"GROUP_CATEGORY","field":"subj"}],"aggregations":[` + agg + `]}`
		},
		"crosstab cell": func(agg string) string { return `{"crosstab":{` + axes + `,"cell":` + agg + `}}` },
		"margin_aggregations": func(agg string) string {
			return `{"crosstab":{` + axes + `,"cell":{"type":"AGG_COUNT","field":"x","label":"n"},"margin_aggregations":[` + agg + `]}}`
		},
	}
	cases := []struct {
		name, agg string
		code      errors.Code
	}{
		{"percentile 150 on decimal128", `{"type":"AGG_PERCENTILE","field":"t_decimal128","label":"a","params":{"percentile":150}}`, errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL},
		{"percentile missing on decimal128", `{"type":"AGG_PERCENTILE","field":"t_decimal128","label":"a"}`, errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL},
		{"percentile 150 on categorical, not ordered", `{"type":"AGG_PERCENTILE","field":"g","label":"a","params":{"percentile":150}}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"percentile missing on categorical, not ordered", `{"type":"AGG_PERCENTILE","field":"g","label":"a","params":{}}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"ordered percentile 150 on numeric", `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":{"ordered":true,"percentile":150}}`, errors.PROCESSING_CONFIG},
		{"ordered percentile missing on numeric", `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":{"ordered":true}}`, errors.PROCESSING_CONFIG},
		{"variance ddof 7 on categorical", `{"type":"AGG_VARIANCE","field":"g","label":"a","params":{"ddof":7}}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"stddev ddof 7 on categorical", `{"type":"AGG_STDDEV","field":"g","label":"a","params":{"ddof":7}}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
	}
	for _, tc := range cases {
		for slotName, wrap := range slots {
			where := tc.name + "/" + slotName
			body := wrap(tc.agg)
			mk := func() *types.Request {
				var req types.Request
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					t.Fatalf("%s: %v", where, err)
				}
				req.Cohort = &types.Cohort{Filename: cohort}
				return &req
			}
			_, rerr := p.Process(ctx, mk())
			env := predictEnvelope(t, p, fs, cohort, mk())
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) {
				t.Errorf("%s: runtime %v, want a coded refusal", where, rerr)
				continue
			}
			if tc.code != "" && ce.Code != tc.code {
				t.Errorf("%s: runtime %s %q, want %s", where, ce.Code, ce.Message, tc.code)
			}
			if param, _ := ce.Details["param"].(string); param != "" {
				t.Errorf("%s: runtime raised the params.%s refusal %q first; the case pins a field-rule-first slot", where, param, ce.Message)
			}
			if len(env.Errors) == 0 {
				t.Errorf("%s: predict accepted; runtime %s %q", where, ce.Code, ce.Message)
				continue
			}
			got := env.Errors[0]
			want, _ := json.Marshal(ce.Details)
			have, _ := json.Marshal(got.Details)
			if got.Code != string(ce.Code) || got.Message != ce.Message || string(want) != string(have) {
				t.Errorf("%s: predict first %s %q %s, runtime %s %q %s (all predict: %+v)", where, got.Code, got.Message, have, ce.Code, ce.Message, want, env.Errors)
			}
		}
	}
}
