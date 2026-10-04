---
name: tool-inspect
kind: tool
description: Read header + schema of a .pulse file without touching record data.
type: reference
applies_to: inspect, mcp
---

## When to use

Schema-only output without running a request: listing fields, debugging dictionaries, confirming a field's type. On MCP a successful inspect rebinds session-scoped tool variants whose schemas enum the field names (best-effort).

## Input

- `path` (string, required): the `.pulse` file or shard archive; `archive.pulse#shard.pulse` opens one shard.

## Output

`descriptor.Envelope` wrapping `InspectResult`: fields (name, type, description, categorical dictionary), `record_count`, `shards`, `suggested_weight {field, source: "spss_sidecar", kind: "probability"}` from the SPSS sidecar's weighting variable. Dictionaries truncated to 100 unless `FullDict: true`. A `0x02` cohort adds `layout` (physical/logical stride), `groups` (`fields`, `entry_count`, resident `dictionary_bytes`, `ratio`, `byte_delta`, `verdict`) and a per-field `group` marker (`kind: constant` = elided); a grouped archive reports canonical groups over its whole `record_count`.

MCP (`pulse_inspect`) returns those keys at top level plus a `warnings` array of coded `{code, message, details}` entries, omitted on a clean read.

## Gotchas

- Header + schema + sidecar metadata, never a record; group `ratio` = `record_count ÷ entry_count` (import report's; `verdict` at floor 2).
- `suggested_weight` is NEVER applied — name it as a request `weight`; SPSS `WEIGHT BY` replicates cases (`kind: "frequency"`). Absent, stale or bad sidecar ⇒ none.
- `record_count` is DERIVED (payload bytes / stride); a torn tail reports the FLOOR, signalled only by an `ENCODING_INVALID` warning (`record_stride`, `trailing_bytes`). Library callers use `Pulse.InspectEnvelope` — `Pulse.Inspect` drops it.
- `FullDict: true` (`--full-dict`): full label list.
- Unknown magic / format-version mismatch → `ENCODING_INVALID`.

## See

- `cohort-schema-design` — `.pulse` byte layout and field-type matrix.
- `tool-predict` — schema validation companion.
- `tool-import` — the other leaf rebinding session tools.
