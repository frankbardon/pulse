package pulse

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// compare_vector_test.go pins a nested-model comparison of two vector
// fits (`compare` over battery fits, U69 #643): member-wise nesting over
// the co-moments — one row per restricted member j, lm(j ~ restricted
// others) against lm(j ~ full others). Predict ≡ runtime on every
// refusal and on the valid summary; each row equals, within float
// noise, the regression comparison of the same two linear models run
// through the regression engine; the rows are byte-identical across
// decode / shard workers and both orchestrators; every row is one member
// of the multiplicity family. The figures against R: internal/service
// compose_compare_vector_oracle_test.go.

// vecFit is a MAT_COVARIANCE vector fit request over the margins
// cohort's fields.
func vecFit(cohort string, fields ...string) *Request {
	return &Request{Cohort: &types.Cohort{Filename: cohort}, Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: fields}}}
}

// vecBatch is a batch over the vector fits m0 (restricted) and m1 (full)
// with the comparisons given.
func vecBatch(cohort string, r0, r1 *Request, compare ...types.FitComparisonSpec) *ComposedRequest {
	c := cmpBatch(cohort, nil, compare...)
	c.Fits = []types.FitSpec{{Name: "m0", Request: r0}, {Name: "m1", Request: r1}}
	return c
}

var vecCmp = types.FitComparisonSpec{Name: "adds", Restricted: "m0", Full: "m1"}

// TestCompareVector_PredictMatchesRuntime: every battery refusal is the
// same coded error at PredictCompose and on both Compose arms, before
// anything runs; a valid batch's predict summary names the test,
// distribution, tested members and rows each runtime result reports.
func TestCompareVector_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p := cmpPulse(t, Options{})
	filtered := vecFit(marginsCohort, "x1", "x2", "y")
	filtered.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "id", Values: []string{"0", "60"}}}
	pattern := vecFit(marginsCohort)
	pattern.Matrices[0].Fields, pattern.Matrices[0].Vector = nil, "v"
	pattern.Vectors = []types.VectorSpec{{Name: "v", Pattern: "x*"}}
	prob := func(r *Request) *Request {
		r.Matrices[0].Weight = types.SlotWeightOf(types.WeightSpec{Field: "x2", Kind: types.WeightKindProbability})
		return r
	}
	for _, tc := range []struct {
		name   string
		c      *ComposedRequest
		reason string
	}{
		{"not nested", vecBatch(marginsCohort, vecFit(marginsCohort, "x1", "c"), vecFit(marginsCohort, "x1", "x2", "y"), vecCmp), "not_nested"},
		{"swapped", vecBatch(marginsCohort, vecFit(marginsCohort, "x1", "x2", "y"), vecFit(marginsCohort, "x1", "x2"), vecCmp), "not_nested"},
		{"no extra member", vecBatch(marginsCohort, vecFit(marginsCohort, "x2", "x1"), vecFit(marginsCohort, "x1", "x2"), vecCmp), "no_extra_terms"},
		{"rows differ", vecBatch(marginsCohort, vecFit(marginsCohort, "x1", "x2"), filtered, vecCmp), "request_mismatch"},
		{"pattern", vecBatch(marginsCohort, pattern, vecFit(marginsCohort, "x1", "x2", "y"), vecCmp), "members_unlisted"},
		{"full a regression", vecBatch(marginsCohort, vecFit(marginsCohort, "x1", "x2"), cmpFit(marginsCohort, cmpOLS("x1")), vecCmp), "fit_kind"},
		{"probability weight", vecBatch(marginsCohort, prob(vecFit(marginsCohort, "x1")), prob(vecFit(marginsCohort, "x1", "x2")), vecCmp), "requires"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := p.PredictCompose(context.Background(), tc.c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_COMPARISON_INVALID) || env.Errors[0].Details["reason"] != tc.reason {
				t.Fatalf("predict errors %+v", env.Errors)
			}
			for name, run := range composeRuns(p) {
				resp, rerr := run(tc.c)
				ce := requireCoded(t, rerr, errors.PULSE_COMPARISON_INVALID)
				if resp != nil || env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}

	valid := func() *ComposedRequest {
		return vecBatch(marginsCohort, vecFit(marginsCohort, "y", "x1"), vecFit(marginsCohort, "x1", "c", "x2", "y"),
			types.FitComparisonSpec{Name: "f", Restricted: "m0", Full: "m1"},
			types.FitComparisonSpec{Name: "lrt", Restricted: "m0", Full: "m1", Test: types.ComparisonTestLRT},
			types.FitComparisonSpec{Name: "wald", Restricted: "m0", Full: "m1", Test: types.ComparisonTestWald})
	}
	summary := predictCompare(t, p, valid())
	if len(summary) != 3 || !slices.Equal(summary[0].Members, []string{"y", "x1"}) || !slices.Equal(summary[0].Terms, []string{"c", "x2"}) {
		t.Fatalf("predict summary %+v", summary)
	}
	for name, run := range composeRuns(p) {
		resp, err := run(valid())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for i, got := range resp.Comparisons {
			s := summary[i]
			rows := make([]string, len(got.Members))
			for k, m := range got.Members {
				rows[k] = m.Member
			}
			if got.Error != nil || got.Test != s.Test || got.Distribution != s.Distribution || !slices.Equal(got.Terms, s.Terms) || !slices.Equal(rows, s.Members) {
				t.Fatalf("%s %s: run %+v, predict %+v", name, got.Name, got, s)
			}
		}
	}
}

// TestCompareVector_MatchesRegressionComparison: each member row is the
// regression comparison of the same two linear models — lm(j ~
// restricted others) vs lm(j ~ full others) through REG_OLS — under f,
// lrt and wald, with one tested member and with two.
func TestCompareVector_MatchesRegressionComparison(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := cmpPulse(t, Options{})
	reg := func(target string, preds ...string) *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_OLS, Target: target, Predictors: preds}
	}
	close := func(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }
	for _, tc := range []struct {
		name       string
		restricted []string
		full       []string
	}{
		{"one tested", []string{"x1", "x2"}, []string{"x1", "x2", "y"}},
		{"two tested", []string{"x1", "x2"}, []string{"x1", "c", "x2", "y"}},
	} {
		for _, test := range []types.ComparisonTest{types.ComparisonTestF, types.ComparisonTestLRT, types.ComparisonTestWald} {
			spec := types.FitComparisonSpec{Name: "v", Restricted: "m0", Full: "m1", Test: test}
			resp, err := p.Compose(ctx, vecBatch(marginsCohort, vecFit(marginsCohort, tc.restricted...), vecFit(marginsCohort, tc.full...), spec))
			if err != nil {
				t.Fatal(err)
			}
			got := resp.Comparisons[0]
			if got.Error != nil || len(got.Members) != len(tc.restricted) {
				t.Fatalf("%s %s: %+v", tc.name, test, got)
			}
			for k, j := range tc.restricted {
				others := func(set []string) []string {
					return slices.DeleteFunc(slices.Clone(set), func(x string) bool { return x == j })
				}
				twin := cmpBatch(marginsCohort, map[string]*types.RegressionSpec{"r0": reg(j, others(tc.restricted)...), "r1": reg(j, others(tc.full)...)},
					types.FitComparisonSpec{Name: "r", Restricted: "r0", Full: "r1", Test: test})
				tr, err := p.Compose(ctx, twin)
				if err != nil {
					t.Fatal(err)
				}
				want, row := tr.Comparisons[0], got.Members[k]
				if want.Error != nil || row.Member != j || !close(row.Statistic, want.Statistic) || !close(row.PValue, want.PValue) ||
					!close(row.RestrictedDeviance, want.RestrictedDeviance) || !close(row.FullDeviance, want.FullDeviance) ||
					!close(row.Dispersion, want.Dispersion) || got.DF != want.DF || got.FullDFResidual != want.FullDFResidual ||
					got.RestrictedDFResidual != want.RestrictedDFResidual || got.DFResidual != want.DFResidual ||
					got.Distribution != want.Distribution || got.NObs != want.NObs {
					t.Fatalf("%s %s %s: row %+v (top %+v), regression %+v", tc.name, test, j, row, got, want)
				}
			}
		}
	}
}

// TestCompareVector_WorkerInvariant: the battery rows are byte-identical
// across decode workers (one file), shard workers (the archive) and
// Compose / ComposeParallel; the single-pair figures are null on the
// wire, keys kept.
func TestCompareVector_WorkerInvariant(t *testing.T) {
	t.Parallel()

	single, sharded := marginsModes(t)
	for _, group := range [][]scorerParityMode{single, sharded} {
		var want string
		for _, m := range group {
			for name, run := range composeRuns(m.p) {
				resp, err := run(vecBatch(m.cohort, vecFit(m.cohort, "x1", "x2"), vecFit(m.cohort, "x1", "x2", "y", "c"), vecCmp))
				if err != nil {
					t.Fatalf("%s %s: %v", m.name, name, err)
				}
				got := comparisonsJSON(t, resp)
				if want == "" {
					want = got
				} else if got != want {
					t.Fatalf("%s %s diverged:\n got %s\nwant %s", m.name, name, got, want)
				}
			}
		}
		if !strings.Contains(want, `"statistic":null`) || !strings.Contains(want, `"p_value":null`) || !strings.Contains(want, `"members":[{"member":"x1"`) {
			t.Fatalf("wire form %s", want)
		}
	}
}

// TestCompareVector_Multiplicity: under a holm block every member row is
// one test of the compose family (m counts the rows) and carries its
// adjusted p beside the raw one; the single-pair figures carry none. A
// none block is byte-identical to no block.
func TestCompareVector_Multiplicity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := cmpPulse(t, Options{})
	batch := func(m *types.Multiplicity) *ComposedRequest {
		c := vecBatch(marginsCohort, vecFit(marginsCohort, "x1", "x2", "c"), vecFit(marginsCohort, "x1", "x2", "c", "y"), vecCmp)
		c.Multiplicity = m
		return c
	}
	resp, err := p.Compose(ctx, batch(&types.Multiplicity{Method: types.MultiplicityMethodHolm}))
	if err != nil {
		t.Fatal(err)
	}
	got := resp.Comparisons[0]
	if got.PAdjusted != nil || len(got.Members) != 3 {
		t.Fatalf("comparison %+v", got)
	}
	for _, row := range got.Members {
		if row.PAdjusted == nil || row.Multiplicity == nil || row.Multiplicity.M != 3 || *row.PAdjusted < row.PValue {
			t.Fatalf("row %+v (multiplicity %+v)", row, row.Multiplicity)
		}
	}
	plain, err := p.Compose(ctx, batch(nil))
	if err != nil {
		t.Fatal(err)
	}
	none, err := p.Compose(ctx, batch(&types.Multiplicity{Method: types.MultiplicityMethodNone}))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(plain.Comparisons)
	b, _ := json.Marshal(none.Comparisons)
	if string(a) != string(b) || strings.Contains(string(a), "p_adjusted") {
		t.Fatalf("none moved the rows:\n%s\n%s", a, b)
	}
}
