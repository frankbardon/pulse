---
id: U79
slug: library-gaps
title: "Optimize bounds, window parameters and extension parameters take expressions, saved and inline models carry their sweep axes, and result(), response(), base, for_each and synth lookup() close the embedder gaps of #427"
track: API & release
size: L
status: done
depends_on: []
soft_depends_on: []
blocks: [U32]
todo_items: [550, 570, 576, 692, 693, 694, 695, 696, 697, 698, 699, 700, 701, 702, 703]
branch: library-gaps
---

# U79 — library-gaps

**Outcome:** Optimize bounds, window parameters and extension parameters take expressions, saved and inline models carry their sweep axes, and `result()`, `response()`, `base`, `for_each` and synth `lookup()` close the embedder gaps of #427.

**Track:** API & release · **Size:** L · **Depends on:** none (built on `main` alpha.37) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

Downstream analysis libraries built on the alpha.37 surface hit eight Pulse gaps, filed as #427 (G1, G2, G4, G5, G7, G8, G9, G11). This unit closes all of them in one effort and one alpha, and adds `AGG_CONSTANT`. Three open items of [U71](U71-mmm-optimize-depth.md) (#550, #570, #576) are the same work and moved here.

| Gap | Surface | Stories |
|---|---|---|
| G11 | Scalar-expression slot core: an optimize `lower` / `upper` / `start` takes an expression (`internal/scalarslot`) | E1-S1 |
| G8 | Window scalar params take expressions, `sweep_axis()` of the winner included | E1-S2 |
| — | Extension params opt in (`ParamMeta.Expression`), with the template / `for_each` / sweep splice bans | E1-S3, E1-S4, E1-S5 |
| — | `AGG_CONSTANT`, a typed constant in every row | E2-S1 |
| G1 | Model-file reset (`model_format_version` 1 + `features`) and saved sweep axes | E3-S1, E3-S2 |
| G2 | Inline models (`ModelRef.inline`) | E3-S3 |
| G9 | Adstock lineage record and `response(fit, channel, spend)` | E4-S1, E4-S2 |
| G7 | `result(slot, aggregation)` with a pre-phase (delegated design call: a slot dependency, not a `scalars` block) | E5-S1, E5-S2 |
| G5a | Compose `base` | E6-S1 |
| G4 | `for_each` on aggregations, matrices and optimize blocks, `Limits.MaxExpandedItems` | E7-S1, E7-S2, E7-S3 |
| G5b | Synth rules call `lookup()` | E8-S1 |

E1-S4, E1-S5 and E7-S3 were added during the effort to close known limits found by earlier stories (owner rule: close known limits in the effort). Breaking and behaviour changes are listed in [the embedder migration guide](../v1.0.0-api-and-release/03-embedder-migration.md) under "Changes from library-gaps (U79)".

## Owned TODO items

- [x] **#550** `optimize` bounds and constraints computed from data aggregates (`total()`-style values); callers pass literals or `params` today. Deferred from U60. Done (library-gaps E5-S1, E5-S2): `result("<slot>", "<aggregation>")` in an optimize bound, start, objective or constraint reads an ungrouped aggregation of a Compose slot; that slot runs once in a pre-phase between the fit phases and the blocks, under `Compose`, `ComposeParallel` (fail-fast or not) and `--stream`; refusals are `PULSE_RESULT_INVALID`. The delegated design call: a slot dependency, not a `scalars` block · [U79](U79-library-gaps.md)
- [x] **#570** An optimizer objective helper `response(fit, channel, spend)` so an `optimize` expression reads a fitted channel's response curve without hand-written Hill algebra. Deferred from U63. Done (library-gaps E4-S1, E4-S2): `response(fit, channel, spend)` = `beta · hill(m · spend, k, s)` in formulas, filters, optimize and the scalar slots, `m` the channel's steady-state adstock multiplier from a per-channel adstock record the fit carries (and a saved model file stores as feature `adstock`); a channel without a record is `PULSE_MODEL_TERM_UNKNOWN` `adstock_unknown` · [U79](U79-library-gaps.md)
- [x] **#576** A window parameter bound to the sweep winner (for example a `WIN_DISTRIBUTED_LAG` `decay` reading `sweep_axis`). Today a window's `decay` must be a literal and the recipe rebuilds adstock with `for_each` `WIN_LAG` plus a Horner formula; a bound param is new syntax and a design question. Deferred from U63. Done (library-gaps E1-S2): a `WIN_DISTRIBUTED_LAG` `decay` / `shape` / `scale` / `max_lag` and a `WIN_EWMA` `alpha` / `decay` take a scalar expression, `sweep_axis()` and `coef()` included; a window reading the fit its own sweep binds is `PULSE_FIT_INVALID` `from_sweep_window_cycle` · [U79](U79-library-gaps.md)
- [x] **#692** The scalar-expression slot core (#427 G11): an optimize variable's `lower` / `upper` / `start` takes any scalar expression over `params`, `coef()` / `sweep_axis()`, `lookup()` / `range_label()` and the built-ins, not only a number or `"params.<name>"`. One evaluator (`internal/scalarslot`) and one refusal vocabulary serve every scalar slot; predict lists a request-bound one under `unresolved_scalars`. Done (library-gaps E1-S1) · [U79](U79-library-gaps.md)
- [x] **#693** Window scalar params as expressions (#427 G8): `WIN_DISTRIBUTED_LAG` `decay` / `shape` / `scale` / `max_lag` and `WIN_EWMA` `alpha` / `decay` accept a string expression resolved before the window reads it, `sweep_axis()` of the sweep winner included. Done (library-gaps E1-S2); closes #576 · [U79](U79-library-gaps.md)
- [x] **#694** Extension operator params as expressions: `pulse.ParamMeta.Expression` lets a number param take a scalar expression across all nine categories, plus the template, `for_each` and sweep splice bans over a flagged param (`PULSE_TEMPLATE_EXPR_SPLICE`, `PULSE_FOR_EACH_EXPR_SPLICE`, `PULSE_SWEEP_EXPR_SPLICE`, slot `extension_param`). Done (library-gaps E1-S3, E1-S4, E1-S5; E1-S4 and E1-S5 were added in the effort to close known limits) · [U79](U79-library-gaps.md)
- [x] **#695** `AGG_CONSTANT`: a fieldless aggregator emitting one typed constant (any of the 22 field types) from `params.value` or `params.expr`, the same in every row, group, crosstab cell and margin. Done (library-gaps E2-S1) · [U79](U79-library-gaps.md)
- [x] **#696** The model-file versioning reset: every save writes `model_format_version` 1 plus a `features` list (`robust_covariance`, `outputs`, `dispersion`, `theta`, `sweep_axes`, `adstock`); versions 2 to 6 are refused with `PULSE_MODEL_VERSION_UNSUPPORTED` and a re-save fixup, an unknown feature likewise. Done (library-gaps E3-S1); no aliases (owner decision) · [U79](U79-library-gaps.md)
- [x] **#697** Saved sweep models keep their winning axes (#427 G1): feature `sweep_axes` persists a `from_sweep` fit's axis values and `File.Bind` restores them, so `sweep_axis()` works on a loaded model (formulas, filters, window params, `AGG_CONSTANT` `expr`, optimize, Materialize). Done (library-gaps E3-S2) · [U79](U79-library-gaps.md)
- [x] **#698** Inline models (#427 G2): `ModelRef.inline` declares a coefficient-only model in the request, every number a literal or an expression over `params`; `coef()` and `sweep_axis()` read it, scoring one is `PULSE_MODEL_SCORE_INVALID` `inline_not_scorable`. Done (library-gaps E3-S3) · [U79](U79-library-gaps.md)
- [x] **#699** `result(slot, aggregation)` slot dependency (#427 G7, the delegated design call: a slot dependency, not a `scalars` block): optimize-only, `PULSE_RESULT_INVALID`, a pre-phase that runs each read slot once, identical under `ComposeParallel` and `--stream`. Done (library-gaps E5-S1, E5-S2); closes #550 · [U79](U79-library-gaps.md)
- [x] **#700** `response(fit, channel, spend)` (#427 G9) and the per-channel adstock lineage record it reads. Done (library-gaps E4-S1, E4-S2); closes #570 · [U79](U79-library-gaps.md)
- [x] **#701** Compose `base` (#427 G5a): `ComposedRequest.Base` merges shared windows, features, attributes and formulas into every explicit slot (`capability:compose_base`, `PULSE_COMPOSE_BASE_COLLISION`). Done (library-gaps E6-S1) · [U79](U79-library-gaps.md)
- [x] **#702** `for_each` on aggregations, matrices and Compose optimize blocks (#427 G4), with `Limits.MaxExpandedItems` and the payload schema admitting `{"$var"}` markers in a fanned-out block. Done (library-gaps E7-S1, E7-S2, E7-S3; E7-S3 was added in the effort to close a known limit) · [U79](U79-library-gaps.md)
- [x] **#703** Synth rules call `lookup()` (#427 G5b): the instance's lookup tables are an input of the run (`synth.Options.LookupTables`, `Spec.HashWithLookups`), checked before row 0, deterministic per spec + seed + tables. Done (library-gaps E8-S1) · [U79](U79-library-gaps.md)

## Handed-on

| Item | To | Why |
|---|---|---|
| #380 `for_each` inside a Compose `sweep` body | [U72](U72-compose-sweep-2.md) | U79 extended the `nested_in_sweep` refusal to aggregation, matrix and optimize `for_each`; lifting it stays with the sweep's lexical-scoping work |
| #704 non-fail-fast `ComposeParallel` returns early when sweep phase 0 fails | [U72](U72-compose-sweep-2.md) | pre-existing; inconsistent with the aggregate contract every later phase follows (sibling of #580) |
| #709 whole-array `{"$var"}` marker in a `for_each` optimize block | [U72](U72-compose-sweep-2.md) | the runtime accepts it; the payload and MCP schemas do not (stricter than runtime) |
| #705 `Explain` does not describe Compose `optimize` blocks | [U71](U71-mmm-optimize-depth.md) | pre-existing; U71 owns the optimize surface |
| #706 `make sites` covers `types.Request` only, not `ComposedRequest` | [U32](U32-docs-audit.md) | extends #560; the tool missed several site families on this effort's Compose-level slots |
| #707 flaky `TestRecord_LayoutCacheReleasesCollectedSchemas` | [U74](U74-memory-bounds.md) | GC-timing failure once in a parallel multi-package run; U78 (the test-hygiene owner) is closed, so the memory-bounds unit is the nearest fit |
| #708 predict does not fold a loaded or inline model's `coef()` / `sweep_axis()` / `response()` values | [U67](U67-predict-extension-parity-2.md) | design option; the values stay under `unresolved_scalars` and are checked at runtime |
| #710 facet predict lists no `unresolved_scalars` for an extension expression param | [U67](U67-predict-extension-parity-2.md) | predict / runtime parity residue |
| #711 splice bans match an extension param by literal `type` only | [U67](U67-predict-extension-parity-2.md) | a rendered `type` flags nothing; parity residue |
| #712 Explain's splice-ban caveat has no dedicated test | [U67](U67-predict-extension-parity-2.md) | pin it with the parity work |

The post-alpha cross-repo acceptance (the Studio dependency bump and the library examples that collect each #427 payoff) is tracked on #427, not here.
