---
name: synth-datetime
description: Synthetic `datetime` columns — `uniform_datetime` draws a LOCAL wall-clock time by weekday and hour weights in a fixed or per-row zone, resolves DST gaps / overlaps per `dst_policy`, and stores the UTC instant rules then read.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, datetime, uniform_datetime, time_zone, time_zone_field, dst_policy, hour_weights, weekday_weights]
requires: [capability:synth]
---

# Synth datetime fields

A `datetime` stores UTC epoch SECONDS; an author knows LOCAL time ("open 08:00-18:00, closed Sundays"). `uniform_datetime` bridges the two. Params: `op-synth-uniform-datetime`.

## The draw

Per row, always four RNG draws: a local calendar day in `[start, end]` (a weekday's share is its `weekday_weights` entry times that weekday's days in range — an intensity, so over whole weeks the realised weekday shares are the normalised weights), a local hour by `hour_weights`, then a weekday occurrence and a uniform second. Weights are 7 (Monday first, ISO — the `dow` / `iso_weekday` order) and 24 non-negative numbers; absent = uniform. A 0 removes that weekday or hour exactly.

## The zone

`time_zone` (IANA or `UTC`) fixes it; `time_zone_field` names a `categorical_*` field whose value is each row's zone — the hours are then local to EACH row. Exclusive. All zone math is `internal/temporal`, over Pulse's embedded tz database. `UTC` / `Etc/UTC` / absent is byte-identical to no zone.

A NULL zone cell makes the row's datetime null — never a UTC fallback — so a zone field with a `null_rate` needs `nullable: true` on the datetime (refused otherwise). An unknown name, declared or drawn, is `PULSE_TIMEZONE_UNKNOWN`.

## DST

A local time a spring-forward skips (gap) or a fall-back repeats (overlap) follows `dst_policy`, the import's spelling: `error` (default) stops the run — `PULSE_SYNTH_DST_NONEXISTENT` / `PULSE_SYNTH_DST_AMBIGUOUS`, details `field`, `local_time`, `tz`; `earlier` takes the pre-transition offset (overlap: first occurrence), `later` the post-transition one. A resolved run warns once per field with the gap / overlap counts over ACCEPTED rows. Zero weight on the transition hour avoids both.

## Where the conversion sits

Before the conversion a ZONED field's row value is the local reading; every arm that writes it (a pair, the copula, a model) writes a local reading. The conversion runs after the model stage and BEFORE the rule pass, consuming no RNG, so rules and constraints read — and write — the UTC instant the file stores. Rewriting the zone field in a rule does not move a datetime already resolved. A spec with no zoned datetime compiles no conversion stage and is byte-identical to before.

`uniform_date` on a `datetime` (and `uniform_datetime` on a `date`) is refused: days and seconds never mix. `uniform_datetime` has no moments, so it cannot join `correlations`.

## See

- `op-synth-uniform-datetime`, `synthetic-data`, `synth-determinism`
- `time-zones` — how queries read the stored instants back on a local clock.
