---
name: tool-imports-list
kind: tool
description: Enumerate every managed-import handle with sidecar metadata.
type: reference
applies_to: mcp
---

## When to use

CALL TO SEE WHICH MANAGED IMPORTS EXIST AND WHICH HAVE EXPIRED.
Render a UI of imports, find expired entries before `pulse_drop`, or check a handle name is free before `pulse_import`. Sweep is NOT invoked — expired entries are flagged via `expired` so callers decide to drop or extend.

## Input

No arguments.

## Output

`{imports: [...]}` (the bare object, no envelope), in handle order, each `{handle, path, sidecar, expires_in, pinned, expired}`: `sidecar` carries `format`, `source_path`, `source_format`, `imported_at`, `expires_at`, `ttl_seconds`, `rows_imported`; `expires_in` is a duration or `"expired"`. `imports: []` when no managed handles (never null).

## Gotchas

- Pulse-format passthroughs are NOT in the list — they are not managed.
- `expired=true` entries are still openable (Pulse never garbage-collects the underlying `.pulse`); use `pulse_drop` to evict.
- `pinned=true` ⇒ TTL is `pin` (`ttl_seconds` ≤ 0); `expires_at` and `expires_in` are absent.

## See

- `tool-import` — create a managed handle.
- `tool-drop` — explicit eviction.
