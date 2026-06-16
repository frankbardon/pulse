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

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `pattern` | string | required | Regex source (Perl/RE2 syntax via `regexp/syntax.Parse`). Non-empty. |
| `max_repeat` | int | `8` | Upper bound for unbounded `*` / `+` / `{m,}` repetitions. Floored at 1. |

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | `categorical_u8`/`u16`/`u32`. Distinct generated strings become dictionary entries. |

## Output

Per-row string emitted by walking the parsed AST: literals copy through, char classes draw uniformly, alternations pick a branch, fixed and bounded repeats expand under `max_repeat`. Encoded as a dictionary index in the host categorical field.

Supported AST ops: `OpLiteral`, `OpCharClass`, `OpAnyChar(NotNL)`, `OpCapture`, `OpConcat`, `OpAlternate`, `OpStar`, `OpPlus`, `OpQuest`, `OpRepeat`, plus anchors (emit nothing).

## Gotchas

- No backreferences — `\1` / `(?P=name)` rejected at parse.
- `OpAnyChar` emits a printable ASCII rune (33–126) to keep output stable across platforms.
- Unbounded `*` / `+` clamps to `max_repeat`; bounded `{m,n}` clamps to `min(n, min + max_repeat)`.
- Empty character class produces no character — keep classes non-empty.
- Dictionary cardinality at write time must fit the declared categorical width — wide patterns + tall cohorts can overflow `categorical_u8`.
- Invalid pattern (parse error) → `SERVICE_VALIDATION` with the underlying syntax error.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-weighted-categorical`, `op-synth-constant`
