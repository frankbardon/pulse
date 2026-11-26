```yaml
name: op-feat-one-hot
description: One-hot encode a categorical or packed_bool field as one derived 0/1 field per level.
kind: operator
category: FEAT
operator: FEAT_ONE_HOT
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, pre-filter, feature-pipeline]
```

Feature operators write derived fields before filters run; no `Response.Components`.

## Use when

Turns a category field into one 0/1 column per category, so a model or a sum can treat each category as its own yes/no.

Questions it answers:

- How do I feed region into a regression as separate yes/no predictors?
- Which rows are in each plan tier, as columns I can sum?

Use something else:

- `FEAT_FREQUENCY_ENCODE` when the field has hundreds of categories and one column each is too many.

## Params

None; `params` is unused.

## Inputs

`field` (required) — `categorical_u8`, `categorical_u16`, `categorical_u32` (must carry a dictionary), or `packed_bool` (no dictionary; levels `"0"`/`"1"`).

## Output

ONE derived field per dictionary entry, named `<prefix>_<category>`; a `packed_bool` emits exactly `<prefix>_0` and `<prefix>_1`, prefix = `label` (default `<field>`). Each row holds `1` in its category's field, `0` elsewhere. The set comes from the SCHEMA dictionary, so predict resolves a `<prefix>_<category>` reference (an unknown one is refused) without scanning records — but its `schema_info` lists source fields only.

## Contract

- Types (`field`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- A dictionary-less categorical `field` → `PROCESSING_CONFIG`.
- Whitespace in labels normalises to `_` (`"New York"` -> `<prefix>_New_York`); other punctuation is the caller's problem.
- A null category emits ALL ZERO across the block. No implicit "unknown" field.
- A dictionary category absent from the records still gets an all-zero field, which keeps downstream layouts stable.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: [`feature-engineering`](feature-engineering.md), [`op-feat-frequency-encode`](op-feat-frequency-encode.md), [`op-feat-target-encode`](op-feat-target-encode.md)
