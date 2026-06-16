---
name: tool-inspect
kind: tool
description: Read header + schema of a .pulse file without touching record data.
type: reference
applies_to: inspect, mcp
---

## When to use

Schema-only output without running a request: listing fields for a UI, debugging dictionary contents, confirming a field's type before authoring a request slot. Side effect on MCP: a successful inspect rebinds session-scoped tool variants whose JSON Schemas embed enum constraints on field-name parameters (best-effort; degrades to global tools on failure).

## Input

- `path` (string, required): filesystem path to the `.pulse` file. Shard archive paths supported; anchor syntax `archive.pulse#shard.pulse` opens a named shard.

## Output

`descriptor.Envelope` wrapping `InspectResult`: field list (name, type, nullable, dictionary contents for categorical fields), record count, format-version metadata, schema hash. Dictionaries truncated to `DefaultDictionaryLimit = 100` unless `FullDict: true`.

## Gotchas

- Header-only: reads `encoding.ReadHeader` + `encoding.ReadSchema` only. No record decode.
- Dictionary truncation is silent at the manifest level — call with `FullDict: true` (CLI) when you need the full label list.
- Unknown magic / format-version mismatch → `ENCODING_INVALID`.

## See

- `cohort-schema-design` — `.pulse` byte layout and field-type matrix.
- `tool-predict` — schema validation companion (no record decode).
- `mcp-integration` (when present) — session-bound tool rebinding semantics.
