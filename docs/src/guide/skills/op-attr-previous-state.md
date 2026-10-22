```yaml
name: op-attr-previous-state
description: Per-row previous state of a long panel — the state the same subject held at its previous time point, typed as the state field.
kind: operator
category: ATTR
operator: ATTR_PREVIOUS_STATE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, buffered-pipeline]
```

## Use when

Adds to each row of a long panel the state the same subject held at its previous date, so moves can be counted from-state by to-state.

Questions it answers:

- How many customers moved from each plan to each other plan between snapshots, when the data has one row per customer per snapshot?
- Which tier was each member in at the previous monthly snapshot?

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `subject_field` | field | (required) | Column identifying the subject (matched by value). |
| `order_field` | field | (required) | `date` / `datetime` column ordering each subject's rows. |

Top-level slot keys: `field` (state), `subject_field`, `order_field`, `label`.

## Inputs

- `field` — a coded integer is not a state: re-import it categorical
- `subject_field` — any scalar but `set_*` / `vec_*`
- `order_field` — `date`, `datetime` — by instant, no `tz`

Source columns only.

## Output

The previous state, typed as `field` (its dictionary, nullable); crosstab it × `field`. `pulse_materialize` writes it as a real column of that type and dictionary.

## Components

- `n_subjects`, `n_first`, `n_gap`, `n_ambiguous`.

## Contract

- Types (`field`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Types (`subject_field`): `categorical_*`, `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Types (`order_field`): `date`, `datetime`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form; an explicit weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: buffered only; components `none`. No streaming form: a subject's records may sit anywhere in the cohort.
- Components: `n`, `n_null`, `n_subjects`, `n_first`, `n_gap`, `n_ambiguous`.

## Gotchas

- Null (never `0`): first time point, null subject / order (never a predecessor), null previous state (no carry-forward).
- Ties (same subject + order) read the time point before; the next reads the tied state only if all agree, else null + `PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE` (error under Strict).
- Runs over the whole cohort before filters: a date window keeps each first in-window record's true previous state; Components count every record. An expression filter may read it by state name, as on the state field (`prev == "paid"`, `prev != nil`); a field filter may not.
- Buffered: no streaming, fused crosstab or shard merge.

## See

- `pulse_examples_search tags=[cohort-analysis]`, `pulse_examples_search tags=[cohort-analysis, overlay]` (flows)
- Skills: [`crosstab-guide`](crosstab-guide.md), [`attribute-composition`](attribute-composition.md), [`op-overlay-markov`](op-overlay-markov.md), [`op-overlay-symmetry`](op-overlay-symmetry.md), [`op-overlay-retention`](op-overlay-retention.md)
