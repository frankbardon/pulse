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
