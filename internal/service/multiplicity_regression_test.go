package service

import (
	"bytes"
	"context"
	"math"
	"sort"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// multiplicity_regression_test.go pins the regression half of the
// multiplicity fold (U48 E2-S1): a member regression's coefficient
// p-values join their family after every other member, p_adjusted /
// significant_adjusted ride beside p_values (which never move), and the
// echo carries the family's m and excluded count — across Process, the
// return veto and Compose (serial and parallel).

// handHolm is Holm's step-down procedure written out by hand (not the
// core): sort ascending, multiply the j-th smallest by m − j + 1, carry
// the running maximum, cap at 1, return in input order.
func handHolm(ps []float64) []float64 {
	m := len(ps)
	order := make([]int, m)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return ps[order[a]] < ps[order[b]] })
	out := make([]float64, m)
	running := 0.0
	for j, idx := range order {
		v := math.Min(1, float64(m-j)*ps[idx])
		if v > running {
			running = v
		}
		out[idx] = running
	}
	return out
}

func regMember(slot string, method types.MultiplicityMethod, explicit bool, coefs ...string) descx.ResolvedMultiplicity {
	rm := member(slot, method)
	rm.Alpha = 0.05
	rm.Coefficients = coefs
	rm.ExplicitCoefficients = explicit
	return rm
}

// TestFoldRegressionMultiplicity is the unit table over hand-built plans
// and results.
func TestFoldRegressionMultiplicity(t *testing.T) {
	t.Parallel()

	holm, bonf := types.MultiplicityMethodHolm, types.MultiplicityMethodBonferroni
	t.Run("holm pools a test with two coefficients", func(t *testing.T) {
		test := result(types.TEST_T, 0.01)
		reg := &types.RegressionResult{Type: types.REG_OLS, PValues: map[string]float64{"(intercept)": 0.5, "x": 0.04, "z": 0.03}}
		plan := &descx.MultiplicityPlan{
			Tests:       []descx.ResolvedMultiplicity{member("tests[0]", holm)},
			Regressions: []descx.ResolvedMultiplicity{regMember("regressions[0]", holm, false, "x", "z")},
		}
		resp := &types.Response{Tests: []*types.TestResult{test}, Regressions: []*types.RegressionResult{reg}}
		if err := foldRequestMultiplicity(plan, resp); err != nil {
			t.Fatal(err)
		}
		// Holm, m = 3, sorted 0.01 (test), 0.03 (z), 0.04 (x):
		// 3·0.01 = 0.03; 2·0.03 = 0.06; max(0.06, 1·0.04) = 0.06.
		if math.Abs(*test.PAdjusted-0.03) > 1e-15 || test.Multiplicity.M != 3 {
			t.Errorf("test p_adjusted %v m %d, want 0.03 m 3", *test.PAdjusted, test.Multiplicity.M)
		}
		want := map[string]float64{"x": 0.06, "z": 0.06}
		if len(reg.PAdjusted) != 2 {
			t.Fatalf("p_adjusted = %v, want x and z only", reg.PAdjusted)
		}
		for k, v := range want {
			if math.Abs(reg.PAdjusted[k]-v) > 1e-15 {
				t.Errorf("p_adjusted[%s] = %v, want %v", k, reg.PAdjusted[k], v)
			}
			if sig, ok := reg.SignificantAdjusted[k]; !ok || sig {
				t.Errorf("significant_adjusted[%s] = %v (present %v), want false", k, sig, ok)
			}
		}
		if reg.PValues["x"] != 0.04 || reg.PValues["z"] != 0.03 || reg.PValues["(intercept)"] != 0.5 {
			t.Errorf("raw p_values moved: %v", reg.PValues)
		}
		if e := reg.Multiplicity; e == nil || e.M != 3 || e.Method != holm || e.Family != types.MultiplicityFamilyRequest || e.Alpha != 0.05 || e.Excluded != 0 {
			t.Errorf("echo = %+v, want holm request alpha 0.05 m 3", e)
		}
	})
	t.Run("an explicit coefficient without a p-value is excluded", func(t *testing.T) {
		reg := &types.RegressionResult{Type: types.REG_OLS, PValues: map[string]float64{"(intercept)": 0.5, "x": 0.02}}
		plan := &descx.MultiplicityPlan{Regressions: []descx.ResolvedMultiplicity{regMember("regressions[0]", bonf, true, "x", "w")}}
		if err := foldRequestMultiplicity(plan, &types.Response{Regressions: []*types.RegressionResult{reg}}); err != nil {
			t.Fatal(err)
		}
		if reg.PAdjusted["x"] != 0.02 || !math.IsNaN(reg.PAdjusted["w"]) {
			t.Errorf("p_adjusted = %v, want x 0.02 and w NaN", reg.PAdjusted)
		}
		if _, ok := reg.SignificantAdjusted["w"]; ok || !reg.SignificantAdjusted["x"] {
			t.Errorf("significant_adjusted = %v, want x true and no w", reg.SignificantAdjusted)
		}
		if reg.Multiplicity.M != 1 || reg.Multiplicity.Excluded != 1 {
			t.Errorf("echo = %+v, want m 1 excluded 1", reg.Multiplicity)
		}
	})
	t.Run("a default predictor without a p-value is skipped", func(t *testing.T) {
		reg := &types.RegressionResult{Type: types.REG_OLS, PValues: map[string]float64{"x": 0.02}}
		plan := &descx.MultiplicityPlan{Regressions: []descx.ResolvedMultiplicity{regMember("regressions[0]", bonf, false, "x", "w")}}
		if err := foldRequestMultiplicity(plan, &types.Response{Regressions: []*types.RegressionResult{reg}}); err != nil {
			t.Fatal(err)
		}
		if _, ok := reg.PAdjusted["w"]; ok || reg.Multiplicity.M != 1 || reg.Multiplicity.Excluded != 0 {
			t.Errorf("p_adjusted %v echo %+v, want x only, m 1, excluded 0", reg.PAdjusted, reg.Multiplicity)
		}
	})
	t.Run("a non-member regression is untouched", func(t *testing.T) {
		reg := &types.RegressionResult{Type: types.REG_OLS, PValues: map[string]float64{"x": 0.02}}
		plan := &descx.MultiplicityPlan{
			Tests:       []descx.ResolvedMultiplicity{member("tests[0]", holm)},
			Regressions: []descx.ResolvedMultiplicity{nonMember("regressions[0]")},
		}
		resp := &types.Response{Tests: []*types.TestResult{result(types.TEST_T, 0.01)}, Regressions: []*types.RegressionResult{reg}}
		if err := foldRequestMultiplicity(plan, resp); err != nil {
			t.Fatal(err)
		}
		if reg.PAdjusted != nil || reg.SignificantAdjusted != nil || reg.Multiplicity != nil || resp.Tests[0].Multiplicity.M != 1 {
			t.Errorf("non-member regression corrected: %+v (test m %d)", reg, resp.Tests[0].Multiplicity.M)
		}
	})
}

// regressionMultRequest is two tests and an OLS fit over m.pulse; with
// block, a request-level holm and the regression's own block claiming
// x and the intercept.
func regressionMultRequest(block bool) *types.Request {
	r := &types.Request{
		Cohort:       &types.Cohort{Filename: "m.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
		Tests: []*types.Test{
			{Type: types.TEST_T, Field: "x", SplitBy: "g", Label: "two"},
			{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Label: "r"},
		},
		Regressions: olsSpec("y", "x"),
	}
	if block {
		r.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm}
		r.Regressions[0].Multiplicity = &types.RegressionMultiplicity{Coefficients: []string{"x", "(intercept)"}}
	}
	return r
}

// TestRegressionMultiplicity_HolmMatchesHandComputation: a Holm family
// mixing two tests and two regression coefficients over a real cohort
// matches Holm written out by hand over the baseline's raw p-values, and
// no raw figure moves.
func TestRegressionMultiplicity_HolmMatchesHandComputation(t *testing.T) {
	t.Parallel()

	_, svc := overlayFoldService(t)
	base := mustProcess(t, svc, regressionMultRequest(false))
	got := mustProcess(t, svc, regressionMultRequest(true))
	ps := []float64{base.Tests[0].PValue, base.Tests[1].PValue,
		base.Regressions[0].PValues["x"], base.Regressions[0].PValues["(intercept)"]}
	for i, p := range ps {
		if math.IsNaN(p) || p <= 0 || p >= 1 {
			t.Fatalf("fixture p-value %d = %v; want a defined interior p", i, p)
		}
	}
	want := handHolm(ps)
	for i, r := range got.Tests {
		if r.PValue != ps[i] || r.PAdjusted == nil || math.Abs(*r.PAdjusted-want[i]) > 1e-12 || r.Multiplicity.M != 4 {
			t.Errorf("test %d: p %v p_adjusted %v echo %+v, want p %v adjusted %v m 4", i, r.PValue, r.PAdjusted, r.Multiplicity, ps[i], want[i])
		}
	}
	reg := got.Regressions[0]
	for j, k := range []string{"x", "(intercept)"} {
		if reg.PValues[k] != base.Regressions[0].PValues[k] {
			t.Errorf("raw p_values[%s] moved: %v -> %v", k, base.Regressions[0].PValues[k], reg.PValues[k])
		}
		if math.Abs(reg.PAdjusted[k]-want[2+j]) > 1e-12 {
			t.Errorf("p_adjusted[%s] = %v, want %v", k, reg.PAdjusted[k], want[2+j])
		}
		if reg.SignificantAdjusted[k] != (want[2+j] < 0.05) {
			t.Errorf("significant_adjusted[%s] = %v, want %v", k, reg.SignificantAdjusted[k], want[2+j] < 0.05)
		}
	}
	if reg.Multiplicity == nil || reg.Multiplicity.M != 4 || reg.Multiplicity.Alpha != types.DefaultMultiplicityAlpha {
		t.Errorf("regression echo = %+v, want m 4 alpha 0.05", reg.Multiplicity)
	}
	// Everything but the additive slots is the baseline's.
	reg.PAdjusted, reg.SignificantAdjusted, reg.Multiplicity = nil, nil, nil
	if !bytes.Equal(mustMarshal(t, base.Regressions), mustMarshal(t, got.Regressions)) {
		t.Errorf("regression result moved beyond the additive slots:\n got  %s\n want %s", mustMarshal(t, got.Regressions), mustMarshal(t, base.Regressions))
	}
}

// TestReturnSkipsRegressions_MultiplicityVeto: a `return` that excludes
// the regressions still fits a member regression, so the pooled tests'
// p_adjusted and m are the full run's; without the block the same
// selection fits nothing.
func TestReturnSkipsRegressions_MultiplicityVeto(t *testing.T) {
	// serial: reads deltas of process-wide internal/processing work counters, which concurrent tests bump

	_, svc := overlayFoldService(t)
	exclude := &types.Return{Exclude: []string{"regressions"}}
	full := mustProcess(t, svc, regressionMultRequest(true))
	mk := func(block bool) *types.Request {
		r := regressionMultRequest(block)
		r.Return = exclude
		return r
	}
	var shaped *types.Response
	if _, _, fits := inferentialWork(t, func() { shaped = mustProcess(t, svc, mk(true)) }); fits != 1 {
		t.Errorf("member regression under an excluding return: %d fits, want 1", fits)
	}
	if !bytes.Equal(mustMarshal(t, full.Tests), mustMarshal(t, shaped.Tests)) {
		t.Errorf("kept tests moved under the regressions skip\n got  %s\n want %s", mustMarshal(t, shaped.Tests), mustMarshal(t, full.Tests))
	}
	if _, _, fits := inferentialWork(t, func() { mustProcess(t, svc, mk(false)) }); fits != 0 {
		t.Errorf("no block: %d fits, want 0", fits)
	}
}

// TestRegressionMultiplicity_Compose: a `compose` family pools one
// coefficient from each slot (Bonferroni, m = 2) while a `request`
// family stays inside its slot (m = 1), and Compose and ComposeParallel
// answer byte for byte.
func TestRegressionMultiplicity_Compose(t *testing.T) {
	t.Parallel()

	svc := composeMultService(t)
	ctx := context.Background()
	slot := func(cohort string, family types.MultiplicityFamily) *types.Request {
		r := &types.Request{
			Label:        cohort,
			Cohort:       &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
			Regressions:  olsSpec("y", "x"),
		}
		if family != "" {
			r.Regressions[0].Multiplicity = &types.RegressionMultiplicity{Method: types.MultiplicityMethodBonferroni, Family: family, Coefficients: []string{"x"}}
		}
		return r
	}
	mk := func(family types.MultiplicityFamily) *types.ComposedRequest {
		return &types.ComposedRequest{Requests: []*types.Request{slot("m0.pulse", family), slot("m1.pulse", family)}}
	}
	base, err := svc.Compose(ctx, mk(""))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		family types.MultiplicityFamily
		m      int
	}{{types.MultiplicityFamilyCompose, 2}, {types.MultiplicityFamilyRequest, 1}} {
		t.Run(string(c.family), func(t *testing.T) {
			got, err := svc.Compose(ctx, mk(c.family))
			if err != nil {
				t.Fatal(err)
			}
			for i, resp := range got.Responses {
				p := base.Responses[i].Regressions[0].PValues["x"]
				reg := resp.Regressions[0]
				if want := math.Min(1, float64(c.m)*p); math.Abs(reg.PAdjusted["x"]-want) > 1e-15 || reg.Multiplicity.M != c.m || reg.Multiplicity.Family != c.family {
					t.Errorf("slot %d: p_adjusted %v echo %+v, want %v m %d", i, reg.PAdjusted["x"], reg.Multiplicity, want, c.m)
				}
			}
			par, err := svc.ComposeParallel(ctx, mk(c.family), ComposeOptions{MaxWorkers: 2})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(mustMarshal(t, got), mustMarshal(t, par)) {
				t.Error("ComposeParallel differs from Compose")
			}
		})
	}
}
