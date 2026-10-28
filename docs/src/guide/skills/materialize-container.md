```yaml
name: materialize-container
description: Write a materialized cohort as a shard archive with output.shards {max_records}, store columns constant over the written rows once with output.elide_constants, and keep or declare parent groups with output.groups — row order, shard boundaries, the shard-count limit, group inheritance, the predict container plan, and the SPSS sidecar. Topical design; child of derived-cohorts.
type: guide
kind: design
applies_to: materialize, predict
covers: [shards, max_records, elide_constants, shard archive, PULSE_SHARD_COUNT_LIMIT, groups, PULSE_MATERIALIZE_GROUP_NOT_INHERITED]
requires: [capability:materialize, capability:materialize_output]
```

# Materialized cohort containers

The `output` block's container keys shape the FILE a materialize run writes, not its columns. They combine with `columns` / `exclude`, or stand alone — an `output` holding only them keeps every column, so a request with no derived column is a pure copy into the new container.

```json
{"output": {"shards": {"max_records": 500000}, "elide_constants": true}}
```

## Shard archive: `shards`

`shards: {max_records}` writes a shard archive instead of a single file. The rows, in output order (source order, or `sort` order), are cut into consecutive shards of `max_records` rows — `part-00001.pulse`, `part-00002.pulse`, … — the last one partial; an output with no row is one empty shard. Every shard carries the same schema and dictionaries, and the archive is published in one atomic step: whole, or nothing at `out`.

- Reading it back (`pulse.Open`, any request) gives the single-file output's rows in the same order, cell for cell.
- The result's `shards` names the entries in archive order.
- `max_records` ≤ 0 → `PULSE_MATERIALIZE_OUTPUT_INVALID` (`details.reason: "shards_max_records"`) before anything is read.
- An archive holds at most 65,535 shards: the row that would open one more fails the run with `PULSE_SHARD_COUNT_LIMIT` (`details.row`, `max_records`, `limit`), nothing written. Raise `max_records`.
- An anchored `archive.pulse#name.pulse` target stays refused (`SERVICE_VALIDATION`): a materialize run always writes a NEW cohort. Append to an archive with [`cohort-sharding`](cohort-sharding.md) tools instead.

## Constant elision: `elide_constants`

`elide_constants: true` stores every written column holding one value on EVERY written row once, instead of on each row — decided by a full pass over the rows, never a sample. The result's `elided_constants` names them. Decoding is unchanged: every request, filter and export sees the same values. A file that elides something is format `0x02`; when nothing qualifies (or the saving would not pay for itself) it is written exactly as without the key. With `shards`, the decision is made once over the whole archive, so a column constant in one shard only is kept.

A filter makes columns constant: materializing one region writes that region's code on every row, and elision stores it once.

## Parent groups: `groups`

A grouped source (format `0x02`) keeps its parent groups through an `output` block: with `groups` absent, each source group whose members are ALL written unchanged — renamed is fine — is declared again under the written names (same key). A member dropped, or re-coded by `type`, `recode` or `bucket`, leaves its group out with `PULSE_MATERIALIZE_GROUP_NOT_INHERITED` (`details.member`, `change`); its members become row fields. Without an `output` block nothing is inherited: the output stays ungrouped, as it always was.

`groups: [{key, members}]` declares exactly these groups over WRITTEN names (the import `groups` shape), replacing inheritance; `groups: []` writes none. A name the output does not write is `PULSE_GROUP_FIELD_UNKNOWN` (`details.path`), a field in two groups `PULSE_GROUP_FIELD_CONFLICT` — before anything is read. A keyed group whose member varies within its key fails the run (`PULSE_GROUP_MEMBER_NOT_CONSTANT`).

Every group passes import's gate: no wider than its 4-byte index → dropped (`PULSE_GROUP_TOO_NARROW`); under 2 rows per tuple → written with `PULSE_DEDUP_LOW_RATIO`. The result's `groups` carries each verdict. Strict (`--strict`, `MaterializeOptions.Strict`) makes a left-out or dropped group, and a low ratio, fatal. Decoding is unchanged; with `shards` the gate judges the whole archive.

## Predict

`predict: true` adds `container`: `kind` (`"file"` or `"shard_archive"`), `max_records`, `max_shards` — the most shards the run writes, `ceil(source records / max_records)`, reached when every record passes the filters — and `elide_constants`. Which columns elide only the rows settle, so none is named. A source holding more records than `max_records` × 65,535 adds a `PULSE_SHARD_COUNT_LIMIT` risk. `container.groups` lists each group: `origin` (`inherited` / `declared`), `verdict` (`planned`, `dropped_too_narrow`, `not_inherited` with `member` / `change`); a declared keyed group adds a `PULSE_GROUP_MEMBER_NOT_CONSTANT` risk.

## Gotchas

- The SPSS metadata sidecar is carried as for a single file: beside the archive, fingerprinted over its bytes (`pulse_inspect` still suggests its weight). An elided column keeps its variable.
- `pulse export` (every format) reads the archive exactly as its flattened single file; a `.sav` applies the carried sidecar. An anchored `archive.pulse#shard.pulse` exports that one shard under the canonical schema, with the archive's sidecar.
- `overwrite: true` replaces an existing cohort or archive whole; its other sidecars are reported stale.

## See

- [`derived-cohorts`](derived-cohorts.md) — the output slot and the two-step recipe.
- [`cohort-sharding`](cohort-sharding.md) — archives: cohesion, appends, compaction.
- [`cohort-parent-groups`](cohort-parent-groups.md) — parent and constant groups, the gate, dedup.
