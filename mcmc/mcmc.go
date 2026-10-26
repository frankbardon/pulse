package mcmc

import "context"

// Model is a target density on the unconstrained space R^Dim().
//
// LogDensityGrad returns log p(theta) up to an additive constant and
// writes ∂ log p / ∂ theta into grad (len(grad) == len(theta) == Dim()).
// A parameter the model constrains (a scale, a probability, a bounded
// rate) is sampled unconstrained and mapped through a Transform, whose
// log-Jacobian the model adds to the returned density. A point outside
// the support returns -Inf (grad is then ignored). A sampler running
// chains concurrently calls LogDensityGrad from several goroutines, so a
// Model must be safe for concurrent use.
type Model interface {
	Dim() int
	LogDensityGrad(theta, grad []float64) float64
}

// Sampler draws from a Model. The same model, seed and options give the
// same Result on every architecture and for any degree of parallelism.
// A cancelled ctx returns ctx.Err().
type Sampler interface {
	Sample(ctx context.Context, model Model, seed uint64, opts Options) (Result, error)
}

// Options are the run settings every sampler shares. A zero field takes
// the sampler's documented default; a sampler's own tuning knobs live on
// the sampler value.
type Options struct {
	// Chains is the number of independent chains.
	Chains int
	// Warmup is the number of adaptation iterations per chain, discarded.
	Warmup int
	// Draws is the number of retained iterations per chain.
	Draws int
	// Init is the unconstrained starting point of every chain
	// (len == Dim()); nil lets the sampler draw one per chain.
	Init []float64
	// Constrain, when set, maps each retained unconstrained draw theta to
	// the values stored in Result.Draws (len(out) == Dim()), typically by
	// applying each parameter's Transform. Nil stores theta itself. It is
	// called concurrently when chains run in parallel.
	Constrain func(theta, out []float64)
}

// Result is a sampler run: the retained draws and their diagnostics.
type Result struct {
	// Draws is indexed [chain][parameter][draw]: the retained draws of
	// each parameter per chain, constrained by Options.Constrain when set.
	Draws [][][]float64
	// Diagnostics are the convergence diagnostics of Draws.
	Diagnostics Diagnostics
}

// Param returns parameter j's draws per chain ([chain][draw]), the shape
// RHat, ESSBulk and ESSTail take. The slices alias Draws.
func (r Result) Param(j int) [][]float64 {
	out := make([][]float64, len(r.Draws))
	for c, chain := range r.Draws {
		out[c] = chain[j]
	}
	return out
}

// Diagnostics summarise a run. The per-parameter slices are indexed like
// the second axis of Result.Draws; an undefined value is NaN.
type Diagnostics struct {
	// RHat is RHat of each parameter's chains.
	RHat []float64
	// ESSBulk is ESSBulk of each parameter's chains.
	ESSBulk []float64
	// ESSTail is ESSTail of each parameter's chains.
	ESSTail []float64
	// Chains holds each chain's sampler statistics, in chain order.
	Chains []ChainStats
}

// ChainStats are one chain's sampler statistics. A sampler without a
// given notion (a step size, a tree) leaves the field zero.
type ChainStats struct {
	// StepSize is the integrator step size fixed at the end of warmup.
	StepSize float64
	// Metric is the adapted diagonal inverse metric (len == Dim()).
	Metric []float64
	// MeanAcceptStat is the mean acceptance statistic over the draws.
	MeanAcceptStat float64
	// Divergences counts retained transitions that diverged.
	Divergences int
	// TreeDepthSaturated counts retained transitions that hit the
	// sampler's maximum tree depth.
	TreeDepthSaturated int
	// TreeDepth is each retained transition's tree depth.
	TreeDepth []int
	// Divergent marks each retained transition that diverged.
	Divergent []bool
	// GradientEvals counts LogDensityGrad calls, warmup included.
	GradientEvals int
}
