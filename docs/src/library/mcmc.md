# Bayesian Sampling (`mcmc`)

`github.com/frankbardon/pulse/mcmc` is Pulse's seeded Markov chain Monte Carlo core: a No-U-Turn sampler (NUTS), change-of-variables helpers and convergence diagnostics. It knows nothing about any particular statistical model. It imports only the standard library and Pulse's `linalg` and deterministic-math packages.

```go
import "github.com/frankbardon/pulse/mcmc"
```

## The `Model` contract

A `Model` is a log density, up to an additive constant, on the **unconstrained** space R^`Dim()`, plus its gradient:

```go
type Model interface {
	Dim() int
	LogDensityGrad(theta, grad []float64) float64
}
```

`LogDensityGrad` returns log p(theta) and writes the gradient into `grad`. A point outside the support returns `-Inf`. The sampler calls it from several goroutines (one per chain), so a `Model` must be safe for concurrent use.

A parameter that must be positive, a probability or a bounded rate is sampled on the real line and mapped through a `Transform`: `Positive` (`exp`), `Unit` (logistic) and `Bounded{Lo, Hi}`. `Forward(x)` returns the constrained value `Y`, its derivative `DyDx`, and the log-Jacobian `LogJac` with its derivative `DLogJac`. The model **adds `LogJac` to its log density** so the sampler targets the right distribution, and chains `DyDx` / `DLogJac` through its hand-written gradient. `Inverse` maps a constrained value back, for building a starting point.

`Options.Constrain(theta, out)` maps each retained unconstrained draw to the values stored in `Result.Draws`, typically by applying each parameter's `Transform`. Nil stores `theta` itself. It is called concurrently, one goroutine per chain.

## Sampling

`NUTS` is Stan's multinomial No-U-Turn sampler with a diagonal metric, dual-averaging step-size adaptation and windowed metric adaptation during warmup. Its zero value is usable:

```go
res, err := mcmc.NUTS{}.Sample(ctx, model, 42, mcmc.Options{Chains: 4, Warmup: 500, Draws: 500})
```

| Field | Zero value means |
|---|---|
| `NUTS.MaxTreeDepth` | `DefaultMaxTreeDepth` (10) |
| `NUTS.TargetAccept` | `DefaultTargetAccept` (0.8) |
| `NUTS.Workers` | `min(Chains, GOMAXPROCS)`; changes wall time only |
| `Options.Chains` | `DefaultChains` (4) |
| `Options.Warmup` | `DefaultWarmup` (1000); a zero field takes the default, so warmup cannot be 0 |
| `Options.Draws` | `DefaultDraws` (1000) |
| `Options.Init` | each chain draws Uniform(−2, 2) per coordinate until the density and gradient are finite |

`Result.Draws` is indexed `[chain][parameter][draw]`; `Result.Param(j)` returns parameter `j` as `[chain][draw]`. `Result.Diagnostics.Chains[c]` holds each chain's `StepSize`, adapted `Metric`, `MeanAcceptStat`, `Divergences`, `TreeDepthSaturated` and per-transition `TreeDepth` / `Divergent`. The realised `MeanAcceptStat` sits above `TargetAccept` (about 0.89 at 0.8) because the retained step size is the averaged iterate; Stan does the same. E-BFMI and per-draw energy are not recorded.

One deliberate departure from Stan's source: when warmup is under 150 iterations, Stan's short-warmup branch never re-initialises the slow adaptation window, so the metric never adapts. Pulse sets the window up as intended, so the metric adapts.

A cancelled `ctx` is honoured at every iteration and returns `ctx.Err()`. A panic in the model or in `Constrain` is re-raised on the caller's goroutine. Invalid options, model shape or `Init` are `PULSE_MCMC_INVALID` (`reason` `nil_model`, `dim`, `chains`, `warmup`, `draws`, `max_tree_depth`, `workers`, `target_accept`, `init_length`, `init_not_finite`, `init_density_not_finite`); a model the sampler cannot run is `PULSE_MCMC_FAILED` (`reason` `init_not_found`, `step_size_unbounded`, `step_size_zero`, `metric_not_finite`, plus `details.chain`). Both are coded errors carrying `details.reason`, and neither returns a result.

## Seeding and reproducibility

Each chain draws from its own PCG stream derived from `(seed, chain index)`; chains run concurrently and merge in chain order. The same model, seed and options give the **same bytes** across runs, any `Workers` or `GOMAXPROCS`, and arm64 and amd64.

That guarantee extends to your model only if the model is deterministic and **FMA-free**. arm64 fuses `a*b + c` into one instruction and amd64 does not, and NUTS compares energies, so one ulp of drift changes a draw. Wrap every product that feeds an add as `float64(a*b)`, and use `linalg/detmath` (`Exp`, `Log`, `Log1p`, `Lgamma`, `Erf`, `Pow`, `NormFloat64`) in place of `math`.

## Diagnostics and thresholds

| Function | Meaning | Healthy |
|---|---|---|
| `RHat(chains)` | rank-normalised split R̂ (Vehtari et al. 2021): the larger of the bulk and folded values | ≤ 1.01 |
| `ESSBulk(chains)` | effective sample size of the rank-normalised split chains | ≥ 400 |
| `ESSTail(chains)` | the smaller ESS of the 5% and 95% quantile indicators | ≥ 400 |

All three take `[][]float64` (`[chain][draw]`) and return NaN when undefined (one chain, a constant parameter). `Sample` fills `Diagnostics.RHat`, `ESSBulk` and `ESSTail` per parameter. They match ArviZ on committed fixture chains. Also check `Divergences` (any retained divergence is a warning sign) and `TreeDepthSaturated`.

```go
for j := range res.Diagnostics.RHat {
	if res.Diagnostics.RHat[j] > 1.01 || res.Diagnostics.ESSBulk[j] < 400 {
		// not converged: more warmup, a reparameterisation, or a higher TargetAccept
	}
}
```

## Plugging in another sampler

`Sampler` is a one-method interface:

```go
type Sampler interface {
	Sample(ctx context.Context, model Model, seed uint64, opts Options) (Result, error)
}
```

`NUTS` implements it. A different algorithm (another HMC variant, a Metropolis scheme) implements the same method and returns a `Result` in the same `[chain][parameter][draw]` layout; fill `Diagnostics` with `RHat`, `ESSBulk` and `ESSTail` so callers judge every sampler by the same thresholds. Code written against `Sampler` then swaps samplers without change. To keep the reproducibility contract, derive per-chain streams from `(seed, chain index)` and merge chains in order.

A runnable example that samples a 2-D Gaussian and checks R̂ and ESS is in the package's godoc (`Example`).
