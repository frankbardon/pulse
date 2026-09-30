---
name: tool-inspect
kind: tool
description: Read header + schema of a .pulse file without touching record data.
type: reference
applies_to: inspect, mcp
---

## When to use

Schema-only output without running a request: listing fields, debugging dictionaries, confirming a field's type before authoring a request. Side effect on MCP: a successful inspect rebinds session-scoped tool variants whose JSON Schemas embed enum constraints on field-name parameters (best-effort; falls back to global tools).

## Input

- `path` (string, required): filesystem path to the `.pulse` file. Shard archive paths supported; anchor syntax `archive.pulse#shard.pulse` opens a named shard.

## Output

`descriptor.Envelope` wrapping `InspectResult`: fields (name, type, description, categorical dictionary), `record_count`, `shards`. Dictionaries truncated to 100 unless `FullDict: true`. A `0x02` cohort adds `layout` (physical/logical stride), `groups` (`fields`, `entry_count`, resident `dictionary_bytes`, `ratio`, `byte_delta`, `verdict`) and a per-field `group` marker (`kind: constant` = elided).

MCP (`pulse_inspect`) returns those keys at the top level plus an additive `warnings` array of coded `{code, message, details}` entries — omitted on a clean read.

## Gotchas

- Header-only: reads `encoding.ReadHeader` + `encoding.ReadSchema` only. No record decode; group `ratio` = `record_count ÷ entry_count`, matching the import report (`verdict` at floor 2).
- `record_count` is DERIVED (payload bytes / record stride) and a payload that is not a whole multiple of the stride reports the FLOOR. The only signal is an `ENCODING_INVALID` warning carrying `record_stride` + `trailing_bytes`; the count itself looks ordinary. Library callers must use `Pulse.InspectEnvelope` — `Pulse.Inspect` drops the warning.
- Pass `FullDict: true` (CLI `--full-dict`) for the full label list.
- Unknown magic / format-version mismatch → `ENCODING_INVALID`.

## See

- `cohort-schema-design` — `.pulse` byte layout and field-type matrix.
- `tool-predict` — schema validation companion (no record decode).
- `tool-import` — the other leaf that rebinds session tools.
