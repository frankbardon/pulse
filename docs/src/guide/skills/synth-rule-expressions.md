```yaml
name: synth-rule-expressions
description: Synth `constraints[]` and the expression side of `rules[]` — the row environment's types, `isnull`, the `set_expr` coercion matrix, set-option addressing, and the pre-rounding gotcha with its `round` (not `int`) remedy.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, constraints, set_expr, isnull, coercion, pre-rounding]
requires: [capability:synth]
```

# Synth rule expressions

## Constraints

`constraints[]` reuse `expr-lang/expr`. Any constraint returning false rejects the row; the generator redraws until `row_count`. Reject cap 50% (`max_rejection_rate`); beyond it `PULSE_SYNTH_CONSTRAINT_INFEASIBLE`. Same-row reach only; no cross-row in v1.

**Env types are the ROW's types** (`sentinelFor`, derived from `fieldTypeFromName` so the two cannot drift): every scalar — `u4`/`u8`…`u64`, `f32`/`f64`, `date`, `decimal128` AND `packed_bool` — is `float64`; `categorical_*` is `string`; `set_*` is `map[string]bool` (`flags["opt"]`). So a boolean is tested `flag == 1`, never bare `flag` (a compile-time refusal, not a boolean). Typing `packed_bool` as Go `bool` was issue #258: compiled, then failed every row with `PROCESSING_RUNTIME: invalid operation: bool(float64)`.

**`isnull(field)`** reads the per-record null mask — the only way to test absence, since a nulled field still carries its drawn value. Takes the field NAME: `isnull(x)` (rewritten to a literal) and `isnull("x")` are equivalent, and an undeclared name in EITHER form is refused before a row exists — bare by expr's unknown-name check, string form by a parse pass (`isnullUnknownField`) applied to rules and `constraints[]` alike, never answered `false`. Only an argument that is a string at RUN time (`isnull(region + "!")`) reaches the loud run-time refusal.

### `set_expr` and the coercion matrix

`set_expr` assigns an expression's RESULT, writing the value and clearing the mask as `set` does — collapsing three `when`-gated rules into one. A wrong cell is a SILENT wrong value:

| result | scalar target | `categorical_*` | `set_*` |
|---|---|---|---|
| bool | **1 / 0** | refused | refused |
| number (`float64`/`int`) | the number, RANGE-CHECKED | refused | refused |
| string | refused (see decimal) | must be a DECLARED value | refused |
| selection (`map[string]bool`, option list, `split()`) | refused | refused | **the selection** |

Scalar = `u4` `u8` `u16` `u32` `u64` `f32` `f64` `date` `packed_bool` `decimal128`. Range is the bound a `set` literal obeys (`u4` 0–15) — ONE shared matrix; NaN/Inf refused. Declared = `weighted_categorical`'s `values` / a `set_*`'s `options`; a `regex` or `constant` categorical is unbounded, so any string lands.

**Set targets are addressed by option NAME, never by bit** — the row value is a `map[string]bool` keyed by the field's declared `options`, so every rule is width-agnostic and a 206-member `set_u256` needs no new syntax; an option the spec does not declare is `PULSE_SYNTH_RULE_VALUE_INVALID` at whatever rung. **What a rule cannot express is a partial edit or a cardinality constraint.** `set_expr` assigns a WHOLE selection (it replaces the mask), and the rule vocabulary has no bit-count, no "add this member" and no "these two are exclusive" primitive. To build on the current selection, read the field back off the row — the coercion accepts a `map[string]bool` taken straight from it — and return the complete result. A constraint you cannot write that way is UNSTATED; do not reach for a bit index instead.

**One asymmetry: a `decimal128` takes an exact string only from a `set` LITERAL.** `set_expr` refuses one — expr already reduced the value to `float64`, so exactness is gone, and a string left in a `float64`-typed row slot breaks the NEXT rule reading it, on gated rows only.

**Two timings, one code.** A fault the RETURN TYPE settles is refused at SPEC PARSE; one only a VALUE settles (range, category) at ROW time. Both `PULSE_SYNTH_RULE_VALUE_INVALID`, naming rule, slot, field, value. The static side owns no table — it probes the same matrix with the type's zero and refuses only on a TYPE fault, so the two timings cannot disagree.

### Pre-rounding

**Pre-rounding.** `when` / `set_expr` read the row's PRE-ROUNDING float, not the wire value, so `== k` fires only on draws landing exactly on `k`. Use comparisons (`>= 9`). It bites `set_expr` hardest because the result still LOOKS right — a three-band NPS classification off raw `nps` sets exactly one flag per row and merely disagrees with the score beside it. Normalise in an EARLIER rule (snapshot semantics means the same rule will not do), with `round`, NOT `int`: an integer field is stored as `floor(v+0.5)`, so `round(v)` reproduces the value the FILE will hold and moves the FLAGS onto the right side of the edge, while `int(v)` truncates and reaches the same band agreement by moving the SCORE down instead. **Guard with `when` whenever the target is nullable** — `set_expr` clears the null flag:

```json
{"when": "!isnull(nps)", "set_expr": {"nps": "round(nps)"}}
```

**LIVE only where row value and stored value differ** — `f32`/`f64`, an integer column over 64 observed levels (a clamped normal), a hand-authored continuous distribution on an integer field. A small integer reconstructs as `discrete` and a `packed_bool` as `bernoulli`, so for those the row value already IS the stored integer and the normalisation is a no-op.

## See

- [`synth-structural-rules`](synth-structural-rules.md) — slots and order; [`synth-rule-validation`](synth-rule-validation.md) — the refusal codes.
- [`synth-marginals`](synth-marginals.md) — why a small integer or `packed_bool` is already exact.
- [`expression-language`](expression-language.md) — the general expr environment the formula attribute and expression filter share (synth rules use their own row env).
