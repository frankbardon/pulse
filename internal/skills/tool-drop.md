---
name: tool-drop
kind: tool
description: Remove a managed-import handle — delete its .pulse file and sidecar.
type: reference
applies_to: mcp
---

## When to use

CALL TO DELETE A MANAGED IMPORT YOU NO LONGER NEED.
Cleaning up after a one-off analysis, or freeing a name for re-import. Expired handles stay on disk; `pulse_drop` is the explicit eviction.

## Input

- `handle` (string, required): managed handle name (from `pulse_imports_list` or the name you passed to `pulse_import`).

## Output

`{handle, dropped: true}` (no envelope). An unknown handle is a tool error `PULSE_IMPORT_SOURCE_MISSING` (`details.handle`).

## Gotchas

- Pulse-format passthroughs (`managed=false`) are NOT in the pool — `pulse_drop` returns `PULSE_IMPORT_SOURCE_MISSING` for them. They were never managed.
- Drop deletes both `imports/<handle>.pulse` under the data directory AND its TTL sidecar `imports/<handle>.pulse.meta.json`. There is no undo.
- A request still naming the handle's path fails to open the cohort; re-import to restore it.

## See

- `tool-import` — create / refresh a handle.
- `tool-imports-list` — enumerate before drop.
