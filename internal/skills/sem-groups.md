---
name: sem-groups
description: The group of a multi-group MAT_SEM fit — which field or computed column splits the rows, its levels and their order, params.group_levels for a group the request computes (a wave pair chosen by request params), and the level warnings (an empty level, rows that are no level).
kind: design
type: guide
applies_to: process, compose, predict
covers: [SEM, multi-group, group, group_levels, derived group, computed group, wave pair, level order, group_reference, empty level, undeclared level]
requires: [MAT_SEM, capability:sem_groups]
---

# SEM groups

`params.group` splits the rows into the groups of ONE joint fit (lavaan's `group =`). How the fit reads the groups: `sem-modeling`.

## Which column

- A cohort field: categorical (levels in dictionary order), `packed_bool` (`0`, `1`) or `u4` / `u8` (the values that hold rows, ascending).
- A column the request computes — an attribute, feature or window label: a **derived group**. A cohort field of the same name wins. The column must hold a number per row (else `group_field_type`)<!-- feature: ATTR_PREVIOUS_STATE, ATTR_FORMULA -->: an `ATTR_PREVIOUS_STATE` over a categorical keeps the category — derive an integer from it with an `ATTR_FORMULA`<!-- /feature -->.
- A null value, or NaN, drops the row listwise (`n_listwise_dropped`).
- Refused as for any group: request `groups` / `crosstab` (`group_with_groups`), a member, a standardizing vector, `params.fit`.

## Declared levels

`group_levels` (a derived group only) fixes the levels, their labels and their order:

```json
"group_levels": [{"value": 1, "name": "from"}, {"value": 2, "name": "to"}]
```

- `value` is an integer the column must equal exactly; `name` is the label (default: the value's text, `"1"`).
- Array order is the `c(...)` position: `c(bfrom, bto)*x` reads `bfrom` on level `from`.
- Labels key the outputs: estimate rows `[from] lval op rval`, `vectors.group`, `n_group_from`, `chi2_group_from`, `group_reference`. The default reference is the first declared level.
- `bad_params`: fewer than two levels, a non-integer value, a repeated value or label, an empty name, `group_levels` on a cohort field, a `group_reference` that is no declared label, `c(...)` whose length is not the level count.

Without `group_levels` a derived group's levels are its integral values, ascending, labelled by their text. `c(...)` modifiers then are `bad_params`: declare the levels. More than 256 distinct values fail the slot alone with `PULSE_MATRIX_GROUP_LEVEL_LIMIT` (`details.limit`) — a measure or an identifier is no group.

`predict` lists declared levels as `group_levels` with `group_levels_basis` `declared` (an upper bound: a level without rows is no group); undeclared, the basis is `unknown`.

<!-- feature: ATTR_FORMULA -->
## A wave pair from request params

Map the two waves to declared levels with an attribute, so one stored request fits any pair. A categorical wave column (`wave_name` here, `wave` in the runnable example) reads as its label string, so the params are labels, not numbers:

```json
{"params": {"from_wave": "wave3", "to_wave": "wave1"},
 "attributes": [{"type": "ATTR_FORMULA", "label": "period",
   "expression": "wave_name == params.to_wave ? 2 : (wave_name == params.from_wave ? 1 : 0)"}]}
```

and the `MAT_SEM` slot's params:

```json
{"group": "period", "group_levels": [{"value": 1, "name": "from"}, {"value": 2, "name": "to"}],
 "model": "... + c(bfrom, bto)*service\nshift := bto - bfrom"}
```

The request `params` reach only the attribute expression: `MAT_SEM` params (`model`, `group_reference`, `group_levels`) are plain JSON and take no `params` reference — hence the fixed `from` / `to` names and the model's `c(bfrom, bto)` labels. Runnable: `internal/examples/matrices/19_sem_wave_pair.json`.

Every other wave is `0` — no level — so its rows drop. A reversed pair (`from` 3, `to` 1) reads the change from wave 3 to wave 1.
<!-- /feature -->

## Level warnings

Both reach the envelope's `warnings` (`details.index` locates the slot) and fail the slot with their own code under Strict:

- `PULSE_MATRIX_GROUP_LEVEL_EMPTY` — declared levels (a dictionary entry, `packed_bool`'s `0` / `1`, a `group_levels` entry) without rows after the filters, `details.levels`. Such a level is no group: no estimate rows, and its `n_group_` / `chi2_group_` scalars are ABSENT (not 0, not null).
- `PULSE_MATRIX_GROUP_LEVEL_UNDECLARED` — rows of a derived group whose value is no level (not declared, or not an integer), `details.count`. They are dropped listwise and counted in `n_listwise_dropped`; the fit stands.

Fewer than two levels with rows, or a reference level without rows, still fails the slot alone (`PULSE_MATRIX_INSUFFICIENT_N`).

<!-- feature: ATTR_SEM_SCORE -->
## Scoring on a derived group

`ATTR_SEM_SCORE` takes the same `group` and `group_levels`. Its group may be a feature output or the label of an attribute declared BEFORE the score slot (a later label is `bad_params`).

- Inline, the fit is the `MAT_SEM` fit on the same rows, so scores match a cohort-field twin bit for bit. A row whose value is no level scores null, counted once in `PULSE_MATRIX_GROUP_LEVEL_UNDECLARED` (`details.attribute`, `count`); a known level without fit rows warns `PULSE_MATRIX_GROUP_LEVEL_EMPTY`; more than 256 undeclared levels null every score with `PULSE_MATRIX_GROUP_LEVEL_LIMIT`.
- A bound fit (`params.fit`) taken on a derived group carries `group_derived` and its `group_levels`. The scoring request must compute the same column ahead of the slot, else `SERVICE_VALIDATION` `fit_group_field` (predict refuses it too). Rows match the fit's declared values exactly; another value scores null with `PULSE_MATRIX_GROUP_LEVEL_UNDECLARED`.
- These warnings sit on the envelope's `warnings`; under Strict the run fails with their code.
<!-- /feature -->

## See

- `op-mat-sem` — the params table.
- `sem-modeling` — invariance (`group_equal`, `group_partial`) and how a grouped fit reads.
- `sem-defined-parameters` — `:=` lines over per-group labels.
