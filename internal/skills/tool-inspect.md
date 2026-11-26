---
name: tool-inspect
kind: tool
description: Read header + schema of a .pulse file without touching record data.
type: reference
applies_to: inspect, mcp
---

## When to use

CALL TO SEE A COHORT'S FIELDS AND TYPES WITHOUT RUNNING A REQUEST.
Listing fields, debugging dictionaries, checking a type. On MCP a successful inspect rebinds session tool variants to the field names (best-effort).

## Input

- `path` (string, required): the `.pulse` file or shard archive; `archive.pulse#shard.pulse` opens one shard.
- `joins` (optional): `[{right, as}]` join targets; the rebound tools' field enums add each target's joined names (`as` + field) and `joins[].on[].right_field` binds to its fields. Output unchanged.

## Output

No envelope: `field_count`, `fields` (`name`, `type`, `nullable` (always present), `byte_offset`, `bit_position`, `description` + `description_source`, `categorical`, `dictionary {total_entries, truncated, values}`, decimal `precision` / `scale`), `record_count`, `shards`<!-- feature: capability:weighting -->, `suggested_weight {field, source: "spss_sidecar", kind: "probability"}` from the SPSS sidecar's weight variable<!-- /feature -->. Dictionaries stop at 100 values (`truncated: true`); `pulse cohort inspect --full-dict` lists all. A `0x02` cohort adds `layout` (physical/logical stride), `groups` (`fields`, `entry_count`, resident `dictionary_bytes`, `ratio`, `byte_delta`, `verdict`) and a per-field `group` marker (`kind: constant` = elided); a grouped archive reports canonical groups over its whole `record_count`. A `vec_f32` / `vec_f64` field adds `vector {dim, kind, scale_min, scale_max, labels}` (absent keys = undeclared; `labels` truncated like a dictionary). A declared default zone field adds `zone_field` (`time-zones`). Coded `warnings` appear only when the read has one.

## Gotchas

- Header + schema + sidecar metadata, never a record; group `ratio` = `record_count ÷ entry_count` (import report's; `verdict` at floor 2).
<!-- feature: capability:weighting -->
- `suggested_weight` is NEVER applied — name it as a request `weight`; SPSS `WEIGHT BY` replicates cases (`kind: "frequency"`). Absent, stale or bad sidecar ⇒ none.
<!-- /feature -->
- `record_count` is DERIVED (payload bytes / stride); a torn tail reports the FLOOR, signalled only by an `ENCODING_INVALID` warning (`record_stride`, `trailing_bytes`). Library callers use `Pulse.InspectEnvelope` — `Pulse.Inspect` drops it.
- A missing file is `SERVICE_RESOURCE`; a bad header (magic, format version) a plain-text error naming `ENCODING_INVALID`.

## See

- `cohort-schema-design` — field-type matrix.
- `tool-predict` — schema validation companion.
- `tool-import` — the other leaf rebinding session tools.
