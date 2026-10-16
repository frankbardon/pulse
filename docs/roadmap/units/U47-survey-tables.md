---
id: U47
slug: survey-tables
title: "A weighted banner table with significance letters is one request, and Pulse can compute the weights"
track: Statistical integrity
size: L
status: in-progress
depends_on: []
soft_depends_on: [U46, U11, U12]
blocks: [U32]
todo_items: [324, 325, 326, 327, 328, 329, 330, 331, 398]
branch: survey-tables
---

# U47 — survey-tables

**Outcome:** A weighted banner table with significance letters is one request, and Pulse can compute the weights.

**Track:** Statistical integrity · **Size:** L · **Depends on:** none · **Soft:** [U46](U46-grouped-analysis.md), [U11](U11-weighting-descriptive.md), [U12](U12-weighting-inferential.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

The brand tracker is the clearest case for Pulse, and it exposed the biggest correctness bug the examples found: on a weighted crosstab normalised to column percentages, `OVERLAY_PAIRWISE_PROP_Z` takes n from the cell's own weighted count instead of the column base, so 28.6% vs 6.8% tests at p = 0.18 instead of p < 0.001, and every non-cell `n_source` is refused on a weighted host. `GROUP_SET_PER_ELEMENT` column margins also count rows whose set is null, so a column % silently becomes "% of everyone" rather than "% of those asked". Beyond the bugs, a standard banner (Total | Gender | Age | Region down, several KPIs across) took a 15-slot and a 14-slot Compose; significance letters, a tested wave-over-wave change, and the weights themselves (rim weighting to census targets) all had to come from elsewhere; and SPSS export cannot carry value labels.

**Source of inspiration:** the showcase examples under the `pulse-casebook` repository ([brand tracker](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/brand-tracker/README.md)), each a worked analysis run end to end in Pulse with a `REPRODUCE.md` of expected numbers. The items below are the gaps those runs hit. Where a fix simplifies an example, this unit's PR converts that example and refreshes its expected numbers.

Numbering note: appended after U43, not renumbered. This unit was not in the original plan. **Release intent:** v1.0.0 (owner decision 2026-10-10: Pulse should be as self-sufficient as possible at v1).

## References

**Read before starting:**
- `.claude/reference/weighting.md` and `.claude/reference/response-components.md` (margins and the cell-admission rule)
- `skills/crosstab-guide.md`, `skills/crosstab-margin-aggregations.md`, `skills/overlay-system.md`, `skills/pairwise-n-sources.md`, `skills/weighting.md`, `skills/spss-export.md`, `skills/label-display.md`
- `.claude/reference/guided-analysis.md` for new operators' `Purpose` / `Interpretation`

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#324** (Survey tables) Weighted pairwise proportion tests on a normalised crosstab use the column base: n comes from the column margin's effective n (or is derived from the normalise denominator automatically); a reference-oracle case pins it
- [x] **#325** (Survey tables) `GROUP_SET_PER_ELEMENT` axis margins follow the cell-admission rule (a row whose set is null reaches no cell and no margin), or the rule is stated and an opt-in provided; column % is "of those asked"
- [x] **#326** (Survey tables) Banner tables: a crosstab takes several independent column groups side by side (Total | Gender | Age | Region) and several measures as stub rows, in one request; a Total column needs no constant field
- [x] **#327** (Survey tables) Significance letters: a presentation option on pairwise overlays that assigns column letters and marks, per cell, which columns it is significantly above, at a chosen alpha and correction
- [x] **#328** (Survey tables) A tested wave-over-wave change: an inferential prior-period overlay on grouped Process (or a crosstab with no column axis), with p-values in the multiplicity family
- [x] **#329** (Survey tables) Weighting: rim (raking) and cell weighting to marginal targets, producing a weight column and an efficiency report (Kish n_eff, design effect, weight range), from the library, CLI and MCP (`Pulse.Rake` in E4-S1; `pulse rake` leaf and `pulse_rake` tool in E4-S2)
- [ ] **#330** (Survey tables) SPSS export carries value labels from label tables and categorical dictionaries; `overlay-system.md` matches the runtime on weighted inferential overlays (overlay-system half done in E1-S3)
- [ ] **#331** (Survey tables) Acceptance: the brand tracker's banner becomes one request per table with letters; its weights are computed by Pulse from census targets instead of inside the synth spec; its SPSS hand-off carries value labels

## Scope

**In scope**
- The weighted base bug and the set-margin rule
- Banners, letters, tested wave change
- Raking and cell weighting
- SPSS value labels

**Out of scope**
- Composite group keys in plain grouped Process ([U46](U46-grouped-analysis.md))
- Complex-sample variance (strata/PSU); state it as a limit

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(survey-tables/E<n>-S<m>): …`; close each epic with `milestone(survey-tables/E<n>): vertical slice complete — <epic title>`.

### E1 — Correct weighted banners
- S1: base-aware weighted n with an oracle case
- S2: set-margin admission rule

### E2 — One-request banners
- S1: independent column groups and measure stubs
- S2: significance letters
- S3: tested wave-over-wave change

### E3 — Weights and hand-off
- S1: raking and cell weighting with an efficiency report
- S2: SPSS value labels; convert the brand tracker

## Acceptance criteria

- [x] The 28.6% vs 6.8% weighted comparison tests at the same p as the weighted indicator-mean method
- [ ] A raked weight reproduces the census marginals to within tolerance and reports Kish n_eff
- [ ] The brand tracker README's Compose banners are replaced by single requests with letters
- [ ] Showcase examples simplified where this unit allows (brand tracker (29 Compose slots, synth-computed weights, unlabelled SPSS)): requests converted, README and REPRODUCE.md expected numbers refreshed, every step still pure Pulse
- [ ] `format_version` stays `"1.1"`; every change is additive (or the unit's decision records the one deliberate bump)
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- the weighting reference oracles, `TestCrosstab_BufferedAuxMarginObservesCellAdmission`, `TestCrosstab_BufferedAuxMarginMatchesFused`, `TestSkillsCoverAllPurposes`, `TestPayloadSchemaGolden`
- New: weighted pairwise base oracle, set-margin admission test, raking convergence and marginal-match test, SPSS value-label round trip
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

### Banner N-scan cost (E3-S3, #326)

The banner is Compose sugar (decision: N scans accepted for v1), so each section × block slot scans the cohort once. `BenchmarkComposeBanner` (root package, `ComposeParallel` with default workers, in-memory cohort, Apple M1 Max, 2026-10-16):

| Rows | One Total-only slot | Banner 1 × 4 (Total + 3 groups) | Banner 3 × 4 (12 slots) |
|---|---|---|---|
| 100,000 | 29.1 ms | 47.3 ms (1.63×) | 116.1 ms (3.99×) |
| 1,000,000 | 288.8 ms | 442.1 ms (1.53×) | 964.1 ms (3.34×) |

Parallel slots overlap, so a 12-slot banner costs about 3.3–4× one scan rather than 12×. The single-scan engine banner (research option (a)) stays unneeded at these sizes.

## Update Demand companions

- `weighting.md` and `response-components.md` (load first); `skills/crosstab-guide.md`, `skills/weighting.md`, `skills/spss-export.md`, the pairwise overlay skills
- New operator/CLI rows: atomic skills, capabilities, `features.go` (`Since`, dependencies), `commandBindings` and `docs/src/cli/flags.md` for a weighting leaf, `make docs`
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)

## Human inputs & decisions

- **Open:** raking as a CLI leaf that writes a weight column into a new cohort (pairs with U41's derived cohort), or as an attribute computed per request.
- **Open:** significance letters on the wire (a presentation block) or left to embedders with a helper.

## Inherited from U43

Filed from [U43 handed on](U43-post-aggregation-ratios.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#398** — A weight-aware `total()`: `ATTR_FORMULA` is not weight-aware today, so `total(expr)` is always the unweighted cohort sum. Found in U43 (E1).
