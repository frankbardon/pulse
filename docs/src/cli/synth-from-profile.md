# pulse synth from-profile

**Audience:** CLI users topping up a real cohort with synthetic rows
whose per-field distributions match it — a tagged, source-preserving
augmentation, not a full anonymised replica.

`pulse synth from-profile` reads a profile JSON captured by
[`pulse profile create`](profile-create.md), reads the `--source`
cohort the profile was captured from, and writes a **new** `.pulse`
file combining every real row from `--source` with `--rows` newly
generated rows. The profile itself retains **no individual rows**
from the source; only summary statistics drive generation.

> **LLM agents using MCP:** see the `synthetic-data` skill.

## Synopsis

```
pulse synth from-profile --profile FILE --source FILE --output FILE --rows N
                         [--seed N] [--fidelity-report FILE] [--json]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--profile` | `-p` | string | (required) | Profile JSON path |
| `--source`  |      | string | (required) | Source `.pulse` cohort the profile was captured from — copied into the output, never opened for write |
| `--output`  | `-o` | string | (required) | Output `.pulse` file path — must be a distinct path from `--source` |
| `--rows`    |      | int    | (required) | Number of **new** rows to generate |
| `--seed`    |      | int    | 0          | Deterministic RNG seed |
| `--fidelity-report` | | string | (none) | Write a JSON fidelity report to this path after generation completes |
| `--json`    |      | bool   | false      | Emit the standard envelope |

`--rows` is required (unlike `from-schema`, which can pull it from
the spec) because the profile does not carry a generation count of
its own. **`--rows` is always an explicit count of new rows to
generate, never a "top up to N total" target** — `--rows 500` against
a 200-row `--source` cohort produces a 700-row output (200 real +
500 synthetic), not a 500-row output.

## Provenance tagging and the new-file-only contract

Every output cohort gains one appended field, `_synthetic`
(`packed_bool`): `false` on every row copied from `--source`, `true`
on every newly generated row. This is the only schema difference
between `--source` and the output — every other field is carried
through unchanged, in order.

`--output` must always be a path distinct from `--source`.
`synth from-profile` never opens `--source` for write and never
mutates it in place — an `--output` that resolves to the same file
as `--source` is refused outright, and the source cohort's bytes and
modification time are unchanged by a run.

## Determinism

Same `(profile, source, seed, rows)` tuple → byte-identical output.
Seeds are `int64`; default `0`.

## Fidelity report

`--fidelity-report <path.json>` writes a JSON document after
generation completes, comparing the newly generated rows against the
source cohort's rows inside the one tagged output. It reuses the
existing statistical-test operators rather than new comparison math:
every **numeric** field is compared with `TEST_KS` (`split_by:
_synthetic`), and every **categorical** field with `TEST_CHISQ`
(contingency against `_synthetic`). Fields of any other on-wire type
(`date`, `datetime`, `packed_bool`, `u4`, `set_*`) are out of scope for
this section today.

Shape:

```json
{
  "source_rows": 200,
  "synthetic_rows": 500,
  "fields": [
    {"field": "score", "test": "TEST_KS", "result": {"type": "TEST_KS", "statistic": 0.04, "p_value": 0.87, "alpha": 0.05, "reject_null": false}},
    {"field": "country", "test": "TEST_CHISQ", "result": {"type": "TEST_CHISQ", "statistic": 1.2, "df": 2, "p_value": 0.55, "alpha": 0.05, "reject_null": false}}
  ],
  "pairwise": [
    {"a": "income", "b": "spend", "source_rho": 0.80, "synthetic_rho": 0.79, "delta": 0.01, "n": 20000}
  ],
  "warnings": [
    "thin numeric pair income x spend: only 12 supporting observation(s) (below 30) — reconstructed correlation may be unstable"
  ]
}
```

A field whose test could not run (e.g. a categorical column with only
one distinct value in one partition) gets `"error"` instead of
`"result"` — that single field's failure never blocks the report for
every other field, nor the generated cohort itself.

`pairwise` reports, for every numeric-numeric correlation the profile
captured (`--conditional`'s `Conditional.NumericPairs`, or the plain
`--include-correlations` stats as a fallback — whichever
`SpecFromProfile` used to populate `spec.Correlations`), the delta
between that pair's captured/target correlation (`source_rho` — the
same figure the copula reconstruction targeted) and its REALIZED
Pearson correlation over just the newly generated rows
(`synthetic_rho`); `delta` is `abs(source_rho - synthetic_rho)`. A pair
whose synthetic partition cannot produce a defined correlation (fewer
than two co-occurring non-null observations) gets `"error"` instead of
`synthetic_rho`/`delta`, following the same non-fatal-per-entry
contract as `fields`. `pairwise` is entirely absent — never an empty
array — when the spec carried no correlations at all (e.g. neither
`--conditional` nor `--include-correlations` was passed at profile
time).

`warnings` mirrors any thin-pair warnings the source profile carried
(`Profile.Warnings` — see [`pulse profile
create`](profile-create.md)'s `--conditional` section) verbatim,
letting a reader see "how well did it match" (`pairwise`) and "which
parts were built on thin data" (`warnings`) in the one document.
Absent, not an empty array, when the profile carried no warnings.

Omitting `--fidelity-report` writes no report at all and changes no
other behavior. The flag only has an effect on the tagged top-up path
(`--source` set); it is a no-op on plain `synth from-schema` (see
[`pulse synth from-schema`](synth-from-schema.md)), which has no
`_synthetic` partition to compare against.

## Profile shape

The profile is a `synth.Profile` JSON object produced by
`pulse profile create`. It carries per-field type, descriptive
statistics, top-K categorical entries (default K = 32), optional
pairwise correlations (when `--include-correlations` was passed at
profile-creation time), an optional row-aligned `conditional` section
(when `--conditional` was passed — preferred over the plain pairwise
stats when both are present, since its `n` is the true co-occurrence
count rather than an approximation), and a row count.

See [`pulse profile create`](profile-create.md) for how to capture
one, and `synth/` for the underlying Go types.

## Output

### Text mode

```
Generated 1000 rows -> sales.synth.pulse (rejected 0)
```

### `--json`

Same envelope shape as
[`synth from-schema`](synth-from-schema.md#output).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Profile parse error, infeasible constraints, missing/colliding `--source`/`--output`, or output write failure |

See `pulse errors lookup PULSE_SYNTH_SOURCE_REQUIRED` /
`PULSE_SYNTH_OUTPUT_REQUIRED` / `PULSE_SYNTH_OUTPUT_COLLISION` /
`PULSE_SYNTH_ALREADY_TAGGED` / `PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH`
for the specific top-up refusal codes.

## Examples

```bash
# Capture once
pulse profile create --input sales.pulse --output sales.profile.json

# Top up with different seeds — sales.pulse is read but never written
pulse synth from-profile --profile sales.profile.json --source sales.pulse --output sales.s42.pulse --rows 10000 --seed 42
pulse synth from-profile --profile sales.profile.json --source sales.pulse --output sales.s43.pulse --rows 10000 --seed 43

# Also write a per-field marginal fidelity report
pulse synth from-profile --profile sales.profile.json --source sales.pulse --output sales.s42.pulse --rows 10000 --seed 42 --fidelity-report sales.s42.fidelity.json
```

## Limitations

- Categorical tails: anything past the captured top-K is replaced
  with a sentinel "other" bucket sized to its observed weight.
- Correlations: pairwise only, and only between numeric fields. The
  profile capture flag `--include-correlations` (or the more accurate
  `--conditional`) opts in; without either, fields are generated
  independently. Reconstruction uses a conditional-Gaussian
  construction (`synth/copula.go`) that exactly targets the captured
  Pearson `rho` for jointly-normal fields — see
  `skills/synthetic-data.md` for the technique and its trade-offs.
- Decimal and geo fields: regenerated within the same type family
  but with synthetic value distributions; downstream uses that
  depend on exact field values (e.g. joinable identifiers) need
  the schema-driven path instead.

## Related

- [`pulse profile create`](profile-create.md)
- [`pulse synth from-schema`](synth-from-schema.md)
- `skills/synthetic-data.md` — the spec / profile grammar
