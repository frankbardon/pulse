---
name: join-design
description: Pushdown hash-join surface — Request.Joins envelope, JoinSpec, OnPair key-type compatibility, As-prefix, v1 limits, and validating a join before running it.
type: guide
kind: design
applies_to: process, predict
covers: [Joins, JoinSpec, OnPair]
requires: [capability:joins]
---

# Join design

`Request.Joins` is a pushdown hash join. v1 supports **exactly one inner join per Request**: the right (build) side materialises in RAM, hashed by the join-key tuple; the left streams as the probe. Joined records then flow through the ordinary pipeline (filter → attribute → group → aggregate) exactly like single-cohort records.

## When to join

Join when the question needs columns that live in two cohorts sharing a key (orders + customers, responses + panel demographics). Skip it when:

- the second cohort only supplies a display name for a code — a label table does that at output time without a join<!-- feature: capability:labels --> (`label-display`)<!-- /feature -->;
- the lookup is a small numeric `key → value` map used inside an expression — an embedder `LookupTables` entry is cheaper (`expression-language`);
- you need several analyses over one cohort — that is a batch, not a join<!-- feature: capability:compose --> (`compose-requests`)<!-- /feature -->.

```jsonc
{
  "cohort": {"filename": "left.pulse"},
  "joins": [{
    "right": "right.pulse",
    "kind":  "inner",
    "as":    "r_",
    "on":    [{"left_field": "id", "right_field": "user_id"}]
  }],
  "aggregations": [
    {"type": "<aggregator>", "field": "score"},
    {"type": "<aggregator>", "field": "r_bonus"}
  ]
}
```

`<aggregator>` stands for any aggregator the manifest lists (`components.aggregators[]`); the join is independent of what runs downstream.

| Field | Meaning |
|---|---|
| `right` | Right-side cohort: single-file `.pulse`, shard archive, or `archive.pulse#shard.pulse` anchor. |
| `kind` | `"inner"` (empty = `"inner"`). `"left"`, `"outer"`, `"anti"` are reserved. |
| `on` | Equi-join key pairs (`OnPair[]`), AND-ed for composite keys. |
| `as` | Optional prefix on every right-side field name in the joined schema. |

The joined schema is `left_fields + right_fields` (right prefixed by `as`). Every downstream slot sees the union; reference right-side columns by their prefixed name (`r_bonus`).

## v1 envelope

The manifest `join` block (`max_joins_per_request`, `kinds`, `spill_bytes`, `limitations`) is the live statement of these limits.

- **One join per Request.** Two or more ⇒ `PULSE_JOIN_TOO_MANY`. For a multi-hop question, pre-join into a cohort.<!-- feature: capability:process_chain --> In a chain only stage 0 may join.<!-- /feature -->
- **Inner only.** `"left"` / `"outer"` / `"anti"` ⇒ `PULSE_JOIN_KIND_NOT_IMPLEMENTED`. An unmatched left row is dropped — it reaches no group, cell or count.
- **No spill.** The right side materialises fully in RAM, `O(right_records)`. Put the SMALLER cohort on the right; the build side is always `right` (no automatic swap). `pulse_inspect` reports each side's `record_count` without reading records.
- **Crosstab honours the join.** `Request.Crosstab` crosstabs the JOINED rows — axes and cell may name `as`-prefixed fields; unmatched left rows reach no cell or margin; always buffered.
- **No shard-parallel join.** A shard-archive left side runs serial on a joined request.

## OnPair key type compatibility

Equi-keys must compare equal after normalisation. Accepted:

- Identical types (`u32` ↔ `u32`).
- `categorical_*` ↔ `categorical_*` at any width (dictionary strings compare as text).
- The unsigned-int / float / date numeric family, interchangeably (`u32` ↔ `f64` ↔ `date`).

Rejected:

- **Any `set_*` column, either side, even at identical rungs.** A bitmask has no unambiguous equality value, and the key would be a lossy float echo of the mask, so different selections collapse onto one key and unrelated rows join silently. Filter on set membership instead. Carrying a set column THROUGH a join (non-key) is fine.
- `decimal128` ↔ any other type (precision/scale matter for hashing).
- `categorical_*` ↔ a non-categorical numeric type.

A mismatch is `PULSE_JOIN_TYPE_MISMATCH` with both field names + types in `details`; a set-key rejection adds `details.reason = "set_key"`. Fix by re-importing one side with a matching type.

**Known limit:** a `u64` key above 2^53 compares through its float echo, so two ids rounding to one float join as equal. `decimal128` keys compare on their exact mantissa.

## Field collisions and the `as` prefix

Two fields with the same name in the union ⇒ `PULSE_JOIN_FIELD_COLLISION`. Set `as` to prefix every right-side column (`"as": "r_"` turns `bonus` into `r_bonus`). Categorical right-side fields keep their own dictionaries, so they render the right side's strings.

## Validate before executing

`pulse_predict` validates a join against BOTH cohorts' headers + schemas, never their records: kind, key existence, key-type compatibility, empty keys, collisions and the one-join limit, with the SAME codes, messages and details the runtime raises (`PULSE_JOIN_KIND_NOT_IMPLEMENTED`, `PULSE_JOIN_FIELD_UNKNOWN`, `PULSE_JOIN_TYPE_MISMATCH`, `PULSE_JOIN_KEYS_EMPTY`, `PULSE_JOIN_FIELD_COLLISION`, `PULSE_JOIN_TOO_MANY`). Every other slot is then checked against the JOINED schema, so a typo'd `r_` field surfaces before any decode. `pulse errors lookup CODE` gives each code's fixups.

## Cost

- **Build** scales with right-side record count; a tall right cohort (10M+ rows) can dominate memory.
- **Probe** is `O(left_records)`, one hash lookup per row; categorical keys resolve through the dictionary per row.

## See

`aggregation-design` (operators over the joined schema) · `cohort-schema-design` (shard archives + `archive.pulse#shard.pulse` anchors) <!-- feature: capability:process_chain --> · `process-chain` (joining at stage 0)<!-- /feature --> · `docs/src/internals/` (adding a join kind + spill wiring).
