---
name: join-design
description: Pulse's pushdown hash-join surface (Request.Joins, JoinSpec, OnPair) — algorithm, schema synthesis, v1 envelope, and roadmap.
type: guide
applies_to: process, predict
---

<skill_overview>
Pulse exposes pushdown hash join via `Request.Joins`. v1 supports exactly one inner join per Request: the right (build) side is materialised in RAM, hashed by the join-key tuple, and the left side streams as the probe. Joined records flow through the standard processor pipeline (filter / attribute / group / aggregator) just like single-cohort records.

The motivation is to give Pulse a first-class equi-join primitive so consumers — chiefly Prism's `compile/inmem/join.go` — can drop their client-side hash join and let Pulse stream + (eventually) spill at scale. This skill describes the v1 envelope, the gotchas, and the roadmap items deferred to follow-up PRs.
</skill_overview>

<reference>
## Request shape

```json
{
  "cohort": {"filename": "left.pulse"},
  "joins": [
    {
      "right": "right.pulse",
      "kind": "inner",
      "as": "r_",
      "on": [
        {"left_field": "id", "right_field": "user_id"}
      ]
    }
  ],
  "aggregations": [
    {"type": "AGG_SUM", "field": "score"},
    {"type": "AGG_SUM", "field": "r_bonus"}
  ]
}
```

| Field | Meaning |
|---|---|
| `right` | Cohort path for the right side. Single-file `.pulse`, shard archive, or `archive#shard.pulse` anchor. |
| `kind` | `"inner"` (or empty == `"inner"`) in v1. `"left"`, `"outer"`, `"anti"` reserved. |
| `on` | Equi-join key pairs. AND-ed for composite-key joins. |
| `as` | Optional prefix prepended to every right-side field name in the joined schema. |

The joined schema is `left_fields + right_fields` (with `as` prefix on right fields). Aggregators / filterers / groupers downstream see the union schema directly — reference right-side columns by their prefixed name (e.g. `r_bonus`).
</reference>

<rule severity="block" topic="v1-envelope">
## v1 envelope

The first cut of pushdown hash join ships with intentional constraints. Plan accordingly:

- **One join per Request.** `PULSE_JOIN_TOO_MANY` for two or more entries in `Joins`. Multi-join chains need a per-join intermediate state machine on the orchestrator — deferred.
- **Inner only.** `PULSE_JOIN_KIND_NOT_IMPLEMENTED` for `"left"`, `"outer"`, `"anti"`. Outer-join correctness depends on the null bitmap path (P-UP-2) being fully wired through every right-side field; today the joined record's null state is inherited from left + right inputs, no synthesised "missing right" rows.
- **No spill.** The right side materialises fully in RAM. Memory cost is `O(right_records × per_record_state)`. A future iteration adds `PULSE_JOIN_SPILL_DIR` + `PULSE_JOIN_MAX_MEMORY_BYTES` and the partition-then-build-per-partition spill algorithm.
- **No smarter-side detection.** Build is always the spec's `right` cohort. `pulse.CountRecords` lands in P-UP-4; the follow-up swaps sides when `count(right) > count(left)`.
- **No shard-parallel join.** When the left cohort is a shard archive, the per-shard parallel reducer (Parallel shards) does not engage on joined requests today.
</rule>

<rule severity="caveat" topic="key-types">
## Key type compatibility

Hash join requires equi-join keys to compare equal byte-for-byte after normalisation. The v1 type-compatibility predicate accepts:

- Identical types (`u32` ↔ `u32`).
- `categorical_*` ↔ `categorical_*` of any width (dict strings normalise to text).
- The unsigned-int / float / date numeric family is interchangeable within itself (`u32` ↔ `f64` ↔ `date` all compare via canonical float stringification).

Reject:

- `decimal128` ↔ any other type. Decimal keys must match exactly because precision / scale matter for hash bucketing.
- `categorical_*` ↔ a non-categorical numeric type.

Mismatches surface `PULSE_JOIN_TYPE_MISMATCH` with the offending left / right field names + types in `details`. Fix by re-importing one side with a matching type.
</rule>

<rule severity="caveat" topic="field-collisions">
## Field collisions

The joined schema unions left + right field names. Two fields with the same name → `PULSE_JOIN_FIELD_COLLISION`. Set `JoinSpec.As` to prefix every right-side column (e.g. `"as": "r_"` turns right's `bonus` into `r_bonus` in the joined record).

Categorical fields keep their original dictionary references — `dict.Resolve` continues to return the right-side strings. The joined Record's `values[r_bonus]` carries the right-side dict id.
</rule>

<reference>
## Validation surface

`descriptor.ValidateJoin(left, right io.ReadSeeker, req)` is the no-execute predict equivalent. It reads both files' header + schema, validates every OnPair, and emits the inferred joined field list under `result.joined_fields`. Errors mirror the runtime codes (`PULSE_JOIN_KIND_NOT_IMPLEMENTED`, `PULSE_JOIN_FIELD_UNKNOWN`, `PULSE_JOIN_TYPE_MISMATCH`, `PULSE_JOIN_KEYS_EMPTY`, `PULSE_JOIN_FIELD_COLLISION`, `PULSE_JOIN_TOO_MANY`).

The descriptor manifest exposes `Manifest.Join` (`JoinCapability`) with the v1 kind allowlist, the spill envelope (zero today), and the limitations list.
</reference>

<rule severity="caveat" topic="performance">
## Performance notes

- **Inner join + mergeable downstream operators stays mergeable** in principle, but the per-shard parallel reducer (Parallel shards) does not engage on joined requests in v1. Until that lands, joined requests run on the serial path.
- **Build cost** is proportional to right-side record count. For tall right cohorts (10M+ rows) the memory budget can dominate — split via shard archives + per-shard joins, or wait for the spill path.
- **Probe cost** is O(left_records) at one hash-lookup per row. Categorical keys go through the dictionary resolver per row; high-cardinality string keys pay the hash overhead.
</rule>
