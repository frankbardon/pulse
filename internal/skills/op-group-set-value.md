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

<!-- generated: use-when -->

## Params

None. The key column is named after `field`; `include` allow-lists composite keys (pipe-joined sorted labels, `"AMEX|VISA"`) and sets emission order.

## Inputs

`field` — any set rung: `set_u8`/`set_u16`/`set_u32`/`set_u64`/`set_u128`/`set_u256`.

## Output

String bucket key per row (`"AMEX|VISA"`). Empty-mask rows land under an empty-key bucket — a valid selection, distinct from null.

Non-empty `include` is order-significant: buckets emit in listed composite-key order (`data` and components `buckets`); absent, alphabetical. Zero-record entries drop.

## Components

- `n_empty_mask` (int, empty zero-bit selections), `buckets` (`{key, count, labels}` per emission).
- **Exactly one of** `mask` (uint64, when the selection fits 64 bits) or `mask_words` (array of uint64 words, low word first, when a `set_u128`/`set_u256` selection reaches bit 64+). Mutually exclusive: a `mask` holding only a wide selection's low word is a plausible wrong number.
- Eligible for the fused crosstab.

<!-- generated: contract -->

## Gotchas

- Empty mask is a real bucket — does NOT increment `n_null`. Null rows do.
- Bucket key is the sorted-label pipe-join; source dict order irrelevant.
- A mask bit past the dictionary is corrupt data: `ENCODING_INVALID` (`set_member_out_of_range`), never folded into the in-range bucket.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `response-components`, `op-group-set-per-element`
