---
id: U33
slug: v1-release
title: "Pulse v1.0.0 is released with a written stability promise"
track: API & release
size: S
status: not-started
depends_on: [U32]
soft_depends_on: [all other units]
blocks: []
todo_items: [168, 169, 170, 171, 173, 174, 175, 176, 177, 178, 179, 206]
branch: v1-release
---

# U33 — v1-release

**Outcome:** Pulse v1.0.0 is released with a written stability promise.

**Track:** API & release · **Size:** S · **Depends on:** [U32](U32-docs-audit.md) · **Soft:** all other units · **Unblocks:** —

## Summary

Publish `STABILITY.md` with the final public package list, cut `v1.0.0-rc.1` through the release pipeline and validate it against the downstream library, run the cross-cutting verification sweep, then tag `v1.0.0`.

## References

**Theme documents (read before starting):**
- [api-and-release 02 — Stability policy draft](../v1.0.0-api-and-release/02-stability-policy.md) — STABILITY.md draft
- [api-and-release 01 — Release pipeline & versioning](../v1.0.0-api-and-release/01-release-pipeline.md) — Release workflow

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#168** (12. Release v1.0.0) `STABILITY.md` published at the repo root, with the final public package list ([api-and-release 02](v1.0.0-api-and-release/02-stability-policy.md))
- [ ] **#169** (12. Release v1.0.0) Release candidate tag (`v1.0.0-rc.1`) built through the release pipeline and exercised by the downstream library
- [ ] **#170** (12. Release v1.0.0) `v1.0.0` tagged
- [ ] **#206** (12. Release v1.0.0) Human statistics sign-off (release-blocking): a named statistics reviewer works through the [U08 review record](../reviews/U08-statistics-review.md) and its open items (the U08 E3 / E4 sections and the U09 section) and the [U12 weighted-inference review](../reviews/U12-weighting-review.md) (the `n_eff` semantics and every lifted weighted formula), signs off the U08 inferential AND the U09 descriptive guidance AND the U12 weighted inference, and owns the statistical-review CODEOWNERS entries
- [ ] **#171** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) Every new operator in every theme is weight-aware (or explicitly refuses a weight) and multiplicity-aware where it emits p-values
- [ ] **#173** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) New `PULSE_MATRIX_*` / `PULSE_VECTOR_*` / `PULSE_OVERLAY_*` / `PULSE_PROFILE_*` / `PULSE_LIMIT_*` / `PULSE_WEIGHT_*` / `PULSE_RETURN_*` / advisory codes all have `codeMetadata` + fixups
- [ ] **#174** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) Every new operator has `Purpose`, `Interpretation` (if inferential), `Since`, dependency edges and an atomic skill
- [ ] **#175** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) Every new gate is listed by name in CLAUDE.md "Non-Skippable CI Gates"
- [ ] **#176** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) The Update Demand table has rows for: `Purpose`, `Since` / dependencies, topical-skill fences, `Request.Vectors` / `Matrices`, `Response.Matrices`, `Request.Weight` / `Multiplicity` / `TimeZone` / `Return`, `Options.Limits` / `Logger` / `Hooks` / `Metrics`
- [ ] **#177** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) New env vars and CLI flags documented (CLAUDE.md "Build / Env", `flags.md`, `session-bootstrap.md`)
- [ ] **#178** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) CLAUDE.md stays at or under 50,000 bytes (long form moved to `.claude/reference/`)
- [ ] **#179** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) `format_version` remains `"1.1"` (every wire change additive)

## Scope

**In scope**
- STABILITY.md
- Human statistics sign-off of the U08 review record, covering U08 inferential and U09 descriptive guidance, and of the U12 weighted-inference review (#206) — release-blocking
- rc tag + downstream validation
- Cross-cutting verification (TODO §13)
- v1.0.0 tag

**Out of scope**
- New features
- Documentation work (U32)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(v1-release/E<n>-S<m>): …`; close each epic with `milestone(v1-release/E<n>): vertical slice complete — <epic title>`.

### E1 — The promise is written
- S1: `STABILITY.md` at the root with the U02 package list

### E2 — Verified and released
- S1: cross-cutting sweep (§13 items)
- S1b: statistics sign-off (#206) — reviewer works the U08 review record's open items (U08 and U09 sections) and the U12 review's open items, records the sign-off in each record, lands CODEOWNERS
- S2: `v1.0.0-rc.1`; downstream library validation
- S3: `v1.0.0`

## Acceptance criteria

- [ ] Every TODO item is ticked, or explicitly moved to post-1.0 in the theme docs
- [ ] The rc builds through the release pipeline, and the downstream library passes its own tests against it
- [ ] The API-compat check is active against `v1.0.0`
- [ ] A named statistics reviewer's sign-off is recorded in `docs/roadmap/reviews/U08-statistics-review.md` and `docs/roadmap/reviews/U12-weighting-review.md`, every open item there (U08, U09 and U12) resolved or deferred with a reason, and `.github/CODEOWNERS` routes the guidance registries to the reviewer
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- All non-skippable gates green; API-compat job enabled

## Update Demand companions

- README links to STABILITY.md
- Release notes mention the MCP default `standard` (from U18)
- Release notes tell users to rebuild a sidecar index built on a pre-1970 `datetime` key before U03 (`pulse index build`); `date` keys are unaffected (`.claude/reference/byte-layout.md`, temporal word sign)

## Human inputs & decisions

- Maintainer runs the downstream validation and tags the release
- **Statistics reviewer** (maintainer sources one) signs off U08's inferential and U09's descriptive guidance and U12's weighted inference (#206); no `rc` is tagged before it

## Notes

- TODO item 172 (missing-data modes) is verified in U16. It is listed in the sweep for completeness.

## Inherited from U03 / U04

- **Pre-1970 `datetime` index rebuild** (U03, PR #301). This is the release-note line under Update Demand companions. The fix changed how such a key is encoded, so an older index on one keys on a rounded float echo.
- **Review `mcpserve.Describe` / `ServeInfo` before the freeze** (U04, PR #304). They exist only to get the profile name into `pulse mcp`'s startup line without a `*Pulse` accessor (`mcpserve/describe.go`). U05 then added `p.FeatureProfile()`. Keep them, or remove them in favour of the accessor, before `STABILITY.md` freezes `mcpserve`.
- **After the `v1.0.0` tag: relax `TestFeaturesHaveSince`.** It requires every built-in `Since` to equal `BuiltinFeatureSince` (`"1.0.0"`, `internal/descriptor/features.go`). The first feature added after the tag must relax the check to "parseable and ≤ the next release". Contract: `.claude/reference/feature-profiles.md`. This is post-release work, recorded here so it is not lost.

## Inherited from U08

U08 shipped Purpose + Interpretation for every `TEST_*`, `OVERLAY_*` and `REG_*` without a human reviewer. Its review was automated: binding deterministic gates (convention registry, prose lint, R oracle) plus a two-pass advisory LLM panel whose findings were all triaged and applied. The record is [`reviews/U08-statistics-review.md`](../reviews/U08-statistics-review.md). Sign-off (#206) starts there:

- **Panel independence.** Two passes of one model family; treat "no finding" as weak evidence. Prioritise what the gates cannot check: `NotFor` routing and assumption completeness.
- **Convention choices.** `very small` as the label below Cohen's small; the Chinn (2000) odds-ratio conversion; Cohen 1992 f² → R² for `REG_OLS` `r2`; rank-biserial, d_z, RM partial η², rank ε², Cramér's V, ρ and τ left unbanded.
- **Decisions the record leaves open.** `OVERLAY_PAIRWISE_PROBIT_T` is anti-conservative by design (keep inferential-flagged, warn, or hide behind a feature profile); `OVERLAY_CHISQ_VS_POP` compares only the listed categories (consider a full-dictionary comparison when `DiscreteTopK` does not truncate); the Shiffler (1988) citation for the ZSCORE_VS_TOTAL bound was not fetched; penalised-OLS p-values are plug-in approximations.
- **`OVERLAY_INDEX_VS_MARGIN` declared scopes.** The skill lists `row` / `column` scopes and a SERIES output; the capability declares only `cell` / MATRIX. Not verified against the scope gate in U08 — resolve which is right before sign-off.
- **Numeric correctness is U36's**, not the reviewer's: the runtime bugs the record lists (infinite Fisher OR / correlation `t`, Brown–Forsythe at zero spread, Shapiro n < 5; the inverse-erf CI was fixed by U12) and the per-output oracles land in [U36](U36-reference-oracles.md) before this unit.
- **CODEOWNERS.** Point the reviewer's entries at the landed registries: `internal/descriptor/{purposes,purposes_*,interpretations,interpretations_*,conventions,glossary,intents}.go`, `internal/descriptor/testdata/conventions.json`, the effect-size code in `internal/processing/test_*.go`, `internal/statdist/` and `scripts/reference/`. `.github/CODEOWNERS` carries only the default `*` owner today.

## Inherited from U09

U09 gave every descriptive built-in (aggregators, attributes, windows, features, filters, groupers, synth distributions) a Purpose, and every needs-reading one a `value` / `value.*` Interpretation. Its review used the same pattern as U08: binding gates plus an advisory LLM panel (scope A in two passes, scope B in one combined pass). The maintainer accepted all 64 findings and applied them. The record is the **U09 section of the same file**, [`reviews/U08-statistics-review.md`](../reviews/U08-statistics-review.md). #206 signs off both units there:

- **Open items.** Work the "Open items for the U33 human reviewer (U09)" list. Most are runtime behaviours the guidance now describes honestly and that are logged for [U36](U36-reference-oracles.md): truthy filters on category labels, 0 for empty groups, text-order group rows, `AGG_ZSCORE`'s shape, normal-not-t CI bounds, the unstable `ATTR_PERCENTILE` tie order, 0 / 50 for missing attribute inputs, the split-blind target encoder, synth `u4` wraparound, and numeric groupers accepting category fields.
- **Unbanded shape readings.** Skewness, kurtosis and the descriptive z / T scores carry no bands (fixture `excluded`). Confirm that is right.
- **CODEOWNERS** covers the descriptive registries too (`purposes_*.go` and `interpretations_*.go` already match the globs above).

## Inherited from U12

U12 made the significance tests, regressions, CI bounds, scores, quantile buckets and inferential overlays weight-aware on one rule (N* = Σw or Kish n_eff; the frequency formula on w*). It ran without a human reviewer: binding gates (unity byte parity, exact frequency expansion against stock R, probability scale invariance, external references) plus a one-pass advisory LLM statistician panel. The record is [`reviews/U12-weighting-review.md`](../reviews/U12-weighting-review.md). #206 signs it off there:

- **Open items.** Work its "Open items for the U33 human reviewer (U12)" list — headed by WS-01 (one-way ANOVA F under between-group probability-weight variation) and the undefined-figure cases at tiny n_eff (WS-05 / WS-12). The docs story applied the documentation findings; the engine findings await owner triage.
- **The method itself.** No external software implements Kish-w* inference, so the probability arm is verified for arithmetic and scale invariance only. Sign off (or not) the framing in `.claude/reference/weighting.md` (Weighted inference): unequal-weighting correction only, weights assumed unrelated to the outcome, not design-based, χ² first-order Kish not Rao-Scott.
- **CODEOWNERS.** Add `internal/weighting/`, the weighted paths of `internal/processing/test_*.go` / `regression/` / `overlay*.go`, and `internal/service/testdata/weight_reference/` to the reviewer's entries.
