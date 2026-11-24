package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// compare_sem_parity_test.go pins the nesting rule of a comparison of
// two SEM fits (U69 #643) predict ≡ runtime: every static refusal is
// the same PULSE_COMPARISON_INVALID (message and details) from
// PredictCompose, Compose and ComposeParallel; a valid pair's predict
// summary is what the run reports; the run-only refusals (df,
// conventions) fail the comparison
// alone with PULSE_SLOT_FAILED while predict accepts the batch.

const semCompareCFA = "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3\nservice =~ s1 + s2 + s3"

// semPair is a batch of two SEM fits over brandMembers (params p0 / p1)
// compared under test.
func semPair(p0, p1 map[string]any, test types.ComparisonTest) *pulse.ComposedRequest {
	fit := func(p map[string]any) *types.Request {
		raw, _ := json.Marshal(p)
		return &types.Request{Cohort: &types.Cohort{Filename: "brand.pulse"},
			Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: brandMembers, Params: raw}}}
	}
	return &pulse.ComposedRequest{
		Fits: []types.FitSpec{{Name: "m0", Request: fit(p0)}, {Name: "m1", Request: fit(p1)}},
		Requests: []*types.Request{{Label: "rows", Cohort: &types.Cohort{Filename: "brand.pulse"},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "q1", Label: "n"}}}},
		Compare: []types.FitComparisonSpec{{Name: "c", Restricted: "m0", Full: "m1", Test: test}},
	}
}

func semComposeRuns(p *pulse.Pulse) map[string]func(*pulse.ComposedRequest) (*pulse.ComposedResponse, error) {
	ctx := context.Background()
	return map[string]func(*pulse.ComposedRequest) (*pulse.ComposedResponse, error){
		"Compose": func(c *pulse.ComposedRequest) (*pulse.ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func(c *pulse.ComposedRequest) (*pulse.ComposedResponse, error) {
			return p.ComposeParallel(ctx, c, pulse.ComposeOptions{MaxWorkers: 4})
		},
	}
}

func semStruct(extra string) map[string]any {
	return map[string]any{"model": semCompareCFA + "\nrecommend ~ quality + value" + extra, "meanstructure": true}
}

// TestCompareSEM_PredictMatchesRuntime: the static refusals, the valid
// summary and the run-only fails-alone outcomes, predict ≡ runtime.
func TestCompareSEM_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	ctx := context.Background()
	full := semStruct(" + service")
	for _, tc := range []struct {
		name   string
		req    *pulse.ComposedRequest
		reason string
	}{
		{"kind mismatch", func() *pulse.ComposedRequest {
			c := semPair(semStruct(""), full, "")
			c.Fits[1].Request = &types.Request{Cohort: &types.Cohort{Filename: "brand.pulse"},
				Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "recommend", Predictors: []string{"q1"}}}}
			return c
		}(), "kind_mismatch"},
		{"wald", semPair(semStruct(""), full, types.ComparisonTestWald), "test_unsupported"},
		{"unknown test", semPair(semStruct(""), full, "score"), "test_unknown"},
		{"rows differ", func() *pulse.ComposedRequest {
			c := semPair(semStruct(""), full, "")
			c.Fits[1].Request.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "q1", Values: []string{"1", "4"}}}
			return c
		}(), "request_mismatch"},
		{"estimator differs", semPair(semStruct(""), map[string]any{"model": full["model"], "meanstructure": true, "estimator": "dwls",
			"ordered": brandOrdered}, ""), "request_mismatch"},
		{"same model", semPair(full, map[string]any{"model": full["model"], "meanstructure": true, "ci_level": 0.9}, ""), "same_model"},
		{"observed differ", semPair(map[string]any{"model": "quality =~ q1 + q2\nvalue =~ v1 + v2 + v3\nservice =~ s1 + s2 + s3\nrecommend ~ quality",
			"meanstructure": true}, full, ""), "observed_differ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := p.PredictCompose(ctx, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_COMPARISON_INVALID) {
				t.Fatalf("predict errors %+v", env.Errors)
			}
			for name, run := range semComposeRuns(p) {
				resp, rerr := run(tc.req)
				var ce *errors.CodedError
				if !stderrors.As(rerr, &ce) || ce.Code != errors.PULSE_COMPARISON_INVALID {
					t.Fatalf("%s: %v", name, rerr)
				}
				if resp != nil || ce.Details["reason"] != tc.reason {
					t.Fatalf("%s: reason %v, want %s", name, ce.Details["reason"], tc.reason)
				}
				if env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}

	// Valid at predict: the summary is what the run reports — or the
	// run-only refusal fails the comparison alone.
	for _, tc := range []struct {
		name   string
		req    *pulse.ComposedRequest
		reason string // "" — the comparison runs
	}{
		{"valid", semPair(semStruct(""), full, ""), ""},
		{"swapped (df)", semPair(full, semStruct(""), types.ComparisonTestLRT), "df_not_positive"},
		{"conventions differ", semPair(map[string]any{"model": semCompareCFA + "\nrecommend ~ quality + value"},
			map[string]any{"model": semCompareCFA + "\nrecommend ~ quality + value + service\nq1 ~ 1"}, ""), "conventions_differ"},
		{"means differ", semPair(map[string]any{"model": semCompareCFA + "\nrecommend ~ a*quality + value\nd := 2*a"},
			map[string]any{"model": semCompareCFA + "\nrecommend ~ quality + value + service\nq1 ~ 1"}, ""), "conventions_differ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := p.PredictCompose(ctx, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) > 0 {
				t.Fatalf("predict refused: %+v", *env.Errors[0])
			}
			raw, _ := json.Marshal(env.Data)
			var data struct {
				Compare []descriptor.ComparisonSummary `json:"compare"`
			}
			if err := json.Unmarshal(raw, &data); err != nil || len(data.Compare) != 1 {
				t.Fatalf("summary %s: %v", raw, err)
			}
			s := data.Compare[0]
			if s.Test != types.ComparisonTestLRT || s.Distribution != types.ComparisonDistributionChiSquare || s.Terms == nil || len(s.Terms) != 0 {
				t.Fatalf("summary %+v", s)
			}
			for name, run := range semComposeRuns(p) {
				resp, err := run(tc.req)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				r := resp.Comparisons[0]
				if tc.reason == "" {
					if r.Error != nil || r.Test != s.Test || r.Distribution != s.Distribution || !(r.DF > 0) {
						t.Fatalf("%s: %+v (error %+v)", name, r, r.Error)
					}
					continue
				}
				if r.Error == nil || r.Error.Code != string(errors.PULSE_COMPARISON_INVALID) || r.Error.Details["reason"] != tc.reason {
					t.Fatalf("%s: error %+v, want reason %s", name, r.Error, tc.reason)
				}
				warned := false
				for _, w := range resp.Warnings {
					warned = warned || (w != nil && w.Code == string(errors.PULSE_SLOT_FAILED))
				}
				if !warned {
					t.Fatalf("%s: no PULSE_SLOT_FAILED warning", name)
				}
			}
		})
	}
}
