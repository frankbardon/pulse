```yaml
name: tool-label-resolve
kind: tool
description: Reverse-resolve a human-readable name (typo-tolerant) to raw categorical key(s).
type: reference
applies_to: mcp
```

## When to use

CALL WHEN THE USER NAMES A CATEGORY VALUE BY ITS DISPLAY LABEL.
Resolve it BEFORE authoring a value filter or category grouper to get the raw key they expect. Labels are output-only; filter / group / sort keys operate on the raw categorical value.

## Input

- `table` (string, required): label table name (from `pulse_label_tables`, e.g. `brand`).
- `query` (string, optional): name to search; typo-tolerant. Empty returns the first rows in browse mode (score 0).
- `limit` (integer, optional): maximum matches. Default `10`.

## Output

`{matches: [...]}` (no envelope), ranked by score, each `{key, value, score}`: `key` the raw categorical value, `value` the display label, `score` a confidence in `[0, 1]`. `1.0` = exact (or exact-key); `~0.9+` = prefix or near-typo; lower = fuzzy (edit-distance + trigram). Matching normalizes case and punctuation.

## Gotchas

- Decision rule: top score `>=0.9` AND clearly ahead of next → use it. Low or several close scores → present the top names to the user and ask which they meant rather than guessing.
- An unknown table is `PULSE_LABEL_TABLE_UNKNOWN`; a non-enumerable (function-backed, forward-only) one is `PULSE_LABEL_TABLE_NOT_ENUMERABLE`. Check `enumerable` via `pulse_label_tables` first.
- Scores are against the whole label: a typo of one word of a two-word label (`nothern` for `Northern Region`) scores low; send the full name.
- Empty `query` is valid — browse mode returns the first rows up to `limit` with score `0`.

## See

- [`tool-label-tables`](tool-label-tables.md) — table discovery companion.
- [`label-display`](label-display.md) — label binding contract.
