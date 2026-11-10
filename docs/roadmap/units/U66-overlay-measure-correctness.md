---
id: U66
slug: overlay-measure-correctness
title: "Overlay handlers score the measure the analyst named, and a code column or a mixed-type crosstab is refused instead of scored silently"
track: API & release
size: M
status: done
depends_on: [U77, U78]
soft_depends_on: [U67]
blocks: [U32]
todo_items: [438, 441, 443, 444, 445, 446, 447, 476, 434, 454, 713]
branch: overlay-measure-correctness
---

# U66 — overlay-measure-correctness

**Outcome:** Overlay handlers score the measure the analyst named, and a code column or a mixed-type crosstab is refused instead of scored silently.

**Track:** API & release · **Size:** M · **Depends on:** U77, U78 (CI and test hygiene land first) · **Soft:** U67

## Summary

Overlay handlers on Process, Compose and chained crosstabs make several silent choices today: which measure a formula scores, which target a two-sample handler reads, whether a dictionary code is treated as a value or a moment. This unit audits each handler for a value-versus-moment reading, adds the missing `params.field` / multi-target refusals, and decides whether numeric aggregators over code columns are refused. Mixed-cell multi-measure crosstabs are refused per measure, predict and runtime alike. Note: predict-extension-parity-2 touches the extension MATRIX overlay rule of #447.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [x] **#438** Process SERIES `OVERLAY_FORMULA` over a grouped host, and the partition semantics of `prior` / `total` over two or more groups. Found in U46 (E4-S15). Done in U66 (E1-S1): Process SERIES `OVERLAY_FORMULA` was already wired in U35 (E3); U66 settled the two-or-more-grouper semantics: `prior` / `total` partition by every key but the last, or `params.partition_by`, on Process and Compose alike. · [U66](units/U66-overlay-measure-correctness.md)
- [x] **#441** Compose `OVERLAY_FORMULA` over SERIES slots scores `Aggregations[0]`; it takes no `params.field` to pick the measure. Found in U46 (E4-S15) Done in U66 (E1-S1): Compose FORMULA takes `params.field`: `value`, `ref_value` and `slot.<label>.value` score the named aggregation, with the partition rule of #438. · [U66](units/U66-overlay-measure-correctness.md)
- [x] **#443** Compose two-sample T/Z handlers (`OVERLAY_T_VS_REF` / `_Z_VS_REF` / `_T_CELL` / `_Z_CELL`) score only `Targets[0]`; extra targets are silently ignored. Decide whether to refuse multi-target specs. Found in U46 (E4-S16) Done in U66 (E2-S1, E2-S2): Fan-out, not refusal: every single-target Compose kind emits one layer per target (`<name>__<target>`; one target byte-identical), capped by `max_panel_targets`, folded into multiplicity per layer. · [U66](units/U66-overlay-measure-correctness.md)
- [x] **#444** The Compose T/Z series arm treats a scored `AGG_VARIANCE` / `AGG_STDDEV` value as the side's mean; consider refusing non-mean scalars. Found in U46 (E4-S16) Done in U66 (E1-S2): the Compose T/Z series arm accepts a mean (`AGG_AVERAGE` / `AGG_WEIGHTED_MEAN`) or moments (`AGG_WELFORD`, `AGG_VARIANCE` / `AGG_STDDEV` via components, an extension declaring `mean` + `variance`); anything else is `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` `measure_not_mean`. · [U66](units/U66-overlay-measure-correctness.md)
- [x] **#445** Compose crosstab slots and whole-chain crosstab stages over `AGG_MODE`-over-categorical cells still score dictionary codes; needs a per-kind value-vs-moment audit (`OVERLAY_T_CELL` / `_Z_CELL` read Welford triples). Found in U46 (E4-S17) Done in U66 (E3-S1): the Compose half: a value-reading kind over a code cell is `PROCESSING_CONFIG` `categorical_code`, predict and runtime. The chain half is not buildable by design: every chain crosstab stage is `PULSE_CHAIN_NOT_MERGEABLE` before any overlay (pinned by `TestChainOverlay_CrosstabStageNeverScored`). · [U66](units/U66-overlay-measure-correctness.md)
- [x] **#446** Non-overlay use of a code column: `AGG_SUM` / `AGG_AVERAGE` etc. over a forwarded code column at chain stage 1 and later (stage 0 was refused in U35). Found in U46 (E4-S17). Done in U66 (E3-S3): stage 0 was already refused in U35; U66 extends the refusal to a code column forwarded at chain stage 1 and later (`PULSE_FIELD_TYPE_UNSUPPORTED`, `forwarded_code: true`). An untyped slot over a forwarded code still defaults to `AGG_SUM` and is refused (owner kept this, 2026-11-09). · [U66](units/U66-overlay-measure-correctness.md)
- [x] **#447** Extension MATRIX overlay kinds are not judged by the categorical-code cell rule: an extension kind declares no reads-cell-value metadata (`overlayhost.ReadsCellValue` covers built-ins only). Found in U46 (E4-S17) Done in U66 (E3-S2): `OverlayRegistration.ReadsCellCountsOnly`: unset, an extension kind with a Matrix or Compose handler is refused over a code cell; declared, it is left alone. · [U66](units/U66-overlay-measure-correctness.md)
- [x] **#713** Extension overlay kinds cannot pick the measure or the partition on a SERIES host or a Compose SERIES slot: `extend.SeriesOverlayHost` / `ComposeOverlayHost` hand the handler the slot's series scored on its FIRST aggregation over every group, and no registration declares that it honours `params.field` / `params.partition_by` the way the built-in SERIES kinds do (`internal/seriesfield`, `internal/seriespartition`). Add an opt-in declaration so the host is built from the named aggregation and per partition, judged identically at predict and runtime. Owner call 2026-11-06: a later item, not U67. Found in U67 (E2-S2) Done in U66 (E4-S1): `OverlayRegistration.HonoursField` / `HonoursPartitionBy`, judged identically at predict and runtime. · [U66](units/U66-overlay-measure-correctness.md)
- [x] **#476** `OVERLAY_PAIRWISE_WELCH_T` / `_TWO_MEANS_Z` on a multi-measure crosstab whose measures have mixed cell types is refused whole; refuse per measure, predict and runtime alike. Found in U47 (E4-S5) Done in U66 (E5-S1): per-measure refusal: a measure without the Welford triple gets null cells plus a `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` warning (`measure`, `measure_index`, `reason`); the other measures score; predict and runtime alike. · [U66](units/U66-overlay-measure-correctness.md)
- [x] **#434** Per-bucket `Components` under `fit: "within"` still describe the whole-set fit of a data-dependent grouper, not each bucket's refit (U46 E4-S1 / E4-S5 decision) Done in U66 (E5-S2): `GrouperComponents.Buckets` (`buckets: [{group_key, total_n, n_null, operator, error}]`) under `fit:"within"`, one entry per outer bucket equal to a standalone run over it; a degenerate refit is a bucket `error` plus `PULSE_SLOT_FAILED`; a crosstab `within` row position's `row_key_components` reads the refit. · [U66](units/U66-overlay-measure-correctness.md)
- [x] **#454** `OVERLAY_SYMMETRY`: an exact binomial McNemar test on a 2 × 2 (`params.exact`, R `binom.test(n_12, n_12 + n_21)`; many texts prefer it when n_12 + n_21 < 25, where the chi-square tail only warns `PULSE_OVERLAY_EXPECTED_LOW` today) and the discordant odds ratio n_12 / n_21 as an effect size (`details.effect_size.*`, with its interval), both pinned to an R oracle. Owner decision 2026-10-14 (U30 E7-S1); open item 2 of the [U30 flows review](../reviews/U30-flows-review.md). Not release-blocking (owner, statistics sign-off 2026-10-31), but ships in v1.0.0 (owner decision 2026-11-02) Done in U66 (E6-S1, E6-S2): `OVERLAY_SYMMETRY` `params.exact` (R `binom.test(n_12, n_12 + n_21)`, oracle-pinned) plus the discordant odds ratio and its exact interval (95% by default; `params.conf_level`, #717), glossary term `discordant-odds-ratio`. · [U66](units/U66-overlay-measure-correctness.md)

## Added in-effort (owner, 2026-11-09)

Not on the original item list; folded in as epic E8 once the effort found them.

- **E8-S1** `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` refuses per measure, like #476: a measure without weighted moments gets null cells and a `reason: no_weighted_moments` warning.
- **E8-S2** Compose predict gives `PULSE_ADVISORY_SERIES_AXIS_NOT_DATE` for a FORMULA over a two-or-more-grouper host slot whose last grouper is not date-family, by the rule Process predict already used.
- **E8-S3** `internal/embeddersmoke` bumped to brotli v1.2.7; the `REG_ORDINAL` interpretation label renders as code (the mdBook warning is gone).

## Decisions kept

- Stage >= 1 smart defaults stay schema-typed: an untyped slot over a forwarded code column defaults to `AGG_SUM` and is refused (owner, 2026-11-09). Not a deferral.
- Chain crosstab stages are always `PULSE_CHAIN_NOT_MERGEABLE`, so #445 has no chain half.

## Handed-on

| Item | To | Why |
|---|---|---|
| #715 `make sites` omits `internal/seriesfield` / `internal/seriespartition` for `OVERLAY_FORMULA` | [U32](U32-docs-audit.md) | same tooling gap family as #706 |
| #716 `AGG_SET_*` over an `f64` chain column: predict and runtime differ in message and details | [U75](U75-zones-labels-2.md) | pre-existing parity edge; U75 owns the remaining parity proofs |
| #717 configurable `conf_level` for the discordant odds-ratio interval | done in U66 (E9-S1) | owner pulled it in 2026-11-10: `params.conf_level`, default 0.95, `exact` only |
| #718 ordered-categorical aggregation | done in U66 (E9-S2) | owner pulled it in 2026-11-10: `params.ordered` / `params.levels` on `AGG_MIN` / `AGG_MAX` / `AGG_MEDIAN` / `AGG_PERCENTILE`, R type 1 over level ranks, the figure a dictionary-index code |
| #719 exact Bowker (k x k) | post-v1 backlog | out of U66's scope; owner to confirm |
