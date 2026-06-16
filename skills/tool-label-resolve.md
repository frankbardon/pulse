---
name: tool-label-resolve
kind: tool
description: Reverse-resolve a human-readable name (typo-tolerant) to raw categorical key(s).
type: reference
applies_to: mcp
---

## When to use

User names a brand / category / region / etc. by display label — call this BEFORE authoring a `FILTER_INCLUDE` or `GROUP_CATEGORY` to get the raw key the filter / grouper expects. Labels are output-only; filter / group / sort keys operate on the raw categorical value.

## Input

- `table` (string, required): label table name (from `pulse_label_tables`, e.g. `brand`).
- `query` (string, optional): name to search; typo-tolerant. Empty returns the first rows in browse mode (score 0).
- `limit` (number, optional): maximum matches. Default `10`.

## Output

`descriptor.Envelope` wrapping `[]LabelMatch`: `key` (raw categorical value), `value` (display label), `score` (confidence in `[0, 1]`). `1.0` = exact (or exact-key); `~0.9+` = prefix or near-typo; lower = fuzzy (edit-distance + trigram). Matching normalizes case and punctuation.

## Gotchas

- Decision rule: top score `>=0.9` AND clearly ahead of next → use it. Low or several close scores → present the top names to the user and ask which they meant rather than guessing.
- Non-enumerable tables (forward-only) → error. Check `enumerable` via `pulse_label_tables` first.
- Empty `query` is valid — browse mode returns the first rows up to `limit` with score `0`.

## See

- `tool-label-tables` — table discovery companion.
- `label-display` — label binding contract.
