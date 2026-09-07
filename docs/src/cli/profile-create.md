# pulse profile create

**Audience:** CLI users capturing a statistical profile of an
existing cohort — typically to feed into
[`pulse synth from-profile`](synth-from-profile.md).

`pulse profile create` reads a `.pulse` file and writes a JSON
profile: per-field type, descriptive statistics, top-K categorical
entries, optional pairwise correlations. **The profile retains no
individual rows from the source.**

> **LLM agents using MCP:** see the `pulse_profile` MCP tool.

## Synopsis

```
pulse profile create --input PATH --output PATH
                     [--top-k N] [--include-stats]
                     [--include-correlations] [--correlation-top-k N]
                     [--conditional]
                     [--sample-limit N] [--json]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--input`                | `-i` | string | (required) | Source `.pulse` cohort |
| `--output`               | `-o` | string | (required) | Output profile JSON path |
| `--top-k`                |      | int    | 32         | Top-K categorical entries to retain per field |
| `--include-stats`        |      | bool   | true       | Include percentile / std stats |
| `--include-correlations` |      | bool   | false      | Capture pairwise numeric correlations |
| `--correlation-top-k`    |      | int    | 16         | Cap on retained correlation pairs |
| `--conditional`          |      | bool   | false      | Capture row-aligned numeric-numeric pair structure (`conditional.numeric_pairs`) for exact correlation reconstruction |
| `--sample-limit`         |      | int    | 0 (unlimited) | Cap rows ingested for the profile (0 disables) |
| `--json`                 |      | bool   | false      | Also print the envelope to stdout |

## What the profile captures

| Field type | What is recorded |
|---|---|
| Numeric (`u*`, `f*`, `decimal128`) | Count, min, max, mean, stddev; percentiles if `--include-stats` |
| Categorical | Top-K most-frequent values + their frequencies; "other" tail weight |
| `date` | Min, max, count |
| `nullable_*` | Null count alongside the above |

## What the profile does NOT capture

- Individual rows.
- The full categorical dictionary beyond `--top-k`.
- Correlations unless `--include-correlations` or `--conditional` is set.

This is by design — profiles are intended to be safe to share with
parties who shouldn't see the underlying data.

## `--conditional`: row-aligned pair reconstruction

`--include-correlations` computes each numeric field's `Pairwise` entry
from independently-capped per-field reservoirs; once any field carries
nulls those reservoirs can drift out of row alignment, and the reported
observation count is only an approximation of true co-occurrence.
`--conditional` instead keeps a row-aligned joint snapshot (capped at
10,000 rows) and writes a `conditional.numeric_pairs` section: each
entry's `rho` and `n` are computed only from rows where BOTH fields were
simultaneously non-null. `synth.SpecFromProfile` prefers
`conditional.numeric_pairs` over `pairwise` when both are present, and
`synth/copula.go`'s conditional-Gaussian construction (the mechanism
that actually reconstructs the requested correlation, replacing the old
approximate ±5%·std blend) consumes either shape identically. A pair
whose `n` falls below **30** (`synth.MinPairObservations`) is still
shipped — never refused — but appends a warning to `warnings` naming the
pair. The section is entirely absent (not empty) when `--conditional`
is not passed, and profiles captured before this flag existed remain
valid `synth from-profile` input.

## Output

The profile JSON is always written to `--output`. With `--json`, the
envelope is also written to stdout (typically piped or `jq`-d).

Profile schema lives in `synth/profile.go` and is documented in
`skills/synthetic-data.md`.

### Text mode summary

```
Profiled 50000 rows from sales.pulse -> sales.profile.json
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Read error, unsupported field type (`PULSE_PROFILE_FIELD_UNSUPPORTED`), or write failure |

## Examples

### Minimal profile

```bash
pulse profile create --input sales.pulse --output sales.profile.json
```

### Rich profile with correlations

```bash
pulse profile create --input sales.pulse --output sales.profile.json \
    --include-stats --include-correlations --top-k 64 --correlation-top-k 32
```

### Sample-limited profile for a huge cohort

```bash
pulse profile create --input ops.pulse --output ops.profile.json --sample-limit 1000000
```

## Round-trip with synth

```bash
pulse profile create --input sales.pulse --output sales.profile.json
pulse synth from-profile --profile sales.profile.json --output sales.synth.pulse --rows 10000 --seed 1
pulse cohort inspect sales.synth.pulse
```

## Related

- [`pulse synth from-profile`](synth-from-profile.md) — the
  consumer of profile JSON
- [`pulse synth from-schema`](synth-from-schema.md) — the alternative
  spec-driven path
- `skills/synthetic-data.md` — full profile and spec grammar
- [Library: pulse.Profile](../library/overview.md)
