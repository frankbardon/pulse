```yaml
name: synth-rule-validation
description: Synth rule validation and diagnostics — the eight eager `PULSE_SYNTH_RULE_*` refusals, the non-nullable-target refusal, and the never-fired warning with its three cause arms.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, rules, PULSE_SYNTH_RULE, validation, never fired]
requires: [capability:synth]
```

# Synth rule validation

### Eager validation

**At spec parse, never at row time.** A rule naming a mistyped field, or carrying an uncompilable expression, is invisible at run time: the run succeeds and the gate is simply absent. Eight codes, each naming the rule INDEX (`rule_index` — a rule has no name) plus slot and field where one exists:

| Code | Fires on |
|---|---|
| `PULSE_SYNTH_RULE_EMPTY` | no action slot (`_evidence` alone is still empty) |
| `_FIELD_UNKNOWN` | a slot's name, AND an undeclared name inside `isnull("…")` — that expression COMPILES, so it is an unknown FIELD, not an `_EXPR_INVALID` |
| `_FIELD_NOT_NULLABLE` | `set_null` only |
| `_EXPR_INVALID` | uncompilable, or a `when` not returning bool |
| `_VALUE_INVALID` | the coercion matrix, either timing |
| `_CONFLICT` | one rule naming a field in two slots that DISAGREE (`set_null` + `null_together` do not disagree and are ordered instead) |
| `_BLOCK_INVALID` | `null_together` naming fewer than two DISTINCT fields |
| `_OWNERSHIP_INVALID` | `owns_nulls` with an empty `set_null` |

Faults report in a fixed slot order with SORTED map keys, so a rule with two faults refuses the same one every run. A `when` failing at RUN time stays `PROCESSING_RUNTIME` — the remaining causes are generic expr faults (a dynamic `isnull` argument, `int()` of a non-numeric string), not "a malformed rule", and the coded error already carries rule index, slot and expression. `pulse errors lookup CODE` is authoritative.

### Nulling a non-nullable field

A field not declared `"nullable": true` gets the type's zero written as an ordinary value with NO null bit — the rule fires and the file cannot show it. **`set_null` over such a field is REFUSED** (`PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE`); the fix is on the FIELD, and on a profile-derived run a field is nullable only if the source had nulls (`--emit-spec`, add the flag, `from-schema`). A `null_together` MEMBER only warns, because that slot copies the gate's decision — which may be "present" on every row — rather than claiming a null. Do NOT "finish the job" by promoting it: the gated-block idiom names a never-null field first on purpose.

## A rule that never fires

Eager validation cannot catch a `when` that is simply NEVER TRUE: the rule validates, compiles, applies to nothing, and the cohort generates cleanly with the structural fact still missing — the same silent-inertness class as a scored pair generation never applied. So firings are COUNTED during generation and a rule applying to ZERO rows warns, naming its index, its `when` verbatim and the row count the zero is out of. Kind ATTENTION (`rule never fired`), so it leads the stderr summary.

**The count is over rows that reached the FILE.** A constraint-rejected row was re-drawn and left no trace, so it is not a firing — which keeps the figure divisible by the row count and makes a rule firing only on rejected rows report the truth (zero). Firing on every row, and on some, are both silent. Many dead rules list at most 20 plus a counted `+N further rule(s) never fired`. Counting consumes no RNG and allocates nothing per row.

**The message names the cause that applies to THAT rule**, in three arms:

1. **CONSTRAINT** — the rule DID select rows and a constraint rejected every one. Recorded during the run (attempt remembered, acceptance counted) rather than inferred, so the line blames the constraint and says nothing about rounding.
2. **PRE-ROUNDING** — the predicate reads a field that rounds on write AND draws continuous values; remedy `round(field)` in an earlier rule, never `int()`.
3. **NEUTRAL** — neither. Names each field the predicate reads with its type and distribution, and RULES PRE-ROUNDING OUT for a field whose row value already IS the stored value.

Arm 3 is why the split exists: a field that rounds on write still draws exact values under `discrete`, `bernoulli`, `uniform_date`, `poisson` or `monotonic_from`, and a small integer reconstructs as `discrete` automatically — so on a survey cohort the pre-rounding advice is wrong for most columns, and sending an author to normalise an already-exact gate is worse than saying nothing.

## See

- [`synth-structural-rules`](synth-structural-rules.md) · [`synth-rule-expressions`](synth-rule-expressions.md) (pre-rounding) · [`synth-rule-nulls`](synth-rule-nulls.md).
- [`tool-errors-lookup`](tool-errors-lookup.md) — `pulse errors lookup CODE` is authoritative.
