# Model extensions: custom regressions, model packs, and the casebook

**Status:** design intent, decided 2026-10-10 by the owner. The committed v1.0.0 part is [U53 custom-regressions](../units/U53-custom-regressions.md). Everything under "After v1.0.0" is out of v1 scope.

## Motivation

The worked [MMM example](../fixtures/mmm-harbor-pine/README.md) and a review of ten published media-mix-modelling case studies found that Pulse's primitives cover most of the work: geometric adstock (`WIN_EWMA` + a compose `sweep`), lags, seasonality controls, interaction terms, OLS / ridge, a conjugate Bayesian fit, difference-in-differences as OLS with a treated × post term, and budget scenarios as compose slots. The gaps are models, not primitives:

| Gap | Kind | Where it lands |
|---|---|---|
| A custom regression an embedder can register | Extension surface | U53 (v1) |
| Per-coefficient prior strength on `REG_BAYES_LINEAR` (experiment-calibrated priors) | Model option | U53 (v1) |
| Non-negative coefficients on `REG_OLS` | Model option | U53 (v1) |
| Weibull (delayed-peak) adstock | Window primitive | U53 (v1) |
| A Hill saturation curve | Expression function | U53 (v1). A compose `sweep` already splices `{{axis}}` into formula strings, so a shape/half-saturation grid works today with a written-out formula |
| Hill parameters estimated jointly with the coefficients (nonlinear least squares) | Model | Model pack, via the U53 contract |
| Hierarchical / partially pooled coefficients across regions | Model | Model pack, via the U53 contract (buffered, iterative) |
| Budget allocation to equal marginal ROI under constraints | Post-fit operation | Model pack, via the U53 post-fit slot |

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

## Where code and examples live

- **Model packs:** nested modules under `contrib/` (the `contrib/otelpulse` / `contrib/prompulse` pattern: root Pulse never imports them, they version with Pulse), or a separate `pulse-extensions` repository if they should release on their own schedule. Decide when the first pack is written.
- **Worked examples:** a companion repository, `frankbardon/pulse-casebook`, published as its own mdBook. It depends on Pulse through the CLI and public library only. No importable Go code lives there: anything reusable belongs in a pack.

## After v1.0.0 (not committed)

- **Casebook repository.** Created after the units that convert the in-repo examples land (U44–U52, and U41–U43 for the MMM fixture), so those units keep updating their example in the same PR. Then `docs/showcase/` and `docs/roadmap/fixtures/mmm-harbor-pine/` move there and the unit links are rewritten.
  - Each example: a narrative README, data or its generator, `req/`, `run.sh`, expected numbers, and a `GAPS.md` ledger of what was still done by hand, each line pointing at a Pulse TODO item (the existing "Still outside Pulse" table).
  - CI: a pinned-version run that must pass, and a nightly run against Pulse `main` that reports regressions and closed gaps.
  - The gap ladder: by hand → in a pack → in core.
- **MMM pack** on the U53 contract: Hill NLS regression, partial pooling across a group field, the budget allocator on the post-fit slot.
- **R bridge pack** on the U53 contract.
