package mcmc_test

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/mcmc"
)

// gauss2 is a correlated 2-D Gaussian with unit variances and correlation
// 0.8, given by its precision matrix. It is FMA-free: every product that
// feeds an add is an explicit float64(a*b), so the draws are the same bits
// on every architecture.
type gauss2 struct{}

func (gauss2) Dim() int { return 2 }

func (gauss2) LogDensityGrad(theta, grad []float64) float64 {
	const c = 1 / (1 - 0.8*0.8) // precision = c * [[1, -0.8], [-0.8, 1]]
	a := float64(c * (theta[0] - float64(0.8*theta[1])))
	b := float64(c * (theta[1] - float64(0.8*theta[0])))
	grad[0], grad[1] = -a, -b
	return float64(-0.5 * (float64(theta[0]*a) + float64(theta[1]*b)))
}

// Sample a 2-D Gaussian with the zero-value NUTS sampler and check
// convergence. The seed fixes every draw, so the output is stable.
func Example() {
	res, err := mcmc.NUTS{}.Sample(context.Background(), gauss2{}, 42, mcmc.Options{
		Chains: 4, Warmup: 500, Draws: 500,
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	d := res.Diagnostics
	for j := range d.RHat {
		fmt.Printf("param %d: rhat<=1.01 %v, ess_bulk>=400 %v\n",
			j, d.RHat[j] <= 1.01, d.ESSBulk[j] >= 400)
	}
	div := 0
	for _, c := range d.Chains {
		div += c.Divergences
	}
	fmt.Println("chains:", len(res.Draws), "draws:", len(res.Draws[0][0]), "divergences:", div)
	// Output:
	// param 0: rhat<=1.01 true, ess_bulk>=400 true
	// param 1: rhat<=1.01 true, ess_bulk>=400 true
	// chains: 4 draws: 500 divergences: 0
}
