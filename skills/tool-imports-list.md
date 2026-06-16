---
name: tool-imports-list
kind: tool
description: Enumerate every managed-import handle with sidecar metadata.
type: reference
applies_to: mcp
---

## When to use

Surface the pool of active managed handles — render a UI of imports, identify expired entries before `pulse_drop`, or check whether a handle name is taken before `pulse_import`. Sweep is NOT invoked — expired entries are flagged via `Expired` so callers decide whether to drop or extend.

## Input

No arguments.

## Output

`descriptor.Envelope` wrapping `[]ImportInfo`: `handle`, `source_path`, `source_format`, `imported_at`, `expires_at`, `ttl`, `expired` (bool), `pinned` (bool). Empty array when no managed handles (never null).

## Gotchas

- Pulse-format passthroughs are NOT in the list — they are not managed.
- `expired=true` entries are still openable (Pulse never garbage-collects the underlying `.pulse`); use `pulse_drop` to evict.
- `pinned=true` ⇒ TTL is `pin`; `expires_at` is reported as zero-value.

## See

- `tool-import` — create a managed handle.
- `tool-drop` — explicit eviction.
