---
name: tool-drop
kind: tool
description: Remove a managed-import handle — delete its .pulse file and sidecar.
type: reference
applies_to: mcp
---

## When to use

Manually evict a managed-import handle from the pool — e.g. cleaning up after a one-off analysis, or freeing a name for re-import. Sidecar `expired=true` entries can stay until swept; `pulse_drop` is the explicit eviction.

## Input

- `handle` (string, required): managed handle name (from `pulse_imports_list` or the name you passed to `pulse_import`).

## Output

`descriptor.Envelope` wrapping a small drop record (`handle`, `dropped: true`). Unknown handle → `PULSE_IMPORT_SOURCE_MISSING`.

## Gotchas

- Pulse-format passthroughs (`managed=false`) are NOT in the pool — `pulse_drop` returns `PULSE_IMPORT_SOURCE_MISSING` for them. They were never managed.
- Drop deletes both the `.pulse` file under `$PULSE_DATA_DIR/imports/` AND the TTL sidecar. There is no undo.
- After drop, any cached session binding pointing at the handle path is stale; the next inspect/import rebinds.

## See

- `tool-import` — create / refresh a handle.
- `tool-imports-list` — enumerate before drop.
