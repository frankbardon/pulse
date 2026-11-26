---
name: op-synth-regex
description: String samples generated from a Perl/RE2 regex pattern, with open-ended repeats capped at max_repeat.
kind: operator
category: SYNTH
operator: regex
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, data-quality]
---

Synth distributions emit per-row values; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `pattern` | string | (required) | Non-empty Perl/RE2 source. |
| `max_repeat` | int | `8` | Floored at 1. Bounds unbounded `*` / `+` / `{m,}`. |

## Inputs

Field `type:` — `categorical_u8`/`u16`/`u32`. Distinct generated strings become dictionary entries.

## Output

Per-row string matching the pattern: literals copy, character classes draw uniformly, alternations pick a branch, groups and `?` / `*` / `+` / `{m,n}` repeat under `max_repeat`, anchors emit nothing. Stored as the field's dictionary code.

<!-- generated: contract -->

## Gotchas

- No backreferences (`\1`, `(?P=name)`); invalid pattern → `SERVICE_VALIDATION` with the syntax error.
- `.` emits printable ASCII (33–126).
- Unbounded `*` / `+` clamps to `max_repeat`; bounded `{m,n}` to `min(n, min + max_repeat)`.
- An empty character class produces no character. More distinct strings than the width holds (256 for `categorical_u8`) → `PULSE_IMPORT_CATEGORICAL_OVERFLOW` at write.

## See

- Skills: `synthetic-data`, `op-synth-weighted-categorical`, `op-synth-constant`
