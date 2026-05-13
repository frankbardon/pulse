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
| `--sample-limit`         |      | int    | 0 (unlimited) | Cap rows ingested for the profile (0 disables) |
| `--json`                 |      | bool   | false      | Also print the envelope to stdout |

## What the profile captures

| Field type | What is recorded |
|---|---|
| Numeric (`u*`, `f*`, `decimal128`) | Count, min, max, mean, stddev; percentiles if `--include-stats` |
| Categorical | Top-K most-frequent values + their frequencies; "other" tail weight |
| `date` | Min, max, count |
| `nullable_*` | Null count alongside the above |
| Geo (`point_f64`, `h3_cell`) | Bounding stats; H3 resolution metadata |

## What the profile does NOT capture

- Individual rows.
- The full categorical dictionary beyond `--top-k`.
- Correlations unless `--include-correlations` is set.

This is by design — profiles are intended to be safe to share with
parties who shouldn't see the underlying data.

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
