```yaml
name: op-synth-uniform-date
description: Uniform calendar-date samples in [start, end] inclusive; days-since-epoch internally.
kind: operator
category: SYNTH
operator: uniform_date
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, time-series]
```

Synth distributions emit per-row values; no `Response.Components`.

## Use when

Draws calendar dates between a start and an end date, every day equally likely, both ends included.

Questions it answers:

- How do I spread synthetic orders across 2024?
- How do I generate sign-up dates within the last quarter?

Use something else:

- `uniform` when the field is a plain number, not a date.
- `constant` when every row shares one date.

## Params

- `start` / `end` — string, both required, ISO-8601 `YYYY-MM-DD`. `end` must not precede `start`; equal is legal. Each parses via `time.Parse("2006-01-02", …)` into days-since-1970-01-01.

## Inputs

Field `type:` — `date` (32-bit days-since-epoch).

## Output

Per-row `float64` days-since-epoch; the writer stores it as the signed 32-bit `date` word. Draws `off = rng.Int64N(span + 1)` over `span = endDays - startDays`, so both endpoints are reachable.

## Gotchas

- Both bounds inclusive — `uniform_date(2024-01-01, 2024-12-31)` can emit either.
- Unparseable dates → `SERVICE_VALIDATION` ("invalid start date" / "invalid end date"). `end == start` is legal (every draw on that day); only `end < start` → `SERVICE_VALIDATION` ("end must not be before start").
- Epoch is 1970-01-01; pre-epoch dates are legal — `date` stores signed `int32` days, so they are negative.
- For sub-day granularity model the timestamp as `u64` seconds-since-epoch via `uniform`.

## See

- `pulse_examples_search tags=[synth]`
- Skills: [`synthetic-data`](synthetic-data.md), [`op-synth-uniform`](op-synth-uniform.md), [`op-synth-monotonic-from`](op-synth-monotonic-from.md)
