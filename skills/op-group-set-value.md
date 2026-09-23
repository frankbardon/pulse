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

None. `Group.Label` overrides the output column name; `Group.Include []string` allow-lists composite keys (pipe-joined sorted labels, `"AMEX|VISA"`) and sets emission order.

## Inputs

`Field` — any set rung: `set_u8`/`set_u16`/`set_u32`/`set_u64`/`set_u128`/`set_u256`.

## Output

String bucket key per row (`"AMEX|VISA"`). Empty-mask rows land under an empty-key bucket — a valid selection, distinct from null.

Non-empty `Include` is order-significant: buckets emit in listed composite-key order (`Data` + `Components.buckets`); empty/absent keeps the prior alphabetical order, byte-identical. Zero-record entries drop.

## Components

Floor `{total_n, n_null}` + `n_empty_mask` (int, empty zero-bit selections) and `buckets` (`[]bucket` of `{key, count, labels}` per emission) plus **exactly one of** `mask` (uint64, when the selection fits 64 bits) or `mask_words` (`[]uint64`, low word first, when a `set_u128`/`set_u256` selection reaches bit 64+). Mutually exclusive: a `mask` holding only a wide selection's low word is a plausible wrong number. `Mergeable`; `StreamableGrouper`, so eligible for fused crosstab.

## Gotchas

- Empty mask is a real bucket — does NOT increment `n_null`. Null rows do.
- Bucket key is the sorted-label pipe-join; source dict order irrelevant.
- For multi-key fan-out use `GROUP_SET_PER_ELEMENT`.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `response-components`, `op-group-set-per-element`
