package regression

import (
	"math"
	"testing"
)

// TestGLMFamilies_SecondDerivative: every (family, link) pair's D2muDeta2
// is the derivative of its DmuDeta — a central difference at points
// inside each link's valid range, 1e-6 relative — so the marginal
// effects' delta-method gradient (margins.go) reads the link it fitted.
func TestGLMFamilies_SecondDerivative(t *testing.T) {
	t.Parallel()

	fams := map[string]glmFamily{}
	for family, links := range glmFamilies {
		for link, f := range links {
			fams[family+"/"+link] = f
		}
	}
	for _, link := range []string{"log", "identity", "sqrt"} {
		fams["negative_binomial/"+link] = negBinFamily(link, 1.7)
	}
	for name, f := range fams {
		if f.D2muDeta2 == nil {
			t.Errorf("%s has no D2muDeta2", name)
			continue
		}
		for _, eta := range []float64{0.15, 0.6, 1.3, 2.4} {
			if f.Link == "logit" || f.Link == "probit" || f.Link == "cloglog" {
				eta -= 1.2 // both signs of the linear predictor
			}
			h := 1e-5 * math.Max(1, math.Abs(eta))
			num := (f.DmuDeta(eta+h) - f.DmuDeta(eta-h)) / (2 * h)
			got := f.D2muDeta2(eta)
			if math.Abs(got-num) > 1e-6*math.Max(1, math.Abs(num)) {
				t.Errorf("%s at η = %g: μ″ = %.12g, central difference %.12g", name, eta, got, num)
			}
		}
	}
}
