---
id: U36
slug: reference-oracles
title: "Every inferential output is pinned to an external reference, and none answers with a wrong or unencodable number"
track: Statistical integrity
size: M
status: not-started
depends_on: [U08]
soft_depends_on: []
blocks: []
todo_items: [202, 203, 204, 205]
branch: reference-oracles
---

# U36 — reference-oracles

**Outcome:** Every inferential output is pinned to an external reference, and none answers with a wrong or unencodable number.

**Track:** Statistical integrity · **Size:** M · **Depends on:** [U08](U08-guidance-backfill-inferential.md) · **Unblocks:** none (U09 holds it as a soft dependency)

## Summary

U08 built the R oracle (`scripts/reference/gen_reference.R`, `make reference`, goldens under `internal/processing/testdata/reference/`) and used it to pin the shared distribution PRIMITIVES (Student t, chi-square, F, normal, Kolmogorov, studentized range) to a relative `1e-10`. It did not pin each operator's ASSEMBLED output: many `TEST_*`, `REG_*` and overlay values are still checked by hand-pasted literals at `1e-2`..`5e-3`, by values the engine produced itself, or by comparing an overlay to the same helper it calls (circular). This unit extends the oracle to every inferential output, and fixes the four runtime bugs the U08 statistics review found but did not own.

Numbering note: appended as U36 after U35 rather than renumbered.

## References

**Read before starting:**
- `.claude/reference/guided-analysis.md` — "Statistical primitives and the R oracle" (generator, tolerance policy, normal-tail rule, `internal/statdist`, what is not yet covered)
- `docs/src/internals/regenerating-goldens.md` — R reference goldens are an external oracle, not a snapshot
- [U08 statistics review](../reviews/U08-statistics-review.md) — "Open items for the U33 human reviewer" (both sections) for the runtime bugs and unverified paths
- U08 research `reference-test-coverage.md` (in the U08 PR) — the per-output coverage tables this unit closes

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#202** (4. Statistical integrity › Reference oracles) Per-output R oracle for every `TEST_*` family at tight relative tolerance: Shapiro–Wilk (Shapiro–Francia W′ + p), Brown–Forsythe, Tukey q / `p_adj`, KS p, Kendall τ-b with ties, Mann–Kendall (`TEST_TREND`) p, Pearson CI, and every family's p-value (row and post twins)
- [ ] **#203** (4. Statistical integrity › Reference oracles) `REG_OLS` (incl. ridge / lasso / elastic net) and `REG_GLM` (binomial, poisson, gamma) coefficients, SEs and p-values, and the `REG_BAYES_LINEAR` posterior, pinned to an external reference (`lm` / `glm` / glmnet / closed-form conjugate posterior)
- [ ] **#204** (4. Statistical integrity › Reference oracles) De-circularised overlay oracles: `OVERLAY_CHISQ_VS_POP`, `OVERLAY_CHISQ_VS_REF`, `OVERLAY_KS_VS_POP` and `OVERLAY_PAIRWISE_PROBIT_T` checked against an independent R computation, never the helper they call
- [ ] **#205** (4. Statistical integrity › Reference oracles) Runtime bugs found in U08: infinite `TEST_FISHER_EXACT` OR and Pearson / Spearman `details.t` break `--json`; `TEST_BROWN_FORSYTHE` reports F = 0, p = 0 at zero within-group spread; the Winitzki inverse-erf CI critical z is ~4.7e-4 too small; the Shapiro–Francia p for n < 5 is uncalibrated

## Scope

**In scope**
- Extend `gen_reference.R` with per-operator cases (R and package versions recorded; `effectsize`, `glmnet` and any other package pinned in the golden header); CI still never runs R
- One table-driven `TestReferenceOracle_*` per family, holding each output to a relative tolerance; a non-`1e-10` tolerance needs a written reason (e.g. a documented approximation such as Royston / Shapiro–Francia)
- Fix every output that fails its oracle, with a before → after row in the PR body
- The four #205 runtime bugs, each with a test
- Correct the manifest / doc-comment prose that still names the pre-U08 `studentTTwoSidedP` helper or the `2·(1 − Φ(|z|))` form (`internal/descriptor/capabilities_overlay.go` ~1064 / 1093 / 1282 / 1314 / 1553 / 1586, `internal/descriptor/overlay.go` ~3261 / 3279, `types/overlay.go` ~2190–2304, `types/overlay_streamability.go` ~340 / 347); regenerate the manifest golden

**Out of scope**
- Weighted variants (U12 adds its own reference fixtures, #59)
- Multiplicity-adjusted p-values (U13)
- New statistical features

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test(reference-oracles/E<n>-S<m>): …`; close each epic with `milestone(reference-oracles/E<n>): vertical slice complete — <epic title>`.

### E1 — Tests match R
- S1: oracle cases for every `TEST_*` row + post twin (#202); fix each failure
- S2: the four runtime bugs (#205) — finite-or-omitted infinities (or a coded refusal) so `--json` always encodes, a Brown–Forsythe refusal or undefined F, `math.Erfinv` / `-standardNormalPPF(α/2)` for the CI critical z, and a decision on Shapiro–Francia at n < 5 (refuse, or an exact method)

### E2 — Models and overlays match R
- S1: `REG_*` oracles (#203), incl. the GLM gamma path (dispersion currently fixed at 1) and penalised-OLS plug-in p-values (documented as approximate, with a stated reference)
- S2: de-circularised overlay oracles (#204) and the stale helper-name prose

## Acceptance criteria

- [ ] No inferential output's numeric test compares against a value the engine computed itself or against the helper it calls
- [ ] Every #205 bug has a failing-before test and is fixed; `pulse api process --json` encodes every fixture
- [ ] Every behaviour change has a before → after row, citing the R value, in the PR body
- [ ] `format_version` stays `"1.1"`; goldens regenerated, never hand-edited
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- New `TestReferenceOracle_*` per family; `TestGoldensNotHandEdited` in each package that consumes an R golden
- Relative tolerances only (amd64 vs arm64 FMA); verify `gh pr checks` on both architectures

## Update Demand companions

- The atomic `op-test-*` / `op-reg-*` / `op-overlay-*` skill of every operator whose output changes
- `.claude/reference/guided-analysis.md` ("Statistical primitives and the R oracle", "Not yet covered") and `update-demand.md` (Other load-bearing contract gates) for the new gates
- `errors/fixup_metadata.go` + `internal/descriptor/error_owners.go` for any new refusal code
- Guidance `Interpretation` caveats that mention a fixed bug (e.g. the Shapiro n < 5 warning, the GLM gamma dispersion caveat) updated in the same change

## Human inputs & decisions

- **Open:** Shapiro–Francia at n < 5 — refuse, or switch to an exact method (E1-S2)
- **Open:** infinite statistics — omit the key, emit a coded refusal, or encode a sentinel; must keep `format_version` `"1.1"` (E1-S2)

## Inherited from U08

- **What U08 already pinned.** Shared primitives at relative `1e-10` (`TestReferenceOracle_*`); every normal-approximation family's tail at |z| ≈ 10 (`*_TinyP`); effect sizes against R `effectsize` / `pwr` at `1e-9`; `OVERLAY_CHISQ_VS_POP` statistic and p against `chisq.test(rescale.p = TRUE)` after the OS-01 fix; `OVERLAY_PROP_Z` against `prop.test(correct = FALSE)`.
- **Weakest today** (U08 research): Shapiro–Wilk, KS, Tukey and Brown–Forsythe are checked only qualitatively; Pearson `ci_low` / `ci_high` for presence only; `REG_GLM` reference values "were derived by running the engine itself"; `REG_OLS` states it does not bind to external values.
- **`OVERLAY_PAIRWISE_PROBIT_T` is anti-conservative by design** (1/√n spread). Its oracle pins the documented formula, not a "correct" probit SE; whether it stays inferential-flagged is a U33 reviewer decision.
- **Stock R is not always accurate enough.** `ptukey` / `qtukey` / far-tail `qt` miss `1e-10`; U08's generator refines them in R and keeps R's own value beside the golden. Follow the same pattern for any new case.
