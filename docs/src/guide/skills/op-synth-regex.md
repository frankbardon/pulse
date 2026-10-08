```yaml
name: op-synth-regex
description: String samples generated from a Perl/RE2 regex pattern; walks regexp/syntax AST with bounded repetition.
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

- `pattern` — string, required, non-empty. Perl/RE2 source via `regexp/syntax.Parse`.
- `max_repeat` — int, default `8`, floored at 1. Bounds unbounded `*` / `+` / `{m,}`.

## Inputs

Field `type:` — `categorical_u8`/`u16`/`u32`. Distinct generated strings become dictionary entries.

## Output

Per-row string from the parsed AST: literals copy, char classes draw uniformly, alternations pick a branch, repeats expand under `max_repeat`. Stored as a dictionary index. Ops: `OpLiteral`, `OpCharClass`, `OpAnyChar(NotNL)`, `OpCapture`, `OpConcat`, `OpAlternate`, `OpStar`, `OpPlus`, `OpQuest`, `OpRepeat`, anchors (emit nothing).

## Gotchas

- No backreferences (`\1`, `(?P=name)`); invalid pattern → `SERVICE_VALIDATION` with the syntax error.
- `OpAnyChar` emits printable ASCII (33–126), for cross-platform stability.
- Unbounded `*` / `+` clamps to `max_repeat`; bounded `{m,n}` to `min(n, min + max_repeat)`.
- An empty character class produces no character. Write-time dictionary cardinality must fit the declared categorical width — wide patterns + tall cohorts overflow `categorical_u8`.

## See

- `pulse_examples_search tags=[synth]`
- Skills: [`synthetic-data`](synthetic-data.md), [`op-synth-weighted-categorical`](op-synth-weighted-categorical.md), [`op-synth-constant`](op-synth-constant.md)
