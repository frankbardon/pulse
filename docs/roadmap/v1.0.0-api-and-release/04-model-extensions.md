# Model extensions: custom regressions, model packs, and the casebook

**Status:** design intent, decided 2026-10-10 by the owner. The committed v1.0.0 part is [U53 custom-regressions](../units/U53-custom-regressions.md), extended by [U60 mmm-estimation](../units/U60-mmm-estimation.md) (decided 2026-10-25: the solver and general optimizer are core, the MMM pack is `contrib/mmmpulse`; see "What U60 changed"; mmm-followups then moved both MMM models into core and removed the pack, see "What mmm-followups changed"). Everything under "After v1.0.0" is out of v1 scope.

## Motivation

The worked [MMM example](../fixtures/mmm-harbor-pine/README.md) and a review of ten published media-mix-modelling case studies found that Pulse's primitives cover most of the work: geometric adstock (`WIN_EWMA` + a compose `sweep`), lags, seasonality controls, interaction terms, OLS / ridge, a conjugate Bayesian fit, difference-in-differences as OLS with a treated × post term, and budget scenarios as compose slots. The gaps are models, not primitives:

| Gap | Kind | Where it lands |
|---|---|---|
| A custom regression an embedder can register | Extension surface | U53 (v1) |
| Per-coefficient prior strength on `REG_BAYES_LINEAR` (experiment-calibrated priors) | Model option | U53 (v1) |
| Non-negative coefficients on `REG_OLS` | Model option | U53 (v1) |
| Weibull (delayed-peak) adstock | Window primitive | U53 (v1) |
| A Hill saturation curve | Expression function | U53 (v1). A compose `sweep` already splices `{{axis}}` into formula strings, so a shape/half-saturation grid works today with a written-out formula |
| Hill parameters estimated jointly with the coefficients (nonlinear least squares) | Model | Model pack `contrib/mmmpulse` (`REG_MMM_HILL`), on the core `optimize` solvers (U60); a built-in regression since `v1.0.0-alpha.29` (mmm-followups) |
| Hierarchical / partially pooled coefficients across regions | Model | Model pack, via the U53 contract (buffered, iterative) |
| Budget allocation to equal marginal ROI under constraints | Post-fit operation | Core: the Compose `optimize` block over `coef()` and `hill()` (U60); the general post-fit slot (#489) stays a v1.x backlog item |

## Decision: three layers

1. **Pulse core stays pure Go and deterministic.** It owns the primitives and the extension contract. A custom regression is a first-class citizen: predict, manifest, payload schema, MCP, feature profiles, guided analysis, weighting and multiplicity treat it like a built-in. The contract lands in v1 because the public surface freezes at v1.0.0 (`TestPublicAPIGolden`); later slots are additive.
2. **Model packs are separate Go modules that depend on Pulse, never the reverse.** They are registered at `pulse.New` through `Options.Extensions`. gonum (already a root dependency, v0.17) provides `gonum.org/v1/gonum/optimize` (BFGS, L-BFGS, Nelder–Mead), enough for Hill fitting, pooled models and constrained allocation without leaving Go. A first pack is an MMM pack (Hill NLS, Weibull-adstock fits, partial pooling, budget allocator).
3. **An R bridge is an optional pack, never core.** It registers operators through the same slots, hands data over as Arrow (Pulse already has the adapter), and every script declares its output schema so predict and the manifest stay honest. It is off unless an embedder registers it. It serves prototyping and long-tail methods; a method that proves popular is ported to Go in layer 2.

### Why not call R from Pulse natively

- **Distribution:** `make dist` ships one static binary for six platforms; native R makes every user install R and matching packages, and results vary with their R version.
- **Storage:** an R script reads files directly, bypassing `afero.Fs`, so `fs.NewMemMap()`, custom storage and resource limits stop applying.
- **Self-description:** predict and the manifest cannot know an arbitrary script's output shape.
- **Security:** a script-running surface exposed over MCP is arbitrary code execution offered to any connected agent.
- **Determinism:** same cohort plus same request must give the same bytes.

R keeps its current role as the reference oracle ([U36](../units/U36-reference-oracles.md)): checking results, not producing them.

## What U60 changed

[U60](../units/U60-mmm-estimation.md) revised the layer split above, once the MMM work showed which parts are general and which are industry-specific.

- **The solver and the general optimizer are now core.** `optimize.Minimize` (SLSQP with constraints and multipliers) and `optimize.LeastSquares` (bounded Levenberg–Marquardt) are a public package; the Compose `sweep` gained seeded `random` / `lhs` modes and a Compose `optimize` block. None of these mention media mixes: price optimisation on a fitted demand curve, hyperparameter search and any bounded nonlinear fit use them with no pack. gonum's `optimize` is no longer the pack's route to an optimizer.
- **The MMM pack is `contrib/mmmpulse`.** This settles the earlier "contrib or a separate repository" question: a nested module under `contrib/`, versioned in lockstep with Pulse, registered through `Options.Extensions`. It carries `REG_MMM_HILL`, the joint nonlinear least-squares fit of every channel's effect and saturation curve, and its skills. Hierarchical / partially pooled models and Bayesian MMM remain future packs on the same contract.
- **The rule: industry-specific goes in a pack, general goes in core.** A capability another field would use unchanged (a constrained solver, a seeded sweep, a window kernel, a deterministic maths set) belongs in core, named for what it does, not for the industry that asked for it (`WIN_ADSTOCK` became `WIN_DISTRIBUTED_LAG`, with "adstock" kept as a search keyword). A model whose structure is one industry's convention (Hill-saturated media channels with non-negative betas) is a pack.
- **The general post-fit slot (#489) is narrowed, not delivered.** Curve evaluation and budget allocation need no registered post-fit operator, because a request reads `coef()` and the `optimize` block solves; the slot remains a v1.x item for embedder-registered computations over fitted results.

## What mmm-followups changed

The pack did not stay a pack. In `v1.0.0-alpha.29` `REG_MMM_HILL` and `REG_MMM_BAYES` became built-in regressions (`internal/regmmm`, registered for every instance, the stock `pulse` CLI and `pulse mcp` included), their skills joined the core pack, and the `contrib/mmmpulse` module was deleted with no shim. The stock CLI and MCP server never register packs, so a pack model was out of their reach. The general/industry rule above still governs new primitives; the remaining contrib modules are the observability adapters.

## Where code and examples live

- **Model packs:** nested modules under `contrib/` (the former `contrib/otelpulse` / `contrib/prompulse` pattern, since folded into the root module as `observe/otel` / `observe/prom`: root Pulse never imports them, they version with Pulse), or a separate `pulse-extensions` repository if they should release on their own schedule. Decided (U60): `contrib/mmmpulse`; reversed by mmm-followups, which made both MMM models built-ins and removed the module.
- **Worked examples:** a companion repository, `frankbardon/pulse-casebook`, published as its own mdBook. It depends on Pulse through the CLI and public library only. No importable Go code lives there: anything reusable belongs in a pack.

## After v1.0.0 (not committed)

- **Casebook repository.** Created after the units that convert the in-repo examples land (U44–U52, and U41–U43 for the MMM fixture), so those units keep updating their example in the same PR. The showcase examples have already moved there; `docs/roadmap/fixtures/mmm-harbor-pine/` follows and the unit links are rewritten.
  - Each example: a narrative README, data or its generator, `req/`, `run.sh`, expected numbers, and a `GAPS.md` ledger of what was still done by hand, each line pointing at a Pulse TODO item (the existing "Still outside Pulse" table).
  - CI: a pinned-version run that must pass, and a nightly run against Pulse `main` that reports regressions and closed gaps.
  - The gap ladder: by hand → in a pack → in core.
- **MMM pack, further models** on the U53 contract: partial pooling across a group field and Bayesian MMM. The Hill NLS regression and the budget allocator already ship (built-ins since `v1.0.0-alpha.29`, the Compose `optimize` block; U60, U62, mmm-followups).
- **R bridge pack** on the U53 contract.
