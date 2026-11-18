package regordinal_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/internal/regordinal"
	"github.com/frankbardon/pulse/types"
)

// TestRegOrdinal_ExponentiatedMatchesR (U68 E3-S2): exponentiate: true
// through Process reports, for every golden case — each link, the
// classical and the hc0 / hc1 / cluster covariances, frequency and
// probability weights — the predictors' cumulative odds ratios
// exp(coef(polr)), their delta-method standard errors exp(β)·SE and
// the exponentiated Wald interval of gen_ordinal.R at 1e-10, and no
// threshold key; the raw coefficient table is the golden's unchanged.
func TestRegOrdinal_ExponentiatedMatchesR(t *testing.T) {
	t.Parallel()
	robust := 0
	for _, c := range regordinal.LoadOracle(t) {
		if c.Exponentiated == nil {
			t.Fatalf("%s: golden has no exponentiated block", c.Name)
		}
		if c.Covariance != "classical" {
			robust++
		}
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			p := newInstance(t, nil)
			cs := cohortSpec{p: len(c.Predictors)}
			var labels []string
			if c.Levels[0] != "1" {
				cs.dict, labels = c.Levels, c.Levels
			}
			writeCohort(t, p, "ord.pulse", cs, c.Rows, labels)
			s := caseSpec(c, c.LevelsParam)
			s.Exponentiate = true
			res := fitOne(t, p, "ord.pulse", s)
			if res.Type != types.REG_ORDINAL {
				t.Fatalf("result type %s", res.Type)
			}
			checkAgainstGolden(t, c, res)
			e := res.Exponentiated
			if e == nil {
				t.Fatal("no exponentiated block")
			}
			for _, m := range []map[string]float64{e.Estimates, e.StdErrors, e.CILower, e.CIUpper} {
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				slices.Sort(keys)
				want := slices.Sorted(slices.Values(c.Predictors))
				if !slices.Equal(keys, want) {
					t.Fatalf("keys %v, want the predictors %v (no threshold)", keys, want)
				}
			}
			check := func(what string, got, want float64) {
				t.Helper()
				if e := rel(got, want); e > e2eRel {
					t.Errorf("%s = %.17g, R %.17g (rel %.3g)", what, got, want, e)
				}
			}
			for j, name := range c.Predictors {
				check("estimate "+name, e.Estimates[name], c.Exponentiated.Estimates[j])
				check("std_error "+name, e.StdErrors[name], c.Exponentiated.StdErrors[j])
				check("ci_lower "+name, e.CILower[name], c.Exponentiated.CILower[j])
				check("ci_upper "+name, e.CIUpper[name], c.Exponentiated.CIUpper[j])
			}
		})
	}
	if robust < 3 {
		t.Fatalf("%d robust-covariance cases, want hc0 / hc1 / cluster covered", robust)
	}
}

// TestRegOrdinal_ExponentiateAbsentIsByteIdentical: a fit with
// exponentiate is the fit without it plus the block, byte for byte.
func TestRegOrdinal_ExponentiateAbsentIsByteIdentical(t *testing.T) {
	t.Parallel()
	c := goldenCase(t, "logit_two_hc1")
	p := newInstance(t, nil)
	writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
	run := func(expo bool) []byte {
		s := caseSpec(c, nil)
		s.Exponentiate = expo
		resp, err := p.Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "ord.pulse"}, Regressions: []*types.RegressionSpec{s}})
		if err != nil {
			t.Fatal(err)
		}
		if expo {
			if resp.Regressions[0].Exponentiated == nil {
				t.Fatal("no exponentiated block")
			}
			resp.Regressions[0].Exponentiated = nil
		}
		b, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if a, b := run(true), run(false); string(a) != string(b) {
		t.Errorf("raw response moved:\n%s\n%s", a, b)
	}
}
