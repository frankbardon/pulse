# Regression upgrade ranking

This is a recommendation, not a decision. The user reviews and picks
which candidates (if any) become real phases on follow-up branches.

Ordered by `(user value) × (cost) × (current-blocker status)`. Three
dimensions:

- **User value** — `high` / `medium` / `low`. How many real Pulse users
  the upgrade unlocks vs. nice-to-have for a small audience. Survival
  / quantile / multinomial all rate high because they unblock major
  use-case categories (biostats, econometrics, multi-class
  classification); attribute-by-reference rates high because the
  ergonomic improvement compounds across every multi-attribute
  request.
- **Cost** — `low` (≤ 1 phase) / `medium` (2 phases) / `high` (3+
  phases). Engineering effort + design surface + risk of contract
  change.
- **Blocker status** — `internal-dep` if it gates another upgrade in
  this list, `partial-prod` if a code path is wired but unverified,
  `none` otherwise. Internal-dep items rise even when their
  standalone value is moderate.

Top of the list = highest ROI for the next implementation slot.

| Rank | Slug | User value | Cost | Blocker status | One-line rationale |
|------|------|------------|------|----------------|--------------------|
| 1    | gamma-family-fixtures   | medium | low    | partial-prod  | Code is shipped; only fixtures missing. Half-a-phase to close a wired-but-unverified hole. |
| 2    | kfold-cv                | high   | low    | none          | Universal ML workflow staple, builds on existing Resample plumbing, single phase. |
| 3    | attr-from-reference     | high   | low    | internal-dep  | Eliminates duplicate fits in common multi-attribute requests; gates glm-leverage's reuse story. |
| 4    | robust-se               | high   | medium | none          | Unblocks econometrics-grade inference; pure postprocess on existing engines, no new operator. |
| 5    | glm-leverage            | medium | low    | internal-dep  | Closes the Phase 7 attribute gap for GLM diagnostics; small engine work; sibling to attr-from-reference. |
| 6    | multinomial             | high   | medium | none          | k>2 classification is widely needed; clean new operator with bounded result-shape extension. |
| 7    | survival                | high   | high   | none          | Highest standalone unlock (biostats, churn, reliability) but largest design surface and result-shape extension. |
| 8    | quantile                | medium | medium | none          | Clear use cases (tail modeling) but smaller audience; engine is moderately involved. |
| 9    | predict-new-data (A)    | high   | medium | none          | Production-scoring story; Route A (in-request) is the lower-risk start; Route B remains a future upgrade. |
| 10   | orthogonal-poly         | low    | low    | none          | Numerical-stability win for a niche audience (Degree ≥ 5 polynomial fits). |
| 11   | mixed-effects           | medium | high   | none          | Real demand but biggest design ambiguity (spec field vs. GROUP_* composition); needs review checkpoint. |
| 12   | online-glm              | medium | high   | none          | Compelling for very large data but architectural overhead and SE-comparability concerns push it down. |
| 13   | mcmc-bayes              | low    | high   | none          | Bayesian conjugate already shipped; the additional unlock (non-conjugate priors, hierarchical models) reaches a small audience and costs a full sampler implementation. |

## Notes on the ranking

- Items 1–5 are recommended as a "next sprint" cluster: each is
  one-or-two-phase work, each closes a known gap, and 3–5 chain
  naturally (attr-from-reference + glm-leverage are siblings;
  robust-se reuses leverage where HC2/HC3 want it).
- Items 6–9 are larger commitments that each individually justify
  their own focused branch. Multinomial is the cleanest of these to
  build (closest in shape to the existing GLM machinery).
- Items 10–13 are deferred candidates: lower combined ROI either
  because the audience is small (orthogonal-poly, mcmc-bayes), the
  design surface is large (mixed-effects, online-glm), or both.
  They remain worth keeping on the list for a future planning cycle.

- Internal dependencies: `glm-leverage` benefits materially from
  `attr-from-reference` landing first (or together), so if both
  promote, sequence #3 → #5. `robust-se`'s HC2 / HC3 variants
  benefit from `glm-leverage` landing first, so if HC2 / HC3 are
  in scope, sequence #5 → #4. `gamma-family-fixtures` is a
  precondition for gamma GLM showing up in `glm-leverage` and
  `robust-se`.
