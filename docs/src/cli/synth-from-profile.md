# pulse synth from-profile

**Audience:** CLI users generating a synthetic `.pulse` cohort whose
distributions match a real cohort — typically to share a sanitised
replica without exposing the underlying rows.

`pulse synth from-profile` reads a profile JSON captured by
[`pulse profile create`](profile-create.md) and writes a synthetic
`.pulse` file whose per-field distributions and (optional) pairwise
correlations follow the profile. The profile retains **no individual
rows** from the source; only summary statistics.

> **LLM agents using MCP:** see the `pulse_synth_from_profile` MCP
> tool and the `synthetic-data` skill.

## Synopsis

```
pulse synth from-profile --profile FILE --output FILE --rows N
                         [--seed N] [--json]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--profile` | `-p` | string | (required) | Profile JSON path |
| `--output`  | `-o` | string | (required) | Output `.pulse` file path |
| `--rows`    |      | int    | (required) | Rows to generate |
| `--seed`    |      | int    | 0          | Deterministic RNG seed |
| `--json`    |      | bool   | false      | Emit the standard envelope |

`--rows` is required (unlike `from-schema`, which can pull it from
the spec) because the profile does not carry a generation count of
its own.

## Determinism

Same `(profile, seed, rows)` triple → byte-identical output. Seeds
are `int64`; default `0`.

## Profile shape

The profile is a `synth.Profile` JSON object produced by
`pulse profile create`. It carries per-field type, descriptive
statistics, top-K categorical entries (default K = 32), optional
pairwise correlations (when `--include-correlations` was passed at
profile-creation time), and a row count.

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
| 1 | Profile parse error, infeasible constraints, or output write failure |

## Examples

```bash
# Capture once
pulse profile create --input sales.pulse --output sales.profile.json

# Re-generate any number of times with different seeds
pulse synth from-profile --profile sales.profile.json --output sales.s42.pulse --rows 10000 --seed 42
pulse synth from-profile --profile sales.profile.json --output sales.s43.pulse --rows 10000 --seed 43
```

## Limitations

- Categorical tails: anything past the captured top-K is replaced
  with a sentinel "other" bucket sized to its observed weight.
- Correlations: pairwise only, and only between numeric fields. The
  profile capture flag `--include-correlations` opts in; without it,
  fields are generated independently.
- Decimal and geo fields: regenerated within the same type family
  but with synthetic value distributions; downstream uses that
  depend on exact field values (e.g. joinable identifiers) need
  the schema-driven path instead.

## Related

- [`pulse profile create`](profile-create.md)
- [`pulse synth from-schema`](synth-from-schema.md)
- `skills/synthetic-data.md` — the spec / profile grammar
