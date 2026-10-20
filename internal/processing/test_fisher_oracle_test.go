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
func TestReferenceOracle_FisherExact(t *testing.T) {
	doc := loadReference[struct {
		Name          string   `json:"name"`
		Table         [4]int   `json:"table"`
		EmptyMargin   bool     `json:"empty_margin"`
		PValue        float64  `json:"p_value"`
		Estimate      *float64 `json:"estimate"`
		EstimateClass string   `json:"estimate_class"`
		REstimate     *float64 `json:"r_estimate"`
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
	var zeros, infs, empty, interior int
	cellNames := [4][2]string{{"r0", "c0"}, {"r0", "c1"}, {"r1", "c0"}, {"r1", "c1"}}
	for _, k := range doc.Cases {
		a, b, cc, d := k.Table[0], k.Table[1], k.Table[2], k.Table[3]
		checkOR(k.Name, "helper", fisherConditionalOddsRatio(a, b, cc, d), k.Estimate, k.EstimateClass)
		c.check(fisherExactTwoSided(a, b, cc, d), k.PValue, "%s helper p_value", k.Name)
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
		for arm, res := range map[string]*types.TestResult{
			"row":       runContingency(t, types.TEST_FISHER_EXACT, plain, ""),
			"frequency": runContingency(t, types.TEST_FISHER_EXACT, weighted, types.WeightKindFrequency),
		} {
			checkOR(k.Name, arm+" statistic", res.Statistic, k.Estimate, k.EstimateClass)
			checkOR(k.Name, arm+" details.odds_ratio", res.Details["odds_ratio"].(float64), k.Estimate, k.EstimateClass)
			c.check(res.PValue, k.PValue, "%s %s p_value", k.Name, arm)
		}
	}
	if zeros < 3 || infs < 3 || empty < 4 || interior < 6 {
		t.Fatalf("golden has %d zero, %d infinite, %d empty-margin, %d interior cases; want ≥ 3 / 3 / 4 / 6", zeros, infs, empty, interior)
	}
	c.done(len(doc.Cases))
}
