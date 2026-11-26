```yaml
name: op-synth-regex
description: String samples generated from a Perl/RE2 regex pattern, with open-ended repeats capped at max_repeat.
kind: operator
category: SYNTH
operator: regex
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, data-quality]
```

Synth distributions emit per-row values; no `Response.Components`.

## Use when

Generates text matching a pattern, such as order codes like AB-1234, for realistic-looking IDs and labels.

Questions it answers:

- How do I generate postcodes that look real?
- How do I fill a SKU column with codes like SKU-00042?

Use something else:

- `weighted_categorical` when the values come from a short known list.
- `monotonic_from` when IDs must be unique.
- `constant` when every row should carry the same text.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `pattern` | string | (required) | Non-empty Perl/RE2 source. |
| `max_repeat` | int | `8` | Floored at 1. Bounds unbounded `*` / `+` / `{m,}`. |

## Inputs

Field `type:` — `categorical_u8`/`u16`/`u32`. Distinct generated strings become dictionary entries.

## Output

Per-row string matching the pattern: literals copy, character classes draw uniformly, alternations pick a branch, groups and `?` / `*` / `+` / `{m,n}` repeat under `max_repeat`, anchors emit nothing. Stored as the field's dictionary code.

## Contract

- Applies to: `categorical` fields.

## Gotchas

- No backreferences (`\1`, `(?P=name)`); invalid pattern → `SERVICE_VALIDATION` with the syntax error.
- `.` emits printable ASCII (33–126).
- Unbounded `*` / `+` clamps to `max_repeat`; bounded `{m,n}` to `min(n, min + max_repeat)`.
- An empty character class produces no character. More distinct strings than the width holds (256 for `categorical_u8`) → `PULSE_IMPORT_CATEGORICAL_OVERFLOW` at write.

## See

- Skills: [`synthetic-data`](synthetic-data.md), [`op-synth-weighted-categorical`](op-synth-weighted-categorical.md), [`op-synth-constant`](op-synth-constant.md)
