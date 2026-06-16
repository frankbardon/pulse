---
name: op-synth-uniform-date
description: Uniform calendar-date samples in [start, end] inclusive; days-since-epoch internally.
kind: operator
category: SYNTH
operator: uniform_date
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, time-series]
---

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `start` | string | required | ISO-8601 calendar date `YYYY-MM-DD`. |
| `end` | string | required | ISO-8601 calendar date `YYYY-MM-DD`. Must be after `start`. |

Internally each bound parses via `time.Parse("2006-01-02", ...)` and converts to days-since-1970-01-01.

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | `date` (32-bit days-since-epoch). |

## Output

Per-row `float64` carrying days-since-epoch. The writer casts to `uint32` for the on-wire `date` cell.

Sampler draws `off = rng.Int64N(span + 1)` over `span = endDays - startDays`, so both endpoints are reachable.

## Gotchas

- Both bounds inclusive — `uniform_date(2024-01-01, 2024-12-31)` can emit either bound.
- Unparseable dates → `SERVICE_VALIDATION` ("invalid start date" / "invalid end date").
- `end <= start` → `SERVICE_VALIDATION` ("end must be after start").
- Epoch is 1970-01-01; pre-epoch dates encode as negative days-since-epoch which the `date` field type cannot store — keep `start >= 1970-01-01`.
- For sub-day granularity model the timestamp as `u64` seconds-since-epoch via `uniform`.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-uniform`, `op-synth-monotonic-from`
