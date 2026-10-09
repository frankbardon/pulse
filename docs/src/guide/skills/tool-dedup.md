```yaml
name: tool-dedup
kind: tool
description: Deduplicate an existing cohort's repeated parent blocks into parent groups (format 0x02), or suggest groups read-only.
type: reference
applies_to: mcp
```

## When to use

CALL WHEN A COHORT REPEATS PARENT ATTRIBUTES ON EVERY CHILD ROW.
A denormalized join: `pulse_dedup` stores each parent tuple once plus a 4-byte row index: smaller file, byte-identical answers. The existing-cohort twin of `pulse_import` `groups`.

## Input

- `path` (string, required): the single-file `.pulse` cohort.
- `suggest_groups` (bool): detect candidates over the cohort's records. Alone the call is READ-ONLY.
- `groups` (array): `[{key: [...], members: [...]}]`, the `pulse_import` shape. An unknown entry key → `PULSE_GROUP_DECLARATION_INVALID`.
- `out` (string): write to this NEW path (must not exist). Omit to rewrite IN PLACE.

## Output

`rewritten`, `in_place`, `records`, `*_before/after` (format version, bytes, stride), `groups` (per-group `verdict`, `ratio`, `dictionary_bytes`, `byte_delta`), `group_warnings`, `group_candidates` (with the `suggested` set) and `invalidated_sidecars` (in place only, each with its rebuild command).

## Gotchas

- **Suggest, then declare.** Call with `suggest_groups` only, then pass the `suggested` candidates' `key`/`members` as `groups`. Never guess.
- **Prefer `out`** unless the user asked for in place. In place is atomic: a failure leaves the cohort byte-identical.
- A member that varies within its key fails `PULSE_GROUP_MEMBER_NOT_CONSTANT` and nothing is written.
- Weak groups warn: `PULSE_GROUP_TOO_NARROW` (dropped), `PULSE_DEDUP_LOW_RATIO` (written; floor 2). Floor, strict and constant elision are `pulse dedup` flags only.
- A grouped cohort is regrouped from scratch.
- Sidecars are reported, never rebuilt; run each `rebuild` before the next point lookup.
- Shard archives → `SERVICE_VALIDATION`: dedup each shard, then `pulse shard create` (it union-merges grouped shards). Output is format 0x02 (older binaries cannot read it).

## See

- [`tool-import`](tool-import.md) — declaring groups at import time.
- [`cohort-schema-design`](cohort-schema-design.md) (Parent groups) — when grouping pays off.
