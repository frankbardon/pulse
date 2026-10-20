package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestReferenceOracle_FisherExact (U36 E1-S4): TEST_FISHER_EXACT's odds
// ratio is fisher.test's conditional MLE — not the sample ad/bc — and
// its p is fisher.test's, on the row and frequency-weighted arms, at the
// shared relative 1e-10. The golden (scripts/reference/gen_test_outputs.R)
// refines fisher.test's uniroot-tolerance estimate (kept beside as
// r_estimate) and carries the boundary cases in fisher.test's order: a
// at the bottom of its support → 0, at the top → +Inf (estimate_class
// "inf", null on the wire). Empty-margin tables cannot reach the
// operator (two levels per axis) and pin the helper alone: 0, never 0/0.
//
// U36 E1-S8: details.ci_low / ci_high are fisher.test's conf.int at
// 1 − alpha, pinned at conf 0.95 and 0.90 on the helper and both arms
// (refined past ncp.L / ncp.U's uniroot tolerance, R's own bounds kept
// beside): a at the bottom of its support → lower exactly 0, at the top
// → upper +Inf (ci_high_class "inf"). The p's tie rule is fisher.test's
// relErr 1 + 1e-7: near_tie_relerr has a table 8.4e-8 relative above the
// observed one, a tie in R but not under a 1e-12 slack.
func TestReferenceOracle_FisherExact(t *testing.T) {
	type ciCase struct {
		ConfLevel   float64  `json:"conf_level"`
		CILow       float64  `json:"ci_low"`
		RCILow      float64  `json:"r_ci_low"`
		CIHigh      *float64 `json:"ci_high"`
		CIHighClass string   `json:"ci_high_class"`
		RCIHigh     *float64 `json:"r_ci_high"`
	}
	doc := loadReference[struct {
		Name          string   `json:"name"`
		Table         [4]int   `json:"table"`
		EmptyMargin   bool     `json:"empty_margin"`
		PValue        float64  `json:"p_value"`
		Estimate      *float64 `json:"estimate"`
		EstimateClass string   `json:"estimate_class"`
		REstimate     *float64 `json:"r_estimate"`
		CI            []ciCase `json:"ci"`
	}](t, "test_fisher_exact")
	c := newOracleCheck(t, "TEST_FISHER_EXACT", oracleRelTol, oracleProbAbsFloor)
	checkOR := func(name, what string, got float64, want *float64, class string) {
		t.Helper()
		switch {
		case class == "inf":
			if !math.IsInf(got, 1) {
				t.Errorf("%s %s odds ratio = %v, want +Inf (fisher.test: a at the top of its support)", name, what, got)
			}
		case class != "":
			t.Fatalf("%s: unknown estimate_class %q", name, class)
		case *want == 0:
			if got != 0 {
				t.Errorf("%s %s odds ratio = %v, want exactly 0 (fisher.test: a at the bottom of its support)", name, what, got)
			}
		default:
			c.check(got, *want, "%s %s odds ratio", name, what)
		}
	}
	// uniroot's default tol (.Machine$double.eps^0.25) on psi below 1,
	// on 1/psi above: how far fisher.test's own figure may sit.
	withinUniroot := func(name, what string, r, refined float64) {
		t.Helper()
		if refined == 0 || refined == 1 || math.IsInf(refined, 1) {
			if r != refined {
				t.Errorf("%s: fisher.test %s %v, refined boundary %v", name, what, r, refined)
			}
			return
		}
		gap := math.Abs(r - refined)
		if refined > 1 {
			gap = math.Abs(1/r - 1/refined)
		}
		if gap > 2*1.220703125e-4 {
			t.Errorf("%s: fisher.test %s %v is %v from the refined %v", name, what, r, gap, refined)
		}
	}
	checkCI := func(name, what string, ci ciCase, lower, upper float64) {
		t.Helper()
		if ci.CILow == 0 {
			if lower != 0 {
				t.Errorf("%s %s ci_low@%v = %v, want exactly 0 (a at the bottom of its support)", name, what, ci.ConfLevel, lower)
			}
		} else {
			c.check(lower, ci.CILow, "%s %s ci_low@%v", name, what, ci.ConfLevel)
		}
		switch ci.CIHighClass {
		case "inf":
			if !math.IsInf(upper, 1) {
				t.Errorf("%s %s ci_high@%v = %v, want +Inf (a at the top of its support)", name, what, ci.ConfLevel, upper)
			}
		case "":
			c.check(upper, *ci.CIHigh, "%s %s ci_high@%v", name, what, ci.ConfLevel)
		default:
			t.Fatalf("%s: unknown ci_high_class %q", name, ci.CIHighClass)
		}
	}
	var zeros, infs, empty, interior, ciZero, ciInf, nearTie int
	confs := map[float64]bool{}
	cellNames := [4][2]string{{"r0", "c0"}, {"r0", "c1"}, {"r1", "c0"}, {"r1", "c1"}}
	for _, k := range doc.Cases {
		a, b, cc, d := k.Table[0], k.Table[1], k.Table[2], k.Table[3]
		checkOR(k.Name, "helper", fisherConditionalOddsRatio(a, b, cc, d), k.Estimate, k.EstimateClass)
		c.check(fisherExactTwoSided(a, b, cc, d), k.PValue, "%s helper p_value", k.Name)
		if len(k.CI) != 2 {
			t.Fatalf("%s: %d ci entries, want 2", k.Name, len(k.CI))
		}
		for _, ci := range k.CI {
			confs[ci.ConfLevel] = true
			lower, upper := fisherConditionalCI(a, b, cc, d, ci.ConfLevel)
			checkCI(k.Name, "helper", ci, lower, upper)
			withinUniroot(k.Name, "ci_low", ci.RCILow, ci.CILow)
			if ci.CILow == 0 {
				ciZero++
			}
			if ci.CIHighClass == "inf" {
				ciInf++
			} else {
				withinUniroot(k.Name, "ci_high", *ci.RCIHigh, *ci.CIHigh)
			}
		}
		if k.Name == "near_tie_relerr" {
			nearTie++
			// The case discriminates: a 1e-12 log slack (the pre-E1-S8
			// rule) leaves the near-tied table out and moves the p.
			if old := fisherSlackP(a, b, cc, d, 1e-12); math.Abs(old-k.PValue) <= 1e-6*k.PValue {
				t.Errorf("near_tie_relerr: 1e-12-slack p %v ≈ fisher.test %v — the case does not pin the tie rule", old, k.PValue)
			}
		}
		switch {
		case k.EmptyMargin:
			empty++
			continue
		case k.EstimateClass == "inf":
			infs++
		case *k.Estimate == 0:
			zeros++
		default:
			interior++
			// fisher.test's own estimate sits within its uniroot tol
			// (.Machine$double.eps^0.25 on psi below 1, on 1/psi above).
			if k.REstimate != nil && *k.Estimate != 1 {
				gap := math.Abs(*k.REstimate - *k.Estimate)
				if *k.Estimate > 1 {
					gap = math.Abs(1 / *k.REstimate - 1 / *k.Estimate)
				}
				if gap > 2*1.220703125e-4 {
					t.Errorf("%s: fisher.test estimate %v is %v from the refined %v", k.Name, *k.REstimate, gap, *k.Estimate)
				}
			}
		}
		var plain, weighted []labelOrderRow
		for i, n := range k.Table {
			for range n {
				plain = append(plain, labelOrderRow{cellNames[i][0], cellNames[i][1], 1})
			}
			if n > 0 {
				weighted = append(weighted, labelOrderRow{cellNames[i][0], cellNames[i][1], float64(n)})
			}
		}
		for _, ci := range k.CI {
			// Every test's confidence knob: the interval sits at 1 − alpha.
			alpha := 1 - ci.ConfLevel
			for arm, res := range map[string]*types.TestResult{
				"row":       runContingencyAlpha(t, types.TEST_FISHER_EXACT, plain, "", alpha),
				"frequency": runContingencyAlpha(t, types.TEST_FISHER_EXACT, weighted, types.WeightKindFrequency, alpha),
			} {
				checkOR(k.Name, arm+" statistic", res.Statistic, k.Estimate, k.EstimateClass)
				checkOR(k.Name, arm+" details.odds_ratio", res.Details["odds_ratio"].(float64), k.Estimate, k.EstimateClass)
				c.check(res.PValue, k.PValue, "%s %s p_value", k.Name, arm)
				checkCI(k.Name, arm+" details", ci, res.Details["ci_low"].(float64), res.Details["ci_high"].(float64))
			}
		}
	}
	if zeros < 3 || infs < 3 || empty < 4 || interior < 6 {
		t.Fatalf("golden has %d zero, %d infinite, %d empty-margin, %d interior cases; want ≥ 3 / 3 / 4 / 6", zeros, infs, empty, interior)
	}
	if ciZero < 8 || ciInf < 8 || nearTie != 1 || !confs[0.95] || !confs[0.9] {
		t.Fatalf("golden has %d zero-lower and %d infinite-upper intervals, %d near-tie cases, levels %v; want ≥ 8 / 8, 1, {0.95, 0.9}", ciZero, ciInf, nearTie, confs)
	}
	c.done(len(doc.Cases))
}

// fisherSlackP is the two-sided p under a log-probability slack (the
// pre-E1-S8 rule, 1e-12), kept only to prove the near-tie case
// discriminates between it and fisher.test's relErr.
func fisherSlackP(a, b, c, d int, slack float64) float64 {
	lo, logdc := fisherLogDensities(a, b, c, d)
	var sum float64
	for _, l := range logdc {
		if l <= logdc[a-lo]+slack {
			sum += math.Exp(l)
		}
	}
	return sum
}

// TestFisherSolveTheta_NoRootStops: a solver handed a function with no
// root (a support boundary its callers keep out) stops at the bracket
// cap with the boundary — 0 or +Inf — rather than doubling forever.
func TestFisherSolveTheta_NoRootStops(t *testing.T) {
	if got := fisherSolveTheta(func(float64) (float64, float64) { return 1, 0 }); got != 0 {
		t.Errorf("always-positive g: got %v, want 0", got)
	}
	if got := fisherSolveTheta(func(float64) (float64, float64) { return -1, 0 }); !math.IsInf(got, 1) {
		t.Errorf("always-negative g: got %v, want +Inf", got)
	}
}
