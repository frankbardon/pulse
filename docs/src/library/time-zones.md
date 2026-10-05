# Time Zones

Storage is always UTC: a `datetime` is epoch seconds, a `date` is epoch days. A time zone only changes how an instant is read (local calendar day, wall clock) or rendered (export). Zone names are `UTC` or an exact IANA `Area/Location` from Pulse's own embedded tz database, so results never depend on the host; anything else is `PULSE_TIMEZONE_UNKNOWN`. The manifest reports the release as `tzdata_version` (see [Refreshing the Time-Zone Database](../internals/refreshing-tzdata.md)).

## Choosing a zone

Per zone-capable slot: slot `tz`, then request `time_zone`, then `Options.DefaultTimeZone`, then UTC. There is no environment variable or default-zone CLI flag. Predict echoes the resolved zone per slot in `time_zones[]`. An explicit `tz` on a `date` field or a non-zone-capable operator is `PROCESSING_CONFIG`; a non-UTC zone on a derived field is refused. UTC or no zone is byte-identical to the pre-zone output.

## Local days in every mode

A `datetime` buckets and filters on its local calendar day in process, stream, compose, shards, crosstab, join, chain and facet, DST changes included. `GROUP_DATE` also takes `component: "hour"` (local wall-clock keys; the skipped DST hour has no bucket, the repeated hour is one) and `week_start`. `ATTR_DATE_PART` and `FEAT_DATE_FEATURES` read the zone's wall clock over a `datetime`. Range literals are local calendar days. `OVERLAY_YOY` follows its host grouper's zone.

## Import and export

`--source-tz [col=]Zone` reads naive datetimes in a source zone on every import path; `--dst-policy error|earlier|later` decides what happens to a skipped or repeated wall time (`PULSE_IMPORT_DST_NONEXISTENT`, `PULSE_IMPORT_DST_AMBIGUOUS`, `PULSE_IMPORT_DST_RESOLVED`). Offset and `Z` literals never move. Native Arrow and Parquet timestamps type `datetime`; SPSS `DATETIME` is a wall clock while `TIME` / `DTIME` are `f64` durations. `export --tz Zone` renders local offsets; SPSS export refuses a non-UTC zone. Flag detail: [CLI flags](../cli/flags.md). Agent-facing form: skill `time-zones`.
