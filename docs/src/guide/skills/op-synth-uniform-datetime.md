```yaml
name: op-synth-uniform-datetime
description: Zone-aware datetime samples — a local wall-clock time drawn by weekday and hour-of-day weights, stored as the UTC epoch second it names in a fixed or per-row zone; DST gaps / overlaps per dst_policy.
kind: operator
category: SYNTH
operator: uniform_datetime
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, time-series]
```

Synth distributions emit per-row values; no `Response.Components`. CLI-only ([`synthetic-data`](synthetic-data.md)).

## Use when

Draws datetime values on a local clock, weighted by weekday and hour like opening hours, in one time zone or each row's own.

Questions it answers:

- How do I generate delivery times that follow each depot's local business hours?
- How do I make synthetic events that never happen on a Sunday?

Use something else:

- `uniform_date` when only the calendar day matters.
- `constant` when every row shares one datetime.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `start` | string | (required) | First LOCAL calendar day, `YYYY-MM-DD`, inclusive. |
| `end` | string | (required) | Last local day, inclusive; may equal `start`, not precede it. |
| `time_zone` | string | — | IANA zone or `"UTC"` the local time is read in; UTC when absent. Exclusive with `time_zone_field`. |
| `time_zone_field` | string | — | A `categorical_*` field of IANA names, one zone per row. |
| `dst_policy` | string | error | DST gap / overlap: `error`, `earlier` or `later` (import spellings). |
| `hour_weights` | list | — | 24 non-negative weights, local hours 0..23; uniform when absent. |
| `weekday_weights` | list | — | 7 non-negative weights, Monday first (ISO); uniform when absent. |
| `month_weights` | list | — | One per month spanned, `start`'s first; day weight = weekday × month. |

## Inputs

Field `type:` — `datetime` only (epoch SECONDS, `int64`). Refused on any other type; `uniform_date` is refused on a `datetime`.

## Output

Per row a local wall-clock reading (a day by weekday × month weight, then an hour, then a uniform second), converted to its UTC instant in the row's zone; the file stores the instant.

## Contract

- Applies to: `datetime` fields.

## Gotchas

- Rules and constraints read the UTC instant; earlier arms write the local reading ([`synth-datetime`](synth-datetime.md)).
- Default `dst_policy: error` stops on a DST gap / overlap (`PULSE_SYNTH_DST_NONEXISTENT` / `_AMBIGUOUS`); `earlier` / `later` resolve and warn with the count.
- A null zone cell is a null datetime; an unknown zone is `PULSE_TIMEZONE_UNKNOWN`.

## See

- Skills: [`synth-datetime`](synth-datetime.md), [`synthetic-data`](synthetic-data.md), [`time-zones`](time-zones.md), [`op-synth-uniform-date`](op-synth-uniform-date.md)
