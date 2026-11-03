---
id: U66
slug: overlay-measure-correctness
title: "Overlay handlers score the measure the analyst named, and a code column or a mixed-type crosstab is refused instead of scored silently"
track: API & release
size: M
status: not-started
depends_on: [U77, U78]
soft_depends_on: [U67]
blocks: [U32]
todo_items: [438, 441, 443, 444, 445, 446, 447, 476]
branch: overlay-measure-correctness
---

# U66 — overlay-measure-correctness

**Outcome:** Overlay handlers score the measure the analyst named, and a code column or a mixed-type crosstab is refused instead of scored silently.

**Track:** API & release · **Size:** M · **Depends on:** U77, U78 (CI and test hygiene land first) · **Soft:** U67

## Summary

Overlay handlers on Process, Compose and chained crosstabs make several silent choices today: which measure a formula scores, which target a two-sample handler reads, whether a dictionary code is treated as a value or a moment. This unit audits each handler for a value-versus-moment reading, adds the missing `params.field` / multi-target refusals, and decides whether numeric aggregators over code columns are refused. Mixed-cell multi-measure crosstabs are refused per measure, predict and runtime alike. Note: predict-extension-parity-2 touches the extension MATRIX overlay rule of #447.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [ ] **#438** Process SERIES `OVERLAY_FORMULA` is refused (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, runs on Compose only); wiring the series / scalar binders on a grouped Process host, and the partition semantics of `prior` / `total` over two or more groups, needs an owner decision. Found in U46 (E4-S15) · [U66](units/U66-overlay-measure-correctness.md)
- [ ] **#441** Compose `OVERLAY_FORMULA` over SERIES slots scores `Aggregations[0]`; it takes no `params.field` to pick the measure. Found in U46 (E4-S15) · [U66](units/U66-overlay-measure-correctness.md)
- [ ] **#443** Compose two-sample T/Z handlers (`OVERLAY_T_VS_REF` / `_Z_VS_REF` / `_T_CELL` / `_Z_CELL`) score only `Targets[0]`; extra targets are silently ignored. Decide whether to refuse multi-target specs. Found in U46 (E4-S16) · [U66](units/U66-overlay-measure-correctness.md)
- [ ] **#444** The Compose T/Z series arm treats a scored `AGG_VARIANCE` / `AGG_STDDEV` value as the side's mean; consider refusing non-mean scalars. Found in U46 (E4-S16) · [U66](units/U66-overlay-measure-correctness.md)
- [ ] **#445** Compose crosstab slots and whole-chain crosstab stages over `AGG_MODE`-over-categorical cells still score dictionary codes; needs a per-kind value-vs-moment audit (`OVERLAY_T_CELL` / `_Z_CELL` read Welford triples). Found in U46 (E4-S17) · [U66](units/U66-overlay-measure-correctness.md)
- [ ] **#446** Non-overlay use of a code column: `AGG_SUM` / `AGG_AVERAGE` etc. over a categorical field (stage 0), or over a forwarded code column (a later chain stage), compute over dictionary indexes; owner decision whether to refuse numeric aggregators over code columns. Found in U46 (E4-S17) · [U66](units/U66-overlay-measure-correctness.md)
- [ ] **#447** Extension MATRIX overlay kinds are not judged by the categorical-code cell rule: an extension kind declares no reads-cell-value metadata (`overlayhost.ReadsCellValue` covers built-ins only). Found in U46 (E4-S17) · [U66](units/U66-overlay-measure-correctness.md)
- [ ] **#476** `OVERLAY_PAIRWISE_WELCH_T` / `_TWO_MEANS_Z` on a multi-measure crosstab whose measures have mixed cell types is refused whole; refuse per measure, predict and runtime alike. Found in U47 (E4-S5) · [U66](units/U66-overlay-measure-correctness.md)
