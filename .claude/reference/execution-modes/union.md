# Execution modes — cohort union

Per-topic slice of the execution-modes reference. Index (Mode index + section map): `.claude/reference/execution-modes.md` — load it first, then only the slice(s) your engine work touches.

## Cohort union

**What it is.** `Request.Union []types.UnionMember{Cohort, Label}` (+ `UnionSourceField`, default `types.DefaultUnionSourceField` = `_source`) names several labelled cohorts read as ONE record stream in place of `Request.Cohort`. The same two slots sit on `ChainRequest` (stage 0's source), `FacetRequest`, `SampleRequest` and `LookupRequest`; grep `UnionSourceField` in `types/` for the current list. Absent `union` is byte-identical everywhere; `format_version` stays `"1.1"`; no `.pulse` byte changes.

**One rule, two sides.** Shape rules live in `internal/descriptor/union.go` (`descx.UnionShapeRefusal`): `cohort` + `union` is `PULSE_UNION_COHORT_CONFLICT`; a member without a cohort `PULSE_UNION_MEMBER_INVALID`; an empty / duplicate label `PULSE_UNION_LABEL_INVALID`; `union_source_field` without a union, or naming a member field, `PULSE_UNION_SOURCE_FIELD_INVALID`; more flattened members than `encx.MaxArchiveShards` `PULSE_UNION_MEMBER_LIMIT`. The merge (`descx.MergeUnion`) is the shard-archive rule on the LOGICAL view — first member canonical, set dictionaries widen, nullability and the default zone field never — and a mismatch is `PULSE_UNION_SCHEMA_MISMATCH` carrying the archive refusal's details plus `archive_code`, `member`, `label`. The source field is appended LAST: non-nullable `categorical_u8` (`u16` past 256 members), dictionary = labels in declared order (grouped output still follows the grouper's sort, never member order). Predict and runtime (`internal/service/union_cohort.go`) both call these, so predict ≡ runtime (`TestUnion_RefusalsMatchRuntime`).

**Runtime.** A union is an IN-MEMORY shard archive: its shards are the members' shards flattened in declared order, its schema the merged one. Each member shard's rows are rewritten into the merged layout as read (`internal/encoding/union_rows.go`: dictionary remap, set widening, the source value), so every archive arm reads a union unchanged (`TestUnion_ReadsAsShardArchiveOracle`). Nothing is written; no index or sidecar is read. `Metadata.cohort_file` is `""`; merge description-divergence warnings ride `Response.warnings`. `shard_count` counts flattened member shards.

**Surfaces.** Every single-cohort surface takes a union except a join's right side: Process, streaming, Compose slots, ProcessChain stage 0, FacetSchema, SampleWithRequest, Materialize (the source field is written as a column), Lookup (always SCANS members in union order — no index read or write), a join's LEFT side, and every predict variant. Model-file provenance over a union records `union: [{label, cohort, fingerprint}]`.

**Refused.** A union on a join's RIGHT side (`PULSE_UNION_JOIN_RIGHT`) and a nested union (`PULSE_UNION_NESTED`) are refused at strict DECODE on every root, located like an unknown key. On an instance hiding `capability:union` a misplaced `union` is a plain `PULSE_REQUEST_UNKNOWN_FIELD`. A raw single-source predict handed a union is `PULSE_UNION_UNSUPPORTED`. Profile capture from a union request is out of scope.

**Feature gate.** `capability:union` (`internal/descriptor/features.go`) gates both keys on every union root (`unionSlotGates`, `request_slots.go`): hidden, a set key — `union_source_field` alone included — is `PULSE_REQUEST_UNKNOWN_FIELD` at decode and in `SlotRefusal`, and the payload schema, MCP bound schemas, manifest `union` block and `PULSE_UNION_*` codes drop with it.

**Observability.** `OperationInfo.Cohort` of a union-reading operation is the members' cohort paths in declared order joined by `+` — never a label (`TestObserveUnionCohort`).

**Adding a surface.** Open members through `Service.openRequestCohort` / `openUnion` (never re-merge), judge predict through `unionHead` / `requestSchemaFor`, add the root's union slots to `gatedSlots` + `setSlots`, add the surface to `unionSurfaces` (`capabilities_union.go`) and to `docs/src/contract/payload-schema.md` (Union slots), and pin predict ≡ runtime.
