```yaml
name: synth-rule-draws
description: Random draws inside synth rules — `uniform()` and `bernoulli(p)` on a seeded stream per call site, what a constraint rejection does to them, and `"output": false` helper fields that rules read but the file never carries; the outcome-drawn-from-a-model recipe.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, uniform, bernoulli, draw, helper, output, outcome]
requires: [capability:synth]
```

# Synth rule draws and helper fields

A rule can DRAW: `uniform()` and `bernoulli(p)` let an outcome be drawn against a probability the rules computed, instead of being a deterministic function of the row. A field declared `"output": false` is a HELPER: drawn, written and read like any other field, never written to the file.

## The draw functions

| call | value |
|---|---|
| `uniform()` | a number in `[0, 1)` |
| `bernoulli(p)` | `true` with probability `p` (a bool: `1 / 0` on a scalar target) |

- **Rules only.** A `when` and a `set_expr` may draw; a `constraints[]` entry may not — a constraint is an acceptance test, and a random one makes rejection itself random. To filter on a draw, store it in a helper (below) and constrain the helper.
- `uniform()` takes no argument (scale it: `lo + (hi - lo) * uniform()`); `bernoulli` takes one `p` in `[0, 1]`. A literal outside is refused at spec parse, a computed one (NaN included) on the row that produced it — clamp: `bernoulli(min(1, max(0, pd)))`. All `PULSE_SYNTH_RULE_DRAW_INVALID`.
- A field named `uniform` / `bernoulli` reads as a bare name; calling the function then is refused (the built-in rule in [`synth-rule-expressions`](synth-rule-expressions.md)).

## Determinism

- **A draw never touches the generation RNG.** Each call site — one `uniform()` / `bernoulli()` call in one rule slot — has its own stream, so a spec without draws is byte-identical to one from before they existed, and ADDING a draw moves no other field's value.
- A value is a pure function of `(seed, call site, accepted-row ordinal, attempt, call)`. The site is the rule's index, its slot (`when`, or `set_expr` and the target field) and the call's position in that expression — moving a rule to a new index changes its draws; editing another rule does not.
- **A constraint-rejected attempt's draws are discarded, never replayed.** The row is redrawn at the same ordinal with the next attempt number, so it reads FRESH values (replaying would reject the same draw forever) and the accepted row is a function of spec and seed alone.
- Two calls never share a value: `uniform() - uniform()` is not 0. To use ONE draw twice, store it.

## Helper fields: `"output": false`

```json
{"name": "true_pd", "type": "f64", "distribution": "constant", "params": {"value": 0}, "output": false}
```

- Drawn in its declaration position like any field, so hiding one moves no other column: the written columns are value-identical to the same spec with the helper written.
- Every rule and constraint reads it (`isnull` too), and a rule may write it. `lag` reads its final value.
- Absent from the written schema and records — so from `inspect`, a profile and the fidelity report (a model or pair over a helper is reported as not scored, not as an empty partition). At least one field must be written. `"output": true` and omitting the key are identical.

## Recipe: an outcome drawn from a model

A true PD is computed in a rule, the outcome drawn against it, and one shared draw reused by later rules — none of which reach the file:

```json
"fields": [
  {"name": "true_pd", "type": "f64", "distribution": "constant", "params": {"value": 0}, "output": false},
  {"name": "draw", "type": "f64", "distribution": "constant", "params": {"value": 0}, "output": false},
  {"name": "default_12m", "type": "packed_bool", "distribution": "bernoulli", "params": {"p": 0}}
],
"rules": [
  {"set_expr": {"true_pd": "1 / (1 + exp(-(-2.4 + 0.04 * (dti - 18))))"}},
  {"set_expr": {"draw": "uniform()"}},
  {"set_expr": {"default_12m": "bernoulli(true_pd)"}},
  {"when": "default_12m == 1", "set_expr": {"recovery": "round(1000 * (0.1 + 0.35 * draw)) / 100"}}
]
```

Order matters as for any rule ([`synth-structural-rules`](synth-structural-rules.md)): the PD rule precedes the draw that reads it. Over the cohort the default count tracks the sum of `true_pd`.

## See

- [`synth-rule-expressions`](synth-rule-expressions.md) — the row env, the coercion matrix; [`synth-structural-rules`](synth-structural-rules.md) — slots and order.
- [`synth-cross-row`](synth-cross-row.md) — `row()` / `lag()` and accepted-row order; [`synth-determinism`](synth-determinism.md) — the seed contract.
