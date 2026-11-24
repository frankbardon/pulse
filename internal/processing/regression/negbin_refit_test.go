package regression

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestNegBinInterceptRefit_MatchesAlternation: the intercept-only
// negative binomial refit a selection scores (and lands on when it
// drops every predictor) is the full alternation's fit of a constant
// design — μ̂ = ȳ, θ̂ and SE(θ̂) — and a fixed θ is held.
func TestNegBinInterceptRefit_MatchesAlternation(t *testing.T) {
	t.Parallel()

	recs := loadGLMNBRefit(t).records()
	spec := glmNBRefitSpec(nil, 0)
	fam, err := selectFamily(spec.Family, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := glmRefit(recs, "y", nil, fam, spec.MaxIters, spec.Tol, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ThetaEstimated || got.Family.Name != fam.Name {
		t.Fatalf("intercept refit: estimated %v family %q", got.ThetaEstimated, got.Family.Name)
	}
	n := len(got.Y)
	x := make([]float64, n)
	for i := range x {
		x[i] = 1
	}
	pr := &irlsProblem{X: x, Y: got.Y, Prior: onesVec(n), K: 1, Columns: []string{InterceptKey},
		Family: fam, MaxIters: 500, Tol: 1e-13}
	nb, err := fitNegBin(pr, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !relNear(got.Theta, nb.theta, 1e-9) || !relNear(got.ThetaSE, nb.thetaSE, 1e-8) ||
		!relNear(got.Intercept, nb.sol.Beta[0], 1e-10) || !relNear(got.Deviance, nb.sol.Deviance, 1e-8) {
		t.Errorf("intercept refit θ %v se %v β0 %v dev %v; alternation θ %v se %v β0 %v dev %v",
			got.Theta, got.ThetaSE, got.Intercept, got.Deviance, nb.theta, nb.thetaSE, nb.sol.Beta[0], nb.sol.Deviance)
	}
	fixed := glmNBRefitSpec(nil, 2)
	g2, err := glmRefit(recs, "y", nil, fam, fixed.MaxIters, fixed.Tol, fixed, nil)
	if err != nil || g2.Theta != 2 || g2.ThetaEstimated || g2.ThetaSE != 0 {
		t.Fatalf("fixed intercept refit: %+v %v", g2, err)
	}
}

// TestWithThetaDerive_OnlyEstimatedTheta: θ̂ joins the replicate's
// derived columns LAST (after the margins columns) for an estimated θ
// only; a fixed θ, another family or another type leaves derive alone.
func TestWithThetaDerive_OnlyEstimatedTheta(t *testing.T) {
	t.Parallel()

	r := &refitResult{Theta: 3.5}
	margins := func(*refitResult) []float64 { return []float64{1, 2} }
	if got := withThetaDerive(glmNBRefitSpec(nil, 0), margins)(r); len(got) != 3 || got[2] != 3.5 || got[0] != 1 {
		t.Errorf("estimated θ with margins: %v", got)
	}
	if got := withThetaDerive(glmNBRefitSpec(nil, 0), nil)(r); len(got) != 1 || got[0] != 3.5 {
		t.Errorf("estimated θ alone: %v", got)
	}
	if withThetaDerive(glmNBRefitSpec(nil, 2), nil) != nil {
		t.Error("fixed θ derives a column")
	}
	if withThetaDerive(&types.RegressionSpec{Type: types.REG_GLM, Family: "poisson"}, nil) != nil {
		t.Error("poisson derives a θ column")
	}
}
