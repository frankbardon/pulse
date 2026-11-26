---
name: tool-label-tables
kind: tool
description: List registered label tables — ID→display-name dictionaries for categorical fields.
type: reference
applies_to: mcp
---

## When to use

CALL TO FIND WHICH CATEGORICAL FIELDS CAN BE RESOLVED BY DISPLAY NAME.
Run it before `pulse_label_resolve`. Output surfaces already render these labels automatically — this tool and `pulse_label_resolve` are for the INPUT direction: turning a user-supplied name into the raw categorical key a filter / grouper needs.

## Input

No arguments.

## Output

`{tables: [...]}` (no envelope), each `{name, row_count, enumerable}`; `enumerable` (reverse-searchable) is true for every table with static rows, false for a function-backed one. `tables: []` when none registered (never null). Tables are loaded from `$PULSE_LABEL_TABLES_DIR/*.json` at `pulse.New` time, or supplied programmatically via `Options.Extensions.LabelTables`.

## Gotchas

- `enumerable=false` tables are forward-only (key→label); `pulse_label_resolve` refuses them with `PULSE_LABEL_TABLE_NOT_ENUMERABLE`.
- Labels are OUTPUT-only — filter / group / sort keys still operate on the raw categorical value. The reverse resolution is for translating user-supplied names into keys.
- A directory table is named by its JSON file stem (`brand.json` → `"brand"`); the file is a `{value: label}` object or a `{description, rows}` wrapper.

## See

- `tool-label-resolve` — reverse-resolve a name to keys.
- `label-display` — label binding contract end-to-end.
