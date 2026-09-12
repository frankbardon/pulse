---
name: op-synth-regex
description: String samples generated from a Perl/RE2 regex pattern; walks regexp/syntax AST with bounded repetition.
kind: operator
category: SYNTH
operator: regex
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, data-quality]
---

Synth distributions emit per-row values; no `Response.Components`.

## Params

- `pattern` — string, required, non-empty. Perl/RE2 source via `regexp/syntax.Parse`.
- `max_repeat` — int, default `8`, floored at 1. Bound for unbounded `*` / `+` / `{m,}`.

## Inputs

Field `type:` — `categorical_u8`/`u16`/`u32`. Distinct generated strings become dictionary entries.

## Output

Per-row string from the parsed AST: literals copy through, char classes draw uniformly, alternations pick a branch, repeats expand under `max_repeat`. Stored as a dictionary index. Ops: `OpLiteral`, `OpCharClass`, `OpAnyChar(NotNL)`, `OpCapture`, `OpConcat`, `OpAlternate`, `OpStar`, `OpPlus`, `OpQuest`, `OpRepeat`, anchors (emit nothing).

## Gotchas

- No backreferences (`\1`, `(?P=name)`); invalid pattern → `SERVICE_VALIDATION` with the syntax error.
- `OpAnyChar` emits printable ASCII (33–126), for cross-platform stability.
- Unbounded `*` / `+` clamps to `max_repeat`; bounded `{m,n}` to `min(n, min + max_repeat)`.
- Empty character class produces no character. Write-time dictionary cardinality must fit the declared categorical width — wide patterns + tall cohorts overflow `categorical_u8`.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-weighted-categorical`, `op-synth-constant`
