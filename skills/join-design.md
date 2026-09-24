---
name: join-design
description: Pushdown hash-join surface — Request.Joins envelope, JoinSpec, OnPair key-type compatibility, As-prefix, v1 limits, and validate-join predict surface.
type: guide
kind: design
applies_to: process, predict
covers: [Joins, JoinSpec, OnPair]
---

# Join design

Pulse exposes pushdown hash join via `Request.Joins`. v1 supports **exactly one inner join per Request**: the right (build) side materialises in RAM hashed by the join-key tuple; the left streams as the probe. Joined records flow through the standard processor pipeline (filter / attribute / group / aggregator) just like single-cohort records.

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
    {"type": "AGG_SUM", "field": "score"},
    {"type": "AGG_SUM", "field": "r_bonus"}
  ]
}
```

| Field | Meaning |
|---|---|
| `right` | Right-side cohort. Single-file `.pulse`, shard archive, or `archive#shard.pulse` anchor. |
| `kind` | `"inner"` (empty = `"inner"`) in v1. `"left"`, `"outer"`, `"anti"` reserved. |
| `on` | Equi-join key pairs (`OnPair[]`). AND-ed for composite keys. |
| `as` | Optional prefix prepended to every right-side field name in the joined schema. |

The joined schema is `left_fields + right_fields` (with `as` prefix on right). Aggregators / filterers / groupers downstream see the union schema; reference right-side columns by their prefixed name (e.g. `r_bonus`).

## v1 envelope

- **One join per Request.** `PULSE_JOIN_TOO_MANY` for two or more. Multi-join chains need a per-join intermediate state machine — deferred.
- **Inner only.** `PULSE_JOIN_KIND_NOT_IMPLEMENTED` for `"left"` / `"outer"` / `"anti"`. Outer-join correctness depends on the null-bitmap path being fully wired through every right-side field.
- **No spill.** Right materialises fully in RAM. Memory cost `O(right_records × per_record_state)`. Future iteration adds `PULSE_JOIN_SPILL_DIR` + `PULSE_JOIN_MAX_MEMORY_BYTES` + a partition-then-build-per-partition algorithm.
- **No smarter-side detection.** Build is always `right`. Follow-up swaps when `count(right) > count(left)` via `pulse.CountRecords`.
- **No shard-parallel join.** When the left cohort is a shard archive, the per-shard parallel reducer does not engage on joined requests today.

## OnPair key type compatibility

Hash join needs equi-keys to compare byte-equal after normalisation. The v1 type-compatibility predicate accepts:

- Identical types (`u32` ↔ `u32`).
- `categorical_*` ↔ `categorical_*` at any width (dict strings normalise to text).
- The unsigned-int / float / date numeric family is interchangeable within itself (`u32` ↔ `f64` ↔ `date` all compare via canonical float stringification).

Rejects:

- **Any `set_*` column, on either side, even at identical rungs.** A bitmask has no unambiguous equality value (empty, single-member and multi-member masks are all distinct legal states), and the key that would have been used is the lossy `float64` echo of the mask — low 64 bits for `set_u128`/`set_u256`, past the 53-bit mantissa for `set_u64` — so different selections collapse onto one key and unrelated rows join silently. Use `FILTER_SET_*`. Join twin of the point-lookup index-key rejection. Carrying a set column **through** a join (non-key) is unaffected.
- `decimal128` ↔ any other type — precision/scale matter for hash bucketing.
- `categorical_*` ↔ a non-categorical numeric type.

`decimal128` keys compare on their **exact 128-bit mantissa bytes**, not the `Float64(scale)` echo — otherwise two decimals closer than float64's spacing share a key. Safe to special-case: `decimal128` is admitted only against `decimal128`.

Mismatches surface `PULSE_JOIN_TYPE_MISMATCH` with the offending field names + types in `details`; a set-key rejection adds `details.reason = "set_key"` and its own sentence, since "not compatible" reads as a typo when both sides carry the same rung. Fix by re-importing one side with a matching type.

**Known limit:** a `u64` key above 2^53 rides that same `float64` echo, so two ids rounding to one float join as equal. Unfixed — it needs the whole unsigned-int/float/date family renormalised together.

## Field collisions and `As` prefix

The joined schema unions left + right field names. Two fields with the same name ⇒ `PULSE_JOIN_FIELD_COLLISION`. Set `JoinSpec.As` to prefix every right-side column (e.g. `"as": "r_"` turns right's `bonus` into `r_bonus`). Categorical fields keep their original dictionary refs — `dict.Resolve` continues to return the right-side strings; the joined `Record.values[r_bonus]` carries the right-side dict id.

## Validation surface

`descriptor.ValidateJoin(left, right io.ReadSeeker, req)` is the no-execute predict equivalent. Reads both files' header + schema, validates every `OnPair`, emits the inferred joined field list at `result.joined_fields`. Error codes mirror runtime: `PULSE_JOIN_KIND_NOT_IMPLEMENTED`, `PULSE_JOIN_FIELD_UNKNOWN`, `PULSE_JOIN_TYPE_MISMATCH`, `PULSE_JOIN_KEYS_EMPTY`, `PULSE_JOIN_FIELD_COLLISION`, `PULSE_JOIN_TOO_MANY`. Descriptor manifest exposes `Manifest.Join` (`JoinCapability`) with the v1 kind allowlist, the spill envelope (zero today), and the limitations list.

## Performance notes

- **Inner join + mergeable downstream operators stays mergeable** in principle, but the per-shard parallel reducer does not engage on joined requests in v1. Joined requests run serial until the per-shard wiring lands.
- **Build cost** is proportional to right-side record count. Tall right cohorts (10M+ rows) can dominate the memory budget — split via shard archives + per-shard joins, or wait for the spill path.
- **Probe cost** is `O(left_records)` at one hash-lookup per row. Categorical keys go through the dictionary resolver per row; high-cardinality string keys pay hash overhead.

## See

- `skills/aggregation-design.md` — operators consuming the joined schema downstream.
- `docs/src/internals/` — Internals chapter: adding a join kind (left/outer/anti) + spill wiring.
- `skills/cohort-schema-design.md` — shard archive + anchor syntax (`archive.pulse#shard.pulse`).
