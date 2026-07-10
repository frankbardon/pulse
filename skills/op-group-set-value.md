---
name: op-group-set-value
description: Partition rows by the exact set mask — one bucket per unique combination; key = sorted labels joined with "|".
kind: operator
category: GROUP
operator: GROUP_SET_VALUE
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, cohort-analysis]
---

## Params

None. `Group.Label` overrides output column name; `Group.Include []string` allow-lists composite keys (pipe-joined sorted labels, e.g. `"AMEX|VISA"`) and sets emission order.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `set_u8`/`set_u16`/`set_u32`/`set_u64` |

## Output

String bucket key per row (e.g. `"AMEX|VISA"`). Empty-mask rows land under an empty-key bucket — a valid distinct selection from null.

Non-empty `Include` is order-significant: buckets emit in listed composite-key order (`Data` + `Components.buckets`); empty/absent keeps prior alphabetical order (byte-identical). Zero-record entries dropped.

## Components

Universal floor `{total_n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `n_empty_mask` | int | Records whose set mask was the empty selection (zero-bit) |
| `buckets` | []bucket | `{key, mask, count, labels}` per emission |

- Mergeability: `Mergeable`
- Streaming: `StreamableGrouper` — eligible for fused crosstab

## Gotchas

- Empty mask is a real bucket — does NOT increment `n_null`. Null rows do.
- Bucket key is the sorted-label pipe-join; source dict order irrelevant.
- For multi-key fan-out use `GROUP_SET_PER_ELEMENT`.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `response-components`, `op-group-set-per-element`
