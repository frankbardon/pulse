# pulse synth from-schema

**Audience:** CLI users generating a synthetic `.pulse` cohort from a
declarative spec — for testing, demos, and bootstrapping fixtures.

`pulse synth from-schema` reads a JSON synth spec (field-by-field
distributions, row count, optional pairwise correlations) and writes
a deterministic `.pulse` file. Same `(spec, seed)` pair produces a
byte-identical output.

> **LLM agents using MCP:** see the `pulse_synth` MCP tool and the
> `synthetic-data` skill — it covers spec authoring, the 12 supported
> distributions, and constraint patterns.

## Synopsis

```
pulse synth from-schema --spec FILE --output FILE
                        [--rows N] [--seed N] [--json]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--spec`   | `-s` | string | (required) | Synth spec JSON path |
| `--output` | `-o` | string | (required) | Output `.pulse` file path |
| `--rows`   |      | int    | from spec | Override `row_count` in the spec |
| `--seed`   |      | int    | 0         | Deterministic RNG seed |
| `--json`   |      | bool   | false     | Emit the standard envelope |

## Spec shape (sketch)

```json
{
  "row_count": 10000,
  "fields": [
    {"name": "id",      "type": "u64",            "distribution": "monotonic_from", "from": 1},
    {"name": "region",  "type": "categorical_u8", "distribution": "weighted_categorical",
                         "weights": {"east": 0.4, "west": 0.4, "north": 0.1, "south": 0.1}},
    {"name": "revenue", "type": "f64",            "distribution": "lognormal", "mu": 4.0, "sigma": 0.8},
    {"name": "sold_on", "type": "date",           "distribution": "uniform_date",
                         "from": "2024-01-01", "to": "2024-12-31"}
  ]
}
```

Full spec grammar (constraints, correlations, regex, …) lives in
`skills/synthetic-data.md` and `synth/`.

## Categorical / numeric / set joint structure (advanced)

Beyond independent per-field distributions and `correlations`, a
hand-authored spec can also declare **conditional joint structure**
between fields directly — the same `Spec` fields `synth.SpecFromProfile`
populates from a captured profile's `conditional.*` sections (see
[`pulse profile create`](profile-create.md)), reachable in from-schema
JSON without ever running `profile create --conditional`:

| Spec field | JSON key | Reconstructs |
|---|---|---|
| `CategoricalPairs` | `categorical_pairs` | field B resampled from a captured contingency table conditioned on field A's drawn value |
| `CategoricalNumericPairs` | `categorical_numeric_pairs` | numeric field B resampled from `Normal(mean, std)` per observed category of categorical field A |
| `SetCategoricalPairs` | `set_categorical_pairs` | a `set_*` field's option (bit) resampled from `Bernoulli(P(selected \| categorical A's value))` |
| `SetNumericPairs` | `set_numeric_pairs` | numeric field B resampled from `Normal(mean, std)` conditioned on a `set_*` field's option state |
| `SetSetPairs` | `set_set_pairs` | one `set_*` field's option resampled conditioned on a DIFFERENT `set_*` field's option state |

Each entry runs as a generation-time **resample** step, applied after
every field's own independent draw and before the numeric-numeric
correlator — the field still needs its own `distribution` declared (a
`categorical_pairs` entry's `b` still needs `weighted_categorical`, a
`categorical_numeric_pairs` entry's `b` still needs `normal`); the
pair only overrides the drawn VALUE, never the field's declared shape.
Omitting all five keys reproduces plain independent-marginal generation
exactly, unchanged from a spec that never mentions them.

Two further keys are hand-authorable on the same terms. `models` (the
`Spec.Models` slot `profile create --fit-models` populates) declares one
additive linear predictor per numeric field — a field it names is drawn
from that model rather than from its own marginal, and any pair or
`correlations` entry naming the same field is dropped with a warning.
`residual_correlations` declares correlations between two **modelled**
fields' residuals, `{a, b, correlation}` exactly like `correlations` but
read on the residual scale; both endpoints must carry a `models` entry
or the spec is refused. Omitting either key is the zero state — every
residual is drawn independently, exactly as before they existed. The
construction, the latent-scale caveat for non-`normal` targets and the
determinism rules are in `skills/synthetic-data.md`.

Minimal example — a categorical-categorical pair (`region` → `tier`)
alongside a categorical-numeric pair (`region` → `revenue`):

```json
{
  "row_count": 10000,
  "fields": [
    {"name": "region",  "type": "categorical_u8", "distribution": "weighted_categorical",
                         "weights": {"east": 0.5, "west": 0.5}},
    {"name": "tier",    "type": "categorical_u8", "distribution": "weighted_categorical",
                         "weights": {"gold": 0.5, "silver": 0.5}},
    {"name": "revenue", "type": "f64", "distribution": "normal", "mean": 500, "std": 100}
  ],
  "categorical_pairs": [
    {"a": "region", "b": "tier", "cells": [
      {"a_value": "east", "b_value": "gold",   "count": 80},
      {"a_value": "east", "b_value": "silver",  "count": 20},
      {"a_value": "west", "b_value": "gold",   "count": 30},
      {"a_value": "west", "b_value": "silver",  "count": 70}
    ]}
  ],
  "categorical_numeric_pairs": [
    {"a": "region", "b": "revenue", "categories": [
      {"category": "east", "mean": 650, "std": 90},
      {"category": "west", "mean": 350, "std": 80}
    ]}
  ]
}
```

Full cell/category shapes: `synth/spec.go`
(`CategoricalPairCellSpec`, `CategoricalNumericCategorySpec`, and the
`Set*PairSpec` family) and `skills/synthetic-data.md` ("Categorical
joint structure (generation)"). A field named by two or more of these
relationships — or by both a relationship and that field's own
`--fit-shape`-captured distribution when the spec came from
`SpecFromProfile` — is a **conflict**, not an error: one relationship
wins by a fixed priority order and every other claimant on that field
is dropped with a warning on `Result.Warnings`
(`data.warnings` under `--json`); see [`pulse profile
create`](profile-create.md) ("`--fit-shape`" section) for the full
priority order.

## Structural rules

Beyond distributions, correlations and joint structure — all of which
describe *variation* — a spec can declare **structural rules**: facts
about which fields a row may carry a value for at all, and what that
value is. A skip-logic gate ("the perception block is not asked of a
respondent who has never heard of the brand") is not a rate, and a
profile reconstructs it as soft noise with a plausible null rate and no
gate.

```json
{
  "row_count": 40000,
  "fields": [ "..." ],
  "rules": [
    {"when": "aware == 0",
     "set_null": ["perception_1", "perception_2"],
     "set": {"segment": "unaware"}},
    {"set_expr": {"promoter": "nps >= 9"}},
    {"null_together": ["nps", "nps_reason"]}
  ]
}
```

| Slot | Type | Meaning |
|---|---|---|
| `when`          | expr string | the rule applies to rows satisfying it; **absent means every row** |
| `set_null`      | array       | null these fields |
| `set`           | object      | assign these **literal** values |
| `set_expr`      | object      | assign the **result** of these expressions |
| `null_together` | array       | null this block as one decision (needs ≥ 2 distinct fields) |

`set` and `set_expr` are separate keys on purpose: with one map,
`{"set": {"region": "west"}}` would be undecidable between a categorical
literal and a bare identifier.

Rules apply in **declaration order, sequentially, last write wins**, in
one pass at the very end of the row — after every distribution, pair,
correlation and model has settled. So a gated field is still *generated*
normally and the rule then masks or replaces it on the rows `when`
selects; rows the rule does not gate keep the value generation inferred.
An expression reads the row as it stood when **its rule** started, and a
later rule may still change it. Rules are deliberately **not** reordered
by dependency: declaration order is the one ordering you can read off the
file.

Running the pass last is what makes `if gate then null else inferred`
true; running it earlier would let a model overwrite its own rule-gated
target. The accepted consequence is that a field a rule **nulls** still
contributed its drawn value to any model that used it as a *predictor*
on that row — the propensity existed, the question was not asked.

`set_null` sets the null flag and leaves the drawn value in the row; the
file gets the type's zero, so a masked value never reaches the wire.
`set` and `set_expr` write the value and clear the null flag, because a
rule stating a field's value is stating the field has one. So `set_null`
then `set` yields the value, and `set` then `set_null` yields the null.

The pass consumes no randomness: a spec carrying rules draws exactly the
same per-row sequence as the same spec without them, and a spec
declaring no rules generates byte-identical output to one written before
the slot existed.

`null_together` is **validated but not yet applied**. A rule whose only
slot is that one is skipped entirely — its `when` is not evaluated
either — so an unimplemented slot cannot fail a working run.

### `set_expr` and the coercion matrix

`set_expr` assigns what an expression computes from the row, which
collapses a derived-field relationship from several `when`-gated rules
into one unconditional rule:

```json
{"set_expr": {"promoter":  "nps >= 9",
              "passive":   "nps >= 7 && nps < 9",
              "detractor": "nps < 7"}}
```

It writes the value and clears the null flag exactly as `set` does.

An expression returns a Go value and the target field is one of the
seventeen declarable types, so the interesting surface is **which
results can land on which targets**. Getting a cell of that wrong is a
silent wrong value — a `true` reaching a `u8` as a formatted string
would encode as `0` — so the whole matrix is written down:

| expression result | scalar target | `categorical_*` | `set_*` |
|---|---|---|---|
| `true` / `false`                    | **1 / 0**                   | refused | refused |
| a number                            | the number, **range-checked** | refused | refused |
| a string                            | refused (see below)         | must be a **declared** value | refused |
| a selection (another `set_*` field, a list of option names, `split()`) | refused | refused | the selection |

*Scalar* means `u4` `u8` `u16` `u32` `u64` `f32` `f64` `date`
`packed_bool` `decimal128`. The range is the same one a `set` literal
obeys — `u4` is 0–15, `packed_bool` is 0–1 — because both go through one
implementation; `NaN` and infinities are refused. *Declared* means an
entry in a `weighted_categorical`'s `params.values`, or in a `set_*`'s
`params.options`; a `regex` or `constant` categorical has no declarable
domain, so any string is accepted there.

**A `decimal128` takes an exact string only from `set`, never from
`set_expr`.** A literal like `"1.25"` is parsed exactly, with no float
round trip. An expression cannot offer that — `expr` sees the field as
the `float64` the row holds, so anything computed already lost the
digits — and a string left in the row for a field the expression
environment types as a number would break the *next* rule that reads it,
on gated rows only. Write the literal with `set`.

**Two timings, one error.** A result type that could never land is
refused when the spec is parsed, before a single row is generated:
`{"set_expr": {"region": "nps * 2"}}` is a number aimed at a
categorical, and `expr` knows that from the types. A fault only a
*value* can settle — a number outside the target's range, a computed
category outside the declared domain — is refused at row time, because
that is the first moment it exists. Both are
`PULSE_SYNTH_RULE_VALUE_INVALID` and both name the rule index, the slot,
the field, the expression and the value.

**Inside one rule, order does not exist.** Every expression in a rule —
its `when` included — reads the row as it was *before* the rule ran, and
all of that rule's writes land afterwards. `{"set_expr": {"a": "b", "b":
"a"}}` swaps the two, the way a SQL `UPDATE` does. A `set` literal in
the same rule is **not** visible to a sibling `set_expr`. Reading the
target's own drawn value (`{"set_expr": {"nps": "nps + 1"}}`) works.
The alternative would have been alphabetical key order, which is not
something you can read off a JSON document and which would make renaming
a column change a number. *Across* rules nothing changed: an expression
sees what an earlier rule wrote and not what a later one will.

Expressions are the same `expr-lang` environment `constraints[]` uses —
every scalar including a boolean is a number (`flag == 1`, never bare
`flag`), a categorical is a string, a `set_*` is a map
(`flags["opt"]`), and `isnull(field)` tests absence. `when` must return
a bool.

The same array is also the standalone **rules file** format that
[`synth from-profile --rules`](synth-from-profile.md) loads, so an
inline declaration and a file are the same JSON.

Every malformed rule is refused **when the spec is parsed**, not at row
400,000: a rule naming a mistyped field, or carrying an expression that
does not compile, would otherwise generate a full cohort with the gate
silently missing. Each refusal names the rule's index (`rule_index`) —
rules have no names of their own — plus the offending slot and field.

### Two things that are silent if you get them wrong

**`set_null` needs a nullable field.** The per-record null bitmap only
carries a bit for a field declared `"nullable": true`. A `set_null` over
any other field writes the type's zero as an ordinary value with no null
flag — indistinguishable from a real `0` on a `u4` or a `packed_bool`.
The rule fires and the file cannot show it, so the run emits a warning
naming the rule index and the field. Declare the field nullable.

**`when` and `set_expr` see the pre-rounding value.** A numeric field is
drawn as a float and rounded on the way to the file, so `familiarity ==
1` tests the *drawn* value, not the `1` you read back: for a clamped
`normal` it fires on the clamp's point mass, not on the whole
wire-value-1 bucket. Use comparisons (`nps >= 9`) for numerics. A
`packed_bool` is exact — its row value is exactly `1.0` or `0.0`, so
`aware == 0` selects precisely the rows the file shows as `0`.

It bites `set_expr` harder than `when`, because the result still looks
right. The three-band NPS rule above, written straight off `nps`, sets
exactly one flag on every scored row — it simply disagrees with the
score printed beside it whenever the float and its rounding fall on
opposite sides of a band edge. Measured on a real 122-field survey
profile at 20,000 rows: 476 of 3,486 scored rows, 13.7%. Normalise
first, in an **earlier** rule, because snapshot semantics means the same
rule will not do:

```json
"rules": [
  {"when": "!isnull(nps)", "set_expr": {"nps": "int(nps)"}},
  {"when": "!isnull(nps)", "set_expr": {"promoter":  "nps >= 9",
                                        "passive":   "nps >= 7 && nps < 9",
                                        "detractor": "nps < 7"}}
]
```

The `when` matters too: a `set_expr` clears the target's null flag, so an
ungated rule would un-null every row the score was missing from and
classify a respondent who was never asked.

Rules apply to **generated rows only**. `synth from-profile --source`
copies the real cohort through unchanged and tags it `_synthetic=false`;
no rule ever rewrites one of those rows.

## Supported distributions

`bernoulli`, `constant`, `exponential`, `lognormal`, `monotonic_from`,
`normal`, `pareto`, `poisson`, `regex`, `uniform`, `uniform_date`,
`weighted_categorical`.

The full catalog (with parameters) is in `skills/synthetic-data.md`
and `pulse --json | jq '.data.distributions'`.

## Determinism

Same `(spec, seed)` → byte-identical output. The seed is a `int64`;
default `0`. Use a fixed seed for fixtures and a random seed for
load-testing variation.

## Output

### Text mode

```
Generated 10000 rows -> sales.pulse (rejected 0)
```

`rejected` counts rows that failed user-defined constraints
(`PULSE_SYNTH_CONSTRAINT_INFEASIBLE` when the rejection rate is too
high to make progress).

Generation warnings — conflict arbitration, correlation matrices
completed by assumption or ridge-regularized, models that could not be
compiled — are summarised on **stderr** in the same grouped, counted,
capped shape [`profile create`](profile-create.md#warning-summary) uses.
Nothing is printed when the run raised none, and nothing is printed on
the `--json` path, where `data.warnings` carries them in full.

### `--json`

```json
{
  "format_version": "1.1",
  "data": {
    "output_path": "sales.pulse",
    "rows_generated": 10000,
    "rows_rejected": 0,
    "seed": 0
  },
  "errors": [],
  "warnings": []
}
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Spec parse error, unknown distribution, infeasible constraints, or output write failure |

## Common error codes

| Code | Cause |
|---|---|
| `PULSE_SYNTH_DISTRIBUTION_UNKNOWN`  | Spec references a distribution name not in the catalog |
| `PULSE_SYNTH_CONSTRAINT_INFEASIBLE` | Constraints reject too high a fraction of generated rows |
| `PULSE_SYNTH_RULE_EMPTY`            | A rule declares no action slot (`set` / `set_expr` / `set_null` / `null_together`) |
| `PULSE_SYNTH_RULE_FIELD_UNKNOWN`    | A rule names a field the spec does not declare |
| `PULSE_SYNTH_RULE_EXPR_INVALID`     | A rule's `when` or a `set_expr` value does not compile (`when` must return a bool) |
| `PULSE_SYNTH_RULE_VALUE_INVALID`    | A `set` literal is the wrong shape for its target field, outside the type's range, or outside the declared `values` / `options` domain |
| `PULSE_SYNTH_RULE_CONFLICT`         | One rule names the same field in two slots that disagree about it |
| `PULSE_SYNTH_RULE_BLOCK_INVALID`    | A `null_together` block names fewer than two distinct fields |

## Examples

```bash
# Build sales.pulse from a spec
pulse synth from-schema --spec sales.spec.json --output sales.pulse --seed 42

# Override row count without editing the spec
pulse synth from-schema --spec sales.spec.json --output sales.pulse --rows 1000

# Programmatic envelope
pulse synth from-schema --spec sales.spec.json --output sales.pulse --json
```

## Related

- [`pulse synth from-profile`](synth-from-profile.md) — generate from
  a captured profile of an existing cohort
- [`pulse profile create`](profile-create.md) — capture the profile
- `skills/synthetic-data.md` — full spec grammar and distribution table
- [Library: pulse.Synth](../library/overview.md)
