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
// an accepted percentile (absent params, an object without the key, a
// bound) runs on both. Before E9-S4 predict accepted every one of them.
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
		{"absent params", `{"type":"AGG_PERCENTILE","field":"x","label":"a"}`, false},
		{"no percentile key", `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":{}}`, false},
		{"null percentile", `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":{"percentile":null}}`, false},
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
