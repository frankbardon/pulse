package regordinal

import (
	"math"
	"testing"
)

// nominal_test.go pins the partial proportional-odds fitter's algebra
// beside the R oracle (oracle_test.go checkNominal): the empty nominal
// set is the proportional-odds model, and the analytic gradient and
// information are the finite differences of ℓ.

// TestPPO_EmptyNominalIsProportionalOdds: with no predictor freed the
// partial model is polr's: the same ℓ, gradient and information as the
// proportional-odds design at any θ, and its fit from the MLE stays
// there (a zero test statistic).
func TestPPO_EmptyNominalIsProportionalOdds(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"logit_two", "probit_three", "cloglog_five"} {
		c := oracleCaseNamed(t, name)
		d, _ := caseDesign(c)
		l := links[c.Link]
		sol, fail := d.newton(l)
		if fail != nil {
			t.Fatalf("%s: %+v", name, fail)
		}
		m := newPPO(d, make([]bool, d.p))
		theta := m.start(sol.theta)
		theta[0] += 0.13 // off the optimum, so the gradient is not ~0
		llP, okP := m.loglik(l, theta)
		llD, okD := d.loglik(l, theta)
		if !okP || !okD || !(math.Abs(llP-llD) <= 1e-12*math.Abs(llD)) {
			t.Errorf("%s: ℓ partial %v, polr %v", name, llP, llD)
		}
		gP, iP := m.gradInfo(l, theta)
		gD, iD := d.gradInfo(l, theta)
		for j := range gP {
			if !(math.Abs(gP[j]-gD[j]) <= 1e-10*math.Max(math.Abs(gD[j]), 1)) {
				t.Errorf("%s: grad[%d] partial %v, polr %v", name, j, gP[j], gD[j])
			}
			for k := 0; k <= j; k++ {
				if a, b := iP.At(j, k), iD.At(j, k); !(math.Abs(a-b) <= 1e-10*math.Max(math.Abs(b), 1)) {
					t.Errorf("%s: info[%d][%d] partial %v, polr %v", name, j, k, a, b)
				}
			}
		}
		ll, f := m.fit(l, sol.theta)
		if f != nil || !(math.Abs(2*(ll-sol.ll)) <= 1e-9) {
			t.Errorf("%s: empty-set fit ℓ %v (failure %+v), polr %v", name, ll, f, sol.ll)
		}
	}
}

// TestPPO_DerivativesMatchFiniteDifferences: with predictors freed, the
// analytic gradient is the central difference of ℓ and the information
// the negated central difference of the gradient, for every link.
func TestPPO_DerivativesMatchFiniteDifferences(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"logit_two", "probit_three", "cloglog_five"} {
		c := oracleCaseNamed(t, name)
		d, _ := caseDesign(c)
		l := links[c.Link]
		sol, fail := d.newton(l)
		if fail != nil {
			t.Fatalf("%s: %+v", name, fail)
		}
		nominal := make([]bool, d.p)
		nominal[0] = true
		nominal[d.p-1] = true
		m := newPPO(d, nominal)
		theta := m.start(sol.theta)
		for j := range theta {
			theta[j] += 0.01 * float64(j%3-1)
		}
		grad, info := m.gradInfo(l, theta)
		const h = 1e-5
		at := func(j int, delta float64) []float64 {
			v := append([]float64(nil), theta...)
			v[j] += delta
			return v
		}
		for j := range theta {
			up, _ := m.loglik(l, at(j, h))
			dn, _ := m.loglik(l, at(j, -h))
			fd := (up - dn) / (2 * h)
			if !(math.Abs(fd-grad[j]) <= 1e-5*math.Max(math.Abs(grad[j]), 1)) {
				t.Errorf("%s: grad[%d] = %v, finite difference %v", name, j, grad[j], fd)
			}
			gu, _ := m.gradInfo(l, at(j, h))
			gd, _ := m.gradInfo(l, at(j, -h))
			for k := range theta {
				fd := -(gu[k] - gd[k]) / (2 * h)
				if got := info.At(j, k); !(math.Abs(fd-got) <= 1e-4*math.Max(math.Abs(got), 1)) {
					t.Errorf("%s: info[%d][%d] = %v, finite difference %v", name, j, k, got, fd)
				}
			}
		}
	}
}

// oracleCaseNamed is the golden case name.
func oracleCaseNamed(t *testing.T, name string) oracleCase {
	t.Helper()
	for _, c := range loadOracle(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no golden case %s", name)
	return oracleCase{}
}
