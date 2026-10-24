```yaml
name: synth-cross-row
description: Synth rows that depend on EARLIER rows — `row()` and `lag()` in rules and constraints, accepted-row order, the panel reset, a recursive adstock recipe, and what profile capture ignores.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, row, lag, adstock, panel, cross-row, accepted-row]
requires: [capability:synth]
```

# Synth cross-row expressions

A rule or constraint may read the row ORDINAL (`row()`) and an EARLIER row's value (`lag`). Generation is serial and row-major with no shuffle, so declaration order IS row order and "the previous row" is well defined.

## Accepted-row order

- `row()` is the 0-based ordinal of the ACCEPTED row. A constraint-rejected attempt redraws at the SAME ordinal, so the written cohort reads exactly `0..row_count-1`. `monotonic_from` counts the same way.
- `lag("f", n[, d])` reads `f`'s value `n` accepted rows back. A rejected attempt never enters the history. `n` is an integer literal 1–1024 and the name a declared-field string literal (`PULSE_SYNTH_LAG_OFFSET_INVALID` / `PULSE_SYNTH_LAG_FIELD_INVALID`).
- Neither consumes RNG: adding one moves no other field's draws.
- Before row `n`, `lag` returns `d`, else null. A lagged NULL stays null (SQL `LAG`), never `d`: guard with `??` or a default. In augment mode the history starts empty at the first generated row.
- It returns the post-rule value BEFORE encode rounding, so on a `u*` / `f32` field it can differ from the stored value. Self-lag works.

## Recipes

Recursive adstock, with seasonal controls (`internal/examples/synth/07_mmm_adstock.synth.json`):

```json
"rules": [
  {"set_expr": {"week": "row()"}},
  {"set_expr": {"true_adstock": "spend + 0.6 * lag(\"true_adstock\", 1, 0)"}},
  {"set_expr": {"fourier_sin": "sin(2 * pi() * week / 52)"}}
]
```

`week` is its own rule so later rules see it (within one rule, expressions read the row as it stood BEFORE the rule).

**Panel reset.** Panels laid out contiguously (52 rows each) restart the recursion at each boundary:

```json
{"set_expr": {"stock": "spend + 0.6 * (row() % 52 == 0 ? 0 : lag(\"stock\", 1, 0))"}}
```

## Not seen by capture

`profile create`, the fidelity report and `--suggest-rules` know nothing of `lag`: a captured profile reconstructs marginals only, the report never scores serial dependence, and no detector proposes a lag rule. Write them by hand.

## See

- [`synth-rule-expressions`](synth-rule-expressions.md) — the row environment and coercion; [`synth-structural-rules`](synth-structural-rules.md) — slots and order.
- [`synth-determinism`](synth-determinism.md) — why none of this draws RNG.
