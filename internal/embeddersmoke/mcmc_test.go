package embeddersmoke

import (
	"context"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/mcmc"
)

// halfNormalScale is an embedder model: σ ~ HalfNormal(1), sampled as
// x = log σ through mcmc.Positive with the log-Jacobian added.
type halfNormalScale struct{}

func (halfNormalScale) Dim() int { return 1 }

func (halfNormalScale) LogDensityGrad(theta, grad []float64) float64 {
	m := mcmc.Positive{}.Forward(theta[0])
	grad[0] = float64(-m.Y*m.DyDx) + m.DLogJac
	return float64(-0.5*m.Y*m.Y) + m.LogJac
}

// exactSampler is an embedder Sampler: independent HalfNormal draws,
// enough to prove the interfaces and the diagnostics compose from
// outside the module.
type exactSampler struct{}

func (exactSampler) Sample(ctx context.Context, model mcmc.Model, seed uint64, opts mcmc.Options) (mcmc.Result, error) {
	var res mcmc.Result
	for c := range opts.Chains {
		r := rand.New(rand.NewPCG(seed+uint64(c), (seed+uint64(c))^0x9E3779B97F4A7C15))
		draws := make([]float64, opts.Draws)
		for i := range draws {
			theta := []float64{detmath.Log(math.Abs(detmath.NormFloat64(r)))}
			out := []float64{theta[0]}
			if opts.Constrain != nil {
				opts.Constrain(theta, out)
			}
			draws[i] = out[0]
		}
		res.Draws = append(res.Draws, [][]float64{draws})
	}
	p := res.Param(0)
	res.Diagnostics = mcmc.Diagnostics{
		RHat: []float64{mcmc.RHat(p)}, ESSBulk: []float64{mcmc.ESSBulk(p)}, ESSTail: []float64{mcmc.ESSTail(p)},
	}
	return res, ctx.Err()
}

// TestMCMCFlow drives the public mcmc package the way an embedder would:
// its own Model and Sampler, a Constrain callback through a Transform,
// and the diagnostics read back.
func TestMCMCFlow(t *testing.T) {
	var model mcmc.Model = halfNormalScale{}
	grad := make([]float64, model.Dim())
	if lp := model.LogDensityGrad([]float64{0}, grad); math.Abs(lp+0.5) > 1e-15 || math.Abs(grad[0]) > 1e-15 {
		t.Fatalf("log density at σ=1: %g, grad %g", lp, grad[0])
	}
	var s mcmc.Sampler = exactSampler{}
	res, err := s.Sample(context.Background(), model, 7, mcmc.Options{
		Chains: 4, Draws: 500,
		Constrain: func(theta, out []float64) { out[0] = mcmc.Positive{}.Forward(theta[0]).Y },
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := res.Diagnostics; d.RHat[0] > 1.01 || d.ESSBulk[0] < 1000 || d.ESSTail[0] < 1000 {
		t.Errorf("independent draws: R̂ %g, bulk ESS %g, tail ESS %g", d.RHat[0], d.ESSBulk[0], d.ESSTail[0])
	}
	if y := res.Draws[0][0][0]; !(y > 0) {
		t.Errorf("constrained draw %g not positive", y)
	}
	var tr mcmc.Transform = mcmc.Bounded{Lo: 0, Hi: 2}
	if x := tr.Inverse(tr.Forward(0.25).Y); math.Abs(x-0.25) > 1e-12 {
		t.Errorf("Bounded round trip %g", x)
	}
	_ = mcmc.Unit{}
}
