---
name: tool-label-tables
kind: tool
description: List registered label tables — ID→display-name dictionaries for categorical fields.
type: reference
applies_to: mcp
---

## When to use

Discover which categorical dimensions can be reverse-resolved by name (e.g. brand, category, region) before calling `pulse_label_resolve`. Output surfaces already render these labels automatically — this tool and `pulse_label_resolve` are for the INPUT direction: turning a user-supplied name into the raw categorical key a filter / grouper needs.

## Input

No arguments.

## Output

`descriptor.Envelope` wrapping `[]LabelTableInfo`: `name`, `row_count`, `enumerable` (bool — whether reverse-searchable). Empty array when no tables registered (never null). Tables are loaded from `$PULSE_LABEL_TABLES_DIR/*.json` at `pulse.New` time, or supplied programmatically via `Options.LabelTables`.

## Gotchas

- `enumerable=false` tables are forward-only (key→label); `pulse_label_resolve` rejects them with a clear error.
- Labels are OUTPUT-only — filter / group / sort keys still operate on the raw categorical value. The reverse resolution is for translating user-supplied names into keys.
- Table names are the JSON filename stem (e.g. `brand.json` → `"brand"`).

## See

- `tool-label-resolve` — reverse-resolve a name to keys.
- `label-display` — label binding contract end-to-end.
