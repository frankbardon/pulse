---
name: synth-structural-rules
description: Entry point for synth spec `rules[]` and `constraints[]` — the five rule slots, declaration order and last-write-wins, why the rule pass runs last, and which focused skill covers expressions, null handling, validation, claims and profile-derived rules.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, rules, constraints, set_null, set_expr, null_together, PULSE_SYNTH_RULE]
requires: [capability:synth]
---

# Synth structural rules

Two synth-spec slots act ON a drawn row rather than describing its variation: `constraints[]` rejects a row and makes the generator redraw; `rules[]` masks or overwrites named fields on the rows a predicate selects. Both compile against the SAME `expr-lang` row environment (`rowExprEnv`) — a second hand-rolled env is what produced issue #258.

Every measurement behind a figure here, and the closed design questions: `docs/src/cli/synth-calibration.md`.

| Question | Skill |
|---|---|
| `constraints[]`, the row expression environment, `set_expr` coercion, pre-rounding | `synth-rule-expressions` |
| `null_together` blocks and `owns_nulls` | `synth-rule-nulls` |
| Refusal codes, non-nullable targets, a rule that never fires | `synth-rule-validation` |
| Which rules retire a field's model / pairs / residual correlations | `synth-rule-claims` |
| `--rules` / `--emit-spec`, and detecting rules with `--suggest-rules` | `synth-rules-from-profile` |
| Detector-specific rules and the emitted candidate order | `synth-rule-detectors` |

## Structural rules

`rules[]` (additive, `omitempty`) state what no statistical summary can — and what therefore comes back from marginals as soft noise with a plausible rate and no gate: a question block not ASKED of a respondent who failed a screener, a flag that is a band of another column.

```json
"rules": [
  {"when": "aware == 0", "set_null": ["perception_1", "perception_2"], "set": {"segment": "unaware"}},
  {"set_expr": {"promoter": "nps >= 9"}},
  {"null_together": ["nps", "nps_reason"]}
]
```

Five slots plus one modifier: `when` optional predicate (**absent means EVERY row**, not "never"); `set` assigns LITERALS; `set_expr` expression RESULTS; `null_together` nulls a block as one decision; `owns_nulls` changes what a `set_null` MEANS rather than adding an action. `when` must return bool.

**`set` and `set_expr` are sibling keys, never one map with a `{"$expr": …}` marker** — one map leaves `{"set": {"region": "west"}}` undecidable between a literal and an identifier.

The pass consumes no RNG and allocates nothing per row, so a rules-free spec is byte-identical to pre-slot output. **The standalone rules-file format is this array itself** — an inline declaration and a `--rules` file are the same JSON.

### Order and placement

**Declaration order, sequentially, LAST WRITE WINS**, in ONE pass that is `drawRow`'s last act, after the model stage — and **the placement IS the semantics**: `if gate then null else inferred`. A gated field is still drawn through its own sampler, model and pairs; the rule masks or replaces it only where `when` selects, so non-gated rows keep the INFERRED value. NOT topologically sorted — declaration order is the one ordering an author can read off the document.

**Accepted consequence: a field a rule NULLS still contributed its DRAWN value to any model using it as a PREDICTOR on that row.** The two-phase alternative (evaluate gates, re-run dependent stages with gated fields withheld) is rejected; the single-phase reading is that the respondent's propensity existed, the question was not asked.

| Slot | Value | Null mask |
|---|---|---|
| `set_null` | LEFT as drawn (the shape `nullableSampler` produces; the wire gets the type's zero, never the masked value) | set |
| `set` / `set_expr` | written | CLEARED — a rule stating a field's value states the field HAS one |

A `set` literal is coerced to the row's Go shape once at compile time via `constantRowValue`, which turns a `set_*` target's `["tv","radio"]` into the row's `map[string]bool`.

**ACROSS rules an expression sees EARLIER writes, not LATER ones. WITHIN one rule order is UNOBSERVABLE:** every expression, `when` included, reads the row as it stood BEFORE the rule ran, and all its writes land after — so `{"set_expr": {"a": "b", "b": "a"}}` swaps, like SQL `UPDATE`. A sibling `set` literal is invisible to a sibling `set_expr`; self-reference (`{"nps": "nps + 1"}`) works and does NOT pre-claim the field. The alternative exposes an order nobody can SEE: a rule's slots are Go maps, JSON key order is gone after decode, and the surviving order is ALPHABETICAL, so renaming a column would silently change a number.

Rules reach GENERATED rows only — `synth from-profile --source` re-encodes the real partition straight through.

## See

- The focused skills in the table above.
- `synthetic-data` — modes, distributions, the determinism rule; `synth-models` — what a claiming rule retires from `--fit-models`.
- `tool-errors-lookup` — `PULSE_SYNTH_RULE_*` / `PULSE_SYNTH_CONSTRAINT_*` recovery; `pulse errors lookup CODE` is authoritative.
- `docs/src/cli/synth-from-schema.md` — CLI surface; the `rules` array is also the standalone rules-file format.
