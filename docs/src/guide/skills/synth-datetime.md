```yaml
name: synth-datetime
description: Synthetic `datetime` columns — `uniform_datetime` draws a LOCAL wall-clock time by weekday and hour weights in a fixed or per-row zone, resolves DST gaps / overlaps per `dst_policy`, and stores the UTC instant rules then read; rule functions `trend` / `season` / `between`; profile capture.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, datetime, uniform_datetime, time_zone, time_zone_field, dst_policy, hour_weights, weekday_weights, trend, season, between]
requires: [capability:synth]
```

# Synth datetime fields

A `datetime` stores UTC epoch SECONDS; an author knows LOCAL time ("open 08:00-18:00, closed Sundays"). `uniform_datetime` bridges the two. Params: [`op-synth-uniform-datetime`](op-synth-uniform-datetime.md).

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

## Time effects in rules

Rule / constraint functions over a BARE `date` or `datetime` field `t`, read on the field's clock (its `time_zone`, the zone this row was DRAWN in for a `time_zone_field` — rewriting the zone field later moves nothing — else UTC; a `date` is a calendar day). No RNG.

| call | returns |
|---|---|
| `trend(t, slope)` | `slope` × days (fractional) since local midnight of `t`'s `start` (`uniform_date` / `uniform_datetime` only); over a number, `slope` × it (`trend(row(), 0.01)`) |
| `season(t, "dow"\|"hour"\|"month", [w…])` | the weight of the local ISO weekday (7, Monday first), hour (24, datetime only) or month (12) |
| `between(t, "a", "b")` | `a` ≤ reading ≤ `b`; naive literals (`YYYY-MM-DD`, `YYYY-MM-DDTHH:MM[:SS]`), no `Z`/offset; a date-only `b` covers its whole day |

```json
{"set_expr": {"p_on_time": "0.80 + trend(ts, 0.15 / 182) - (between(ts, \"2026-05-12\", \"2026-05-14\") ? 0.35 : 0)"}},
{"set_expr": {"volume": "base * season(ts, \"dow\", [1, 1, 1, 1, 1.2, 0.6, 0])"}}
```

Unit, weights and bounds are literals checked at parse (`PULSE_SYNTH_RULE_EXPR_INVALID`; `SERVICE_VALIDATION` in a constraint). A null `t` returns null — guard with `!isnull(ts) &&`.

Recover them with `GROUP_DATE` `day` / `day_of_week` / `hour_of_day` and `tz_field` = the zone field.

## Profile capture

`profile create` records a datetime as `datetime` — UTC day range plus UTC weekday (Monday first) and hour counts — and `SpecFromProfile` rebuilds an UNZONED `uniform_datetime` (weekday count ÷ its days in range, hour counts as weights), so the instants' UTC weekday and hour marginals hold; volume trends across days are not captured. A datetime joins no correlation, pair or model.

## See

- [`op-synth-uniform-datetime`](op-synth-uniform-datetime.md), [`synthetic-data`](synthetic-data.md), [`synth-determinism`](synth-determinism.md)
- [`time-zones`](time-zones.md) — how queries read the stored instants back on a local clock.
