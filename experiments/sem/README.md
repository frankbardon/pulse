# Experiment: SEM as a Pulse extension matrix operator

**Status: experiment, not for `main`.** A nested Go module (`experiments/sem/`, its own `go.mod` with a `replace` to the repo root), so the root module, its gates and its public surface are untouched.

`MAT_LAB_SEM` is a structural equation model fitted by maximum likelihood. It is registered through `pulse.Options.Extensions.MatrixOps`, the existing extension contract for `MAT_*` operators.

**The default setup is a semopy 2.3.11 clone**: `semopy.Model(desc).fit(data)`, with `obj="MLW"`, `solver="SLSQP"`, `mimic_lavaan=False` and SciPy's default `ftol`. It runs a Go port of SciPy's SLSQP, so it takes the same iterates and stops at the same point. Given the same data it returns semopy's estimates, standardized estimates, standard errors, z and p values, and fit indices to rounding (≤ 4e-12), early stop included. Code written against semopy ports 1 for 1. Other setups are opt-in parameters.

## Use

```go
p, _ := pulse.New(pulse.Options{
    DataDir:    dir,
    Extensions: pulse.Extensions{MatrixOps: []pulse.MatrixOpRegistration{sem.Registration()}},
})
resp, _ := p.Process(ctx, &types.Request{
    Cohort: &types.Cohort{Filename: "survey.pulse"},
    Matrices: []types.MatrixSpec{{
        Type:   sem.TypeName,          // "MAT_LAB_SEM"
        Name:   "sem",
        Fields: model.Observed,         // every observed variable the model names
        Params: json.RawMessage(`{"model": "visual =~ x1 + x2 + x3\n..."}`),
    }},
})
```

Command line (imports a CSV, fits, prints the matrix result as JSON):

```bash
go run ./cmd/semfit -csv sem/testdata/hier_data.csv -model sem/testdata/hier_model.txt
```

`semfit` takes the configuration below as flags (`-solver`, `-information`, `-ftol`, `-max-iter`, `-grad-tol`).

**Params:**

| Param | Default | Meaning |
|---|---|---|
| `model` | one factor over every member | semopy / lavaan syntax: `=~` measurement, `~` regression, `~~` covariance, `label*x`, `0.5*x` |
| `solver` | `slsqp` | `slsqp`: the SciPy SLSQP port, semopy's results exactly. `fisher`: damped Fisher scoring, which converges to the optimum (semopy run with a tight `ftol`, to ~1e-7) |
| `information` | `dense` | how the gradient and expected information are computed. `dense` forms each ∂Σₖ as a matrix, as semopy does. `rank2` uses ∂Σₖ = Σ(a·bᵀ + b·aᵀ), so every trace becomes a dot product. It gives the same figures (≤ 3e-12 from dense) at a fraction of the cost on large models |
| `ftol` | `1e-6` | SLSQP's `ftol` (SciPy's and semopy's default) |
| `max_iter` | 10000 / 500 | iteration cap (`slsqp`: semopy's 10000; `fisher`: 500) |
| `grad_tol` | `1e-10` | Fisher scoring stops when every free gradient entry is below it |

As in semopy, the baseline (independence) model behind CFI, TLI, NFI and GFI is always fitted with SLSQP at its defaults under `solver: slsqp`, whatever `ftol` / `max_iter` say. The engine's own matrix keys `missing`, `max_drop_share` and `fit` apply as for any matrix operator. Only `missing: "listwise"` is accepted.

**Output (`types.MatrixResult`):**

| Part | Content |
|---|---|
| `primary` | the model-implied covariance Σ(θ̂) over the observed variables |
| `auxiliary.estimates` | one row per `lval op rval` (semopy's inspect rows), columns `estimate`, `std_estimate`, `std_err`, `z_value`, `p_value`. SE, z and p are null for fixed parameters |
| `auxiliary.residual_covariance` | S − Σ(θ̂) |
| `vectors.parameter`, `vectors.free` | the parameter name (`_b3`, a label, ...) and whether it is free, aligned with the estimate rows |
| `scalars` | `dof`, `dof_baseline`, `chi2`, `chi2_p_value`, `chi2_baseline`, `cfi`, `gfi`, `agfi`, `nfi`, `tli`, `rmsea`, `aic`, `bic`, `loglik`, `objective`, `n`, `n_params`, `iterations`, `converged` |

Grouped requests fit one model per group bucket. Non-convergence returns the last iterate plus a `PULSE_MATRIX_NOT_CONVERGED` warning. A bad model or a missing member is refused with `PROCESSING_CONFIG`.

## What is reproduced from semopy (and how)

- **Model specification:** variable classification, the Beta / Lambda / Psi / Theta layout, default free variances, exogenous-latent covariances, observed-exogenous moments fixed to the sample, the first indicator fixed to 1, `label*x` equality constraints and `0.5*x` fixed values (`sem/model.go`).
- **Estimation:** Σ = Λ(I−B)⁻¹Ψ(I−B)⁻ᵀΛᵀ + Θ, and the Wishart ML discrepancy F = tr(SΣ⁻¹) − p + ln|Σ| − ln|S| over the **biased** sample covariance (divisor N, as numpy `bias=True`). Starting values follow semopy's rules. Variances are bounded at ≥ 0 (`sem/fit.go`).
- **Optimiser:** by default, `sem/slsqp.go` ports SciPy 1.18.1's SLSQP (`slsqp.c`, `nnls.c`, `_slsqp_py.py`; license in `sem/SCIPY_LICENSE.txt`). It covers the path semopy exercises: simple bounds and no general constraints. The port includes the QP subproblem (LSQ → LSI → LDP → NNLS), the L1 line search, the BFGS update of the LDLᵀ factors and the stopping rules. `sem/lapack.go` transcribes the reference BLAS / LAPACK routines it calls. Results differ from semopy only by floating-point summation order inside OpenBLAS kernels.
- **Standard errors:** the inverse of the expected information (N/2)·tr(Σ⁻¹∂ΣₖΣ⁻¹∂Σₗ), with semopy's handling of small negative variances. z = θ/(SE + 1e-12) and p = 2(1 − Φ(|z|)).
- **Standardized estimates:** semopy's `std_est=True` scaling.
- **Fit statistics** (`sem/stats.go`): `calc_stats`, including its conventions. "LogLik" is F itself, AIC = 2(k − F), BIC = ln(N)·k − 2F, GFI equals NFI, and the baseline is semopy's independence model.

## Parity results

Four models (three datasets), run through Pulse end to end (CSV → `.pulse` → `Process`). Reproduce with `scripts/run_parity.sh`.

| Model | Size | default vs semopy default | `fisher` + `rank2` vs semopy converged (`ftol=1e-14`) |
|---|---|---|---|
| `hier`: a synthetic three-level hierarchy (9 first-order, 3 second-order and 1 general factor, 3 outcomes; simulated n = 1200, 1–7 items) | 57 observed, 126 free, df 1527 | 2.6e-13 (49 iterations, as semopy) | 6.7e-8 |
| `poldem`: Bollen's political democracy (latent regressions + residual covariances) | 11 observed, 31 free | 4.4e-14 | 6.7e-7 |
| `hz`: Holzinger–Swineford three-factor CFA (covarying exogenous latents) | 9 observed | 1.2e-14 | 8.5e-8 |
| `hz_ext`: equality labels, observed covariates, fixed + labelled covariances, an output regression | 13 observed | 8.7e-14 | 4.5e-7 |

Each figure is the largest relative difference over every estimate, standardized estimate, SE, z, p and fit index. Note that semopy's default fit is *not* the optimum: SLSQP stops at `ftol=1e-6`, about 1e-3 short in the parameters on these models. The default setup reproduces that stop. `fisher` (or `slsqp` with a small `ftol`) goes to the optimum.

Timing on `hier` (semopy 0.23 s by default, 0.21 s at `ftol=1e-14`):

| Setup | Time |
|---|---|
| default (`slsqp`, `dense`) | 0.25 s |
| `slsqp` + `rank2` | 0.24 s |
| `fisher` + `dense` | 0.83 s |
| `fisher` + `rank2` | 0.15 s |

`sem/operator_test.go` pins all five setups (default, `slsqp`+`rank2`, `slsqp` at `ftol=1e-14`, `fisher`+`dense`, `fisher`+`rank2`) on all four models against the matching semopy reference: 1e-9 against semopy's defaults, 1e-6 against its converged fits. `TestRank2MatchesDense` pins rank-2 against dense derivatives to 1e-12.

## Not covered (semopy features outside its default `Model`)

- Mean structures and intercepts (`ModelMeans`). Only the covariance structure is fitted.
- Missing data. Pulse forms the covariance listwise. semopy's `cov` on data with NaNs uses masked (pairwise-style) moments, and its `FIML` objective needs raw rows. The two agree only on complete data.
- Other objectives (ULS, GLS, WLS, DWLS, FIML), ordinal / polychoric correlations, robust (sandwich) SEs, observed-information SEs, `std_est="lv"`, `mimic_lavaan=True`, `cov_diag=True`.
- semopy operation commands (`START`, `BOUND`, `DEFINE`, `CONSTRAINT`, ...) and covariances between an inner and an output variable.
- Row weights. The registration is not `WeightAware`, so a weight on the slot is refused.
- semopy orients covariances between exogenous latents by Python set order, which changes from run to run. A row may read `speed ~~ visual` on one side and `visual ~~ speed` on the other. `scripts/compare.py` and the tests treat `~~` symmetrically.
