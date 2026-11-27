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

Synth distributions emit per-row values; no `Response.Components`. No MCP tool runs synth: the CLI (`pulse synth from-schema`) or `Pulse.Synth` does ([`synthetic-data`](synthetic-data.md)).

## Use when

Draws calendar dates between a start and an end date, both ends included, every day equally likely unless month weights are set.

Questions it answers:

- How do I spread synthetic orders across 2024?
- How do I generate sign-up dates within the last quarter?

Use something else:

- `uniform` when the field is a plain number, not a date.
- `constant` when every row shares one date.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `start` | string | (required) | ISO-8601 `YYYY-MM-DD`. |
| `end` | string | (required) | ISO-8601 `YYYY-MM-DD`; must not precede `start` (equal is legal). |
| `month_weights` | list | — | One per-day weight per calendar month spanned, `start`'s first; uniform when absent. |

Both parse into days since 1970-01-01.

## Inputs

Field `type:` — `date` (32-bit days-since-epoch).

## Output

Per-row days since the epoch, stored as the signed 32-bit `date` value; every day in `[start, end]` equally likely (or at its month's weight with `month_weights`).

## Contract

- Applies to: `date` fields.

## Gotchas

- Both bounds inclusive — `uniform_date(2024-01-01, 2024-12-31)` can emit either.
- Unparseable dates → `SERVICE_VALIDATION` ("invalid start date" / "invalid end date"). `end == start` is legal (every draw on that day); only `end < start` → `SERVICE_VALIDATION` ("end must not be before start").
- `month_weights` needs one weight per month spanned (`SERVICE_VALIDATION`); a partial month draws only its in-range days.
- Epoch is 1970-01-01; pre-epoch dates are legal — `date` stores signed `int32` days, so they are negative.
- For a timestamp use a `datetime` field with `uniform_datetime` (zone-aware, hour / weekday weights); `uniform_date` is refused on `datetime`.

## See

- Skills: [`synthetic-data`](synthetic-data.md), [`op-synth-uniform`](op-synth-uniform.md), [`op-synth-monotonic-from`](op-synth-monotonic-from.md), [`op-synth-uniform-datetime`](op-synth-uniform-datetime.md)
