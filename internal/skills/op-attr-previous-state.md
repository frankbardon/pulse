---
name: op-attr-previous-state
description: Per-row previous state of a long panel — the state the same subject held at its previous time point, typed as the state field.
kind: operator
category: ATTR
operator: ATTR_PREVIOUS_STATE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, buffered-pipeline]
---

<!-- generated: use-when -->

## Params

None. Top-level keys: `field` (state), `subject_field`, `order_field`, `label`.

## Inputs

| Slot | Accepted field types |
|---|---|
| `field` | `categorical_*`, `packed_bool`; a coded integer → `PULSE_FIELD_TYPE_UNSUPPORTED` (re-import categorical) |
| `subject_field` | any scalar but `set_*` / `vec_*` |
| `order_field` | `date`, `datetime` — by instant, no `tz` |

Source columns only.

## Output

The previous state, typed as `field` (its dictionary, nullable); crosstab it × `field`.<!-- feature: capability:materialize --> `pulse_materialize` writes it as a real column of that type and dictionary.<!-- /feature -->

## Components

Floor `{n, n_null}` + `n_subjects`, `n_first`, `n_gap`, `n_ambiguous`. `None`: terminal chunk only.

## Gotchas

- Null (never `0`): first time point, null subject / order (never a predecessor), null previous state (no carry-forward).
- Ties (same subject + order) read the time point before; the next reads the tied state only if all agree, else null + `PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE` (error under Strict).
- Runs over the whole cohort before filters: a date window keeps each first in-window record's true previous state; Components count every record. An expression filter may read it (`prev != nil`; it holds the state's code), a field filter may not.
- Buffered: no streaming, fused crosstab or shard merge.
- Weight refused (`"weight": null` runs it).

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `crosstab-guide`, `attribute-composition`
